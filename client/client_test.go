package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestClientAgainstRealSidecar(t *testing.T) {
	binary := buildSidecar(t)
	var stderr bytes.Buffer
	client, err := New(Config{Path: binary, Stderr: &stderr})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := client.Start(ctx); err != nil {
		t.Fatalf("Start: %v; stderr=%s", err, stderr.String())
	}
	capabilities, err := client.Capabilities(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if capabilities.ProtocolVersion != ProtocolVersion || capabilities.CommandPlanSchema != "1" || capabilities.ExecutesCommands {
		t.Fatalf("capabilities = %#v", capabilities)
	}

	snapshot := DeviceSnapshot{Locations: []NamedRef{}, Groups: []NamedRef{}, Devices: []SnapshotDevice{{Serial: "d073d5000001", Label: "Desk"}}}
	const requests = 20
	var wait sync.WaitGroup
	errorsCh := make(chan error, requests)
	for range requests {
		wait.Add(1)
		go func() {
			defer wait.Done()
			plan, err := client.Interpret(ctx, InterpretInput{Text: "turn desk on", Snapshot: snapshot})
			if err != nil {
				errorsCh <- err
				return
			}
			if len(plan.Commands) != 1 || len(plan.Commands[0].Targets) != 1 || plan.Commands[0].Targets[0].Serial != "d073d5000001" {
				errorsCh <- fmt.Errorf("unexpected plan: %#v", plan)
			}
		}()
	}
	wait.Wait()
	close(errorsCh)
	for err := range errorsCh {
		t.Error(err)
	}
}

func TestClientReturnsTypedAPIError(t *testing.T) {
	client := helperClient(t, "normal", false)
	_, err := client.Transcribe(context.Background(), TranscribeInput{AudioPath: "/tmp/missing.wav"})
	var apiError *APIError
	if !errors.As(err, &apiError) || apiError.Code != "method_unavailable" {
		t.Fatalf("error = %#v", err)
	}
}

func TestTranscribeAndInterpretKeepsBothStages(t *testing.T) {
	client := helperClient(t, "pipeline", false)
	result, err := client.TranscribeAndInterpret(
		context.Background(),
		TranscribeInput{AudioPath: "/tmp/voice.wav", Language: "en"},
		DeviceSnapshot{Devices: []SnapshotDevice{{Serial: "d073d5000001", Label: "Desk"}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Transcript.Text != "turn desk on" || len(result.Plan.Commands) != 1 {
		t.Fatalf("result = %#v", result)
	}
}

func TestClientRestartsAfterCrashWhenEnabled(t *testing.T) {
	client := helperClient(t, "one-response", true)
	if _, err := client.Health(context.Background()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if _, err := client.Health(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestClientHonorsCancellation(t *testing.T) {
	client := helperClient(t, "hang", false)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := client.Health(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v", err)
	}
}

func TestClientRejectsCallsAfterClose(t *testing.T) {
	client, err := New(Config{Path: os.Args[0]})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Health(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatalf("error = %v", err)
	}
}

func helperClient(t *testing.T, mode string, restart bool) *Client {
	t.Helper()
	client, err := New(Config{
		Path:           os.Args[0],
		Args:           []string{"-test.run=TestHelperProcess"},
		Env:            []string{"LIFX_CLIENT_HELPER=1", "LIFX_CLIENT_HELPER_MODE=" + mode},
		RestartOnCrash: restart,
		Stderr:         &bytes.Buffer{},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestHelperProcess(t *testing.T) {
	if os.Getenv("LIFX_CLIENT_HELPER") != "1" {
		return
	}
	mode := os.Getenv("LIFX_CLIENT_HELPER_MODE")
	scanner := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		if mode == "hang" {
			for {
				time.Sleep(time.Hour)
			}
		}
		var request struct {
			ID     uint64 `json:"id"`
			Method string `json:"method"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			os.Exit(3)
		}
		if mode == "pipeline" && request.Method == "transcribe" {
			_ = encoder.Encode(map[string]any{"id": request.ID, "result": map[string]any{"schema_version": "1", "text": "turn desk on", "language": "en", "segments": []any{}}})
		} else if mode == "pipeline" && request.Method == "interpret" {
			_ = encoder.Encode(map[string]any{"id": request.ID, "result": map[string]any{"schema_version": "1", "confidence": 0.95, "confidence_result": map[string]any{"level": "high", "reasons": []string{"rule parser"}}, "needs_confirmation": false, "summary": "Turn on Desk", "commands": []any{map[string]any{"targets": []any{map[string]any{"serial": "d073d5000001", "label": "Desk"}}, "action": map[string]any{"power": true}}}}})
		} else if request.Method == "transcribe" {
			_ = encoder.Encode(map[string]any{"id": request.ID, "error": map[string]any{"code": "method_unavailable", "message": "transcription is not configured"}})
		} else {
			_ = encoder.Encode(map[string]any{"id": request.ID, "result": map[string]any{"status": "ok"}})
		}
		if mode == "one-response" {
			return
		}
	}
}

func buildSidecar(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "lifx-command-engine")
	command := exec.Command("go", "build", "-o", binary, "../cmd/lifx-command-engine")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build sidecar: %v: %s", err, output)
	}
	return binary
}
