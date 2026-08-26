// Package streamprobe implements an experimental, replayable bounded-window
// transcription benchmark. It is intentionally not part of the public speech
// protocol: its purpose is to validate whether speculative Whisper inference
// improves perceived latency on constrained hardware before an API is fixed.
package streamprobe

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/alessio-palumbo/lifx-command-engine/internal/schema"
	"github.com/alessio-palumbo/lifx-command-engine/internal/speech"
)

type Config struct {
	AudioPath       string
	Language        string
	ExpectedText    string
	ChunkDuration   time.Duration
	PartialAfter    time.Duration
	PartialInterval time.Duration
	PartialWindow   time.Duration
	MaxUtterance    time.Duration
	TempDir         string
}

type PartialEvent struct {
	Sequence  uint64 `json:"sequence"`
	AudioMS   int64  `json:"audio_ms"`
	Text      string `json:"text"`
	Stable    bool   `json:"stable"`
	LatencyMS int64  `json:"inference_ms"`
}

type Metrics struct {
	CaptureDurationMS    int64  `json:"capture_duration_ms"`
	TimeToFirstPartialMS *int64 `json:"time_to_first_partial_ms,omitempty"`
	PartialBeforeRelease bool   `json:"partial_before_release"`
	ReleaseToFinalMS     int64  `json:"release_to_final_ms"`
	FinalInferenceMS     int64  `json:"final_inference_ms"`
	PartialInferenceMS   int64  `json:"partial_inference_ms"`
	PartialPreemptMS     int64  `json:"partial_preempt_ms,omitempty"`
	TotalElapsedMS       int64  `json:"total_elapsed_ms"`
	PartialAttempts      int    `json:"partial_attempts"`
	PartialCompleted     int    `json:"partial_completed"`
	PartialCanceled      int    `json:"partial_canceled"`
}

type Report struct {
	Mode            string                  `json:"mode"`
	FinalTranscript schema.TranscribeResult `json:"final_transcript"`
	Partials        []PartialEvent          `json:"partials"`
	PartialErrors   []string                `json:"partial_errors,omitempty"`
	Metrics         Metrics                 `json:"metrics"`
	ExpectedText    string                  `json:"expected_text,omitempty"`
	NormalizedMatch *bool                   `json:"normalized_match,omitempty"`
}

type partialResult struct {
	result      schema.TranscribeResult
	err         error
	elapsed     time.Duration
	audioMS     int64
	completedAt time.Time
}

type activePartial struct {
	cancel context.CancelFunc
	done   <-chan partialResult
}

