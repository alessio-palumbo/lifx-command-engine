package speech

import (
	"context"
	"fmt"
	"github.com/alessio-palumbo/lifx-command-engine/internal/schema"
)

// Transcriber is reserved for optional speech runtimes such as whisper.cpp.
type Transcriber interface {
	Transcribe(context.Context, schema.TranscribeInput) (schema.TranscribeResult, error)
}

type InvalidInputError struct{ Err error }

func (e *InvalidInputError) Error() string { return e.Err.Error() }
func (e *InvalidInputError) Unwrap() error { return e.Err }

type RuntimeError struct{ Err error }

func (e *RuntimeError) Error() string { return fmt.Sprintf("transcription runtime failed: %v", e.Err) }
func (e *RuntimeError) Unwrap() error { return e.Err }
