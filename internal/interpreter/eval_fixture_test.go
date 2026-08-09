package interpreter

import (
	"bufio"
	"encoding/json"
	"os"
	"testing"

	"github.com/alessio-palumbo/lifx-command-engine/internal/schema"
)

func TestFunctionGemmaEvalFixture(t *testing.T) {
	f, err := os.Open("../../testdata/functiongemma-eval.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	seen := map[string]bool{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var example struct {
			Name     string                `json:"name"`
			Input    schema.InterpretInput `json:"input"`
			Expected json.RawMessage       `json:"expected"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &example); err != nil {
			t.Fatalf("invalid fixture: %v", err)
		}
		if example.Name == "" || example.Input.Text == "" || len(example.Expected) == 0 {
			t.Fatalf("incomplete fixture: %s", scanner.Bytes())
		}
		if seen[example.Name] {
			t.Fatalf("duplicate fixture %q", example.Name)
		}
		seen[example.Name] = true
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(seen) < 6 {
		t.Fatalf("expected at least 6 cases, got %d", len(seen))
	}
}
