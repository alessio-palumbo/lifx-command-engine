package lifxlancommand

import (
	"fmt"
	"math"
	"regexp"
	"strings"
	"unicode"

	"github.com/alessio-palumbo/lifx-command-engine/internal/schema"
	"github.com/alessio-palumbo/lifxlan-go/pkg/command"
	"github.com/alessio-palumbo/lifxlan-go/pkg/device"
	"github.com/alessio-palumbo/lifxprotocol-go/gen/protocol/packets"
)

type Adapter struct {
	parser  *command.CommandParser
	devices map[string]schema.SnapshotDevice
}

var (
	relativeBrightness = regexp.MustCompile(`\b(dim|darken|darker|lower|brighten|brighter|raise|more bright|less bright)\b`)
)

func New(snapshot schema.DeviceSnapshot) (*Adapter, error) {
	devices := make([]device.Device, 0, len(snapshot.Devices))
	bySerial := make(map[string]schema.SnapshotDevice, len(snapshot.Devices))
	for _, d := range snapshot.Devices {
		serial, err := device.SerialFromHex(strings.ToLower(d.Serial))
		if err != nil {
			return nil, fmt.Errorf("device %q: %w", d.Label, err)
		}
		if err := validateSnapshotDevice(d); err != nil {
			return nil, fmt.Errorf("device %q: %w", d.Label, err)
		}
		parsedDevice := device.Device{
			Serial: serial, Label: d.Label, Group: d.Group, Location: d.Location,
			ProductID: d.ProductID,
			ColorProperties: device.ColorProperties{
				HasColor:         d.HasColor,
				TemperatureRange: device.TemperatureRange{Min: int(d.MinKelvin), Max: int(d.MaxKelvin)},
			},
		}
		if d.CurrentState != nil {
			state := d.CurrentState
			if state.Power != nil {
				parsedDevice.PoweredOn = *state.Power
			}
			if state.Hue != nil {
				parsedDevice.Color.Hue = *state.Hue
			}
			if state.Saturation != nil {
				parsedDevice.Color.Saturation = *state.Saturation
			}
			if state.Brightness != nil {
				parsedDevice.Color.Brightness = *state.Brightness
			}
			if state.Kelvin != nil {
				parsedDevice.Color.Kelvin = *state.Kelvin
			}
		}
		devices = append(devices, parsedDevice)
		bySerial[serial.String()] = d
	}
	return &Adapter{parser: command.NewCommandParser(devices), devices: bySerial}, nil
}

func (a *Adapter) Parse(text string) ([]schema.CommandIntent, error) {
	parsed := a.parser.Parse(text)
	if err := a.validateRelativeState(text, parsed); err != nil {
		return nil, err
	}
	out := make([]schema.CommandIntent, 0, len(parsed))
	for _, cmd := range parsed {
		intent := schema.CommandIntent{}
		for _, serial := range cmd.Targets {
			d := a.devices[serial.String()]
			intent.Targets = append(intent.Targets, schema.TargetRef{Serial: serial.String(), Label: d.Label, Group: d.Group, Location: d.Location})
		}
		for _, msg := range cmd.Msgs {
			switch p := msg.Payload.(type) {
			case *packets.DeviceSetPower:
				v := p.Level > 0
				intent.Action.Power = &v
			case *packets.LightSetPower:
				v := p.Level > 0
				intent.Action.Power = &v
				if p.Duration > 0 {
					v := p.Duration
					intent.Action.DurationMS = &v
				}
			case *packets.LightSetWaveformOptional:
				if p.SetHue {
					v := external(p.Color.Hue, 360)
					intent.Action.Hue = &v
				}
				if p.SetSaturation {
					v := external(p.Color.Saturation, 100)
					intent.Action.Saturation = &v
				}
				if p.SetBrightness {
					v := external(p.Color.Brightness, 100)
					intent.Action.Brightness = &v
				}
				if p.SetKelvin {
					v := p.Color.Kelvin
					intent.Action.Kelvin = &v
				}
				if p.Period > 1 {
					v := p.Period
					intent.Action.DurationMS = &v
				}
			default:
				return nil, fmt.Errorf("unsupported parser payload %T", msg.Payload)
			}
		}
		out = append(out, intent)
	}
	if out == nil {
		out = []schema.CommandIntent{}
	}
	return out, nil
}

