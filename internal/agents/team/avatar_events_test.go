package team

import (
	"reflect"
	"testing"

	"github.com/yogasw/wick/internal/agents/store"
)

func strp(s string) *string { return &s }

// An agent's event table survives a save: known rows stay, Off wins over
// a pose, unknown events / states / expressions are dropped, and a table
// with nothing left is no table at all.
func TestAvatarEventsRoundTrip(t *testing.T) {
	in := Avatar{Kind: AvatarKindBlob, Shape: "cloud", Color: "#4b8fea", Expression: "happy", Events: map[string]AvatarEventPose{
		"read":    {State: "orbit", Expression: strp("suspicious")},
		"error":   {Off: true, State: "wide"},
		"done":    {Expression: strp("")},
		"write":   {State: "nope", Expression: strp("grumpy")},
		"mystery": {State: "idle"},
	}}
	got := DecodeAvatar(EncodeAvatar(in))
	want := map[string]AvatarEventPose{
		"read":  {State: "orbit", Expression: strp("suspicious")},
		"error": {Off: true},
		"done":  {Expression: strp("")},
	}
	if !reflect.DeepEqual(got.Events, want) {
		t.Fatalf("events %+v", got.Events)
	}
	if got.Expression != "happy" || got.Shape != "cloud" {
		t.Fatalf("look changed: %+v", got)
	}
	if a := NormalizeAvatar(Avatar{Shape: "circle", Color: "#000", Events: map[string]AvatarEventPose{"read": {State: "bogus"}}}); a.Events != nil {
		t.Fatalf("empty table kept: %+v", a.Events)
	}
	// Rows written before events decode with no table.
	if a := DecodeAvatar(`{"shape":"circle","color":"#6366f1"}`); a.Events != nil {
		t.Fatalf("old row: %+v", a)
	}
}

// AvatarEvents and BlobStates mirror the UI's lists.
func TestAvatarEventListsMatchUI(t *testing.T) {
	if len(AvatarEvents) != 9 || AvatarEvents[0] != "read" || AvatarEvents[8] != "compact" {
		t.Fatalf("AvatarEvents %v", AvatarEvents)
	}
	if len(BlobStates) != 13 || BlobStates[0] != "idle" || BlobStates[12] != "comet" {
		t.Fatalf("BlobStates %v", BlobStates)
	}
}

// ToolFailed is the newest finished tool's error, cleared by the next
// tool_use; thinking or text after the failure keep it.
func TestToolFailed(t *testing.T) {
	use := store.TurnEvent{Type: "tool_use", ToolName: "Bash", ToolUseID: "1"}
	bad := store.TurnEvent{Type: "tool_result", ToolUseID: "1", IsError: true}
	ok := store.TurnEvent{Type: "tool_result", ToolUseID: "1"}
	think := store.TurnEvent{Type: "thinking"}
	cases := []struct {
		evs  []store.TurnEvent
		want bool
	}{
		{nil, false},
		{[]store.TurnEvent{use}, false},
		{[]store.TurnEvent{use, ok}, false},
		{[]store.TurnEvent{use, bad}, true},
		{[]store.TurnEvent{use, bad, think}, true},
		{[]store.TurnEvent{use, bad, use}, false},
	}
	for i, c := range cases {
		if got := ToolFailed(c.evs); got != c.want {
			t.Errorf("case %d: %v, want %v", i, got, c.want)
		}
	}
}
