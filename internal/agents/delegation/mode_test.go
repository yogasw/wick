package delegation

import (
	"context"
	"testing"

	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/entity"
)

// The whole point of the flip: a caller that says nothing does not block.
func TestModeDefaultsToBackground(t *testing.T) {
	cases := []struct {
		name      string
		requested string
		roleDflt  string
		want      string
	}{
		{"nothing stated", "", "", ModeAsync},
		{"role says nothing, caller wants foreground", "foreground", "", ModeSync},
		{"role wants foreground", "", "foreground", ModeSync},
		{"caller overrides a foreground role", "background", "foreground", ModeAsync},
		{"legacy spellings still resolve", "async", "sync", ModeAsync},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := NormalizeMode(c.requested, c.roleDflt); got != c.want {
				t.Fatalf("NormalizeMode(%q, %q) = %q, want %q",
					c.requested, c.roleDflt, got, c.want)
			}
		})
	}
}

// A model that learned "async" from an older tool description must not be
// punished for it, and a typo must not be silently read as the default.
func TestParseModeAcceptsBothVocabulariesAndRejectsJunk(t *testing.T) {
	for _, in := range []string{"background", "BACKGROUND", " async ", "async"} {
		got, ok := ParseMode(in)
		if !ok || got != ModeAsync {
			t.Fatalf("ParseMode(%q) = %q, %v; want async, true", in, got, ok)
		}
	}
	for _, in := range []string{"foreground", "Foreground", "sync"} {
		got, ok := ParseMode(in)
		if !ok || got != ModeSync {
			t.Fatalf("ParseMode(%q) = %q, %v; want sync, true", in, got, ok)
		}
	}
	if _, ok := ParseMode("blocking"); ok {
		t.Fatal("ParseMode accepted a mode nobody defined")
	}
	if got, ok := ParseMode(""); !ok || got != "" {
		t.Fatalf(`ParseMode("") = %q, %v; want "", true`, got, ok)
	}
}

// The stored value is sync/async; everything a model reads says
// foreground/background. A leader deciding whether the answer is in front
// of it reads Mode, so the label has to be the spoken one.
func TestModeLabelSpeaksTheUserFacingNames(t *testing.T) {
	if got := ModeLabel(ModeSync); got != ModeForeground {
		t.Fatalf("ModeLabel(sync) = %q, want %q", got, ModeForeground)
	}
	if got := ModeLabel(ModeAsync); got != ModeBackground {
		t.Fatalf("ModeLabel(async) = %q, want %q", got, ModeBackground)
	}
	// Empty is background too: an unset mode never blocks.
	if got := ModeLabel(""); got != ModeBackground {
		t.Fatalf("ModeLabel(\"\") = %q, want %q", got, ModeBackground)
	}
}

// End to end: a delegation with no mode asked for returns a handle, not an
// answer, and the leader is woken rather than left blocking.
func TestDelegationWithNoModeRunsInTheBackground(t *testing.T) {
	stream := &scriptedStream{events: []StreamEvent{
		{Type: event.TextDelta, Text: "the answer"},
		{Type: event.Done},
	}}
	s, r, _ := runService(t, stream, &fakeRunner{})
	del := &recordingDeliverer{}
	s.Deliver = del

	req := baseReq()
	req.Mode = "" // exactly what a caller that says nothing sends

	res, err := s.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Mode != ModeBackground {
		t.Fatalf("mode = %q, want %q", res.Mode, ModeBackground)
	}
	if res.Result != "" {
		t.Fatalf("a background handle must not carry a result, got %q", res.Result)
	}

	waitForStatus(t, r, res.DelegationID, entity.DelegationDone)
	if got := del.sessionDeliveries(); len(got) != 1 {
		t.Fatalf("session deliveries = %v, want exactly one", got)
	}
}
