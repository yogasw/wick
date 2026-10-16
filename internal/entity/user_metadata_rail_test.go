package entity

import (
	"encoding/json"
	"testing"
)

func boolp(b bool) *bool { return &b }

// Labels are the default: only an explicit false turns them off.
func TestRailPrefsLabelsOn(t *testing.T) {
	if !(RailPrefs{}).LabelsOn() {
		t.Fatal("unset: LabelsOn = false, want labels on")
	}
	if (RailPrefs{Labels: boolp(false)}).LabelsOn() {
		t.Fatal("explicit false: LabelsOn = true, want icons only")
	}
	if !(RailPrefs{Labels: boolp(true)}).LabelsOn() {
		t.Fatal("explicit true: LabelsOn = false, want labels on")
	}
}

// A layout save from the rail must not reset the label choice made in the
// profile, and must still store the arrangement it was given.
func TestRailPrefsWithLayoutKeepsLabels(t *testing.T) {
	cur := RailPrefs{Order: []string{"todos"}, Hidden: []string{"process"}, Labels: boolp(false)}
	got := cur.WithLayout(RailPrefs{Order: []string{"files", "todos"}, Hidden: []string{}})
	if got.Labels == nil || *got.Labels {
		t.Fatalf("explicit false was not kept by a layout save: %v", got.Labels)
	}
	if len(got.Order) != 2 || got.Order[0] != "files" || got.Hidden == nil || len(got.Hidden) != 0 {
		t.Fatalf("layout not taken from the save: %+v", got)
	}
	if got := (RailPrefs{}).WithLayout(RailPrefs{Labels: boolp(false)}); got.Labels != nil {
		t.Fatal("a layout save must not set the labels choice")
	}
}

// Unset leaves "labels" off the wire, and the client reads a missing key as
// labels on; an explicit choice is written either way.
func TestRailPrefsLabelsWire(t *testing.T) {
	b, _ := json.Marshal(RailPrefs{})
	if string(b) != `{"hidden":null}` {
		t.Fatalf("zero RailPrefs = %s", b)
	}
	b, _ = json.Marshal(RailPrefs{Labels: boolp(true)})
	if string(b) != `{"hidden":null,"labels":true}` {
		t.Fatalf("labels on = %s", b)
	}
	b, _ = json.Marshal(RailPrefs{Labels: boolp(false)})
	if string(b) != `{"hidden":null,"labels":false}` {
		t.Fatalf("labels off = %s", b)
	}
	var p RailPrefs
	if err := json.Unmarshal([]byte(`{"hidden":null}`), &p); err != nil || !p.LabelsOn() {
		t.Fatalf("missing key: LabelsOn = %v (err %v), want true", p.LabelsOn(), err)
	}
}
