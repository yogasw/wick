package workflow

import (
	"encoding/json"
	"reflect"
	"testing"
)

// TestOpenDMInput_Schema verifies that OpenDMInput has the expected JSON field.
func TestOpenDMInput_Schema(t *testing.T) {
	input := OpenDMInput{UserID: "U12345678"}

	data, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("json.Marshal(OpenDMInput): %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	if _, ok := m["user_id"]; !ok {
		t.Error("OpenDMInput JSON must contain field \"user_id\"")
	}

	// Confirm struct tag via reflection.
	typ := reflect.TypeOf(OpenDMInput{})
	f, ok := typ.FieldByName("UserID")
	if !ok {
		t.Fatal("OpenDMInput must have a UserID field")
	}
	if tag := f.Tag.Get("json"); tag != "user_id" {
		t.Errorf("OpenDMInput.UserID json tag = %q, want \"user_id\"", tag)
	}
}

// TestOpenDMOutput_Fields verifies that OpenDMOutput carries both channel_id
// and user_id fields and marshals them correctly.
func TestOpenDMOutput_Fields(t *testing.T) {
	output := OpenDMOutput{ChannelID: "D87654321", UserID: "U12345678"}

	data, err := json.Marshal(output)
	if err != nil {
		t.Fatalf("json.Marshal(OpenDMOutput): %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	for _, field := range []string{"channel_id", "user_id"} {
		if _, ok := m[field]; !ok {
			t.Errorf("OpenDMOutput JSON must contain field %q", field)
		}
	}

	if m["channel_id"] != "D87654321" {
		t.Errorf("channel_id = %v, want D87654321", m["channel_id"])
	}
	if m["user_id"] != "U12345678" {
		t.Errorf("user_id = %v, want U12345678", m["user_id"])
	}

	// Confirm struct tags via reflection.
	typ := reflect.TypeOf(OpenDMOutput{})
	cases := map[string]string{
		"ChannelID": "channel_id",
		"UserID":    "user_id",
	}
	for fieldName, wantTag := range cases {
		f, ok := typ.FieldByName(fieldName)
		if !ok {
			t.Errorf("OpenDMOutput must have a %s field", fieldName)
			continue
		}
		if tag := f.Tag.Get("json"); tag != wantTag {
			t.Errorf("OpenDMOutput.%s json tag = %q, want %q", fieldName, tag, wantTag)
		}
	}
}
