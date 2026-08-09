package interpreter

import (
	"context"
	"github.com/alessio-palumbo/lifx-command-engine/internal/schema"
)

type Interpreter interface {
	Interpret(context.Context, schema.InterpretInput) (schema.CommandPlan, error)
}
