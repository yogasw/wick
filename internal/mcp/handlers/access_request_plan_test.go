package handlers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/accessreq"
)

type fakeAccess struct {
	created []accessreq.CreateInput
	views   []accessreq.View
}

func (f *fakeAccess) Create(_ context.Context, in accessreq.CreateInput) (accessreq.View, error) {
	f.created = append(f.created, in)
	return accessreq.View{ID: "req-new", Status: "pending", Key: in.Key, Version: 1, Pending: len(in.Actions)}, nil
}

func (f *fakeAccess) ViewFor(_ context.Context, id, _ string) (accessreq.View, error) {
	for _, v := range f.views {
		if v.ID == id {
			return v, nil
		}
	}
	return accessreq.View{}, accessreq.ErrNotFound
}

func (f *fakeAccess) Cancel(context.Context, string, string) error { return nil }

func (f *fakeAccess) SessionRequests(context.Context, string, int) ([]accessreq.View, error) {
	return f.views, nil
}

func withFakeAccess(t *testing.T, f *fakeAccess) {
	t.Helper()
	prev := AccessRequests
	AccessRequests = f
	t.Cleanup(func() { AccessRequests = prev })
}

func TestAccessViewByRefFindsKeyAndPrefix(t *testing.T) {
	f := &fakeAccess{views: []accessreq.View{
		{ID: "bbbbbbbb-2", SessionID: "s1", Key: "step-2", Version: 2},
		{ID: "aaaaaaaa-1", SessionID: "s1", Key: "step-2", Version: 1},
		{ID: "cccccccc-3", SessionID: "s2", Key: "other"},
	}}
	withFakeAccess(t, f)
	ctx := context.Background()
	if v, err := accessViewByRef(ctx, "s1", "step-2"); err != nil || v.ID != "bbbbbbbb-2" {
		t.Fatalf("by key = %v %v, want the newest version", v.ID, err)
	}
	if v, err := accessViewByRef(ctx, "s1", "aaaaaaaa"); err != nil || v.ID != "aaaaaaaa-1" {
		t.Fatalf("by prefix = %v %v", v.ID, err)
	}
	if _, err := accessViewByRef(ctx, "s1", "cccccccc-3"); err == nil {
		t.Fatal("another session's request found")
	}
}

func TestAccessListRowReadsLikeTodo(t *testing.T) {
	v := accessreq.View{ID: "r1", Key: "step-3", Version: 1, Status: "pending",
		AfterRef: &accessreq.Ref{ID: "r0", Key: "step-2", Version: 2}, AfterState: accessreq.AfterWaiting,
		Counts: map[string]int{accessreq.ItemQueued: 2},
		Items:  []accessreq.ItemView{{Status: accessreq.ItemQueued}, {Status: accessreq.ItemQueued}}}
	b, _ := json.Marshal(accessListRow(v))
	s := string(b)
	for _, want := range []string{`"key":"step-3"`, `"after":"step-2 v2"`, `"after_state":"waiting"`, `"counts":"2 queued"`} {
		if !strings.Contains(s, want) {
			t.Errorf("row misses %s: %s", want, s)
		}
	}
}

func TestRecordReqNeedsReason(t *testing.T) {
	if r, err := recordReqOf(map[string]any{}); r != nil || err != nil {
		t.Fatalf("no request_access = %v %v", r, err)
	}
	if _, err := recordReqOf(map[string]any{"request_access": map[string]any{"key": "k"}}); err == nil {
		t.Fatal("missing reason accepted")
	}
	if _, err := recordReqOf(map[string]any{"request_access": "yes"}); err == nil {
		t.Fatal("non-object accepted")
	}
	r, err := recordReqOf(map[string]any{"request_access": map[string]any{"reason": " fix the form ", "key": "k", "after": "k0"}})
	if err != nil || r.Reason != "fix the form" || r.Key != "k" || r.After != "k0" {
		t.Fatalf("parsed = %+v %v", r, err)
	}
}

func TestFileRecordedMakesOneCard(t *testing.T) {
	f := &fakeAccess{}
	withFakeAccess(t, f)
	rec := &writeRecorder{}
	if n := rec.add(accessreq.ActionInput{ConnectorID: "c1", OpKey: "update"}); n != 1 {
		t.Fatalf("n = %d", n)
	}
	rec.add(accessreq.ActionInput{ConnectorID: "c1", OpKey: "update", Params: map[string]any{"x": 1}})
	if !strings.Contains(recordStub(2), `"recorded":true`) {
		t.Fatal("stub does not say recorded")
	}
	out, err := fileRecorded(context.Background(), "s1", &recordReq{Reason: "r", Key: "k"}, rec.actions())
	if err != nil || len(f.created) != 1 || len(f.created[0].Actions) != 2 || f.created[0].Key != "k" {
		t.Fatalf("filed = %v %+v", err, f.created)
	}
	if out["request_id"] != "req-new" {
		t.Fatalf("out = %v", out)
	}
	if out, err := fileRecorded(context.Background(), "s1", &recordReq{Reason: "r"}, nil); err != nil || out["status"] != "nothing_to_approve" {
		t.Fatalf("no writes = %v %v", out, err)
	}
	many := make([]accessreq.ActionInput, accessreq.MaxActions+1)
	if _, err := fileRecorded(context.Background(), "s1", &recordReq{Reason: "r"}, many); err == nil {
		t.Fatal("over MaxActions filed")
	}
}
