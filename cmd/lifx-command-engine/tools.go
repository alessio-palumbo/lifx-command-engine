package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/alessio-palumbo/lifx-command-engine/internal/diagnostics"
	"github.com/alessio-palumbo/lifx-command-engine/internal/modelcatalog"
	"github.com/alessio-palumbo/lifx-command-engine/internal/modelinstall"
)

func runDoctor(args []string, stdout, stderr io.Writer) error {
	options, err := parseRuntimeOptions("lifx-command-engine doctor", args, stderr)
	if err != nil {
		return err
	}
	if err := options.validate(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	report := diagnostics.Run(ctx, diagnostics.Runtime{
		ModelCommand:   options.model.Command,
		ModelArgs:      options.model.Args,
		WhisperCommand: options.whisper.Command,
		WhisperModel:   options.whisper.ModelPath,
	})
	if err := writeJSON(stdout, report); err != nil {
		return err
	}
	if report.Status == diagnostics.Fail {
		return fmt.Errorf("one or more runtime checks failed")
	}
	return nil
}

func runModels(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("models requires list or install")
	}
	switch args[0] {
	case "list":
		if len(args) != 1 {
			return fmt.Errorf("models list accepts no arguments")
		}
		return writeJSON(stdout, map[string]any{"models": modelcatalog.List()})
	case "install":
		return runModelInstall(args[1:], stdout, stderr)
	default:
		return fmt.Errorf("unknown models command %q", args[0])
	}
}

func runModelInstall(args []string, stdout, stderr io.Writer) error {
	modelName := ""
	if len(args) > 0 && args[0] != "" && args[0][0] != '-' {
		modelName = args[0]
		args = args[1:]
	}
	set := flag.NewFlagSet("lifx-command-engine models install", flag.ContinueOnError)
	set.SetOutput(stderr)
	source := set.String("source", "kaggle", "model source")
	python := set.String("python", "python3", "Python interpreter with kagglehub installed")
	outputDir := set.String("output", "", "optional destination directory; defaults to the KaggleHub cache")
	timeout := set.Duration("timeout", 30*time.Minute, "download and checksum timeout")
	if err := set.Parse(args); err != nil {
		return err
	}
	if modelName == "" && set.NArg() == 1 {
		modelName = set.Arg(0)
	} else if set.NArg() != 0 {
		return fmt.Errorf("models install requires exactly one model name")
	}
	if modelName == "" {
		return fmt.Errorf("models install requires exactly one model name")
	}
	entry, err := modelcatalog.Find(modelName, *source)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	result, err := modelinstall.InstallKaggle(ctx, modelinstall.ExecRunner{}, *python, *outputDir, entry)
	if err != nil {
		return err
	}
	return writeJSON(stdout, result)
}

func writeJSON(w io.Writer, value any) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
