// Package schema defines the engine's transport-independent public data model.
package schema

type DeviceSnapshot struct {
	Locations []NamedRef       `json:"locations"`
	Groups    []NamedRef       `json:"groups"`
	Devices   []SnapshotDevice `json:"devices"`
}

type NamedRef struct {
	ID    string `json:"id,omitempty"`
	Label string `json:"label"`
}

type SnapshotDevice struct {
	Serial    string `json:"serial"`
	Label     string `json:"label"`
	Group     string `json:"group,omitempty"`
	Location  string `json:"location,omitempty"`
	ProductID uint32 `json:"product_id,omitempty"`
	HasColor  bool   `json:"has_color,omitempty"`
	MinKelvin uint16 `json:"min_kelvin,omitempty"`
	MaxKelvin uint16 `json:"max_kelvin,omitempty"`
}

type InterpretInput struct {
	Text     string         `json:"text"`
	Snapshot DeviceSnapshot `json:"snapshot"`
}

type CommandPlan struct {
	SchemaVersion     string           `json:"schema_version"`
	Confidence        float64          `json:"confidence"`
	ConfidenceResult  ConfidenceResult `json:"confidence_result"`
	NeedsConfirmation bool             `json:"needs_confirmation"`
	Summary           string           `json:"summary"`
	Commands          []CommandIntent  `json:"commands"`
}

type ConfidenceResult struct {
	Level   string   `json:"level"`
	Reasons []string `json:"reasons"`
}

type CommandIntent struct {
	Targets []TargetRef `json:"targets"`
	Action  Action      `json:"action"`
}

// TargetRef carries the stable device identity plus display metadata. Serial is
// the only execution identity; labels, groups and locations are informational.
type TargetRef struct {
	Serial   string `json:"serial"`
	Label    string `json:"label,omitempty"`
	Group    string `json:"group,omitempty"`
	Location string `json:"location,omitempty"`
}

type Action struct {
	Power      *bool    `json:"power,omitempty"`
	Hue        *float64 `json:"hue,omitempty"`
	Saturation *float64 `json:"saturation,omitempty"`
	Brightness *float64 `json:"brightness,omitempty"`
	Kelvin     *uint16  `json:"kelvin,omitempty"`
	DurationMS *uint32  `json:"duration_ms,omitempty"`
}

type Capabilities struct {
	ProtocolVersion   string   `json:"protocol_version"`
	CommandPlanSchema string   `json:"command_plan_schema"`
	Methods           []string `json:"methods"`
	Interpreters      []string `json:"interpreters"`
	Transcription     bool     `json:"transcription"`
	ExecutesCommands  bool     `json:"executes_commands"`
}

type TranscribeInput struct {
	AudioPath string `json:"audio_path"`
}
type TranscribeResult struct {
	Text string `json:"text"`
}
