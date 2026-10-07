package agents

import (
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/store"
	"github.com/yogasw/wick/internal/entity"
)

func TestTeamAllowList(t *testing.T) {
	own := []entity.AgentPersona{{ID: "a1"}, {ID: "a2"}, {ID: "a3"}}
	got, err := teamAllowList([]string{"a2", " a3", "a2", ""}, own, "a1")
	if err != nil || len(got) != 2 || got[0] != "a2" || got[1] != "a3" {
		t.Fatalf("got %v, %v", got, err)
	}
	if _, err := teamAllowList([]string{"a1"}, own, "a1"); err == nil {
		t.Fatal("self allowed")
	}
	if _, err := teamAllowList([]string{"zz"}, own, "a1"); err == nil {
		t.Fatal("another owner's agent allowed")
	}
}

func TestIDListRoundTrip(t *testing.T) {
	if encodeIDList(nil) != "[]" || len(decodeIDList("not json")) != 0 {
		t.Fatal("empty forms")
	}
	if got := decodeIDList(encodeIDList([]string{"a", "b"})); len(got) != 2 || got[1] != "b" {
		t.Fatalf("round trip = %v", got)
	}
}

func TestHopLimitTurnNamesTheCap(t *testing.T) {
	turn := hopLimitTurn(map[string]string{}, 2, time.Unix(0, 0))
	if turn.Kind != store.KindHopLimit || turn.Text != "Agent-to-agent limit of 2 turns reached — reply to continue" || turn.Extras["max_turns"] != "2" {
		t.Fatalf("turn = %+v", turn)
	}
}
