package modelinstall

import (
	"context"
	"errors"
	"testing"

	"github.com/alessio-palumbo/lifx-command-engine/internal/modelcatalog"
)

type fakeRunner struct {
	output []byte
	err    error
	args   []string
}

func (f *fakeRunner) Run(_ context.Context, _ string, args ...string) ([]byte, error) {
	f.args = args
	return f.output, f.err
}

func TestInstallKaggle(t *testing.T) {
	runner := &fakeRunner{output: []byte("download log\n{\"name\":\"functiongemma-270m-it\",\"source\":\"kaggle\",\"path\":\"/models/functiongemma\",\"total_bytes\":42,\"file_sha256\":{\"config.json\":\"abc\"}}\n")}
	result, err := InstallKaggle(context.Background(), runner, "python", "/models", modelcatalog.List()[0])
	if err != nil {
		t.Fatal(err)
	}
	if result.TotalBytes != 42 || result.FileSHA256["config.json"] != "abc" || len(runner.args) < 3 {
		t.Fatalf("result=%#v args=%v", result, runner.args)
	}
}

func TestInstallKaggleReportsDependencyFailure(t *testing.T) {
	runner := &fakeRunner{output: []byte("missing kagglehub"), err: errors.New("exit 1")}
	if _, err := InstallKaggle(context.Background(), runner, "python", "", modelcatalog.List()[0]); err == nil {
		t.Fatal("expected install error")
	}
}
