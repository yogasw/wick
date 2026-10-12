package systemprompt

import (
	"strings"
	"testing"
)

// teamFanInSnapshot is the fan-in rule of the Team overlay, word for word:
// the batched wake (teamlink/batch.go) writes the headers it names, so a
// change on either side has to move both.
const teamFanInSnapshot = "- **Team results arrive together.** Replies to what you sent in one turn\n" +
	"  are held and wake you once, in a single `[team results …]` message\n" +
	"  (unless you chose `fan_in: each`; a teammate's question still comes at\n" +
	"  once). Answer the user ONCE with the combined result; never reply per\n" +
	"  teammate or between replies. A `[team results update …]` brings a late\n" +
	"  reply: update your answer with only what changed."

func TestImmutableTeam_FanInLine(t *testing.T) {
	got := ImmutableTeam()
	if !strings.Contains(got, teamFanInSnapshot) {
		t.Fatalf("Team overlay lost the fan-in rule; want it verbatim:\n%s", teamFanInSnapshot)
	}
	// The old per-reply rule must not come back next to it.
	if strings.Contains(got, "each reply arrives on its own") {
		t.Error("Team overlay still says each reply arrives on its own")
	}
}
