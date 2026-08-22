package diagnostics

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRunReportsOptionalRuntimesAsWarnings(t *testing.T) {
	report := Run(context.Background(), Runtime{})
	if report.Status != Warn || len(report.Checks) != 3 {
		t.Fatalf("report = %#v", report)
	}
}

func TestRunChecksWhisperFiles(t *testing.T) {
	dir := t.TempDir()
	model := filepath.Join(dir, "model.bin")
	if err := os.WriteFile(model, []byte("model"), 0o600); err != nil {
		t.Fatal(err)
	}
	report := Run(context.Background(), Runtime{WhisperCommand: os.Args[0], WhisperModel: model, WhisperPersistent: true})
	foundMode := false
	for _, check := range report.Checks {
		if check.Name == "whisper_model" && check.Status != Pass {
			t.Fatalf("check = %#v", check)
		}
		if check.Name == "whisper_mode" && check.Status == Pass && check.Message == "persistent whisper-server runtime configured" {
			foundMode = true
		}
	}
	if !foundMode {
		t.Fatalf("report = %#v", report)
	}
}
