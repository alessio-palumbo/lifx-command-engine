package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultCommandStillServesJSONL(t *testing.T) {
	input := strings.NewReader("{\"id\":\"health\",\"method\":\"health\"}\n")
	var output, stderr bytes.Buffer
	if err := run(nil, input, &output, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `"status":"ok"`) {
		t.Fatalf("output = %s", output.String())
	}
}

func TestConfigEnablesWhisperCapability(t *testing.T) {
	dir := t.TempDir()
	model := filepath.Join(dir, "small.bin")
	if err := os.WriteFile(model, []byte("model"), 0o600); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(dir, "config.json")
	contents := `{"schema_version":"1","whisper":{"command":"whisper-cli","model_path":` + quote(model) + `}}`
	if err := os.WriteFile(config, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	input := strings.NewReader("{\"id\":\"caps\",\"method\":\"capabilities\"}\n")
	var output, stderr bytes.Buffer
	if err := run([]string{"serve", "-config", config}, input, &output, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `"transcription":true`) {
		t.Fatalf("output = %s", output.String())
	}
}

func TestRuntimeOptionsPreserveCLIWhisperAndValidatePersistentArgs(t *testing.T) {
	var stderr bytes.Buffer
	options, err := parseRuntimeOptions("test", []string{"-whisper-command", "whisper-cli", "-whisper-model", "model.bin"}, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if options.whisper.Persistent || options.whisper.Command != "whisper-cli" {
		t.Fatalf("options = %#v", options.whisper)
	}
	options, err = parseRuntimeOptions("test", []string{"-whisper-command", "whisper-server", "-whisper-model", "model.bin", "-whisper-persistent", "-whisper-arg=--host=0.0.0.0"}, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if err := options.validate(); err == nil || !strings.Contains(err.Error(), "--host") {
		t.Fatalf("validate error = %v", err)
	}
}

func TestModelsList(t *testing.T) {
	var output, stderr bytes.Buffer
	if err := run([]string{"models", "list"}, strings.NewReader(""), &output, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "functiongemma-270m-it") {
		t.Fatalf("output = %s", output.String())
	}
}

func quote(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
}
