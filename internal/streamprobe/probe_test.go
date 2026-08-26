package streamprobe

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alessio-palumbo/lifx-command-engine/internal/schema"
)

type fakeTranscriber struct {
	calls        atomic.Int32
	finalAudioMS atomic.Int64
	partialDelay time.Duration
	blockPartial bool
}

func (f *fakeTranscriber) Transcribe(ctx context.Context, input schema.TranscribeInput) (schema.TranscribeResult, error) {
	call := f.calls.Add(1)
	if call == 1 {
		if f.blockPartial {
			<-ctx.Done()
			return schema.TranscribeResult{}, ctx.Err()
		}
		timer := time.NewTimer(f.partialDelay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return schema.TranscribeResult{}, ctx.Err()
		case <-timer.C:
			return schema.TranscribeResult{SchemaVersion: "1", Text: "turn desk", Language: "en", Segments: []schema.TranscribeSegment{}}, nil
		}
	}
	if audio, err := ReadPCM16WAV(input.AudioPath); err == nil {
		f.finalAudioMS.Store(audio.Duration().Milliseconds())
	}
	return schema.TranscribeResult{SchemaVersion: "1", Text: "turn desk off", Language: "en", Segments: []schema.TranscribeSegment{}}, nil
}

func TestRunPreemptsActivePartialAndUsesAuthoritativeCompleteFinal(t *testing.T) {
	audio := writeTestWAV(t, 250*time.Millisecond)
	transcriber := &fakeTranscriber{blockPartial: true}
	report, err := Run(context.Background(), transcriber, Config{
		AudioPath: audio, ExpectedText: "turn desk off", ChunkDuration: 25 * time.Millisecond,
		PartialAfter: 25 * time.Millisecond, PartialInterval: time.Second, PartialWindow: 100 * time.Millisecond,
		MaxUtterance: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.FinalTranscript.Text != "turn desk off" || report.NormalizedMatch == nil || !*report.NormalizedMatch {
		t.Fatalf("report = %#v", report)
	}
	if report.Metrics.PartialAttempts != 1 || report.Metrics.PartialCanceled != 1 || report.Metrics.PartialCompleted != 0 || transcriber.calls.Load() != 2 {
		t.Fatalf("metrics = %#v, calls = %d", report.Metrics, transcriber.calls.Load())
	}
	if transcriber.finalAudioMS.Load() != 250 {
		t.Fatalf("final audio = %dms, want complete 250ms utterance", transcriber.finalAudioMS.Load())
	}
}

func TestRunReportsPartialBeforeRelease(t *testing.T) {
	audio := writeTestWAV(t, 300*time.Millisecond)
	transcriber := &fakeTranscriber{partialDelay: 10 * time.Millisecond}
	report, err := Run(context.Background(), transcriber, Config{
		AudioPath: audio, ChunkDuration: 25 * time.Millisecond, PartialAfter: 25 * time.Millisecond,
		PartialInterval: time.Second, PartialWindow: 100 * time.Millisecond, MaxUtterance: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Partials) != 1 || report.Partials[0].Stable || !report.Metrics.PartialBeforeRelease || report.Metrics.TimeToFirstPartialMS == nil {
		t.Fatalf("report = %#v", report)
	}
}

func TestReadPCM16WAVValidationAndRoundTrip(t *testing.T) {
	path := writeTestWAV(t, 100*time.Millisecond)
	got, err := ReadPCM16WAV(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.SampleRate != 16000 || got.Channels != 1 || got.BitsPerSample != 16 || got.Duration() != 100*time.Millisecond {
		t.Fatalf("WAV = %#v, duration = %v", got, got.Duration())
	}
	bad := filepath.Join(t.TempDir(), "bad.wav")
	if err := os.WriteFile(bad, []byte("not wav"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadPCM16WAV(bad); err == nil {
		t.Fatal("invalid WAV succeeded")
	}
}

func TestRunHonorsCancellationDuringReplay(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Run(ctx, &fakeTranscriber{}, Config{AudioPath: writeTestWAV(t, 100*time.Millisecond)})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
}

func writeTestWAV(t *testing.T, duration time.Duration) string {
	t.Helper()
	pcm := make([]byte, int(duration*16000*2/time.Second))
	var output bytes.Buffer
	if err := WritePCM16WAV(&output, 16000, pcm); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "audio.wav")
	if err := os.WriteFile(path, output.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
