package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/alessio-palumbo/lifx-command-engine/internal/appconfig"
	"github.com/alessio-palumbo/lifx-command-engine/internal/interpreter"
	"github.com/alessio-palumbo/lifx-command-engine/internal/jsonl"
	"github.com/alessio-palumbo/lifx-command-engine/internal/model"
	"github.com/alessio-palumbo/lifx-command-engine/internal/schema"
	"github.com/alessio-palumbo/lifx-command-engine/internal/speech"
	"github.com/alessio-palumbo/lifx-command-engine/internal/speech/whispercpp"
)

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) > 0 {
		switch args[0] {
		case "serve":
			args = args[1:]
		case "doctor":
			return runDoctor(args[1:], stdout, stderr)
		case "models":
			return runModels(args[1:], stdout, stderr)
		}
	}
	return runServe(args, stdin, stdout, stderr)
}

func runServe(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	options, err := parseRuntimeOptions("lifx-command-engine", args, stderr)
	if err != nil {
		return err
	}
	if err := options.validate(); err != nil {
		return err
	}

	var active interpreter.Interpreter = interpreter.RuleInterpreter{}
	capabilities := schema.Capabilities{ProtocolVersion: jsonl.ProtocolVersion, CommandPlanSchema: "1", Methods: []string{"health", "capabilities", "interpret"}, Interpreters: []string{"rules"}, Transcription: false, ExecutesCommands: false}
	if options.model.Command != "" {
		var generator model.Generator = model.ProcessGenerator{Path: options.model.Command, Args: options.model.Args}
		if options.model.Persistent {
			persistent := &model.PersistentProcessGenerator{Path: options.model.Command, Args: options.model.Args}
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
	if options.whisper.Command != "" {
		if options.whisper.Persistent {
			persistent, err := whispercpp.NewPersistentTranscriber(options.whisper.Command, options.whisper.ModelPath, options.whisper.Args)
			if err != nil {
				return err
			}
			startupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			err = persistent.Start(startupCtx)
			cancel()
			if err != nil {
				_ = persistent.Close()
				return err
			}
			defer persistent.Close()
			transcriber = persistent
		} else {
			transcriber = whispercpp.Transcriber{Command: options.whisper.Command, ModelPath: options.whisper.ModelPath, Args: options.whisper.Args}
		}
		capabilities.Methods = append(capabilities.Methods, "transcribe")
		capabilities.Transcription = true
		capabilities.TranscriptionSchema = "1"
	}
	s := jsonl.Server{Interpreter: active, Transcriber: transcriber, Capabilities: capabilities}
	return s.Serve(context.Background(), stdin, stdout)
}

type runtimeOptions struct {
	model   appconfig.RuntimeConfig
	whisper appconfig.WhisperConfig
}

func parseRuntimeOptions(name string, args []string, stderr io.Writer) (runtimeOptions, error) {
	set := flag.NewFlagSet(name, flag.ContinueOnError)
	set.SetOutput(stderr)
	configPath := set.String("config", "", "optional versioned JSON configuration file")
	modelCommand := set.String("model-command", "", "optional local FunctionGemma-compatible runtime executable")
	modelPersistent := set.Bool("model-persistent", false, "keep a compatible JSONL model runtime loaded")
	var modelArgs stringList
	set.Var(&modelArgs, "model-arg", "argument for model command (repeatable)")
	whisperCommand := set.String("whisper-command", "", "optional whisper.cpp CLI executable")
	whisperModel := set.String("whisper-model", "", "local whisper.cpp model path")
	whisperPersistent := set.Bool("whisper-persistent", false, "keep a local whisper-server and model loaded")
	var whisperArgs stringList
	set.Var(&whisperArgs, "whisper-arg", "argument placed before managed whisper.cpp arguments (repeatable)")
	if err := set.Parse(args); err != nil {
		return runtimeOptions{}, err
	}
	if set.NArg() != 0 {
		return runtimeOptions{}, fmt.Errorf("unexpected arguments: %v", set.Args())
	}

	var config appconfig.Config
	if *configPath != "" {
		loaded, err := appconfig.LoadFile(*configPath)
		if err != nil {
			return runtimeOptions{}, err
		}
		config = loaded
	}
	visited := map[string]bool{}
	set.Visit(func(f *flag.Flag) { visited[f.Name] = true })
	if visited["model-command"] {
		config.Model.Command = *modelCommand
	}
	if visited["model-persistent"] {
		config.Model.Persistent = *modelPersistent
	}
	if visited["model-arg"] {
		config.Model.Args = modelArgs
	}
	if visited["whisper-command"] {
		config.Whisper.Command = *whisperCommand
	}
	if visited["whisper-model"] {
		config.Whisper.ModelPath = *whisperModel
	}
	if visited["whisper-persistent"] {
		config.Whisper.Persistent = *whisperPersistent
	}
	if visited["whisper-arg"] {
		config.Whisper.Args = whisperArgs
	}
	return runtimeOptions{model: config.Model, whisper: config.Whisper}, nil
}

func (o runtimeOptions) validate() error {
	if o.model.Persistent && o.model.Command == "" {
		return fmt.Errorf("model persistence requires a model command")
	}
	if (o.whisper.Command == "") != (o.whisper.ModelPath == "") {
		return fmt.Errorf("whisper command and model path must be set together")
	}
	if o.whisper.Persistent {
		if o.whisper.Command == "" {
			return fmt.Errorf("whisper persistence requires a command and model path")
		}
		if err := whispercpp.ValidatePersistentArgs(o.whisper.Args); err != nil {
			return err
		}
	}
	return nil
}

type stringList []string

func (s *stringList) String() string         { return fmt.Sprint([]string(*s)) }
func (s *stringList) Set(value string) error { *s = append(*s, value); return nil }
