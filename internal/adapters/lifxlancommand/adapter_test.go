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
