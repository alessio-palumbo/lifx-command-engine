package interpreter

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	"github.com/alessio-palumbo/lifx-command-engine/internal/adapters/lifxlancommand"
	"github.com/alessio-palumbo/lifx-command-engine/internal/confidence"
	"github.com/alessio-palumbo/lifx-command-engine/internal/schema"
)

type RuleInterpreter struct{}

func (RuleInterpreter) Interpret(ctx context.Context, input schema.InterpretInput) (schema.CommandPlan, error) {
	if err := ctx.Err(); err != nil {
		return schema.CommandPlan{}, err
	}
	if strings.TrimSpace(input.Text) == "" {
		return schema.CommandPlan{}, fmt.Errorf("text must not be empty")
	}
	adapter, err := lifxlancommand.New(input.Snapshot)
	if err != nil {
		return schema.CommandPlan{}, err
	}
	commands, err := adapter.Parse(input.Text)
	if err != nil {
		return schema.CommandPlan{}, err
	}
	score, result := confidence.Score(input.Text, commands, ambiguousTarget(input.Text, input.Snapshot))
	return schema.CommandPlan{
		SchemaVersion: "1", Confidence: score, ConfidenceResult: result,
		NeedsConfirmation: score < .9, Summary: summarize(commands), Commands: commands,
	}, nil
}

func ambiguousTarget(text string, s schema.DeviceSnapshot) bool {
	labels := map[string]int{}
	for _, d := range s.Devices {
		label := normalizeWords(d.Label)
		if label != "" {
			labels[label]++
		}
	}
	normalizedText := " " + normalizeWords(text) + " "
	for label, count := range labels {
		if count > 1 && strings.Contains(normalizedText, " "+label+" ") {
			return true
		}
	}
	return false
}

func normalizeWords(value string) string {
	return strings.Join(strings.FieldsFunc(strings.ToLower(value), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	}), " ")
}

func summarize(commands []schema.CommandIntent) string {
	if len(commands) == 0 {
		return "No supported LIFX command found"
	}
	if len(commands) > 1 {
		return fmt.Sprintf("Prepared %d LIFX commands", len(commands))
	}
	c := commands[0]
	target := "target"
	if len(c.Targets) == 1 && c.Targets[0].Label != "" {
		target = c.Targets[0].Label
	} else if len(c.Targets) > 1 {
		target = fmt.Sprintf("%d devices", len(c.Targets))
	}
	parts := []string{}
	if c.Action.Power != nil {
		if *c.Action.Power {
			parts = append(parts, "turn on")
		} else {
			parts = append(parts, "turn off")
		}
	}
	if c.Action.Kelvin != nil {
		parts = append(parts, fmt.Sprintf("set %dK", *c.Action.Kelvin))
	}
	if c.Action.Brightness != nil {
		parts = append(parts, fmt.Sprintf("set brightness to %.0f%%", *c.Action.Brightness))
	}
	if c.Action.Hue != nil {
		parts = append(parts, fmt.Sprintf("set hue to %.0f°", *c.Action.Hue))
	}
	if len(parts) == 0 {
		parts = append(parts, "update")
	}
	return strings.ToUpper(parts[0][:1]) + parts[0][1:] + " " + target + strings.Join(prefixEach(parts[1:], ", "), "")
}

func prefixEach(in []string, prefix string) []string {
	for i := range in {
		in[i] = prefix + in[i]
	}
	return in
}
