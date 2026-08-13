package interpreter

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/alessio-palumbo/lifx-command-engine/internal/model"
	"github.com/alessio-palumbo/lifx-command-engine/internal/schema"
)

type fakeGenerator struct {
	output  string
	err     error
	request model.Request
}

func (f *fakeGenerator) Generate(_ context.Context, request model.Request) ([]byte, error) {
	f.request = request
	return []byte(f.output), f.err
}

func TestModelInterpreterValidatesAndNormalizesPlan(t *testing.T) {
	g := &fakeGenerator{output: `{"schema_version":"1","confidence":0.82,"confidence_result":{"level":"high","reasons":["model match"]},"needs_confirmation":true,"summary":"Turn on Desk","commands":[{"targets":[{"serial":"D073D5000001","label":"invented"}],"action":{"power":true}}]}`}
	input := schema.InterpretInput{Text: "illuminate my work area", Snapshot: schema.DeviceSnapshot{Devices: []schema.SnapshotDevice{{Serial: "d073d5000001", Label: "Desk", Group: "Office"}}}}
	plan, err := (ModelInterpreter{Generator: g}).Interpret(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	got := plan.Commands[0].Targets[0]
	if got.Serial != "d073d5000001" || got.Label != "Desk" || got.Group != "Office" {
		t.Fatalf("target not normalized: %#v", got)
	}
	if !strings.Contains(g.request.OutputSchema, "CommandPlan") || len(g.request.Input) == 0 {
		t.Fatalf("incomplete model request: %#v", g.request)
	}
	if g.request.ContractVersion != "1" {
		t.Fatalf("contract version = %q", g.request.ContractVersion)
	}
}

func TestModelInterpreterRejectsUnsafeOutput(t *testing.T) {
	tests := []struct{ name, output, contains string }{
		{"unknown target", `{"schema_version":"1","confidence":0.8,"confidence_result":{"level":"high","reasons":[]},"needs_confirmation":true,"summary":"x","commands":[{"targets":[{"serial":"d073d5009999"}],"action":{"power":true}}]}`, "not in snapshot"},
		{"range", `{"schema_version":"1","confidence":0.8,"confidence_result":{"level":"high","reasons":[]},"needs_confirmation":true,"summary":"x","commands":[{"targets":[{"serial":"d073d5000001"}],"action":{"brightness":101}}]}`, "out of range"},
		{"unknown field", `{"schema_version":"1","confidence":0.8,"confidence_result":{"level":"high","reasons":[]},"needs_confirmation":true,"summary":"x","execute":true,"commands":[]}`, "unknown field"},
	}
	input := schema.InterpretInput{Text: "x", Snapshot: schema.DeviceSnapshot{Devices: []schema.SnapshotDevice{{Serial: "d073d5000001", Label: "Desk"}}}}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := (ModelInterpreter{Generator: &fakeGenerator{output: tc.output}}).Interpret(context.Background(), input)
			if err == nil || !strings.Contains(err.Error(), tc.contains) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestModelInterpreterInfersPowerAndSplitsMixedTargets(t *testing.T) {
	poweredOn := true
	g := &fakeGenerator{output: `{"schema_version":"1","confidence":0.7,"confidence_result":{"level":"medium","reasons":["model match"]},"needs_confirmation":true,"summary":"Set office blue","commands":[{"targets":[{"serial":"d073d5000001"},{"serial":"d073d5000002"}],"action":{"hue":250,"saturation":100}}]}`}
	input := schema.InterpretInput{Text: "office blue", Snapshot: schema.DeviceSnapshot{Devices: []schema.SnapshotDevice{
		{Serial: "d073d5000001", Label: "Desk", Group: "Office"},
		{Serial: "d073d5000002", Label: "Shelf", Group: "Office", CurrentState: &schema.DeviceState{Power: &poweredOn}},
	}}}
	plan, err := (ModelInterpreter{Generator: g}).Interpret(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Commands) != 2 {
		t.Fatalf("commands = %#v", plan.Commands)
	}
	if plan.Commands[0].Targets[0].Label != "Desk" || plan.Commands[0].Action.Power == nil || !*plan.Commands[0].Action.Power {
		t.Fatalf("off command = %#v", plan.Commands[0])
	}
	if plan.Commands[1].Targets[0].Label != "Shelf" || plan.Commands[1].Action.Power != nil {
		t.Fatalf("on command = %#v", plan.Commands[1])
	}
}

func TestModelInterpreterExplicitOffPreventsPowerInference(t *testing.T) {
	g := &fakeGenerator{output: `{"schema_version":"1","confidence":0.7,"confidence_result":{"level":"medium","reasons":["model match"]},"needs_confirmation":true,"summary":"Set and turn off Desk","commands":[{"targets":[{"serial":"d073d5000001"}],"action":{"power":false,"brightness":35}}]}`}
	input := schema.InterpretInput{Text: "desk brightness 35% then off", Snapshot: schema.DeviceSnapshot{Devices: []schema.SnapshotDevice{{Serial: "d073d5000001", Label: "Desk"}}}}
	plan, err := (ModelInterpreter{Generator: g}).Interpret(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Commands[0].Action.Power == nil || *plan.Commands[0].Action.Power {
		t.Fatalf("command = %#v", plan.Commands[0])
	}
}

type fakeInterpreter struct {
	plan  schema.CommandPlan
	err   error
	calls int
}

func (f *fakeInterpreter) Interpret(context.Context, schema.InterpretInput) (schema.CommandPlan, error) {
	f.calls++
	return f.plan, f.err
}

func TestHybridInterpreter(t *testing.T) {
	input := schema.InterpretInput{Text: "anything"}
	t.Run("rules accepted", func(t *testing.T) {
		rules := &fakeInterpreter{plan: schema.CommandPlan{Confidence: .9}}
		model := &fakeInterpreter{}
		got, err := (HybridInterpreter{Rules: rules, Model: model}).Interpret(context.Background(), input)
		if err != nil || got.Confidence != .9 || model.calls != 0 {
			t.Fatalf("plan=%#v err=%v calls=%d", got, err, model.calls)
		}
	})
	t.Run("model used", func(t *testing.T) {
		rules := &fakeInterpreter{plan: schema.CommandPlan{Confidence: .4}}
		model := &fakeInterpreter{plan: schema.CommandPlan{Confidence: .85}}
		got, err := (HybridInterpreter{Rules: rules, Model: model}).Interpret(context.Background(), input)
		if err != nil || got.Confidence != .85 || model.calls != 1 {
			t.Fatalf("plan=%#v err=%v calls=%d", got, err, model.calls)
		}
	})
	t.Run("unavailable falls back", func(t *testing.T) {
		rules := &fakeInterpreter{plan: schema.CommandPlan{Confidence: .4, ConfidenceResult: schema.ConfidenceResult{Level: "low"}}}
		model := &fakeInterpreter{err: errors.New("offline")}
		got, err := (HybridInterpreter{Rules: rules, Model: model}).Interpret(context.Background(), input)
		if err != nil || !got.NeedsConfirmation || got.ConfidenceResult.Reasons[len(got.ConfidenceResult.Reasons)-1] != "model fallback unavailable" {
			t.Fatalf("plan=%#v err=%v", got, err)
		}
	})
}
