package main

import (
	"context"
	"fmt"
	"os"

	"github.com/alessio-palumbo/lifx-command-engine/internal/interpreter"
	"github.com/alessio-palumbo/lifx-command-engine/internal/jsonl"
)

func main() {
	s := jsonl.Server{Interpreter: interpreter.RuleInterpreter{}}
	if err := s.Serve(context.Background(), os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
