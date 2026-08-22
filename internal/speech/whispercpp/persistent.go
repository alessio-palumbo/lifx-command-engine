package whispercpp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alessio-palumbo/lifx-command-engine/internal/schema"
	"github.com/alessio-palumbo/lifx-command-engine/internal/speech"
)

const (
	serverHost       = "127.0.0.1"
	startupAttempts  = 3
	readinessPeriod  = 25 * time.Millisecond
	processStopDelay = 2 * time.Second
)

// PersistentTranscriber keeps a whisper-server process and its loaded model
// alive between transcription requests. It must be created with
// NewPersistentTranscriber and closed by its owner.
type PersistentTranscriber struct {
	Command   string
	ModelPath string
	Args      []string

	mu             sync.Mutex
	inference      chan struct{}
	lifetimeCtx    context.Context
	lifetimeCancel context.CancelFunc
	process        *serverProcess
	closed         bool
	client         *http.Client
}

type serverProcess struct {
	cmd     *exec.Cmd
	port    int
	done    chan struct{}
	waitErr error
	stdout  *tailBuffer
	stderr  *tailBuffer
}

// NewPersistentTranscriber validates server-only configuration and creates a
// transcriber with a process lifetime independent from any Start context.
func NewPersistentTranscriber(command, modelPath string, args []string) (*PersistentTranscriber, error) {
	if strings.TrimSpace(command) == "" || strings.TrimSpace(modelPath) == "" {
		return nil, fmt.Errorf("whisper.cpp command and model must be configured")
	}
	if err := ValidatePersistentArgs(args); err != nil {
		return nil, err
	}
	lifetimeCtx, lifetimeCancel := context.WithCancel(context.Background())
	t := &PersistentTranscriber{
		Command: command, ModelPath: modelPath, Args: append([]string(nil), args...),
		inference: make(chan struct{}, 1), lifetimeCtx: lifetimeCtx, lifetimeCancel: lifetimeCancel,
		client: &http.Client{},
	}
	t.inference <- struct{}{}
	return t, nil
}

// ValidatePersistentArgs rejects options managed by the engine or incompatible
// with the stable timestamped transcription response.
func ValidatePersistentArgs(args []string) error {
	reserved := map[string]bool{
		"-m": true, "--model": true, "--host": true, "--port": true,
		"--request-path": true, "--inference-path": true, "--convert": true,
		"-l": true, "--language": true, "-f": true, "--file": true,
		"-oj": true, "--output-json": true, "-of": true, "--output-file": true,
		"-nt": true, "--no-timestamps": true,
	}
	for _, arg := range args {
		name := arg
		if index := strings.IndexByte(name, '='); index >= 0 {
			name = name[:index]
		}
		if reserved[name] {
			return fmt.Errorf("whisper persistent argument %q is managed or unsupported", name)
		}
	}
	return nil
}

// Start eagerly starts whisper-server and waits until its inference endpoint is
// ready. ctx bounds readiness only; it does not own the process lifetime.
func (t *PersistentTranscriber) Start(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return fmt.Errorf("whisper persistent transcriber is closed")
	}
	if t.runningLocked() {
		return nil
	}
	return t.startLocked(ctx)
}

func (t *PersistentTranscriber) Transcribe(ctx context.Context, input schema.TranscribeInput) (schema.TranscribeResult, error) {
	if err := validateAudioPath(input.AudioPath); err != nil {
		return schema.TranscribeResult{}, err
	}
	select {
	case <-ctx.Done():
		return schema.TranscribeResult{}, ctx.Err()
	case <-t.inference:
	}
	defer func() { t.inference <- struct{}{} }()

	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return schema.TranscribeResult{}, &speech.RuntimeError{Err: fmt.Errorf("whisper persistent transcriber is closed")}
	}
	if !t.runningLocked() {
		if err := t.startLocked(ctx); err != nil {
			t.mu.Unlock()
			return schema.TranscribeResult{}, &speech.RuntimeError{Err: err}
		}
	}
	process := t.process
	t.mu.Unlock()

	result, err := t.infer(ctx, process, input)
	if err != nil {
		var transportError *serverTransportError
		if errors.As(err, &transportError) && ctx.Err() == nil {
			t.discardProcess(process)
		}
		return schema.TranscribeResult{}, &speech.RuntimeError{Err: err}
	}
	return result, nil
}

func (t *PersistentTranscriber) startLocked(ctx context.Context) error {
	var lastErr error
	attempts := 0
	for attempt := 0; attempt < startupAttempts; attempt++ {
		attempts++
		port, err := availablePort()
		if err != nil {
			return fmt.Errorf("select whisper-server port: %w", err)
		}
		process, err := t.launchLocked(port)
		if err != nil {
			return err
		}
		t.process = process
		if err := t.waitReadyLocked(ctx, process); err == nil {
			return nil
		} else {
			lastErr = err
		}
		t.stopProcessLocked(process)
		t.process = nil
		if ctx.Err() != nil || t.lifetimeCtx.Err() != nil {
			break
		}
	}
	return fmt.Errorf("start whisper-server after %d attempt(s): %w", attempts, lastErr)
}

