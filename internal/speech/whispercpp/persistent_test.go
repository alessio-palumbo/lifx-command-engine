package whispercpp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alessio-palumbo/lifx-command-engine/internal/schema"
	"github.com/alessio-palumbo/lifx-command-engine/internal/speech"
)

func TestPersistentTranscriberReusesProcessAndConvertsResponse(t *testing.T) {
	audio := testAudio(t)
	countFile := filepath.Join(t.TempDir(), "starts")
	transcriber := testPersistentTranscriber(t, countFile, "normal")
	startCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := transcriber.Start(startCtx); err != nil {
		cancel()
		t.Fatal(err)
	}
	cancel() // The readiness context must not own the child lifetime.
	for index := 0; index < 2; index++ {
		got, err := transcriber.Transcribe(context.Background(), schema.TranscribeInput{AudioPath: audio, Language: "en"})
		if err != nil {
			t.Fatal(err)
		}
		if got.Text != "turn the desk on" || got.Language != "en" || len(got.Segments) != 2 || got.Segments[1].StartMS != 500 || got.Segments[1].EndMS != 901 {
			t.Fatalf("result = %#v", got)
		}
	}
	if starts := countLines(t, countFile); starts != 1 {
		t.Fatalf("server starts = %d, want 1", starts)
	}
	process := transcriber.process
	if err := transcriber.Close(); err != nil {
		t.Fatal(err)
	}
	if process.cmd.ProcessState == nil {
		t.Fatal("server process was not reaped")
	}
}

