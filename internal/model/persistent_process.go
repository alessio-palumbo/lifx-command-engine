package model

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sync"
)

// PersistentProcessGenerator keeps a versioned JSONL model runtime alive and
// serializes requests through it. A failed or cancelled process is discarded
// and started again on the next request.
type PersistentProcessGenerator struct {
	Path string
	Args []string

	mu       sync.Mutex
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	stdout   *bufio.Reader
	stderr   rollingBuffer
	stderrWG sync.WaitGroup
}

type persistentMessage struct {
	Type            string          `json:"type,omitempty"`
	ContractVersion string          `json:"contract_version"`
	Result          json.RawMessage `json:"result,omitempty"`
	Error           string          `json:"error,omitempty"`
}

func (p *PersistentProcessGenerator) Generate(ctx context.Context, request Request) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.Path == "" {
		return nil, fmt.Errorf("model command is not configured")
	}
	if err := p.ensureStarted(ctx); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("encode model request: %w", err)
	}
	if _, err := p.stdin.Write(append(encoded, '\n')); err != nil {
		p.stopLocked()
		return nil, fmt.Errorf("write persistent model request: %w", err)
	}
	line, err := p.readLine(ctx)
	if err != nil {
		p.stopLocked()
		detail := p.stderr.String()
		return nil, fmt.Errorf("read persistent model response: %w: %s", err, detail)
	}
	var response persistentMessage
	if err := json.Unmarshal(line, &response); err != nil {
		p.stopLocked()
		return nil, fmt.Errorf("decode persistent model response: %w", err)
	}
	if response.ContractVersion != "1" {
		p.stopLocked()
		return nil, fmt.Errorf("unsupported persistent model contract version %q", response.ContractVersion)
	}
	if response.Error != "" {
		return nil, fmt.Errorf("persistent model runtime: %s", response.Error)
	}
	if len(response.Result) == 0 {
		p.stopLocked()
		return nil, fmt.Errorf("persistent model response has no result")
	}
	return append([]byte(nil), response.Result...), nil
}

func (p *PersistentProcessGenerator) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stopLocked()
}

func (p *PersistentProcessGenerator) ensureStarted(ctx context.Context) error {
	if p.cmd != nil {
		return nil
	}
	args := append(append([]string(nil), p.Args...), "--serve")
	cmd := exec.Command(p.Path, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("open model stdin: %w", err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("open model stdout: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("open model stderr: %w", err)
	}
	p.stderr.Reset()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start persistent model runtime: %w", err)
	}
	p.cmd, p.stdin, p.stdout = cmd, stdin, bufio.NewReader(stdoutPipe)
	p.stderrWG.Add(1)
	go func() { defer p.stderrWG.Done(); _, _ = io.Copy(&p.stderr, stderrPipe) }()
	line, err := p.readLine(ctx)
	if err != nil {
		p.stopLocked()
		detail := p.stderr.String()
		return fmt.Errorf("persistent model handshake: %w: %s", err, detail)
	}
	var ready persistentMessage
	if err := json.Unmarshal(line, &ready); err != nil {
		p.stopLocked()
		return fmt.Errorf("decode persistent model handshake: %w", err)
	}
	if ready.Type != "ready" || ready.ContractVersion != "1" {
		p.stopLocked()
		return fmt.Errorf("invalid persistent model handshake")
	}
	return nil
}

func (p *PersistentProcessGenerator) readLine(ctx context.Context) ([]byte, error) {
	type result struct {
		line []byte
		err  error
	}
	resultCh := make(chan result, 1)
	go func() {
		line, err := readBoundedLine(p.stdout, maxModelOutputBytes)
		resultCh <- result{line: line, err: err}
	}()
	select {
	case result := <-resultCh:
		return result.line, result.err
	case <-ctx.Done():
		if p.cmd != nil && p.cmd.Process != nil {
			_ = p.cmd.Process.Kill()
		}
		<-resultCh
		return nil, ctx.Err()
	}
}

func (p *PersistentProcessGenerator) stopLocked() error {
	if p.cmd == nil {
		return nil
	}
	_ = p.stdin.Close()
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
	_ = p.cmd.Wait()
	p.stderrWG.Wait()
	p.cmd, p.stdin, p.stdout = nil, nil, nil
	return nil
}

func readBoundedLine(reader *bufio.Reader, limit int) ([]byte, error) {
	var output bytes.Buffer
	for {
		part, err := reader.ReadSlice('\n')
		if output.Len()+len(part) > limit {
			return nil, fmt.Errorf("model response exceeds %d bytes", limit)
		}
		_, _ = output.Write(part)
		if err == bufio.ErrBufferFull {
			continue
		}
		if err != nil {
			return nil, err
		}
		return bytes.TrimSpace(output.Bytes()), nil
	}
}

type rollingBuffer struct {
	mu   sync.Mutex
	data []byte
}

func (b *rollingBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.data = append(b.data, p...)
	if len(b.data) > 64*1024 {
		b.data = append([]byte(nil), b.data[len(b.data)-64*1024:]...)
	}
	return len(p), nil
}
func (b *rollingBuffer) String() string { b.mu.Lock(); defer b.mu.Unlock(); return string(b.data) }
func (b *rollingBuffer) Reset()         { b.mu.Lock(); defer b.mu.Unlock(); b.data = nil }
