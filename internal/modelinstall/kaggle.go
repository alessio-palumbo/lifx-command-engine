// Package modelinstall performs explicit optional model installation.
package modelinstall

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/alessio-palumbo/lifx-command-engine/internal/modelcatalog"
)

type Result struct {
	Name         string            `json:"name"`
	Source       string            `json:"source"`
	Handle       string            `json:"handle"`
	Revision     string            `json:"revision"`
	Path         string            `json:"path"`
	TotalBytes   int64             `json:"total_bytes"`
	FileSHA256   map[string]string `json:"file_sha256"`
	MetadataPath string            `json:"metadata_path"`
}

type CommandRunner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

func InstallKaggle(ctx context.Context, runner CommandRunner, python, outputDir string, model modelcatalog.Model) (Result, error) {
	if python == "" {
		python = "python3"
	}
	output, err := runner.Run(ctx, python, "-c", kaggleProgram, model.Handle, outputDir, model.Name, model.Source, model.Revision)
	if err != nil {
		return Result{}, fmt.Errorf("Kaggle model install failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	lines := bytes.Split(bytes.TrimSpace(output), []byte("\n"))
	if len(lines) == 0 {
		return Result{}, fmt.Errorf("Kaggle model install returned no result")
	}
	var result Result
	if err := json.Unmarshal(lines[len(lines)-1], &result); err != nil {
		return Result{}, fmt.Errorf("decode Kaggle install result: %w", err)
	}
	return result, nil
}

const kaggleProgram = `
import hashlib, json, pathlib, sys
try:
    import kagglehub
except ImportError:
    raise SystemExit("missing kagglehub dependency; install it with: pip install kagglehub")
handle, output_dir, name, source, revision = sys.argv[1:]
kwargs = {"output_dir": output_dir} if output_dir else {}
path = pathlib.Path(kagglehub.model_download(handle, **kwargs)).resolve()
hashes = {}
total = 0
for file in sorted(item for item in path.rglob("*") if item.is_file() and item.name != ".lifx-command-engine-model.json"):
    digest = hashlib.sha256()
    with file.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    hashes[str(file.relative_to(path))] = digest.hexdigest()
    total += file.stat().st_size
metadata_path = path / ".lifx-command-engine-model.json"
result = {"name": name, "source": source, "handle": handle, "revision": revision, "path": str(path), "total_bytes": total, "file_sha256": hashes, "metadata_path": str(metadata_path)}
metadata_path.write_text(json.dumps(result, indent=2) + "\n", encoding="utf-8")
print(json.dumps(result, separators=(",", ":")))
`
