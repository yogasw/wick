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

func TestConfigCardExpiryAndClose(t *testing.T) {
	var woke []string
	prev := WakeSession
	WakeSession = func(_ context.Context, _ string, text string) error {
		woke = append(woke, text)
		return nil
	}
	t.Cleanup(func() { WakeSession = prev })

	if cardExpired([]string{"s", "i", "k"}) {
		t.Fatal("a card without a stamp never expires")
	}
	if !cardExpired([]string{"s", "i", "k", "1"}) {
		t.Fatal("a stamp in the past is expired")
	}
	if cardExpired([]string{"s", "i", "k", "99999999999"}) {
		t.Fatal("a stamp in the future is not expired")
	}

	s := &Channel{cfgCards: map[string]cfgCardRef{"sess-b|sw_2": {chatID: "C01TESTCHAN", ts: "5.6", label: "acme"}}}
	s.expireConfigCard("sess-b", "sw_2", "9.9") // stale timer of an older card
	if len(woke) != 0 {
		t.Fatalf("stale timer must not close the newer card: %v", woke)
	}
	s.expireConfigCard("sess-b", "sw_2", "5.6")
	if len(woke) != 1 || !strings.Contains(woke[0], "expired") {
		t.Fatalf("wake = %v", woke)
	}
	if !s.cfgClosed["5.6"] {
		t.Fatal("expired card must be remembered as closed")
	}
	if s.SessionConfigFilled(context.Background(), "sess-b", "sw_2", "alice", []string{"api_token"}) {
		t.Fatal("expired card must not be filled from the web form")
	}
}