func Run(ctx context.Context, transcriber speech.Transcriber, config Config) (Report, error) {
	if transcriber == nil {
		return Report{}, fmt.Errorf("transcriber is required")
	}
	applyDefaults(&config)
	audio, err := ReadPCM16WAV(config.AudioPath)
	if err != nil {
		return Report{}, err
	}
	if audio.Channels != 1 || audio.BitsPerSample != 16 {
		return Report{}, fmt.Errorf("audio must be mono 16-bit PCM, got %d channel(s) and %d-bit samples", audio.Channels, audio.BitsPerSample)
	}
	duration := audio.Duration()
	if duration > config.MaxUtterance {
		return Report{}, fmt.Errorf("audio duration %v exceeds bounded utterance limit %v", duration, config.MaxUtterance)
	}

	report := Report{Mode: "bounded_window_whisper_probe", Partials: []PartialEvent{}, ExpectedText: config.ExpectedText}
	report.Metrics.CaptureDurationMS = duration.Milliseconds()
	started := time.Now()
	bytesPerSecond := audio.SampleRate * int(audio.Channels) * int(audio.BitsPerSample) / 8
	chunkBytes := durationBytes(config.ChunkDuration, bytesPerSecond)
	windowBytes := durationBytes(config.PartialWindow, bytesPerSecond)
	if chunkBytes < 2 {
		return Report{}, fmt.Errorf("chunk duration is too small for sample rate")
	}
	chunkBytes -= chunkBytes % 2
	windowBytes -= windowBytes % 2

	var active *activePartial
	var captured []byte
	nextPartialAt := config.PartialAfter
	sequence := uint64(0)
	var firstPartialAt *time.Time
	collect := func(value partialResult) {
		report.Metrics.PartialInferenceMS += value.elapsed.Milliseconds()
		if value.err != nil {
			if errors.Is(value.err, context.Canceled) {
				report.Metrics.PartialCanceled++
			} else {
				report.PartialErrors = append(report.PartialErrors, value.err.Error())
			}
			return
		}
		sequence++
		report.Metrics.PartialCompleted++
		event := PartialEvent{Sequence: sequence, AudioMS: value.audioMS, Text: value.result.Text, Stable: false, LatencyMS: value.elapsed.Milliseconds()}
		report.Partials = append(report.Partials, event)
		if report.Metrics.TimeToFirstPartialMS == nil {
			elapsed := value.completedAt.Sub(started).Milliseconds()
			report.Metrics.TimeToFirstPartialMS = &elapsed
			completedAt := value.completedAt
			firstPartialAt = &completedAt
		}
	}

	for offset := 0; offset < len(audio.PCM); offset += chunkBytes {
		end := min(offset+chunkBytes, len(audio.PCM))
		chunkDuration := time.Duration(end-offset) * time.Second / time.Duration(bytesPerSecond)
		if err := waitContext(ctx, chunkDuration); err != nil {
			if active != nil {
				active.cancel()
			}
			return Report{}, err
		}
		captured = append(captured, audio.PCM[offset:end]...)
		if active != nil {
			select {
			case value := <-active.done:
				active.cancel()
				collect(value)
				active = nil
			default:
			}
		}
		audioDuration := time.Duration(len(captured)) * time.Second / time.Duration(bytesPerSecond)
		if active == nil && audioDuration >= nextPartialAt && end < len(audio.PCM) {
			window := captured
			if len(window) > windowBytes {
				window = window[len(window)-windowBytes:]
			}
			path, err := writeTemporaryWAV(config.TempDir, audio.SampleRate, append([]byte(nil), window...))
			if err != nil {
				return Report{}, err
			}
			partialCtx, cancel := context.WithCancel(ctx)
			done := make(chan partialResult, 1)
			report.Metrics.PartialAttempts++
			go func(audioMS int64) {
				defer os.Remove(path)
				inferenceStarted := time.Now()
				result, err := transcriber.Transcribe(partialCtx, schema.TranscribeInput{AudioPath: path, Language: config.Language})
				done <- partialResult{result: result, err: err, elapsed: time.Since(inferenceStarted), audioMS: audioMS, completedAt: time.Now()}
			}(audioDuration.Milliseconds())
			active = &activePartial{cancel: cancel, done: done}
			nextPartialAt = audioDuration + config.PartialInterval
		}
	}

	released := time.Now()
	if active != nil {
		select {
		case value := <-active.done:
			active.cancel()
			collect(value)
		default:
			preempted := time.Now()
			active.cancel()
			value := <-active.done
			report.Metrics.PartialPreemptMS = time.Since(preempted).Milliseconds()
			collect(value)
		}
	}
	if firstPartialAt != nil {
		report.Metrics.PartialBeforeRelease = firstPartialAt.Before(released)
	}

	finalPath, err := writeTemporaryWAV(config.TempDir, audio.SampleRate, captured)
	if err != nil {
		return Report{}, err
	}
	defer os.Remove(finalPath)
	finalStarted := time.Now()
	final, err := transcriber.Transcribe(ctx, schema.TranscribeInput{AudioPath: finalPath, Language: config.Language})
	report.Metrics.FinalInferenceMS = time.Since(finalStarted).Milliseconds()
	if err != nil {
		return Report{}, fmt.Errorf("authoritative final transcription: %w", err)
	}
	report.FinalTranscript = final
	report.Metrics.ReleaseToFinalMS = time.Since(released).Milliseconds()
	report.Metrics.TotalElapsedMS = time.Since(started).Milliseconds()
	if config.ExpectedText != "" {
		matched := normalize(config.ExpectedText) == normalize(final.Text)
		report.NormalizedMatch = &matched
	}
	return report, nil
}

