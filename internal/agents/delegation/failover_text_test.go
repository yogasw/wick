package delegation

import (
	"context"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/event"
)

// A turn a provider failover took over: the failed provider's limit line
// is dropped, the result is the next provider's answer alone.
func TestRunDropsFailedOverText(t *testing.T) {
	stream := &scriptedStream{events: []StreamEvent{
		{Type: event.TextDelta, Text: "You've hit your weekly limit · resets Oct 24"},
		{Type: event.Warning, Text: "Provider failed over", FailedOver: true},
		{Type: event.TextDelta, Text: "the build is green"},
		{Type: event.Done},
	}}
	s, _, _ := runService(t, stream, &fakeRunner{})
	res, err := s.Run(context.Background(), baseReq())
	if err != nil {
		t.Fatal(err)
	}
	if res.Result != "the build is green" {
		t.Fatalf("result = %q", res.Result)
	}
}

// Any other warning leaves the text alone.
func TestRunKeepsTextOnPlainWarning(t *testing.T) {
	stream := &scriptedStream{events: []StreamEvent{
		{Type: event.TextDelta, Text: "part one, "},
		{Type: event.Warning, Text: "slow tool"},
		{Type: event.TextDelta, Text: "part two"},
		{Type: event.Done},
	}}
	s, _, _ := runService(t, stream, &fakeRunner{})
	res, err := s.Run(context.Background(), baseReq())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Result, "part one, part two") {
		t.Fatalf("result = %q", res.Result)
	}
}

// The failed provider's text routes no mention.
func TestObserverDropsFailedOverText(t *testing.T) {
	var got []string
	o := NewTurnObserver(func(_, _ string, text string) { got = append(got, text) })
	o.OnEvent("s1", "claude", event.AgentEvent{Type: event.TextDelta, Text: "You've hit your weekly limit @worker"})
	o.OnEvent("s1", "claude", event.AgentEvent{Type: event.Warning, FailedOver: true})
	o.OnEvent("s1", "claude", event.AgentEvent{Type: event.TextDelta, Text: "@worker check auth"})
	o.OnEvent("s1", "claude", event.AgentEvent{Type: event.Done})
	if len(got) != 1 || got[0] != "@worker check auth" {
		t.Fatalf("got %q", got)
	}
}
