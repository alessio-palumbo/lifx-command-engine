// Package model contains optional, runtime-neutral model integration.
package model

import (
	"context"
	"encoding/json"
)

// Request is sent to a configured local model runtime. The runtime must return
// one CommandPlan JSON object and must never execute the proposed actions.
type Request struct {
	DeveloperInstruction string          `json:"developer_instruction"`
	Input                json.RawMessage `json:"input"`
	OutputSchema         string          `json:"output_schema"`
}

type Generator interface {
	Generate(context.Context, Request) ([]byte, error)
}
