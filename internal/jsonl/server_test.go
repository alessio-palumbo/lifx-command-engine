package jsonl

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/alessio-palumbo/lifx-command-engine/internal/interpreter"
	"github.com/alessio-palumbo/lifx-command-engine/internal/schema"
)

type fakeTranscriber struct {
	result schema.TranscribeResult
	err    error
}

func (f fakeTranscriber) Transcribe(context.Context, schema.TranscribeInput) (schema.TranscribeResult, error) {
	return f.result, f.err
}

func TestServerJSONL(t *testing.T) {
	input := strings.Join([]string{
		`{"id":"h","method":"health"}`,
		`{"id":2,"method":"capabilities"}`,
		`{"id":"i","method":"interpret","params":{"text":"desk on","snapshot":{"locations":[],"groups":[],"devices":[{"serial":"d073d5000001","label":"Desk"}]}}}`,
		`{"id":"x","method":"missing"}`,
	}, "\n")
	var out bytes.Buffer
	if err := (Server{Interpreter: interpreter.RuleInterpreter{}}).Serve(context.Background(), strings.NewReader(input), &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 4 {
		t.Fatalf("got %d lines: %s", len(lines), out.String())
	}
	var interpret map[string]any
	if err := json.Unmarshal([]byte(lines[2]), &interpret); err != nil {
		t.Fatal(err)
	}
	result := interpret["result"].(map[string]any)
	if len(result["commands"].([]any)) != 1 {
		t.Fatalf("response: %s", lines[2])
	}
	var missing Response
	if err := json.Unmarshal([]byte(lines[3]), &missing); err != nil {
		t.Fatal(err)
	}
	if missing.Error == nil || missing.Error.Code != "method_not_found" {
		t.Fatalf("response: %#v", missing)
	}
}

func TestServerMalformedAndInvalidParams(t *testing.T) {
	input := "not json\n" + `{"id":1,"method":"interpret","params":{"text":"","snapshot":{}}}`
	var out bytes.Buffer
	if err := (Server{Interpreter: interpreter.RuleInterpreter{}}).Serve(context.Background(), strings.NewReader(input), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"parse_error"`) || !strings.Contains(out.String(), `"invalid_params"`) {
		t.Fatalf("responses: %s", out.String())
	}
}

func TestServerRejectsUnknownFieldsAndVersions(t *testing.T) {
	input := strings.Join([]string{
		`{"id":1,"method":"health","unexpected":true}`,
		`{"id":2,"protocol_version":"99","method":"health"}`,
		`{"id":3,"method":"interpret","params":{"text":"desk on","snapshot":{},"unexpected":true}}`,
	}, "\n")
	var out bytes.Buffer
	if err := (Server{Interpreter: interpreter.RuleInterpreter{}}).Serve(context.Background(), strings.NewReader(input), &out); err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"parse_error", "unsupported_protocol_version", "invalid_params"} {
		if !strings.Contains(out.String(), `"`+code+`"`) {
			t.Errorf("missing %s in %s", code, out.String())
		}
	}
}

func TestServerRejectsOversizedRequestAndContinues(t *testing.T) {
	input := strings.Repeat("x", MaxRequestBytes+1) + "\n" + `{"id":2,"method":"health"}`
	var out bytes.Buffer
	if err := (Server{Interpreter: interpreter.RuleInterpreter{}}).Serve(context.Background(), strings.NewReader(input), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"request_too_large"`) || !strings.Contains(out.String(), `"status":"ok"`) {
		t.Fatalf("responses: %s", out.String())
	}
}

func TestServerTranscribe(t *testing.T) {
	input := `{"id":"t","method":"transcribe","params":{"audio_path":"/tmp/voice.wav"}}`
	var out bytes.Buffer
	s := Server{Interpreter: interpreter.RuleInterpreter{}, Transcriber: fakeTranscriber{result: schema.TranscribeResult{Text: "desk on", Language: "en", Segments: []schema.TranscribeSegment{}}}}
	if err := s.Serve(context.Background(), strings.NewReader(input), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"text":"desk on"`) {
		t.Fatalf("response: %s", out.String())
	}
}

func TestServerTranscribeUnavailable(t *testing.T) {
	input := `{"id":"t","method":"transcribe","params":{"audio_path":"/tmp/voice.wav"}}`
	var out bytes.Buffer
	if err := (Server{Interpreter: interpreter.RuleInterpreter{}}).Serve(context.Background(), strings.NewReader(input), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"method_unavailable"`) {
		t.Fatalf("response: %s", out.String())
	}
}