func (t *PersistentTranscriber) launchLocked(port int) (*serverProcess, error) {
	args := append([]string(nil), t.Args...)
	args = append(args, "--model", t.ModelPath, "--host", serverHost, "--port", strconv.Itoa(port))
	cmd := exec.CommandContext(t.lifetimeCtx, t.Command, args...)
	cmd.WaitDelay = processStopDelay
	stdout := newTailBuffer(64 * 1024)
	stderr := newTailBuffer(64 * 1024)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	process := &serverProcess{cmd: cmd, port: port, done: make(chan struct{}), stdout: stdout, stderr: stderr}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start whisper-server: %w", err)
	}
	go func() {
		process.waitErr = cmd.Wait()
		close(process.done)
	}()
	return process, nil
}

func (t *PersistentTranscriber) waitReadyLocked(ctx context.Context, process *serverProcess) error {
	ticker := time.NewTicker(readinessPeriod)
	defer ticker.Stop()
	for {
		request, err := http.NewRequestWithContext(ctx, http.MethodOptions, process.endpoint(), nil)
		if err != nil {
			return err
		}
		response, err := t.client.Do(request)
		if err == nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
			_ = response.Body.Close()
			if response.StatusCode >= 200 && response.StatusCode < 300 {
				return nil
			}
		}
		select {
		case <-process.done:
			return processError("whisper-server exited before readiness", process.waitErr, process)
		case <-ctx.Done():
			return fmt.Errorf("wait for whisper-server readiness: %w", ctx.Err())
		case <-t.lifetimeCtx.Done():
			return fmt.Errorf("wait for whisper-server readiness: transcriber closed")
		case <-ticker.C:
		}
	}
}

func (t *PersistentTranscriber) runningLocked() bool {
	if t.process == nil {
		return false
	}
	select {
	case <-t.process.done:
		t.process = nil
		return false
	default:
		return true
	}
}

func (t *PersistentTranscriber) infer(ctx context.Context, process *serverProcess, input schema.TranscribeInput) (schema.TranscribeResult, error) {
	bodyReader, bodyWriter := io.Pipe()
	multipartWriter := multipart.NewWriter(bodyWriter)
	uploadDone := make(chan error, 1)
	go func() {
		err := writeMultipartAudio(multipartWriter, input)
		if err == nil {
			err = multipartWriter.Close()
		}
		_ = bodyWriter.CloseWithError(err)
		uploadDone <- err
	}()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, process.endpoint(), bodyReader)
	if err != nil {
		_ = bodyReader.CloseWithError(err)
		<-uploadDone
		return schema.TranscribeResult{}, fmt.Errorf("create whisper-server request: %w", err)
	}
	request.Header.Set("Content-Type", multipartWriter.FormDataContentType())
	response, err := t.client.Do(request)
	if err != nil {
		_ = bodyReader.CloseWithError(err)
		<-uploadDone
		return schema.TranscribeResult{}, &serverTransportError{err: fmt.Errorf("whisper-server inference: %w%s", err, diagnosticSuffix(process))}
	}
	defer response.Body.Close()
	uploadErr := <-uploadDone
	if uploadErr != nil {
		return schema.TranscribeResult{}, fmt.Errorf("upload audio: %w", uploadErr)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxOutputBytes+1))
	if err != nil {
		return schema.TranscribeResult{}, fmt.Errorf("read whisper-server response: %w", err)
	}
	if len(raw) > maxOutputBytes {
		return schema.TranscribeResult{}, fmt.Errorf("whisper-server response exceeds output limit")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return schema.TranscribeResult{}, fmt.Errorf("whisper-server returned HTTP %d: %s%s", response.StatusCode, strings.TrimSpace(string(raw)), diagnosticSuffix(process))
	}
	result, err := decodeServerOutput(raw, input.Language)
	if err != nil {
		return schema.TranscribeResult{}, err
	}
	return result, nil
}

func writeMultipartAudio(writer *multipart.Writer, input schema.TranscribeInput) error {
	file, err := os.Open(input.AudioPath)
	if err != nil {
		return err
	}
	defer file.Close()
	part, err := writer.CreateFormFile("file", filepath.Base(input.AudioPath))
	if err != nil {
		return err
	}
	if _, err := io.Copy(part, file); err != nil {
		return err
	}
	language := strings.TrimSpace(input.Language)
	if language == "" {
		language = "auto"
	}
	if err := writer.WriteField("language", language); err != nil {
		return err
	}
	if err := writer.WriteField("response_format", "verbose_json"); err != nil {
		return err
	}
	return writer.WriteField("no_language_probabilities", "true")
}

type serverOutput struct {
	Text             string `json:"text"`
	Language         string `json:"language"`
	DetectedLanguage string `json:"detected_language"`
	Segments         []struct {
		Start float64 `json:"start"`
		End   float64 `json:"end"`
		Text  string  `json:"text"`
	} `json:"segments"`
}

