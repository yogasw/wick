package delegation

import (
	"context"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/entity"
)

// What a child's stream carries across a provider failover (pool
// failover.go): the failed provider's partial text, the failover notice
// as a Warning (its Error is rewritten, its exit publishes no Done), then
// the target's continuation and the target's Done. The run must end Done
// with the target's answer, not Failed on the notice.
func TestRunCarriesOnThroughProviderFailover(t *testing.T) {
	stream := &scriptedStream{events: []StreamEvent{
		{Type: event.TextDelta, Text: "half "},
		{Type: event.Warning, Text: "claude/a stopped: usage limit reached. Continuing on claude/b."},
		{Type: event.TextDelta, Text: "answer from b"},
		{Type: event.Done},
	}}
	s, _, _ := runService(t, stream, &fakeRunner{})

	res, err := s.Run(context.Background(), baseReq())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Status != entity.DelegationDone {
		t.Fatalf("status = %q, want %q", res.Status, entity.DelegationDone)
	}
	if !strings.Contains(res.Result, "answer from b") {
		t.Fatalf("result = %q, want the target's answer", res.Result)
	}
}

// A failover that could not go through publishes the original Error
// (OnFailoverAbort): the run fails on it as it would have without
// failover, instead of hanging on a turn nobody carries on.
func TestRunFailsWhenFailoverAborts(t *testing.T) {
	stream := &scriptedStream{events: []StreamEvent{
		{Type: event.Warning, Text: "claude/a stopped: usage limit reached. Continuing on claude/b."},
		{Type: event.Error, Text: "You've hit your session limit"},
		{Type: event.Done},
	}}
	s, _, _ := runService(t, stream, &fakeRunner{})

	res, err := s.Run(context.Background(), baseReq())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Status != entity.DelegationFailed {
		t.Fatalf("status = %q, want %q", res.Status, entity.DelegationFailed)
	}
}
