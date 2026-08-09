// Package whispercpp adapts the whisper.cpp command-line client.
package whispercpp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/alessio-palumbo/lifx-command-engine/internal/schema"
	"github.com/alessio-palumbo/lifx-command-engine/internal/speech"
)

const maxOutputBytes = 16 * 1024 * 1024

type Transcriber struct {
	Command   string
	ModelPath string
	Args      []string
}

func (t Transcriber) Transcribe(ctx context.Context, input schema.TranscribeInput) (schema.TranscribeResult, error) {
	if strings.TrimSpace(input.AudioPath) == "" {
		return schema.TranscribeResult{}, invalid("audio_path must not be empty")
	}
	info, err := os.Stat(input.AudioPath)
	if err != nil {
		return schema.TranscribeResult{}, &speech.InvalidInputError{Err: fmt.Errorf("audio_path: %w", err)}
	}
	if !info.Mode().IsRegular() {
		return schema.TranscribeResult{}, invalid("audio_path must be a regular file")
	}
	if t.Command == "" || t.ModelPath == "" {
		return schema.TranscribeResult{}, &speech.RuntimeError{Err: fmt.Errorf("whisper.cpp command and model must be configured")}
	}
	language := input.Language
	if language == "" {
		language = "auto"
	}
	args := append([]string{}, t.Args...)
	outputDir, err := os.MkdirTemp("", "lifx-command-engine-whisper-")
	if err != nil {
		return schema.TranscribeResult{}, &speech.RuntimeError{Err: fmt.Errorf("create output directory: %w", err)}
	}
	defer os.RemoveAll(outputDir)
	outputBase := outputDir + string(os.PathSeparator) + "transcript"
	args = append(args, "--model", t.ModelPath, "--file", input.AudioPath, "--language", language, "--output-json", "--output-file", outputBase, "--no-prints")
	cmd := exec.CommandContext(ctx, t.Command, args...)
	var stdout, stderr limitedBuffer
	stdout.remaining, stderr.remaining = maxOutputBytes, 64*1024
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return schema.TranscribeResult{}, &speech.RuntimeError{Err: fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))}
	}
	outputFile, err := os.Open(outputBase + ".json")
	if err != nil {
		return schema.TranscribeResult{}, &speech.RuntimeError{Err: fmt.Errorf("open whisper.cpp JSON: %w", err)}
	}
	defer outputFile.Close()
	raw, err := io.ReadAll(io.LimitReader(outputFile, maxOutputBytes+1))
	if err != nil {
		return schema.TranscribeResult{}, &speech.RuntimeError{Err: fmt.Errorf("read whisper.cpp JSON: %w", err)}
	}
	if len(raw) > maxOutputBytes {
		return schema.TranscribeResult{}, &speech.RuntimeError{Err: fmt.Errorf("whisper.cpp JSON exceeds output limit")}
	}
	result, err := decodeOutput(raw)
	if err != nil {
		return schema.TranscribeResult{}, &speech.RuntimeError{Err: err}
	}
	return result, nil
}

type output struct {
	Result struct {
		Language string `json:"language"`
	} `json:"result"`
	Transcription []struct {
		Offsets struct {
			From int64 `json:"from"`
			To   int64 `json:"to"`
		} `json:"offsets"`
		Text string `json:"text"`
	} `json:"transcription"`
}

func decodeOutput(data []byte) (schema.TranscribeResult, error) {
	var decoded output
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&decoded); err != nil {
		return schema.TranscribeResult{}, fmt.Errorf("decode whisper.cpp JSON: %w", err)
	}
	result := schema.TranscribeResult{SchemaVersion: "1", Language: decoded.Result.Language, Segments: make([]schema.TranscribeSegment, 0, len(decoded.Transcription))}
	texts := make([]string, 0, len(decoded.Transcription))
	for _, segment := range decoded.Transcription {
		text := strings.TrimSpace(segment.Text)
		result.Segments = append(result.Segments, schema.TranscribeSegment{StartMS: segment.Offsets.From, EndMS: segment.Offsets.To, Text: text})
		if text != "" {
			texts = append(texts, text)
		}
	}
	result.Text = strings.Join(texts, " ")
	return result, nil
}

func invalid(message string) error { return &speech.InvalidInputError{Err: fmt.Errorf("%s", message)} }

type limitedBuffer struct {
	bytes.Buffer
	remaining int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if len(p) > b.remaining {
		allowed := b.remaining
		if allowed > 0 {
			_, _ = b.Buffer.Write(p[:allowed])
		}
		b.remaining = 0
		return allowed, fmt.Errorf("output limit exceeded")
	}
	b.remaining -= len(p)
	return b.Buffer.Write(p)
}
