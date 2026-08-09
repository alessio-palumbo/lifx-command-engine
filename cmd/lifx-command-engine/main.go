package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/alessio-palumbo/lifx-command-engine/internal/interpreter"
	"github.com/alessio-palumbo/lifx-command-engine/internal/jsonl"
	"github.com/alessio-palumbo/lifx-command-engine/internal/model"
	"github.com/alessio-palumbo/lifx-command-engine/internal/schema"
)

func main() {
	modelCommand := flag.String("model-command", "", "optional local FunctionGemma-compatible runtime executable")
	var modelArgs stringList
	flag.Var(&modelArgs, "model-arg", "argument for model command (repeatable)")
	flag.Parse()

	var active interpreter.Interpreter = interpreter.RuleInterpreter{}
	capabilities := schema.Capabilities{ProtocolVersion: jsonl.ProtocolVersion, CommandPlanSchema: "1", Methods: []string{"health", "capabilities", "interpret"}, Interpreters: []string{"rules"}, Transcription: false, ExecutesCommands: false}
	if *modelCommand != "" {
		modelInterpreter := interpreter.ModelInterpreter{Generator: model.ProcessGenerator{Path: *modelCommand, Args: modelArgs}}
		active = interpreter.HybridInterpreter{Rules: active, Model: modelInterpreter}
		capabilities.Interpreters = []string{"rules", "model", "hybrid"}
		capabilities.ModelRuntime = "external_command"
	}
	s := jsonl.Server{Interpreter: active, Capabilities: capabilities}
	if err := s.Serve(context.Background(), os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type stringList []string

func (s *stringList) String() string         { return fmt.Sprint([]string(*s)) }
func (s *stringList) Set(value string) error { *s = append(*s, value); return nil }
