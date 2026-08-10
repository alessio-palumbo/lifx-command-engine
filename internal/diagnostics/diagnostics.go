// Package diagnostics performs read-only runtime readiness checks.
package diagnostics

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Status string

const (
	Pass Status = "pass"
	Warn Status = "warn"
	Fail Status = "fail"
)

type Check struct {
	Name    string `json:"name"`
	Status  Status `json:"status"`
	Message string `json:"message"`
}

type Report struct {
	Status Status  `json:"status"`
	Checks []Check `json:"checks"`
}

type Runtime struct {
	ModelCommand   string
	ModelArgs      []string
	WhisperCommand string
	WhisperModel   string
}

func Run(ctx context.Context, runtime Runtime) Report {
	checks := []Check{{Name: "rules", Status: Pass, Message: "deterministic interpreter available"}}
	if runtime.ModelCommand == "" {
		checks = append(checks, Check{Name: "model", Status: Warn, Message: "optional model runtime is not configured"})
	} else {
		checks = append(checks, checkExecutable("model_command", runtime.ModelCommand))
		checks = append(checks, checkModelArgs(runtime.ModelArgs)...)
		if isPython(runtime.ModelCommand) {
			checks = append(checks, checkPythonDependencies(ctx, runtime.ModelCommand))
		}
	}
	if runtime.WhisperCommand == "" {
		checks = append(checks, Check{Name: "transcription", Status: Warn, Message: "optional whisper.cpp runtime is not configured"})
	} else {
		checks = append(checks, checkExecutable("whisper_command", runtime.WhisperCommand))
		checks = append(checks, checkRegularFile("whisper_model", runtime.WhisperModel))
	}
	status := Pass
	for _, check := range checks {
		if check.Status == Fail {
			status = Fail
			break
		}
		if check.Status == Warn {
			status = Warn
		}
	}
	return Report{Status: status, Checks: checks}
}

func checkExecutable(name, command string) Check {
	path, err := exec.LookPath(command)
	if err != nil {
		return Check{Name: name, Status: Fail, Message: err.Error()}
	}
	return Check{Name: name, Status: Pass, Message: path}
}

func checkModelArgs(args []string) []Check {
	var checks []Check
	for index, arg := range args {
		if strings.HasSuffix(arg, ".py") {
			checks = append(checks, checkRegularFile("model_runner", arg))
		}
		if arg == "--model" && index+1 < len(args) {
			checks = append(checks, checkDirectory("functiongemma_model", args[index+1]))
		} else if strings.HasPrefix(arg, "--model=") {
			checks = append(checks, checkDirectory("functiongemma_model", strings.TrimPrefix(arg, "--model=")))
		}
	}
	return checks
}

func checkRegularFile(name, path string) Check {
	info, err := os.Stat(path)
	if err != nil {
		return Check{Name: name, Status: Fail, Message: err.Error()}
	}
	if !info.Mode().IsRegular() {
		return Check{Name: name, Status: Fail, Message: fmt.Sprintf("%s is not a regular file", path)}
	}
	return Check{Name: name, Status: Pass, Message: path}
}

func checkDirectory(name, path string) Check {
	info, err := os.Stat(path)
	if err != nil {
		return Check{Name: name, Status: Fail, Message: err.Error()}
	}
	if !info.IsDir() {
		return Check{Name: name, Status: Fail, Message: fmt.Sprintf("%s is not a directory", path)}
	}
	for _, required := range []string{"config.json", "tokenizer.json"} {
		if _, err := os.Stat(filepath.Join(path, required)); err != nil {
			return Check{Name: name, Status: Fail, Message: fmt.Sprintf("model directory missing %s", required)}
		}
	}
	return Check{Name: name, Status: Pass, Message: path}
}

func checkPythonDependencies(ctx context.Context, python string) Check {
	command := exec.CommandContext(ctx, python, "-c", "import torch, transformers")
	if output, err := command.CombinedOutput(); err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		return Check{Name: "functiongemma_dependencies", Status: Fail, Message: message}
	}
	return Check{Name: "functiongemma_dependencies", Status: Pass, Message: "torch and transformers import successfully"}
}

func isPython(command string) bool {
	base := strings.ToLower(filepath.Base(command))
	return strings.HasPrefix(base, "python")
}
