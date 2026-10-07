package delegation

import (
	"context"
	"strings"
	"testing"
)

type fakeTeamRouter struct{ sent []string }

func (f *fakeTeamRouter) TeamHandles(_ context.Context, sessionID string) []string {
	if sessionID != "team-sess" {
		return nil
	}
	return []string{"anton", "vera"}
}

func (f *fakeTeamRouter) SendTeam(_ context.Context, _, handle, body string, human bool) error {
	f.sent = append(f.sent, handle+":"+body)
	return nil
}

// A Team handle is routed over the Team link even with no delegation
// tree; an unknown token and a role-only session stay untouched.
func TestRouteTeamMentions(t *testing.T) {
	tr := &fakeTeamRouter{}
	s := &Service{TeamRouter: tr}
	text := "first\n@anton check the 401s\n@ts-ignore nope\n@anton again"
	ds := s.Route(context.Background(), RouteInput{SessionID: "team-sess", Text: text, Human: true})
	if len(ds) != 1 || ds[0].Kind != TargetTeam || ds[0].Token != "anton" {
		t.Fatalf("dispatches = %+v", ds)
	}
	if len(tr.sent) != 1 || tr.sent[0] != "anton:check the 401s" {
		t.Fatalf("sent = %v", tr.sent)
	}
	if got := FormatDispatches(ds); got != "dispatched: @anton (messaged)" {
		t.Fatalf("format = %q", got)
	}
	note := s.PreRouteNote(context.Background(), RouteInput{SessionID: "team-sess", Text: "@vera hi"})
	if !strings.HasPrefix(note, RoutedMarker) || !strings.Contains(note, "@vera") {
		t.Fatalf("note = %q", note)
	}
	if ds := s.Route(context.Background(), RouteInput{SessionID: "plain", Text: "@anton hi"}); len(ds) != 0 {
		t.Fatalf("non-Team session routed: %+v", ds)
	}
}
