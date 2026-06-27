package entity

import "testing"

// MapToStruct must fill non-string fields by kind, not blindly SetString.
// Regression for a panic ("reflect: call of reflect.Value.SetString on bool
// Value") when a channel config struct carried a bool field (ask_user_enabled)
// and the DB row had that key set.
func TestMapToStruct_NonStringKinds(t *testing.T) {
	type S struct {
		Name    string  `wick:"key=name"`
		Enabled bool    `wick:"bool;key=enabled"`
		Count   int     `wick:"number;key=count"`
		Ratio   float64 `wick:"number;key=ratio"`
	}
	m := map[string]string{
		"name":    "abc",
		"enabled": "true",
		"count":   "7",
		"ratio":   "1.5",
	}
	var s S
	MapToStruct(m, &s) // must not panic

	if s.Name != "abc" {
		t.Errorf("Name: got %q want abc", s.Name)
	}
	if !s.Enabled {
		t.Errorf("Enabled: got false want true")
	}
	if s.Count != 7 {
		t.Errorf("Count: got %d want 7", s.Count)
	}
	if s.Ratio != 1.5 {
		t.Errorf("Ratio: got %v want 1.5", s.Ratio)
	}
}

// Unparseable / empty values leave the field at its zero value, never panic.
func TestMapToStruct_BadValuesSkipped(t *testing.T) {
	type S struct {
		Enabled bool `wick:"bool;key=enabled"`
		Count   int  `wick:"number;key=count"`
	}
	m := map[string]string{"enabled": "notabool", "count": "xyz"}
	var s S
	MapToStruct(m, &s)
	if s.Enabled {
		t.Errorf("Enabled should stay false on bad input")
	}
	if s.Count != 0 {
		t.Errorf("Count should stay 0 on bad input, got %d", s.Count)
	}
}
