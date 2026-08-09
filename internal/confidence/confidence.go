package confidence

import (
	"regexp"
	"strings"

	"github.com/alessio-palumbo/lifx-command-engine/internal/schema"
)

var styleWords = regexp.MustCompile(`\b(cozy|cosy|relaxing|romantic|focus|energizing|mood|vibe|gentle|dramatic)\b`)
var unsupportedPhrases = regexp.MustCompile(`\bwarm\s+white\b|\bat\s+\d+%`)

func Score(text string, commands []schema.CommandIntent, ambiguous bool) (float64, schema.ConfidenceResult) {
	reasons := []string{}
	if len(commands) == 0 {
		return 0.1, schema.ConfidenceResult{Level: "low", Reasons: []string{"no command parsed"}}
	}
	score := 0.95
	if ambiguous {
		score -= 0.25
		reasons = append(reasons, "target label resolves to multiple devices")
	}
	if len(commands) > 1 {
		score -= 0.08
		reasons = append(reasons, "multiple commands parsed")
	}
	targets := 0
	for _, c := range commands {
		targets += len(c.Targets)
	}
	if targets > len(commands) {
		score -= 0.08
		reasons = append(reasons, "command affects multiple devices")
	}
	if styleWords.MatchString(strings.ToLower(text)) || unsupportedPhrases.MatchString(strings.ToLower(text)) {
		score -= 0.35
		reasons = append(reasons, "unsupported style language ignored")
	}
	if len(reasons) == 0 {
		reasons = append(reasons, "exact rule-parser match")
	}
	level := "high"
	if score < .8 {
		level = "medium"
	}
	if score < .5 {
		level = "low"
	}
	return score, schema.ConfidenceResult{Level: level, Reasons: reasons}
}
