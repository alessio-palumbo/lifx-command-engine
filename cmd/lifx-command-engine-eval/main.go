package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/alessio-palumbo/lifx-command-engine/internal/evaluation"
	"github.com/alessio-palumbo/lifx-command-engine/internal/interpreter"
	"github.com/alessio-palumbo/lifx-command-engine/internal/model"
)

func main() {
	fixturesPath := flag.String("fixtures", "testdata/functiongemma-eval.jsonl", "JSONL evaluation fixtures")
	mode := flag.String("mode", "rules", "rules, model, or hybrid")
	modelCommand := flag.String("model-command", "", "model runtime executable (required for model/hybrid)")
	var modelArgs stringList
	flag.Var(&modelArgs, "model-arg", "model runtime argument (repeatable)")
	timeout := flag.Duration("timeout", 5*time.Minute, "timeout for the complete evaluation")
	flag.Parse()

	file, err := os.Open(*fixturesPath)
	fatalIf(err)
	fixtures, err := evaluation.Load(file)
	_ = file.Close()
	fatalIf(err)
	rules := interpreter.RuleInterpreter{}
	var subject interpreter.Interpreter = rules
	switch *mode {
	case "rules":
	case "model", "hybrid":
		if *modelCommand == "" {
			fatalIf(fmt.Errorf("-model-command is required for mode %s", *mode))
		}
		modelInterpreter := interpreter.ModelInterpreter{Generator: model.ProcessGenerator{Path: *modelCommand, Args: modelArgs}}
		if *mode == "model" {
			subject = modelInterpreter
		} else {
			subject = interpreter.HybridInterpreter{Rules: rules, Model: modelInterpreter}
		}
	default:
		fatalIf(fmt.Errorf("unsupported mode %q", *mode))
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	report := (evaluation.Evaluator{Mode: *mode, Subject: subject, Rules: rules}).Run(ctx, fixtures)
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	fatalIf(enc.Encode(report))
	if report.Failed > 0 {
		os.Exit(1)
	}
}

func fatalIf(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}

type stringList []string

func (s *stringList) String() string         { return fmt.Sprint([]string(*s)) }
func (s *stringList) Set(value string) error { *s = append(*s, value); return nil }
