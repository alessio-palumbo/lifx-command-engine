package model

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
)

const maxModelOutputBytes = 1024 * 1024

// ProcessGenerator invokes an explicitly configured local runtime without a
// shell. The request is written to stdin and exactly one JSON value is read
// from stdout. Model and cache management remain the runtime's responsibility.
type ProcessGenerator struct {
	Path string
	Args []string
}

func (p ProcessGenerator) Generate(ctx context.Context, request Request) ([]byte, error) {
	if p.Path == "" {
		return nil, fmt.Errorf("model command is not configured")
	}
	input, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("encode model request: %w", err)
	}
	cmd := exec.CommandContext(ctx, p.Path, p.Args...)
	cmd.Stdin = bytes.NewReader(append(input, '\n'))
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &limitedWriter{Buffer: &stdout, Remaining: maxModelOutputBytes}
	cmd.Stderr = &limitedWriter{Buffer: &stderr, Remaining: 64 * 1024}
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("model command failed: %w: %s", err, stderr.String())
	}
	return stdout.Bytes(), nil
}

type limitedWriter struct {
	Buffer    *bytes.Buffer
	Remaining int
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	if len(p) > w.Remaining {
		allowed := w.Remaining
		if allowed > 0 {
			_, _ = w.Buffer.Write(p[:allowed])
		}
		w.Remaining = 0
		return allowed, fmt.Errorf("output limit exceeded")
	}
	w.Remaining -= len(p)
	return w.Buffer.Write(p)
}
