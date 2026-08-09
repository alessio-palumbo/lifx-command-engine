package model

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
)

func TestProcessGenerator(t *testing.T) {
	g := ProcessGenerator{Path: os.Args[0], Args: []string{"-test.run=TestProcessHelper", "--", "model-helper"}}
	out, err := g.Generate(context.Background(), Request{DeveloperInstruction: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(out)) != `{"ok":true}` {
		t.Fatalf("output = %q", out)
	}
}

func TestProcessHelper(t *testing.T) {
	if len(os.Args) == 0 || os.Args[len(os.Args)-1] != "model-helper" {
		return
	}
	if _, err := io.ReadAll(os.Stdin); err != nil {
		os.Exit(2)
	}
	_, _ = os.Stdout.WriteString(`{"ok":true}`)
	os.Exit(0)
}