func TestPersistentTranscriberNormalizesDetectedLanguage(t *testing.T) {
	transcriber := testPersistentTranscriber(t, filepath.Join(t.TempDir(), "starts"), "normal")
	t.Cleanup(func() { _ = transcriber.Close() })
	got, err := transcriber.Transcribe(context.Background(), schema.TranscribeInput{AudioPath: testAudio(t), Language: "auto"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Language != "it" {
		t.Fatalf("language = %q, want it", got.Language)
	}
}

func TestPersistentTranscriberCancellation(t *testing.T) {
	transcriber := testPersistentTranscriber(t, filepath.Join(t.TempDir(), "starts"), "slow")
	t.Cleanup(func() { _ = transcriber.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := transcriber.Transcribe(ctx, schema.TranscribeInput{AudioPath: testAudio(t)})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want deadline exceeded", err)
	}
}

func TestPersistentTranscriberCancellationWhileQueued(t *testing.T) {
	transcriber := testPersistentTranscriber(t, filepath.Join(t.TempDir(), "starts"), "slow")
	t.Cleanup(func() { _ = transcriber.Close() })
	audio := testAudio(t)
	firstCtx, cancelFirst := context.WithCancel(context.Background())
	firstDone := make(chan error, 1)
	go func() {
		_, err := transcriber.Transcribe(firstCtx, schema.TranscribeInput{AudioPath: audio})
		firstDone <- err
	}()
	time.Sleep(75 * time.Millisecond)
	queuedCtx, cancelQueued := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancelQueued()
	_, err := transcriber.Transcribe(queuedCtx, schema.TranscribeInput{AudioPath: audio})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("queued error = %v, want deadline exceeded", err)
	}
	cancelFirst()
	if err := <-firstDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("first error = %v, want canceled", err)
	}
}

func TestPersistentTranscriberRestartsOnlyOnNextRequest(t *testing.T) {
	audio := testAudio(t)
	countFile := filepath.Join(t.TempDir(), "starts")
	transcriber := testPersistentTranscriber(t, countFile, "crash-first")
	t.Cleanup(func() { _ = transcriber.Close() })
	if _, err := transcriber.Transcribe(context.Background(), schema.TranscribeInput{AudioPath: audio}); err == nil {
		t.Fatal("first request succeeded after server crash")
	}
	got, err := transcriber.Transcribe(context.Background(), schema.TranscribeInput{AudioPath: audio})
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "turn the desk on" || countLines(t, countFile) != 2 {
		t.Fatalf("result = %#v, starts = %d", got, countLines(t, countFile))
	}
}

func TestPersistentTranscriberRejectsInvalidAudioWithoutStarting(t *testing.T) {
	countFile := filepath.Join(t.TempDir(), "starts")
	transcriber := testPersistentTranscriber(t, countFile, "normal")
	t.Cleanup(func() { _ = transcriber.Close() })
	_, err := transcriber.Transcribe(context.Background(), schema.TranscribeInput{AudioPath: filepath.Join(t.TempDir(), "missing.wav")})
	var invalid *speech.InvalidInputError
	if !errors.As(err, &invalid) || countLines(t, countFile) != 0 {
		t.Fatalf("error = %v, starts = %d", err, countLines(t, countFile))
	}
}

func TestValidatePersistentArgs(t *testing.T) {
	for _, arg := range []string{"--model", "--model=other.bin", "-m", "--host=0.0.0.0", "--port", "--request-path=/x", "--inference-path", "--convert", "--language=en", "-l", "--file=x.wav", "-f", "--output-json", "-oj", "--output-file=x", "-of", "--no-timestamps", "-nt"} {
		t.Run(strings.ReplaceAll(arg, "/", "_"), func(t *testing.T) {
			if err := ValidatePersistentArgs([]string{arg}); err == nil {
				t.Fatalf("ValidatePersistentArgs(%q) succeeded", arg)
			}
		})
	}
	if err := ValidatePersistentArgs([]string{"-bo", "1", "-bs", "1", "-nf", "-ac", "768", "--prompt=Desk"}); err != nil {
		t.Fatal(err)
	}
}

func TestTailBufferRetainsBoundedDiagnostics(t *testing.T) {
	buffer := newTailBuffer(5)
	if _, err := buffer.Write([]byte("123")); err != nil {
		t.Fatal(err)
	}
	if _, err := buffer.Write([]byte("4567")); err != nil {
		t.Fatal(err)
	}
	if got := buffer.String(); got != "34567" {
		t.Fatalf("tail = %q", got)
	}
}

func testPersistentTranscriber(t *testing.T, countFile, mode string) *PersistentTranscriber {
	t.Helper()
	transcriber, err := NewPersistentTranscriber(os.Args[0], "model.bin", []string{
		"-test.run=TestPersistentWhisperServerHelper", "--", "whisper-server-helper",
		"--fake-count-file", countFile, "--fake-mode", mode,
	})
	if err != nil {
		t.Fatal(err)
	}
	return transcriber
}

func testAudio(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sample.wav")
	if err := os.WriteFile(path, []byte("RIFF fake wav"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func countLines(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return len(strings.Fields(string(data)))
}

func TestPersistentWhisperServerHelper(t *testing.T) {
	if !containsArg(os.Args, "whisper-server-helper") {
		return
	}
	values := map[string]string{}
	for index, arg := range os.Args {
		if strings.HasPrefix(arg, "--") && index+1 < len(os.Args) {
			values[arg] = os.Args[index+1]
		}
	}
	port, err := strconv.Atoi(values["--port"])
	if err != nil || values["--host"] != serverHost || values["--model"] == "" {
		os.Exit(2)
	}
	countFile := values["--fake-count-file"]
	file, err := os.OpenFile(countFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		os.Exit(3)
	}
	_, _ = fmt.Fprintln(file, "start")
	_ = file.Close()
	startNumber := countLinesForHelper(countFile)
	mode := values["--fake-mode"]
	var requests atomic.Int32
	handler := http.NewServeMux()
	handler.HandleFunc("/inference", func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodOptions {
			writer.WriteHeader(http.StatusNoContent)
			return
		}
		if mode == "crash-first" && startNumber == 1 {
			os.Exit(9)
		}
		if mode == "slow" {
			select {
			case <-request.Context().Done():
				return
			case <-time.After(5 * time.Second):
			}
		}
		if err := request.ParseMultipartForm(1 << 20); err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
		if request.FormValue("response_format") != "verbose_json" || request.FormValue("language") == "" || request.FormValue("no_language_probabilities") != "true" {
			http.Error(writer, "missing managed fields", http.StatusBadRequest)
			return
		}
		uploaded, _, err := request.FormFile("file")
		if err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
		_, _ = io.Copy(io.Discard, uploaded)
		_ = uploaded.Close()
		requests.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"text": " turn the desk on ", "language": "english", "detected_language": "italian",
			"segments": []map[string]any{
				{"start": 0.0, "end": 0.5, "text": " turn the desk"},
				{"start": 0.5, "end": 0.9006, "text": " on "},
			},
		})
	})
	server := &http.Server{Addr: serverHost + ":" + strconv.Itoa(port), Handler: handler}
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		os.Exit(4)
	}
	os.Exit(0)
}

func containsArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}

func countLinesForHelper(path string) int {
	data, _ := os.ReadFile(path)
	return len(strings.Fields(string(data)))
}
