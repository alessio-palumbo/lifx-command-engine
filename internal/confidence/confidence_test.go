package confidence

import (
	"github.com/alessio-palumbo/lifx-command-engine/internal/schema"
	"testing"
)

func TestScore(t *testing.T) {
	command := schema.CommandIntent{Targets: []schema.TargetRef{{Serial: "d073d5000001"}}, Action: schema.Action{Power: ptr(true)}}
	tests := []struct {
		name, text string
		commands   []schema.CommandIntent
		ambiguous  bool
		wantLevel  string
		max        float64
	}{
		{"exact", "desk on", []schema.CommandIntent{command}, false, "high", 1},
		{"style", "make desk cozy and on", []schema.CommandIntent{command}, false, "medium", .7},
		{"none", "do something", nil, false, "low", .2},
		{"ambiguous", "lamp on", []schema.CommandIntent{command}, true, "medium", .8},
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
func ptr(v bool) *bool { return &v }
