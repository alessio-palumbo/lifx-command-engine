package interpreter

import (
	"context"

	"github.com/alessio-palumbo/lifx-command-engine/internal/schema"
)

type HybridInterpreter struct {
	Rules     Interpreter
	Model     Interpreter
	Threshold float64
}

func (h HybridInterpreter) Interpret(ctx context.Context, input schema.InterpretInput) (schema.CommandPlan, error) {
	rules := h.Rules
	if rules == nil {
		rules = RuleInterpreter{}
	}
	plan, err := rules.Interpret(ctx, input)
	if err != nil {
		return schema.CommandPlan{}, err
	}
	threshold := h.Threshold
	if threshold == 0 {
		threshold = .8
	}
	if plan.Confidence >= threshold || h.Model == nil {
		return plan, nil
	}
	modelPlan, modelErr := h.Model.Interpret(ctx, input)
	if modelErr == nil {
		return modelPlan, nil
	}
	plan.NeedsConfirmation = true
	plan.ConfidenceResult.Reasons = append(plan.ConfidenceResult.Reasons, "model fallback unavailable")
	return plan, nil
}
