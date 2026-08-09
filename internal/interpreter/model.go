package interpreter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/alessio-palumbo/lifx-command-engine/internal/model"
	"github.com/alessio-palumbo/lifx-command-engine/internal/schema"
)

const planOutputSchema = `CommandPlan JSON schema version 1: {"schema_version":"1","confidence":number 0..1,"confidence_result":{"level":"high|medium|low","reasons":[string]},"needs_confirmation":boolean,"summary":string,"commands":[{"targets":[{"serial":string}],"action":{"power"?:boolean,"hue"?:number,"saturation"?:number,"brightness"?:number,"kelvin"?:integer,"duration_ms"?:integer}}]}. Return JSON only.`

type ModelInterpreter struct{ Generator model.Generator }

func (m ModelInterpreter) Interpret(ctx context.Context, input schema.InterpretInput) (schema.CommandPlan, error) {
	if m.Generator == nil {
		return schema.CommandPlan{}, fmt.Errorf("model runtime unavailable")
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return schema.CommandPlan{}, fmt.Errorf("encode model input: %w", err)
	}
	raw, err := m.Generator.Generate(ctx, model.Request{
		ContractVersion:      "1",
		DeveloperInstruction: "Translate the request into a LIFX command plan. Use only serials present in the snapshot. Propose actions only; never execute anything.",
		Input:                encoded, OutputSchema: planOutputSchema,
	})
	if err != nil {
		return schema.CommandPlan{}, fmt.Errorf("model runtime unavailable: %w", err)
	}
	var plan schema.CommandPlan
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&plan); err != nil {
		return schema.CommandPlan{}, fmt.Errorf("invalid model output: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return schema.CommandPlan{}, fmt.Errorf("invalid model output: multiple JSON values")
	}
	if err := validateModelPlan(&plan, input.Snapshot); err != nil {
		return schema.CommandPlan{}, fmt.Errorf("invalid model output: %w", err)
	}
	return plan, nil
}

func validateModelPlan(plan *schema.CommandPlan, snapshot schema.DeviceSnapshot) error {
	if plan.SchemaVersion != "1" {
		return fmt.Errorf("unsupported command plan schema %q", plan.SchemaVersion)
	}
	if plan.Confidence < 0 || plan.Confidence > 1 {
		return fmt.Errorf("confidence must be between 0 and 1")
	}
	if plan.ConfidenceResult.Level != "low" && plan.ConfidenceResult.Level != "medium" && plan.ConfidenceResult.Level != "high" {
		return fmt.Errorf("invalid confidence level")
	}
	if strings.TrimSpace(plan.Summary) == "" {
		return fmt.Errorf("summary must not be empty")
	}
	if len(plan.Commands) == 0 && plan.Confidence >= .5 {
		return fmt.Errorf("empty plan must have low confidence")
	}
	devices := make(map[string]schema.SnapshotDevice, len(snapshot.Devices))
	for _, d := range snapshot.Devices {
		devices[strings.ToLower(d.Serial)] = d
	}
	for ci := range plan.Commands {
		cmd := &plan.Commands[ci]
		if len(cmd.Targets) == 0 {
			return fmt.Errorf("command %d has no targets", ci)
		}
		if emptyAction(cmd.Action) {
			return fmt.Errorf("command %d has no action", ci)
		}
		if cmd.Action.Hue != nil && (*cmd.Action.Hue < 0 || *cmd.Action.Hue > 360) {
			return fmt.Errorf("command %d hue out of range", ci)
		}
		for name, value := range map[string]*float64{"saturation": cmd.Action.Saturation, "brightness": cmd.Action.Brightness} {
			if value != nil && (*value < 0 || *value > 100) {
				return fmt.Errorf("command %d %s out of range", ci, name)
			}
		}
		if cmd.Action.Kelvin != nil && (*cmd.Action.Kelvin < 1500 || *cmd.Action.Kelvin > 9000) {
			return fmt.Errorf("command %d kelvin out of range", ci)
		}
		for ti := range cmd.Targets {
			target := &cmd.Targets[ti]
			d, ok := devices[strings.ToLower(target.Serial)]
			if !ok {
				return fmt.Errorf("command %d target %q is not in snapshot", ci, target.Serial)
			}
			target.Serial, target.Label, target.Group, target.Location = strings.ToLower(d.Serial), d.Label, d.Group, d.Location
		}
	}
	if plan.Commands == nil {
		plan.Commands = []schema.CommandIntent{}
	}
	// Model-generated plans always require a host preview/confirmation.
	plan.NeedsConfirmation = true
	return nil
}

func emptyAction(a schema.Action) bool {
	return a.Power == nil && a.Hue == nil && a.Saturation == nil && a.Brightness == nil && a.Kelvin == nil && a.DurationMS == nil
}
