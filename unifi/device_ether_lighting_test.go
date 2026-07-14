package unifi

import (
	"testing"

	ui "github.com/ubiquiti-community/go-unifi/unifi"
)

func TestMergeRadioTablePreservesUnmanagedFields(t *testing.T) {
	antennaGain := int64(5)
	antennaID := int64(-1)
	ht := int64(40)
	current := []ui.DeviceRadioTable{{
		Radio:       "na",
		Name:        "wifi1",
		Channel:     "auto",
		Ht:          &ht,
		AntennaGain: &antennaGain,
		AntennaID:   &antennaID,
		TxPowerMode: "auto",
	}}
	plannedHt := int64(80)
	planned := []ui.DeviceRadioTable{{Radio: "na", Channel: "36", Ht: &plannedHt}}

	got := mergeRadioTable(current, planned)
	if len(got) != 1 {
		t.Fatalf("got %d radios, want 1", len(got))
	}
	if got[0].Channel != "36" || got[0].Ht == nil || *got[0].Ht != 80 {
		t.Fatalf("planned fields were not applied: %+v", got[0])
	}
	if got[0].AntennaGain == nil || *got[0].AntennaGain != 5 || got[0].Name != "wifi1" || got[0].TxPowerMode != "auto" {
		t.Fatalf("unmanaged fields were not preserved: %+v", got[0])
	}
}

func TestBuildMinimalUpdateDevicePreservesPortOverrides(t *testing.T) {
	portIndex := int64(1)
	current := &ui.Device{PortOverrides: []ui.DevicePortOverrides{{PortIDX: &portIndex, Name: "uplink"}}}
	target := &ui.Device{ID: "device", MAC: "00:11:22:33:44:55"}

	got := buildMinimalUpdateDevice(target, current, current.PortOverrides)
	if len(got.PortOverrides) != 1 || got.PortOverrides[0].Name != "uplink" {
		t.Fatalf("port overrides were not preserved: %+v", got.PortOverrides)
	}
}
