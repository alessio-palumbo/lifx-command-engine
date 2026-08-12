package interpreter_test

import (
	"context"
	"os"
	"testing"

	"github.com/alessio-palumbo/lifx-command-engine/internal/evaluation"
	"github.com/alessio-palumbo/lifx-command-engine/internal/interpreter"
)

func TestRuleParserEvaluationCorpus(t *testing.T) {
	file, err := os.Open("../../testdata/rule-parser-eval.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	fixtures, err := evaluation.Load(file)
	if err != nil {
		t.Fatal(err)
	}
	report := (evaluation.Evaluator{Mode: "rules", Subject: interpreter.RuleInterpreter{}}).Run(context.Background(), fixtures)
	if report.Failed != 0 {
		t.Fatalf("report = %#v", report)
	}
}
