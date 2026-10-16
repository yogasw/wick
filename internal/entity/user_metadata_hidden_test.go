package entity

import (
	"encoding/json"
	"testing"
)

func TestHiddenConnectorsRoundTrip(t *testing.T) {
	in := UserMetadata{Theme: "dark", HiddenConnectors: []string{"a", "b"}}
	v, err := in.Value()
	if err != nil {
		t.Fatalf("value: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(v.([]byte), &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := raw["hidden_connectors"]; !ok {
		t.Fatalf("hidden_connectors missing from %s", v)
	}
	var out UserMetadata
	if err := out.Scan(v); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if !out.IsConnectorHidden("a") || !out.IsConnectorHidden("b") || out.IsConnectorHidden("c") {
		t.Errorf("round trip lost ids: %+v", out.HiddenConnectors)
	}
	if out.Theme != "dark" {
		t.Errorf("other fields lost: %+v", out)
	}

	// Empty list stays off the wire, so rows written before the field
	// existed and rows that unhid everything look the same.
	v, _ = UserMetadata{}.Value()
	raw = nil
	if err := json.Unmarshal(v.([]byte), &raw); err != nil {
		t.Fatalf("decode empty: %v", err)
	}
	if _, ok := raw["hidden_connectors"]; ok {
		t.Errorf("empty metadata carries hidden_connectors: %s", v)
	}
}

func TestHiddenConnectorSetNilSafe(t *testing.T) {
	var m UserMetadata
	set := m.HiddenConnectorSet()
	if set != nil || set["x"] {
		t.Errorf("empty metadata set = %v, want nil", set)
	}
	if m.IsConnectorHidden("") || m.IsConnectorHidden("x") {
		t.Error("empty metadata reports a hidden id")
	}
	m.HiddenConnectors = []string{"x"}
	if !m.HiddenConnectorSet()["x"] {
		t.Error("set misses a hidden id")
	}
}