func decodeServerOutput(data []byte, requestedLanguage string) (schema.TranscribeResult, error) {
	var decoded serverOutput
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&decoded); err != nil {
		return schema.TranscribeResult{}, fmt.Errorf("decode whisper-server JSON: %w", err)
	}
	result := schema.TranscribeResult{SchemaVersion: "1", Segments: make([]schema.TranscribeSegment, 0, len(decoded.Segments))}
	texts := make([]string, 0, len(decoded.Segments))
	for _, segment := range decoded.Segments {
		text := strings.TrimSpace(segment.Text)
		result.Segments = append(result.Segments, schema.TranscribeSegment{
			StartMS: int64(math.Round(segment.Start * 1000)), EndMS: int64(math.Round(segment.End * 1000)), Text: text,
		})
		if text != "" {
			texts = append(texts, text)
		}
	}
	result.Text = strings.Join(texts, " ")
	if result.Text == "" {
		result.Text = strings.TrimSpace(decoded.Text)
	}
	requestedLanguage = strings.TrimSpace(requestedLanguage)
	if requestedLanguage != "" && requestedLanguage != "auto" {
		result.Language = requestedLanguage
	} else {
		result.Language = normalizeLanguage(firstNonEmpty(decoded.DetectedLanguage, decoded.Language))
	}
	return result, nil
}

func normalizeLanguage(language string) string {
	normalized := strings.ToLower(strings.TrimSpace(language))
	if code, ok := languageCodes[normalized]; ok {
		return code
	}
	return normalized
}

var languageCodes = map[string]string{
	"english": "en", "italian": "it", "spanish": "es", "french": "fr", "german": "de",
	"portuguese": "pt", "dutch": "nl", "polish": "pl", "russian": "ru", "japanese": "ja",
	"chinese": "zh", "korean": "ko", "arabic": "ar", "turkish": "tr", "ukrainian": "uk",
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func validateAudioPath(path string) error {
	if strings.TrimSpace(path) == "" {
		return invalid("audio_path must not be empty")
	}
	info, err := os.Stat(path)
	if err != nil {
		return &speech.InvalidInputError{Err: fmt.Errorf("audio_path: %w", err)}
	}
	if !info.Mode().IsRegular() {
		return invalid("audio_path must be a regular file")
	}
	return nil
}

func availablePort() (int, error) {
	listener, err := net.Listen("tcp4", serverHost+":0")
	if err != nil {
		return 0, err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		return 0, err
	}
	return port, nil
}

func (p *serverProcess) endpoint() string {
	return "http://" + net.JoinHostPort(serverHost, strconv.Itoa(p.port)) + "/inference"
}

func processError(prefix string, err error, process *serverProcess) error {
	if err == nil {
		err = errors.New("process exited")
	}
	return fmt.Errorf("%s: %w%s", prefix, err, diagnosticSuffix(process))
}

func diagnosticSuffix(process *serverProcess) string {
	parts := []string{}
	if value := strings.TrimSpace(process.stderr.String()); value != "" {
		parts = append(parts, "stderr: "+value)
	}
	if value := strings.TrimSpace(process.stdout.String()); value != "" {
		parts = append(parts, "stdout: "+value)
	}
	if len(parts) == 0 {
		return ""
	}
	return ": " + strings.Join(parts, "; ")
}

// Close stops and reaps whisper-server. It is safe to call more than once.
func (t *PersistentTranscriber) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	t.lifetimeCancel()
	process := t.process
	t.process = nil
	t.mu.Unlock()
	if process == nil {
		return nil
	}
	select {
	case <-process.done:
		return nil
	case <-time.After(processStopDelay + time.Second):
		if process.cmd.Process != nil {
			_ = process.cmd.Process.Kill()
		}
		<-process.done
		return nil
	}
}

func (t *PersistentTranscriber) stopProcessLocked(process *serverProcess) {
	select {
	case <-process.done:
		return
	default:
	}
	if process.cmd.Process != nil {
		_ = process.cmd.Process.Kill()
	}
	select {
	case <-process.done:
	case <-time.After(processStopDelay):
	}
}

func (t *PersistentTranscriber) discardProcess(process *serverProcess) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.process != process {
		return
	}
	t.stopProcessLocked(process)
	t.process = nil
}

type serverTransportError struct{ err error }

func (e *serverTransportError) Error() string { return e.err.Error() }
func (e *serverTransportError) Unwrap() error { return e.err }

type tailBuffer struct {
	mu    sync.Mutex
	limit int
	data  []byte
}

func newTailBuffer(limit int) *tailBuffer { return &tailBuffer{limit: limit} }

func (b *tailBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.data = append(b.data, data...)
	if len(b.data) > b.limit {
		b.data = append([]byte(nil), b.data[len(b.data)-b.limit:]...)
	}
	return len(data), nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.data)
}
