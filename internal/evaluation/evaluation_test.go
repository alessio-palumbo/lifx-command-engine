package evaluation

import (
	"context"
	"strings"
	"testing"

	"github.com/alessio-palumbo/lifx-command-engine/internal/schema"
)

type fakeInterpreter struct {
	plan schema.CommandPlan
	err  error
}

func (f fakeInterpreter) Interpret(context.Context, schema.InterpretInput) (schema.CommandPlan, error) {
	return f.plan, f.err
}

func TestLoad(t *testing.T) {
	fixtures, err := Load(strings.NewReader(`{"schema_version":"1","name":"desk_on","input":{"text":"desk on","snapshot":{}},"expected":{"command_count":1}}`))
	if err != nil || len(fixtures) != 1 || fixtures[0].Name != "desk_on" {
		t.Fatalf("fixtures=%#v err=%v", fixtures, err)
	}
	if _, err := Load(strings.NewReader(`{"schema_version":"2","name":"bad","input":{"text":"x"},"expected":{}}`)); err == nil {
		t.Fatal("expected version error")
	}
}

func TestEvaluatorScoresTargetsAndActions(t *testing.T) {
	power := true
	confirm := true
	fixtures := []Fixture{{SchemaVersion: "1", Name: "desk_on", Input: schema.InterpretInput{Text: "desk on"}, Expected: Expected{TargetSerials: []string{"d073d5000001"}, Action: &schema.Action{Power: &power}, RequiresConfirmation: &confirm}}}
	plan := schema.CommandPlan{Confidence: .8, NeedsConfirmation: true, ConfidenceResult: schema.ConfidenceResult{Level: "high"}, Commands: []schema.CommandIntent{{Targets: []schema.TargetRef{{Serial: "d073d5000001"}}, Action: schema.Action{Power: &power}}}}
	report := (Evaluator{Mode: "model", Subject: fakeInterpreter{plan: plan}}).Run(context.Background(), fixtures)
	if report.Passed != 1 || report.Failed != 0 || report.TargetAccuracy == nil || *report.TargetAccuracy != 1 || report.ActionAccuracy == nil || *report.ActionAccuracy != 1 {
		t.Fatalf("report = %#v", report)
	}
}

func TestEvaluatorReportsMismatch(t *testing.T) {
	count := 0
	fixtures := []Fixture{{SchemaVersion: "1", Name: "none", Input: schema.InterpretInput{Text: "x"}, Expected: Expected{CommandCount: &count}}}
	power := true
	plan := schema.CommandPlan{Commands: []schema.CommandIntent{{Targets: []schema.TargetRef{{Serial: "d073d5000001"}}, Action: schema.Action{Power: &power}}}}
	report := (Evaluator{Mode: "rules", Subject: fakeInterpreter{plan: plan}}).Run(context.Background(), fixtures)
	if report.Failed != 1 || len(report.Cases[0].Failures) != 1 {
		t.Fatalf("report = %#v", report)
	}
}
