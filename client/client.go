package client

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

const (
	ProtocolVersion  = "1"
	maxResponseBytes = 1024 * 1024
)

var ErrClosed = errors.New("lifx command engine client is closed")

type Config struct {
	Path            string
	Args            []string
	Dir             string
	Env             []string
	RestartOnCrash  bool
	StartupTimeout  time.Duration
	ShutdownTimeout time.Duration
	Stderr          io.Writer
}

type Client struct {
	config Config
	stderr *lockedWriter
	nextID atomic.Uint64

	mu      sync.Mutex
	process *process
	closed  bool
	pending map[uint64]chan response
	writeMu sync.Mutex
}

type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (w *lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.w.Write(p)
}

type process struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	done  chan struct{}
	once  sync.Once
	err   error
}

type request struct {
	ID              uint64 `json:"id"`
	ProtocolVersion string `json:"protocol_version"`
	Method          string `json:"method"`
	Params          any    `json:"params,omitempty"`
}

type response struct {
	ID     uint64          `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *APIError       `json:"error,omitempty"`
	err    error
}

type APIError struct {
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *APIError) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

func New(config Config) (*Client, error) {
	if config.Path == "" {
		return nil, fmt.Errorf("sidecar path is required")
	}
	if config.StartupTimeout <= 0 {
		config.StartupTimeout = 5 * time.Second
	}
	if config.ShutdownTimeout <= 0 {
		config.ShutdownTimeout = 2 * time.Second
	}
	if config.Stderr == nil {
		config.Stderr = os.Stderr
	}
	return &Client{config: config, stderr: &lockedWriter{w: config.Stderr}, pending: make(map[uint64]chan response)}, nil
}

func (c *Client) Start(ctx context.Context) error {
	if _, err := c.ensureProcess(); err != nil {
		return err
	}
	startupCtx, cancel := context.WithTimeout(ctx, c.config.StartupTimeout)
	defer cancel()
	result, err := c.Health(startupCtx)
	if err != nil {
		c.stopProcess()
		return fmt.Errorf("sidecar readiness: %w", err)
	}
	if result.Status != "ok" {
		c.stopProcess()
		return fmt.Errorf("sidecar readiness returned status %q", result.Status)
	}
	return nil
}

func (c *Client) Health(ctx context.Context) (HealthResult, error) {
	var result HealthResult
	err := c.call(ctx, "health", nil, &result)
	return result, err
}

func (c *Client) Capabilities(ctx context.Context) (Capabilities, error) {
	var result Capabilities
	err := c.call(ctx, "capabilities", nil, &result)
	return result, err
}

func (c *Client) Interpret(ctx context.Context, input InterpretInput) (CommandPlan, error) {
	var result CommandPlan
	err := c.call(ctx, "interpret", input, &result)
	return result, err
}

func (c *Client) Transcribe(ctx context.Context, input TranscribeInput) (TranscribeResult, error) {
	var result TranscribeResult
	err := c.call(ctx, "transcribe", input, &result)
	return result, err
}

func (c *Client) TranscribeAndInterpret(ctx context.Context, audio TranscribeInput, snapshot DeviceSnapshot) (SpeechCommandResult, error) {
	transcript, err := c.Transcribe(ctx, audio)
	if err != nil {
		return SpeechCommandResult{}, err
	}
	plan, err := c.Interpret(ctx, InterpretInput{Text: transcript.Text, Snapshot: snapshot})
	if err != nil {
		return SpeechCommandResult{Transcript: transcript}, err
	}
	return SpeechCommandResult{Transcript: transcript, Plan: plan}, nil
}

func (c *Client) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	p := c.process
	c.process = nil
	c.failPendingLocked(ErrClosed)
	c.mu.Unlock()
	return stop(p, c.config.ShutdownTimeout)
}

func (c *Client) call(ctx context.Context, method string, params, destination any) error {
	p, err := c.ensureProcess()
	if err != nil {
		return err
	}
	id := c.nextID.Add(1)
	encoded, err := json.Marshal(request{ID: id, ProtocolVersion: ProtocolVersion, Method: method, Params: params})
	if err != nil {
		return fmt.Errorf("encode %s request: %w", method, err)
	}
	result := make(chan response, 1)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrClosed
	}
	if c.process != p {
		c.mu.Unlock()
		return fmt.Errorf("sidecar stopped before %s request", method)
	}
	c.pending[id] = result
	c.mu.Unlock()

	c.writeMu.Lock()
	_, writeErr := p.stdin.Write(append(encoded, '\n'))
	c.writeMu.Unlock()
	if writeErr != nil {
		c.removePending(id)
		c.failProcess(p, fmt.Errorf("write sidecar request: %w", writeErr))
		return fmt.Errorf("write %s request: %w", method, writeErr)
	}
	select {
	case received := <-result:
		if received.err != nil {
			return received.err
		}
		if received.Error != nil {
			return received.Error
		}
		if err := json.Unmarshal(received.Result, destination); err != nil {
			return fmt.Errorf("decode %s result: %w", method, err)
		}
		return nil
	case <-ctx.Done():
		c.removePending(id)
		return ctx.Err()
	}
}

func (c *Client) ensureProcess() (*process, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, ErrClosed
	}
	if c.process != nil {
		return c.process, nil
	}
	if c.nextID.Load() > 0 && !c.config.RestartOnCrash {
		return nil, fmt.Errorf("sidecar is not running")
	}
	cmd := exec.Command(c.config.Path, c.config.Args...)
	cmd.Dir = c.config.Dir
	if c.config.Env != nil {
		cmd.Env = append(os.Environ(), c.config.Env...)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open sidecar stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("open sidecar stdout: %w", err)
	}
	cmd.Stderr = c.stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start sidecar: %w", err)
	}
	p := &process{cmd: cmd, stdin: stdin, done: make(chan struct{})}
	c.process = p
	go c.readResponses(p, stdout)
	go func() {
		err := cmd.Wait()
		c.failProcess(p, fmt.Errorf("sidecar exited: %w", err))
	}()
	return p, nil
}

func (c *Client) readResponses(p *process, stdout io.Reader) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), maxResponseBytes)
	for scanner.Scan() {
		var message response
		if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
			c.failProcess(p, fmt.Errorf("decode sidecar response: %w", err))
			return
		}
		c.mu.Lock()
		channel := c.pending[message.ID]
		delete(c.pending, message.ID)
		c.mu.Unlock()
		if channel != nil {
			channel <- message
		}
	}
	if err := scanner.Err(); err != nil {
		c.failProcess(p, fmt.Errorf("read sidecar response: %w", err))
	}
}

func (c *Client) failProcess(p *process, err error) {
	if p == nil {
		return
	}
	p.once.Do(func() {
		p.err = err
		if p.cmd.Process != nil {
			_ = p.cmd.Process.Kill()
		}
		close(p.done)
		c.mu.Lock()
		if c.process == p {
			c.process = nil
			c.failPendingLocked(err)
		}
		c.mu.Unlock()
	})
}

func (c *Client) failPendingLocked(err error) {
	for id, channel := range c.pending {
		channel <- response{err: err}
		delete(c.pending, id)
	}
}

func (c *Client) removePending(id uint64) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

func (c *Client) stopProcess() {
	c.mu.Lock()
	p := c.process
	c.process = nil
	c.mu.Unlock()
	_ = stop(p, c.config.ShutdownTimeout)
}

func stop(p *process, gracePeriod time.Duration) error {
	if p == nil {
		return nil
	}
	_ = p.stdin.Close()
	timer := time.NewTimer(gracePeriod)
	defer timer.Stop()
	select {
	case <-p.done:
		return nil
	case <-timer.C:
	}
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
	<-p.done
	return nil
}
