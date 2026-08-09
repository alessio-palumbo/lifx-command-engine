package whispercpp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alessio-palumbo/lifx-command-engine/internal/schema"
	"github.com/alessio-palumbo/lifx-command-engine/internal/speech"
)

func TestTranscribe(t *testing.T) {
	audio := filepath.Join(t.TempDir(), "sample.wav")
	if err := os.WriteFile(audio, []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
	transcriber := Transcriber{Command: os.Args[0], ModelPath: "model.bin", Args: []string{"-test.run=TestWhisperHelper", "--", "whisper-helper"}}
	got, err := transcriber.Transcribe(context.Background(), schema.TranscribeInput{AudioPath: audio})
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "turn the desk on" || got.Language != "en" || len(got.Segments) != 2 || got.Segments[1].StartMS != 500 {
		t.Fatalf("result = %#v", got)
	}
}

func TestTranscribeRejectsInvalidPath(t *testing.T) {
	_, err := (Transcriber{}).Transcribe(context.Background(), schema.TranscribeInput{AudioPath: filepath.Join(t.TempDir(), "missing.wav")})
	var invalid *speech.InvalidInputError
	if err == nil || !strings.Contains(err.Error(), "audio_path") || !errors.As(err, &invalid) {
		t.Fatalf("error = %v", err)
	}
}

func TestDecodeOutputRejectsMalformedJSON(t *testing.T) {
	if _, err := decodeOutput([]byte("not json")); err == nil {
		t.Fatal("expected error")
	}
}

func TestWhisperHelper(t *testing.T) {
	found := false
	for _, arg := range os.Args {
		if arg == "whisper-helper" {
			found = true
		}
	}
	if !found {
		return
	}
	var outputBase string
	for i, arg := range os.Args {
		if arg == "--output-file" && i+1 < len(os.Args) {
			outputBase = os.Args[i+1]
		}
	}
	if outputBase == "" {
		os.Exit(2)
	}
	if err := os.WriteFile(outputBase+".json", []byte(`{"result":{"language":"en"},"transcription":[{"offsets":{"from":0,"to":500},"text":" turn the desk"},{"offsets":{"from":500,"to":900},"text":" on "}]}`), 0o600); err != nil {
		os.Exit(3)
	}
	os.Exit(0)
}
