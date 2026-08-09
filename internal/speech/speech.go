package speech

import (
	"context"
	"github.com/alessio-palumbo/lifx-command-engine/internal/schema"
)

// Transcriber is reserved for optional speech runtimes such as whisper.cpp.
type Transcriber interface {
	Transcribe(context.Context, schema.TranscribeInput) (schema.TranscribeResult, error)
}
