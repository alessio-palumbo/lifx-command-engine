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
	"github.com/alessio-palumbo/lifx-command-engine/internal/speech"
	"github.com/alessio-palumbo/lifx-command-engine/internal/speech/whispercpp"
)

func main() {
	modelCommand := flag.String("model-command", "", "optional local FunctionGemma-compatible runtime executable")
	modelPersistent := flag.Bool("model-persistent", false, "keep a compatible JSONL model runtime loaded")
	var modelArgs stringList
	flag.Var(&modelArgs, "model-arg", "argument for model command (repeatable)")
	whisperCommand := flag.String("whisper-command", "", "optional whisper.cpp CLI executable")
	whisperModel := flag.String("whisper-model", "", "local whisper.cpp model path")
	var whisperArgs stringList
	flag.Var(&whisperArgs, "whisper-arg", "argument placed before managed whisper.cpp arguments (repeatable)")
	flag.Parse()
	if *modelPersistent && *modelCommand == "" {
		fmt.Fprintln(os.Stderr, "-model-persistent requires -model-command")
		os.Exit(2)
	}
	if (*whisperCommand == "") != (*whisperModel == "") {
		fmt.Fprintln(os.Stderr, "-whisper-command and -whisper-model must be set together")
		os.Exit(2)
	}

	var active interpreter.Interpreter = interpreter.RuleInterpreter{}
	capabilities := schema.Capabilities{ProtocolVersion: jsonl.ProtocolVersion, CommandPlanSchema: "1", Methods: []string{"health", "capabilities", "interpret"}, Interpreters: []string{"rules"}, Transcription: false, ExecutesCommands: false}
	if *modelCommand != "" {
		var generator model.Generator = model.ProcessGenerator{Path: *modelCommand, Args: modelArgs}
		if *modelPersistent {
			persistent := &model.PersistentProcessGenerator{Path: *modelCommand, Args: modelArgs}
			defer persistent.Close()
			generator = persistent
			capabilities.ModelRuntime = "persistent_external_command"
		} else {
			capabilities.ModelRuntime = "external_command"
		}
		modelInterpreter := interpreter.ModelInterpreter{Generator: generator}
		active = interpreter.HybridInterpreter{Rules: active, Model: modelInterpreter}
		capabilities.Interpreters = []string{"rules", "model", "hybrid"}
	}
	var transcriber speech.Transcriber
	if *whisperCommand != "" {
		transcriber = whispercpp.Transcriber{Command: *whisperCommand, ModelPath: *whisperModel, Args: whisperArgs}
		capabilities.Methods = append(capabilities.Methods, "transcribe")
		capabilities.Transcription = true
		capabilities.TranscriptionSchema = "1"
	}
	s := jsonl.Server{Interpreter: active, Transcriber: transcriber, Capabilities: capabilities}
	if err := s.Serve(context.Background(), os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type stringList []string

func (s *stringList) String() string         { return fmt.Sprint([]string(*s)) }
func (s *stringList) Set(value string) error { *s = append(*s, value); return nil }
