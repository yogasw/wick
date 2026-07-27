package sessionoverride

import "testing"

type demoOverride struct {
	Enabled bool   `wick:"bool;key=enabled;desc=Toggle."`
	Level   string `wick:"key=level;dropdown=a|b|c;default=b;desc=Level."`
	Ignored string // no wick tag → not a field
}

func TestRegistrySchema(t *testing.T) {
	Register("demo", demoOverride{})
	schema := Schema("demo")
	if len(schema) != 2 {
		t.Fatalf("want 2 fields (tagged only), got %d: %+v", len(schema), schema)
	}
	byKey := map[string]string{}
	for _, f := range schema {
		byKey[f.Key] = f.Type
	}
	// A `bool` tag yields widget "bool" (an alias of "checkbox" per the tag
	// reference — the FE renderer treats bool/boolean/checkbox identically).
	if byKey["enabled"] != "bool" {
		t.Errorf("enabled should be a bool widget, got %q", byKey["enabled"])
	}
	if byKey["level"] != "dropdown" {
		t.Errorf("level should be a dropdown, got %q", byKey["level"])
	}
	if !HasSchema("demo") || HasSchema("nope") {
		t.Errorf("HasSchema wrong")
	}
	if Schema("nope") != nil {
		t.Errorf("unregistered provider should have nil schema")
	}
}

func TestStoreSetValuesApplyClear(t *testing.T) {
	Register("demo", demoOverride{})
	sid := "s1"
	t.Cleanup(func() { Clear(sid) })

	// Empty before any set.
	if len(Values(sid)) != 0 {
		t.Fatalf("expected empty values initially")
	}

	Set(sid, "enabled", "true")
	Set(sid, "level", "c")

	// Values is a copy — mutating it must not affect the store.
	v := Values(sid)
	v["enabled"] = "MUTATED"
	if Values(sid)["enabled"] != "true" {
		t.Errorf("Values must return a copy; store was mutated")
	}

	var o demoOverride
	Apply(sid, &o)
	if !o.Enabled || o.Level != "c" {
		t.Fatalf("Apply didn't fill struct: %+v", o)
	}

	// A field never set falls back to the tag default.
	Clear(sid)
	Set(sid, "enabled", "true")
	var o2 demoOverride
	Apply(sid, &o2)
	if o2.Level != "b" {
		t.Errorf("unset field should default to b, got %q", o2.Level)
	}

	Clear(sid)
	if len(Values(sid)) != 0 {
		t.Errorf("Clear should empty the session")
	}
}

// Sessions are isolated from each other.
func TestStoreIsolation(t *testing.T) {
	t.Cleanup(func() { Clear("a"); Clear("b") })
	Set("a", "level", "a")
	Set("b", "level", "b")
	if Values("a")["level"] != "a" || Values("b")["level"] != "b" {
		t.Fatalf("session values bled across ids")
	}
}
