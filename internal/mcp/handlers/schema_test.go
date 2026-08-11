package handlers

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/yogasw/wick/pkg/entity"
)

// TestDropdownEnumSplitsOnPipe covers a schema that rejected the only valid values.
//
// entity.Config documents Options as pipe-separated ("a|b|c"), but splitOptions split
// on "," — so a dropdown declared `wick:"dropdown=push|pop|list"` produced a
// one-element enum holding the literal string "push|pop|list". Every value a caller
// could legitimately send was outside the enum. wick does not validate enums, which is
// why it went unnoticed, but a strict JSON Schema validator on the caller's side would
// have refused exactly the correct inputs.
func TestDropdownEnumSplitsOnPipe(t *testing.T) {
	got := ConfigsToJSONSchema([]entity.Config{
		{Key: "action", Type: "dropdown", Options: "push|pop|list"},
	})
	prop := got.Properties["action"]

	if prop.Type != "string" {
		t.Errorf("type = %q, want string", prop.Type)
	}
	want := []string{"push", "pop", "list"}
	if !reflect.DeepEqual(prop.Enum, want) {
		t.Errorf("enum = %#v, want %#v — one element containing pipes rejects every valid value",
			prop.Enum, want)
	}
}

// TestDropdownEnumStillAcceptsCommas keeps an option list written the other way
// working, rather than collapsing it into one bogus value.
func TestDropdownEnumStillAcceptsCommas(t *testing.T) {
	got := ConfigsToJSONSchema([]entity.Config{
		{Key: "mode", Type: "dropdown", Options: "soft, mixed , hard"},
	})
	want := []string{"soft", "mixed", "hard"}
	if !reflect.DeepEqual(got.Properties["mode"].Enum, want) {
		t.Errorf("enum = %#v, want %#v", got.Properties["mode"].Enum, want)
	}
}

// TestBooleanWidgetsDeclareBoolean covers the other half: a boolean field declared as
// a string.
//
// widgetFor lets an explicit tag flag win over the Go type, so a Go bool tagged
// `wick:"bool"` produces the widget "bool" rather than "checkbox". This switch only
// knew "checkbox", so those fields fell through to the default and were declared
// strings. Parsing was fine — CfgBool accepts "true"/"false" — but the schema left a
// caller guessing whether to send true, "true" or "1", which is the whole reason the
// schema exists.
func TestBooleanWidgetsDeclareBoolean(t *testing.T) {
	for _, widget := range []string{"checkbox", "bool", "boolean"} {
		got := ConfigsToJSONSchema([]entity.Config{{Key: "dry_run", Type: widget}})
		if tp := got.Properties["dry_run"].Type; tp != "boolean" {
			t.Errorf("widget %q declared type %q, want boolean", widget, tp)
		}
	}
}

// TestSchemaMarshalsEnumAsAnArray is the wire-level check. The two tests above compare
// Go slices; this one confirms the JSON a caller actually receives is an array of
// separate strings, which is the form a validator reads.
func TestSchemaMarshalsEnumAsAnArray(t *testing.T) {
	blob, err := json.Marshal(ConfigsToJSONSchema([]entity.Config{
		{Key: "action", Type: "dropdown", Options: "push|pop|list"},
		{Key: "force", Type: "bool"},
	}))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out struct {
		Properties struct {
			Action struct {
				Enum []string `json:"enum"`
			} `json:"action"`
			Force struct {
				Type string `json:"type"`
			} `json:"force"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(blob, &out); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, blob)
	}
	if len(out.Properties.Action.Enum) != 3 {
		t.Errorf("enum has %d entries, want 3: %s", len(out.Properties.Action.Enum), blob)
	}
	if out.Properties.Force.Type != "boolean" {
		t.Errorf("force type = %q, want boolean: %s", out.Properties.Force.Type, blob)
	}
}
