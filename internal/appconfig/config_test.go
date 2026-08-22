package appconfig

import (
	"strings"
	"testing"
)

func TestDecode(t *testing.T) {
	config, err := Decode(strings.NewReader(`{
  "schema_version":"1",
  "model":{"command":"python","args":["runner.py"],"persistent":true},
  "whisper":{"command":"whisper-server","model_path":"small.bin","persistent":true}
}`))
	if err != nil {
		t.Fatal(err)
	}
	if config.Model.Command != "python" || !config.Model.Persistent || config.Whisper.ModelPath != "small.bin" || !config.Whisper.Persistent {
		t.Fatalf("config = %#v", config)
	}
}

func TestDecodeRejectsUnknownAndIncompleteConfig(t *testing.T) {
	for _, input := range []string{
		`{"schema_version":"1","unknown":true}`,
		`{"schema_version":"2"}`,
		`{"schema_version":"1","whisper":{"command":"whisper-cli"}}`,
		`{"schema_version":"1","whisper":{"persistent":true}}`,
		`{"schema_version":"1","model":{"persistent":true}}`,
	} {
		if _, err := Decode(strings.NewReader(input)); err == nil {
			t.Fatalf("Decode(%s) succeeded", input)
		}
	}
}
