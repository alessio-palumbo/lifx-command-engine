package model

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPersistentProcessGeneratorReusesProcessAndRecoversFromRequestError(t *testing.T) {
	g := &PersistentProcessGenerator{Path: os.Args[0], Args: []string{"-test.run=TestPersistentProcessHelper", "--", "persistent-model-helper"}}
	t.Cleanup(func() { _ = g.Close() })
	first, err := g.Generate(context.Background(), Request{ContractVersion: "1"})
	if err != nil || strings.TrimSpace(string(first)) != `{"sequence":1}` {
		t.Fatalf("first=%s err=%v", first, err)
	}
	if _, err := g.Generate(context.Background(), Request{ContractVersion: "1", DeveloperInstruction: "fail"}); err == nil {
		t.Fatal("expected request error")
	}
	third, err := g.Generate(context.Background(), Request{ContractVersion: "1"})
	if err != nil || strings.TrimSpace(string(third)) != `{"sequence":3}` {
		t.Fatalf("third=%s err=%v", third, err)
	}
}

func TestPersistentProcessGeneratorRestartsAfterCancellation(t *testing.T) {
	g := &PersistentProcessGenerator{Path: os.Args[0], Args: []string{"-test.run=TestPersistentProcessHelper", "--", "persistent-model-helper"}}
	t.Cleanup(func() { _ = g.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := g.Generate(ctx, Request{ContractVersion: "1", DeveloperInstruction: "hang"}); err == nil {
		t.Fatal("expected cancellation")
	}
	result, err := g.Generate(context.Background(), Request{ContractVersion: "1"})
	if err != nil || strings.TrimSpace(string(result)) != `{"sequence":1}` {
		t.Fatalf("result=%s err=%v", result, err)
	}
}

func TestPersistentProcessHelper(t *testing.T) {
	found := false
	for _, arg := range os.Args {
		if arg == "persistent-model-helper" {
			found = true
		}
	}
	if !found {
		return
	}
	writer := bufio.NewWriter(os.Stdout)
	_, _ = writer.WriteString(`{"type":"ready","contract_version":"1"}` + "\n")
	_ = writer.Flush()
	scanner := bufio.NewScanner(os.Stdin)
	sequence := 0
	for scanner.Scan() {
		sequence++
		var request Request
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			os.Exit(2)
		}
		if request.DeveloperInstruction == "hang" {
			time.Sleep(time.Hour)
		}
		if request.DeveloperInstruction == "fail" {
			_, _ = writer.WriteString(`{"contract_version":"1","error":"requested failure"}` + "\n")
		} else {
			response := map[string]any{"contract_version": "1", "result": map[string]int{"sequence": sequence}}
			encoded, _ := json.Marshal(response)
			_, _ = writer.Write(append(encoded, '\n'))
		}
		_ = writer.Flush()
	}
	os.Exit(0)
}
