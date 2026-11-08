package unifi

import (
	"encoding/json"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/ubiquiti-community/go-unifi/unifi/settings"
)

func TestApplyGlobalSwitchModelPreservesUnmanagedFields(t *testing.T) {
	var setting settings.RawSetting
	raw := `{"_id":"abc","key":"global_switch","flowctrl_enabled":false,"auto_stp_edge_detection_enabled":false,"link_debounce":0,"stp_version":"rstp","dhcp_snoop":true}`
	if err := json.Unmarshal([]byte(raw), &setting); err != nil {
		t.Fatal(err)
	}

	applyGlobalSwitchModel(setting.Data, settingGlobalSwitchModel{
		FlowControlEnabled:          types.BoolValue(true),
		AutoStpEdgeDetectionEnabled: types.BoolValue(true),
	})

	encoded, err := json.Marshal(&setting)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}

	want := map[string]any{
		"_id":                             "abc",
		"key":                             "global_switch",
		"flowctrl_enabled":                true,
		"auto_stp_edge_detection_enabled": true,
		"link_debounce":                   float64(0),
		"stp_version":                     "rstp",
		"dhcp_snoop":                      true,
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}
}

func TestApplyGlobalSwitchModelLeavesUnsetEdgeDetection(t *testing.T) {
	data := map[string]any{"auto_stp_edge_detection_enabled": true}

	applyGlobalSwitchModel(data, settingGlobalSwitchModel{
		FlowControlEnabled:          types.BoolValue(false),
		AutoStpEdgeDetectionEnabled: types.BoolNull(),
	})

	if data["auto_stp_edge_detection_enabled"] != true {
		t.Errorf("auto_stp_edge_detection_enabled = %v, want true", data["auto_stp_edge_detection_enabled"])
	}
	if data["flowctrl_enabled"] != false {
		t.Errorf("flowctrl_enabled = %v, want false", data["flowctrl_enabled"])
	}
}

func TestGlobalSwitchModelFromData(t *testing.T) {
	model := globalSwitchModelFromData(map[string]any{
		"flowctrl_enabled":                true,
		"auto_stp_edge_detection_enabled": true,
	})
	if !model.FlowControlEnabled.ValueBool() || !model.AutoStpEdgeDetectionEnabled.ValueBool() {
		t.Errorf("model = %+v, want both true", model)
	}

	empty := globalSwitchModelFromData(map[string]any{})
	if empty.FlowControlEnabled.ValueBool() || empty.AutoStpEdgeDetectionEnabled.ValueBool() {
		t.Errorf("empty model = %+v, want both false", empty)
	}
}
