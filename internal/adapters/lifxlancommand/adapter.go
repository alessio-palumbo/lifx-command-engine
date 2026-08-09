package lifxlancommand

import (
	"fmt"
	"math"
	"strings"

	"github.com/alessio-palumbo/lifx-command-engine/internal/schema"
	"github.com/alessio-palumbo/lifxlan-go/pkg/command"
	"github.com/alessio-palumbo/lifxlan-go/pkg/device"
	"github.com/alessio-palumbo/lifxprotocol-go/gen/protocol/packets"
)

type Adapter struct {
	parser  *command.CommandParser
	devices map[string]schema.SnapshotDevice
}

func New(snapshot schema.DeviceSnapshot) (*Adapter, error) {
	devices := make([]device.Device, 0, len(snapshot.Devices))
	bySerial := make(map[string]schema.SnapshotDevice, len(snapshot.Devices))
	for _, d := range snapshot.Devices {
		serial, err := device.SerialFromHex(strings.ToLower(d.Serial))
		if err != nil {
			return nil, fmt.Errorf("device %q: %w", d.Label, err)
		}
		devices = append(devices, device.Device{Serial: serial, Label: d.Label, Group: d.Group, Location: d.Location})
		bySerial[serial.String()] = d
	}
	return &Adapter{parser: command.NewCommandParser(devices), devices: bySerial}, nil
}

func (a *Adapter) Parse(text string) ([]schema.CommandIntent, error) {
	parsed := a.parser.Parse(text)
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

func external(v uint16, max float64) float64 {
	return math.Round((float64(v)/math.MaxUint16*max)*100) / 100
}