func applyDefaults(config *Config) {
	if config.Language == "" {
		config.Language = "en"
	}
	if config.ChunkDuration <= 0 {
		config.ChunkDuration = 100 * time.Millisecond
	}
	if config.PartialAfter <= 0 {
		config.PartialAfter = time.Second
	}
	if config.PartialInterval <= 0 {
		config.PartialInterval = 1500 * time.Millisecond
	}
	if config.PartialWindow <= 0 {
		config.PartialWindow = 4 * time.Second
	}
	if config.MaxUtterance <= 0 {
		config.MaxUtterance = 15 * time.Second
	}
}

func waitContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func durationBytes(duration time.Duration, bytesPerSecond int) int {
	return int(duration * time.Duration(bytesPerSecond) / time.Second)
}

func normalize(value string) string {
	return strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + ('a' - 'A')
		}
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == ' ' {
			return r
		}
		return ' '
	}, value)), " ")
}

type PCM16WAV struct {
	SampleRate    int
	Channels      uint16
	BitsPerSample uint16
	PCM           []byte
}

func (w PCM16WAV) Duration() time.Duration {
	bytesPerSecond := w.SampleRate * int(w.Channels) * int(w.BitsPerSample) / 8
	if bytesPerSecond <= 0 {
		return 0
	}
	return time.Duration(len(w.PCM)) * time.Second / time.Duration(bytesPerSecond)
}

func ReadPCM16WAV(path string) (PCM16WAV, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return PCM16WAV{}, fmt.Errorf("read audio: %w", err)
	}
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return PCM16WAV{}, fmt.Errorf("audio must be a RIFF/WAVE file")
	}
	var result PCM16WAV
	var formatFound bool
	for offset := 12; offset+8 <= len(data); {
		name := string(data[offset : offset+4])
		size := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		offset += 8
		if size < 0 || offset+size > len(data) {
			return PCM16WAV{}, fmt.Errorf("invalid WAV chunk %q", name)
		}
		chunk := data[offset : offset+size]
		switch name {
		case "fmt ":
			if len(chunk) < 16 || binary.LittleEndian.Uint16(chunk[0:2]) != 1 {
				return PCM16WAV{}, fmt.Errorf("audio must use uncompressed PCM")
			}
			result.Channels = binary.LittleEndian.Uint16(chunk[2:4])
			result.SampleRate = int(binary.LittleEndian.Uint32(chunk[4:8]))
			result.BitsPerSample = binary.LittleEndian.Uint16(chunk[14:16])
			formatFound = true
		case "data":
			result.PCM = append([]byte(nil), chunk...)
		}
		offset += size
		if size%2 != 0 {
			offset++
		}
	}
	if !formatFound || result.SampleRate <= 0 || result.PCM == nil {
		return PCM16WAV{}, fmt.Errorf("WAV is missing format or audio data")
	}
	return result, nil
}

func writeTemporaryWAV(directory string, sampleRate int, pcm []byte) (string, error) {
	file, err := os.CreateTemp(directory, "lifx-whisper-stream-probe-*.wav")
	if err != nil {
		return "", fmt.Errorf("create probe WAV: %w", err)
	}
	path := file.Name()
	if err := WritePCM16WAV(file, sampleRate, pcm); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

func WritePCM16WAV(writer io.Writer, sampleRate int, pcm []byte) error {
	var header bytes.Buffer
	header.WriteString("RIFF")
	_ = binary.Write(&header, binary.LittleEndian, uint32(36+len(pcm)))
	header.WriteString("WAVEfmt ")
	_ = binary.Write(&header, binary.LittleEndian, uint32(16))
	_ = binary.Write(&header, binary.LittleEndian, uint16(1))
	_ = binary.Write(&header, binary.LittleEndian, uint16(1))
	_ = binary.Write(&header, binary.LittleEndian, uint32(sampleRate))
	_ = binary.Write(&header, binary.LittleEndian, uint32(sampleRate*2))
	_ = binary.Write(&header, binary.LittleEndian, uint16(2))
	_ = binary.Write(&header, binary.LittleEndian, uint16(16))
	header.WriteString("data")
	_ = binary.Write(&header, binary.LittleEndian, uint32(len(pcm)))
	if _, err := writer.Write(header.Bytes()); err != nil {
		return fmt.Errorf("write WAV header: %w", err)
	}
	if _, err := writer.Write(pcm); err != nil {
		return fmt.Errorf("write WAV audio: %w", err)
	}
	return nil
}
