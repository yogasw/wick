package slack

import (
	"context"
	"strings"
	"testing"
)

func TestSessionConfigFilledWakesOnlyForOpenCard(t *testing.T) {
	var woke []string
	prev := WakeSession
	WakeSession = func(_ context.Context, sessionID, text string) error {
		woke = append(woke, sessionID+": "+text)
		return nil
	}
	t.Cleanup(func() { WakeSession = prev })

	s := &Channel{}
	if s.SessionConfigFilled(context.Background(), "sess-a", "sw_1", "alice", []string{"api_token"}) {
		t.Fatal("no card was posted: want false")
	}
	if len(woke) != 0 {
		t.Fatalf("plain config edit must not wake: %v", woke)
	}

	s.cfgCards = map[string]cfgCardRef{"sess-a|sw_1": {chatID: "C01TESTCHAN", ts: "1.2", label: "acme staging"}}
	if !s.SessionConfigFilled(context.Background(), "sess-a", "sw_1", "alice", []string{"api_token"}) {
		t.Fatal("open card: want true")
	}
	if len(woke) != 1 || !strings.Contains(woke[0], "alice filled api_token on sw_1") {
		t.Fatalf("wake = %v", woke)
	}
	if _, still := s.cfgCards["sess-a|sw_1"]; still {
		t.Fatal("card must be forgotten after it is filled")
	}
	if s.SessionConfigFilled(context.Background(), "sess-a", "sw_1", "alice", []string{"api_token"}) {
		t.Fatal("second fill of a closed card must not wake again")
	}
}
