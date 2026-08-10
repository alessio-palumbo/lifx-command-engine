// Package evaluation scores interpreters against versioned JSONL fixtures.
package evaluation

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/alessio-palumbo/lifx-command-engine/internal/interpreter"
	"github.com/alessio-palumbo/lifx-command-engine/internal/schema"
)

type Fixture struct {
	SchemaVersion string                `json:"schema_version"`
	Name          string                `json:"name"`
	Input         schema.InterpretInput `json:"input"`
	Expected      Expected              `json:"expected"`
}

type Expected struct {
	TargetSerials        []string       `json:"target_serials,omitempty"`
	CommandCount         *int           `json:"command_count,omitempty"`
	Action               *schema.Action `json:"action,omitempty"`
	Ambiguous            bool           `json:"ambiguous,omitempty"`
	RequiresConfirmation *bool          `json:"requires_confirmation,omitempty"`
}

type CaseResult struct {
	Name             string   `json:"name"`
	Passed           bool     `json:"passed"`
	LatencyMS        float64  `json:"latency_ms"`
	Failures         []string `json:"failures,omitempty"`
	Error            string   `json:"error,omitempty"`
	FallbackEligible bool     `json:"fallback_eligible,omitempty"`
	FallbackUsed     bool     `json:"fallback_used,omitempty"`
}

type Report struct {
	Mode             string       `json:"mode"`
	Total            int          `json:"total"`
	Passed           int          `json:"passed"`
	Failed           int          `json:"failed"`
	TargetAccuracy   *float64     `json:"target_accuracy,omitempty"`
	ActionAccuracy   *float64     `json:"action_accuracy,omitempty"`
	InvalidPlans     int          `json:"invalid_plans"`
	RuntimeErrors    int          `json:"runtime_errors"`
	FallbackEligible int          `json:"fallback_eligible"`
	FallbackUsed     int          `json:"fallback_used"`
	AverageLatencyMS float64      `json:"average_latency_ms"`
	P95LatencyMS     float64      `json:"p95_latency_ms"`
	Cases            []CaseResult `json:"cases"`
}

type Evaluator struct {
	Mode      string
	Subject   interpreter.Interpreter
	Rules     interpreter.Interpreter
	Threshold float64
}

func Load(r io.Reader) ([]Fixture, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	seen := map[string]bool{}
	var fixtures []Fixture
	for line := 1; scanner.Scan(); line++ {
		if strings.TrimSpace(scanner.Text()) == "" {
			continue
		}
		var fixture Fixture
		dec := json.NewDecoder(bytes.NewReader(scanner.Bytes()))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&fixture); err != nil {
			return nil, fmt.Errorf("fixture line %d: %w", line, err)
		}
		var extra any
		if err := dec.Decode(&extra); err != io.EOF {
			return nil, fmt.Errorf("fixture line %d: multiple JSON values", line)
		}
		if fixture.SchemaVersion != "1" {
			return nil, fmt.Errorf("fixture line %d: unsupported schema_version %q", line, fixture.SchemaVersion)
		}
		if fixture.Name == "" || fixture.Input.Text == "" {
			return nil, fmt.Errorf("fixture line %d: name and input.text are required", line)
		}
		if seen[fixture.Name] {
			return nil, fmt.Errorf("fixture line %d: duplicate name %q", line, fixture.Name)
		}
		seen[fixture.Name] = true
		fixtures = append(fixtures, fixture)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read fixtures: %w", err)
	}
	if len(fixtures) == 0 {
		return nil, fmt.Errorf("no fixtures found")
	}
	return fixtures, nil
}

