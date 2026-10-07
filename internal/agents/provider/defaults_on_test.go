package provider

import "testing"

func TestStoredFlagDefaultsOn(t *testing.T) {
	if !boolOr(nil, true) {
		t.Fatal("unset flag must read as the default (on)")
	}
	if boolOr(boolPtr(false), true) {
		t.Fatal("an explicit off must survive an on default")
	}
	if !boolOr(boolPtr(true), false) {
		t.Fatal("an explicit on must be kept")
	}
}
