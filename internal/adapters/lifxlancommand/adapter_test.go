package lifxlancommand

import (
	"testing"

	"github.com/alessio-palumbo/lifx-command-engine/internal/schema"
)

func TestParseConvertsProtocolMessages(t *testing.T) {
	a, err := New(schema.DeviceSnapshot{Devices: []schema.SnapshotDevice{{Serial: "d073d5000001", Label: "Desk", Group: "Office"}}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := a.Parse("desk 2700k brightness 35%")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(got[0].Targets) != 1 {
		t.Fatalf("unexpected commands: %#v", got)
	}
	if got[0].Targets[0].Serial != "d073d5000001" {
		t.Errorf("serial = %q", got[0].Targets[0].Serial)
	}
	if got[0].Action.Kelvin == nil || *got[0].Action.Kelvin != 2700 {
		t.Errorf("kelvin = %#v", got[0].Action.Kelvin)
	}
	if got[0].Action.Brightness == nil || *got[0].Action.Brightness < 34.9 || *got[0].Action.Brightness > 35.1 {
		t.Errorf("brightness = %#v", got[0].Action.Brightness)
	}
}

func TestNewRejectsInvalidSerial(t *testing.T) {
	_, err := New(schema.DeviceSnapshot{Devices: []schema.SnapshotDevice{{Serial: "bad", Label: "Desk"}}})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestParseRelativeCommandsUseCurrentState(t *testing.T) {
	a, err := New(schema.DeviceSnapshot{Devices: []schema.SnapshotDevice{
		{
			Serial: "d073d5000001", Label: "Desk", Group: "Office", MinKelvin: 2500, MaxKelvin: 6500,
			CurrentState: &schema.DeviceState{Brightness: ptr(60.0), Saturation: ptr(50.0), Kelvin: ptr(uint16(4000))},
		},
		{
			Serial: "d073d5000002", Label: "Shelf", Group: "Office", MinKelvin: 2500, MaxKelvin: 6500,
			CurrentState: &schema.DeviceState{Brightness: ptr(30.0), Saturation: ptr(20.0), Kelvin: ptr(uint16(3000))},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := a.Parse("office dim 20%")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !near(got[0].Action.Brightness, 40) || !near(got[1].Action.Brightness, 10) {
		t.Fatalf("commands = %#v", got)
	}
	got, err = a.Parse("desk warmer")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Action.Kelvin == nil || *got[0].Action.Kelvin != 4500 {
		t.Fatalf("commands = %#v", got)
	}
	got, err = a.Parse("desk cool")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Action.Kelvin == nil || *got[0].Action.Kelvin != 3500 {
		t.Fatalf("commands = %#v", got)
	}
	got, err = a.Parse("shelf richer")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !near(got[0].Action.Saturation, 30) {
		t.Fatalf("commands = %#v", got)
	}
}

func TestParseRelativeCommandRejectsUnknownCurrentState(t *testing.T) {
	a, err := New(schema.DeviceSnapshot{Devices: []schema.SnapshotDevice{{Serial: "d073d5000001", Label: "Desk"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Parse("desk brighter"); err == nil {
		t.Fatal("expected missing current brightness error")
	}
	if _, err := a.Parse("desk blue"); err != nil {
		t.Fatalf("absolute command failed: %v", err)
	}
}

func TestParseRicherAbsoluteCommands(t *testing.T) {
	a, err := New(schema.DeviceSnapshot{Devices: []schema.SnapshotDevice{{Serial: "d073d5000001", Label: "Desk"}}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := a.Parse("desk soft pink brightness 40% in 1 minute")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !near(got[0].Action.Hue, 325) || !near(got[0].Action.Saturation, 45) || !near(got[0].Action.Brightness, 40) || got[0].Action.DurationMS == nil || *got[0].Action.DurationMS != 60000 {
		t.Fatalf("commands = %#v", got)
	}
	got, err = a.Parse("desk warm white")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !near(got[0].Action.Saturation, 0) || got[0].Action.Kelvin == nil || *got[0].Action.Kelvin != 2700 {
		t.Fatalf("commands = %#v", got)
	}
}

func TestParseSequentialCommands(t *testing.T) {
	a, err := New(schema.DeviceSnapshot{Devices: []schema.SnapshotDevice{
		{Serial: "d073d5000001", Label: "Desk"},
		{Serial: "d073d5000002", Label: "Shelf"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := a.Parse("desk blue then shelf off")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Targets[0].Label != "Desk" || got[1].Targets[0].Label != "Shelf" {
		t.Fatalf("commands = %#v", got)
	}
}

func TestParseBarePercentageAndInferredPower(t *testing.T) {
	poweredOn := true
	a, err := New(schema.DeviceSnapshot{Devices: []schema.SnapshotDevice{
		{Serial: "d073d5000001", Label: "Desk", Group: "Office"},
		{Serial: "d073d5000002", Label: "Shelf", Group: "Office", CurrentState: &schema.DeviceState{Power: &poweredOn}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := a.Parse("turn office warm white at 35%")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("commands = %#v", got)
	}
	if got[0].Targets[0].Label != "Desk" || got[0].Action.Power == nil || !*got[0].Action.Power || !near(got[0].Action.Brightness, 35) {
		t.Fatalf("off command = %#v", got[0])
	}
	if got[1].Targets[0].Label != "Shelf" || got[1].Action.Power != nil || !near(got[1].Action.Brightness, 35) {
		t.Fatalf("on command = %#v", got[1])
	}
}

func TestParseExplicitOffPreventsInferredPower(t *testing.T) {
	a, err := New(schema.DeviceSnapshot{Devices: []schema.SnapshotDevice{{Serial: "d073d5000001", Label: "Desk"}}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := a.Parse("desk warm white at 35% off")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Action.Power == nil || *got[0].Action.Power || !near(got[0].Action.Brightness, 35) {
		t.Fatalf("commands = %#v", got)
	}
}

func near(value *float64, want float64) bool {
	return value != nil && *value > want-.1 && *value < want+.1
}

func ptr[T any](value T) *T { return &value }