func validateSnapshotDevice(d schema.SnapshotDevice) error {
	if (d.MinKelvin == 0) != (d.MaxKelvin == 0) || (d.MinKelvin > 0 && d.MinKelvin > d.MaxKelvin) {
		return fmt.Errorf("invalid kelvin range %d-%d", d.MinKelvin, d.MaxKelvin)
	}
	if d.CurrentState == nil {
		return nil
	}
	for name, value := range map[string]*float64{
		"hue": d.CurrentState.Hue, "saturation": d.CurrentState.Saturation, "brightness": d.CurrentState.Brightness,
	} {
		if value == nil {
			continue
		}
		max := 100.0
		if name == "hue" {
			max = 360
		}
		if *value < 0 || *value > max {
			return fmt.Errorf("current %s %.2f outside 0-%.0f", name, *value, max)
		}
	}
	if kelvin := d.CurrentState.Kelvin; kelvin != nil && (*kelvin < 1500 || *kelvin > 9000) {
		return fmt.Errorf("current kelvin %d outside 1500-9000", *kelvin)
	}
	return nil
}

func (a *Adapter) validateRelativeState(text string, commands []command.Command) error {
	normalized := normalize(text)
	requireBrightness := relativeBrightness.MatchString(normalized)
	requireKelvin := hasRelativeKelvin(normalized)
	requireSaturation := hasRelativeSaturation(normalized)
	if !requireBrightness && !requireKelvin && !requireSaturation {
		return nil
	}
	for _, parsed := range commands {
		for _, serial := range parsed.Targets {
			snapshot := a.devices[serial.String()]
			state := snapshot.CurrentState
			missing := ""
			switch {
			case requireBrightness && (state == nil || state.Brightness == nil):
				missing = "brightness"
			case requireSaturation && (state == nil || state.Saturation == nil):
				missing = "saturation"
			case requireKelvin && (state == nil || state.Kelvin == nil):
				missing = "kelvin"
			}
			if missing != "" {
				return fmt.Errorf("relative command requires current %s for device %q", missing, snapshot.Label)
			}
		}
	}
	return nil
}

func hasRelativeKelvin(text string) bool {
	words := strings.Fields(text)
	for index, word := range words {
		if word == "warmer" || word == "cooler" {
			return true
		}
		if (word == "warm" || word == "cool") && !(index+1 < len(words) && words[index+1] == "white") {
			return true
		}
	}
	return false
}

func normalize(text string) string {
	return strings.Join(strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	}), " ")
}

func hasRelativeSaturation(text string) bool {
	words := strings.Fields(text)
	styles := map[string]bool{
		"muted": true, "soft": true, "washed": true, "deep": true, "rich": true, "strong": true, "vivid": true,
		"pastel": true, "intense": true, "soften": true, "softer": true, "deeper": true, "richer": true, "saturated": true,
	}
	colors := map[string]bool{"red": true, "orange": true, "yellow": true, "green": true, "cyan": true, "blue": true, "purple": true, "pink": true}
	for index, word := range words {
		if styles[word] && !(index+1 < len(words) && colors[words[index+1]]) {
			return true
		}
	}
	return strings.Contains(text, "more pastel") || strings.Contains(text, "less pastel") || strings.Contains(text, "more intense") || strings.Contains(text, "less intense")
}

func external(v uint16, max float64) float64 {
	return math.Round((float64(v)/math.MaxUint16*max)*100) / 100
}
