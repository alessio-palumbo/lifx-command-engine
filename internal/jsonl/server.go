// Package jsonl implements the engine's newline-delimited JSON protocol.
package jsonl

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/alessio-palumbo/lifx-command-engine/internal/interpreter"
	"github.com/alessio-palumbo/lifx-command-engine/internal/schema"
)

const (
	ProtocolVersion = "1"
	MaxRequestBytes = 1024 * 1024
)

type Request struct {
	ID              json.RawMessage `json:"id"`
	ProtocolVersion string          `json:"protocol_version,omitempty"`
	Method          string          `json:"method"`
	Params          json.RawMessage `json:"params,omitempty"`
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

type Server struct {
	Interpreter  interpreter.Interpreter
	Capabilities schema.Capabilities
}

func (s Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	reader := bufio.NewReader(in)
	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)
	for {
		line, tooLarge, err := readLine(reader)
		if tooLarge {
			if encodeErr := enc.Encode(fail(nil, "request_too_large", "request exceeds 1 MiB", nil)); encodeErr != nil {
				return fmt.Errorf("encode response: %w", encodeErr)
			}
		}
		if err != nil && err != io.EOF {
			return fmt.Errorf("read request: %w", err)
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			if err == io.EOF {
				return nil
			}
			continue
		}
		resp := s.handle(ctx, line)
		if err := enc.Encode(resp); err != nil {
			return fmt.Errorf("encode response: %w", err)
		}
		if err == io.EOF {
			return nil
		}
	}
}

func (s Server) handle(ctx context.Context, line []byte) Response {
	var req Request
	if err := decodeStrict(line, &req); err != nil {
		return fail(nil, "parse_error", "invalid JSON", err.Error())
	}
	if len(req.ID) == 0 || string(req.ID) == "null" {
		return fail(nil, "invalid_request", "id is required", nil)
	}
	if req.Method == "" {
		return fail(req.ID, "invalid_request", "method is required", nil)
	}
	if req.ProtocolVersion != "" && req.ProtocolVersion != ProtocolVersion {
		return fail(req.ID, "unsupported_protocol_version", "unsupported protocol version: "+req.ProtocolVersion, map[string]string{"supported": ProtocolVersion})
	}
	switch req.Method {
	case "health":
		return Response{ID: req.ID, Result: map[string]string{"status": "ok"}}
	case "capabilities":
		capabilities := s.Capabilities
		if capabilities.ProtocolVersion == "" {
			capabilities = schema.Capabilities{ProtocolVersion: ProtocolVersion, CommandPlanSchema: "1", Methods: []string{"health", "capabilities", "interpret"}, Interpreters: []string{"rules"}, Transcription: false, ExecutesCommands: false}
		}
		return Response{ID: req.ID, Result: capabilities}
	case "interpret":
		var input schema.InterpretInput
		if len(req.Params) == 0 {
			return fail(req.ID, "invalid_params", "params are required", nil)
		}
		decErr := decodeStrict(req.Params, &input)
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

func decodeStrict(data []byte, dst any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fmt.Errorf("multiple JSON values")
	}
	return nil
}

func readLine(r *bufio.Reader) ([]byte, bool, error) {
	var line []byte
	tooLarge := false
	for {
		part, err := r.ReadSlice('\n')
		if !tooLarge {
			if len(line)+len(part) > MaxRequestBytes {
				tooLarge = true
				line = nil
			} else {
				line = append(line, part...)
			}
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		return line, tooLarge, err
	}
}

func fail(id json.RawMessage, code, message string, data any) Response {
	return Response{ID: id, Error: &Error{Code: code, Message: message, Data: data}}
}