func (e Evaluator) Run(ctx context.Context, fixtures []Fixture) Report {
	report := Report{Mode: e.Mode, Total: len(fixtures), Cases: make([]CaseResult, 0, len(fixtures))}
	var durations []float64
	var targetPassed, targetTotal, actionPassed, actionTotal int
	threshold := e.Threshold
	if threshold == 0 {
		threshold = .8
	}
	for _, fixture := range fixtures {
		caseResult := CaseResult{Name: fixture.Name}
		var rulePlan schema.CommandPlan
		if e.Mode == "hybrid" && e.Rules != nil {
			rulePlan, _ = e.Rules.Interpret(ctx, fixture.Input)
			caseResult.FallbackEligible = rulePlan.Confidence < threshold
			if caseResult.FallbackEligible {
				report.FallbackEligible++
			}
		}
		started := time.Now()
		plan, err := e.Subject.Interpret(ctx, fixture.Input)
		caseResult.LatencyMS = float64(time.Since(started).Microseconds()) / 1000
		durations = append(durations, caseResult.LatencyMS)
		if err != nil {
			caseResult.Error = err.Error()
			if strings.Contains(err.Error(), "invalid model output") {
				report.InvalidPlans++
			} else {
				report.RuntimeErrors++
			}
		} else {
			if caseResult.FallbackEligible && !hasReason(plan, "model fallback unavailable") {
				caseResult.FallbackUsed = true
				report.FallbackUsed++
			}
			caseResult.Failures = score(plan, fixture.Expected)
			if fixture.Expected.TargetSerials != nil {
				targetTotal++
				if !containsPrefix(caseResult.Failures, "targets:") {
					targetPassed++
				}
			}
			if fixture.Expected.Action != nil {
				actionTotal++
				if !containsPrefix(caseResult.Failures, "action:") {
					actionPassed++
				}
			}
		}
		caseResult.Passed = caseResult.Error == "" && len(caseResult.Failures) == 0
		if caseResult.Passed {
			report.Passed++
		} else {
			report.Failed++
		}
		report.Cases = append(report.Cases, caseResult)
	}
	if targetTotal > 0 {
		value := float64(targetPassed) / float64(targetTotal)
		report.TargetAccuracy = &value
	}
	if actionTotal > 0 {
		value := float64(actionPassed) / float64(actionTotal)
		report.ActionAccuracy = &value
	}
	if len(durations) > 0 {
		for _, d := range durations {
			report.AverageLatencyMS += d
		}
		report.AverageLatencyMS /= float64(len(durations))
		slices.Sort(durations)
		report.P95LatencyMS = durations[max(0, int(math.Ceil(.95*float64(len(durations))))-1)]
	}
	return report
}

func score(plan schema.CommandPlan, expected Expected) []string {
	var failures []string
	if expected.CommandCount != nil && len(plan.Commands) != *expected.CommandCount {
		failures = append(failures, fmt.Sprintf("command_count: got %d want %d", len(plan.Commands), *expected.CommandCount))
	}
	if expected.TargetSerials != nil {
		got := targetSerials(plan)
		want := append([]string(nil), expected.TargetSerials...)
		slices.Sort(got)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			failures = append(failures, fmt.Sprintf("targets: got %v want %v", got, want))
		}
	}
	if expected.Action != nil && !matchesAction(plan.Commands, *expected.Action) {
		failures = append(failures, "action: no command matched expected action exactly")
	}
	if expected.RequiresConfirmation != nil && plan.NeedsConfirmation != *expected.RequiresConfirmation {
		failures = append(failures, fmt.Sprintf("confirmation: got %t want %t", plan.NeedsConfirmation, *expected.RequiresConfirmation))
	}
	if expected.Ambiguous && (!plan.NeedsConfirmation || plan.ConfidenceResult.Level == "high") {
		failures = append(failures, "ambiguity: plan was not cautious")
	}
	return failures
}

func targetSerials(plan schema.CommandPlan) []string {
	var out []string
	for _, c := range plan.Commands {
		for _, t := range c.Targets {
			out = append(out, strings.ToLower(t.Serial))
		}
	}
	return out
}
func matchesAction(commands []schema.CommandIntent, expected schema.Action) bool {
	for _, c := range commands {
		if actionMatches(c.Action, expected) {
			return true
		}
	}
	return false
}
func actionMatches(got, want schema.Action) bool {
	if !equalBool(got.Power, want.Power) {
		return false
	}
	if !equalFloat(got.Hue, want.Hue) {
		return false
	}
	if !equalFloat(got.Saturation, want.Saturation) {
		return false
	}
	if !equalFloat(got.Brightness, want.Brightness) {
		return false
	}
	if !equalUint16(got.Kelvin, want.Kelvin) {
		return false
	}
	if !equalUint32(got.DurationMS, want.DurationMS) {
		return false
	}
	return true
}

func equalBool(a, b *bool) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

func equalFloat(a, b *float64) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && closeFloat(*a, *b))
}

func equalUint16(a, b *uint16) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

func equalUint32(a, b *uint32) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

func closeFloat(a, b float64) bool { return math.Abs(a-b) <= .01 }
func hasReason(plan schema.CommandPlan, reason string) bool {
	return slices.Contains(plan.ConfidenceResult.Reasons, reason)
}
func containsPrefix(values []string, prefix string) bool {
	for _, value := range values {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}
