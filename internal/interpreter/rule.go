package interpreter

import (
	"context"
	"fmt"
	"strings"

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
	score, result := confidence.Score(input.Text, commands, ambiguousSnapshot(input.Snapshot))
	return schema.CommandPlan{
		SchemaVersion: "1", Confidence: score, ConfidenceResult: result,
		NeedsConfirmation: score < .9, Summary: summarize(commands), Commands: commands,
	}, nil
}

func ambiguousSnapshot(s schema.DeviceSnapshot) bool {
	seen := map[string]bool{}
	for _, d := range s.Devices {
		for _, value := range []string{d.Label, d.Group, d.Location} {
			value = strings.ToLower(strings.TrimSpace(value))
			if value == "" {
				continue
			}
			if seen[value] {
				return true
			}
			seen[value] = true
		}
	}
	return false
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
