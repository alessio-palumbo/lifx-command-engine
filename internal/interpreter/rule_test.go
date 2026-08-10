package interpreter

import (
	"context"
	"testing"

	"github.com/alessio-palumbo/lifx-command-engine/internal/schema"
)

func TestRuleConfidenceUsesRequestedLabelNotSharedHierarchy(t *testing.T) {
	input := schema.InterpretInput{Text: "turn desk on", Snapshot: schema.DeviceSnapshot{Devices: []schema.SnapshotDevice{
		{Serial: "d073d5000001", Label: "Desk", Group: "Office", Location: "Home"},
		{Serial: "d073d5000002", Label: "Shelf", Group: "Office", Location: "Home"},
	}}}
	plan, err := (RuleInterpreter{}).Interpret(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Confidence != .95 || len(plan.Commands) != 1 || len(plan.Commands[0].Targets) != 1 || plan.Commands[0].Targets[0].Label != "Desk" {
		t.Fatalf("plan = %#v", plan)
	}
}

func TestRuleConfidenceFlagsRequestedDuplicateLabel(t *testing.T) {
	input := schema.InterpretInput{Text: "turn lamp off", Snapshot: schema.DeviceSnapshot{Devices: []schema.SnapshotDevice{
		{Serial: "d073d5000001", Label: "Lamp", Group: "Office"},
		{Serial: "d073d5000002", Label: "Lamp", Group: "Bedroom"},
	}}}
	plan, err := (RuleInterpreter{}).Interpret(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Confidence >= .8 || !plan.NeedsConfirmation {
		t.Fatalf("plan = %#v", plan)
	}
}
