package confidence

import (
	"github.com/alessio-palumbo/lifx-command-engine/internal/schema"
	"testing"
)

func TestScore(t *testing.T) {
	command := schema.CommandIntent{Targets: []schema.TargetRef{{Serial: "d073d5000001"}}, Action: schema.Action{Power: ptr(true)}}
	multiTarget := command
	multiTarget.Targets = append(multiTarget.Targets, schema.TargetRef{Serial: "d073d5000002"})
	tests := []struct {
		name, text string
		commands   []schema.CommandIntent
		ambiguous  bool
		wantLevel  string
		max        float64
	}{
		{"exact", "desk on", []schema.CommandIntent{command}, false, "high", 1},
		{"exact multi target", "office off", []schema.CommandIntent{multiTarget}, false, "high", 1},
		{"style", "make desk cozy and on", []schema.CommandIntent{command}, false, "medium", .7},
		{"supported white", "desk warm white at 35%", []schema.CommandIntent{command}, false, "high", 1},
		{"random", "desk random", []schema.CommandIntent{command}, false, "medium", .8},
		{"none", "do something", nil, false, "low", .2},
		{"ambiguous", "lamp on", []schema.CommandIntent{command}, true, "medium", .8},
		{"multiple commands", "desk on then shelf off", []schema.CommandIntent{command, command}, false, "high", 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			score, got := Score(tc.text, tc.commands, tc.ambiguous)
			if got.Level != tc.wantLevel || score > tc.max {
				t.Fatalf("score=%v result=%#v", score, got)
			}
		})
	}
}

func TestCommandCountDoesNotChangeConfidence(t *testing.T) {
	command := schema.CommandIntent{Targets: []schema.TargetRef{{Serial: "d073d5000001"}}, Action: schema.Action{Power: ptr(true)}}
	for _, text := range []string{"desk on then shelf off", "make desk cozy", "lamp on", "desk random"} {
		ambiguous := text == "lamp on"
		wantScore, want := Score(text, []schema.CommandIntent{command}, ambiguous)
		score, got := Score(text, []schema.CommandIntent{command, command}, ambiguous)
		if score != wantScore || got.Level != want.Level || len(got.Reasons) != len(want.Reasons) {
			t.Fatalf("%q: score=%v result=%#v; want score=%v result=%#v", text, score, got, wantScore, want)
		}
		for i := range want.Reasons {
			if got.Reasons[i] != want.Reasons[i] {
				t.Fatalf("%q: reasons=%v; want %v", text, got.Reasons, want.Reasons)
			}
		}
	}
}
func ptr(v bool) *bool { return &v }
