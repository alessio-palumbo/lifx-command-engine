// Package jsonl implements the engine's newline-delimited JSON protocol.
package jsonl

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/alessio-palumbo/lifx-command-engine/internal/interpreter"
	"github.com/alessio-palumbo/lifx-command-engine/internal/schema"
)

type Request struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}
type Response struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Result any             `json:"result,omitempty"`
	Error  *Error          `json:"error,omitempty"`
}
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type Server struct{ Interpreter interpreter.Interpreter }

func (s Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		resp := s.handle(ctx, []byte(line))
		if err := enc.Encode(resp); err != nil {
			return fmt.Errorf("encode response: %w", err)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read request: %w", err)
	}
	return nil
}

func (s Server) handle(ctx context.Context, line []byte) Response {
	var req Request
	if err := json.Unmarshal(line, &req); err != nil {
		return fail(nil, "parse_error", "invalid JSON", err.Error())
	}
	if len(req.ID) == 0 || string(req.ID) == "null" {
		return fail(nil, "invalid_request", "id is required", nil)
	}
	if req.Method == "" {
		return fail(req.ID, "invalid_request", "method is required", nil)
	}
	switch req.Method {
	case "health":
		return Response{ID: req.ID, Result: map[string]string{"status": "ok"}}
	case "capabilities":
		return Response{ID: req.ID, Result: schema.Capabilities{ProtocolVersion: "1", Methods: []string{"health", "capabilities", "interpret"}, Interpreters: []string{"rules"}, Transcription: false, ExecutesCommands: false}}
	case "interpret":
		var input schema.InterpretInput
		if len(req.Params) == 0 {
			return fail(req.ID, "invalid_params", "params are required", nil)
		}
		decErr := json.Unmarshal(req.Params, &input)
		if decErr != nil {
			return fail(req.ID, "invalid_params", "invalid interpret params", decErr.Error())
		}
		plan, err := s.Interpreter.Interpret(ctx, input)
		if err != nil {
			return fail(req.ID, "invalid_params", err.Error(), nil)
		}
		return Response{ID: req.ID, Result: plan}
	default:
		return fail(req.ID, "method_not_found", "unknown method: "+req.Method, nil)
	}
}

func fail(id json.RawMessage, code, message string, data any) Response {
	return Response{ID: id, Error: &Error{Code: code, Message: message, Data: data}}
}
