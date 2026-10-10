package entity

import (
	"encoding/json"
	"testing"
)

// A layout save from the rail must not reset the label choice made in the
// profile, and must still store the arrangement it was given.
func TestRailPrefsWithLayoutKeepsLabels(t *testing.T) {
	cur := RailPrefs{Order: []string{"todos"}, Hidden: []string{"process"}, Labels: true}
	got := cur.WithLayout(RailPrefs{Order: []string{"files", "todos"}, Hidden: []string{}})
	if !got.Labels {
		t.Fatal("Labels was reset by a layout save")
	}
	if len(got.Order) != 2 || got.Order[0] != "files" || got.Hidden == nil || len(got.Hidden) != 0 {
		t.Fatalf("layout not taken from the save: %+v", got)
	}
	if (RailPrefs{}).WithLayout(RailPrefs{Labels: true}).Labels {
		t.Fatal("a layout save must not turn labels on")
	}
}

// Unset means icons only: the zero value leaves "labels" off the wire, and
// the client reads a missing key as icons.
func TestRailPrefsLabelsWire(t *testing.T) {
	b, _ := json.Marshal(RailPrefs{})
	if string(b) != `{"hidden":null}` {
		t.Fatalf("zero RailPrefs = %s", b)
	}
	b, _ = json.Marshal(RailPrefs{Labels: true})
	if string(b) != `{"hidden":null,"labels":true}` {
		t.Fatalf("labels on = %s", b)
	}
}
