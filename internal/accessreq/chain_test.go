package accessreq

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func (e *env) createIn(t *testing.T, in CreateInput) View {
	t.Helper()
	if in.SessionID == "" {
		in.SessionID = "sess-1"
	}
	if in.AgentID == "" {
		in.AgentID = "agent-1"
	}
	if in.Reason == "" {
		in.Reason = "plan step"
	}
	v, err := e.svc.Create(context.Background(), in)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	return v
}

func approveAll(t *testing.T, e *env, v View) View {
	t.Helper()
	var ds []Decision
	for _, it := range v.Items {
		if it.Status == ItemPending {
			ds = append(ds, Decision{ItemID: it.ID, Mode: ModeOnce})
		}
	}
	out, res, err := e.svc.Decide(context.Background(), v.ID, "owner-a", ds)
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	for _, r := range res {
		if !r.OK {
			t.Fatalf("decision refused: %+v", r)
		}
	}
	return out
}

func TestSameKeyReplacesUntouchedRequest(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	v1 := e.createIn(t, CreateInput{Key: "step-2", Actions: []ActionInput{act("c-notes", "update_page", map[string]any{"page_id": "p1", "v": 1})}})
	v2 := e.createIn(t, CreateInput{Key: "step-2", Actions: []ActionInput{act("c-notes", "update_page", map[string]any{"page_id": "p1", "v": 2})}})
	if v2.Version != 2 || v2.Key != "step-2" {
		t.Fatalf("v2 = version %d key %q", v2.Version, v2.Key)
	}
	old, _ := e.svc.ViewFor(ctx, v1.ID, "owner-a")
	if old.Status != StatusSuperseded || old.SupersededBy != v2.ID || old.Replacement == nil || old.Replacement.Version != 2 {
		t.Fatalf("old = status %s by %q", old.Status, old.SupersededBy)
	}
	if old.ViewerCanDecide {
		t.Fatal("a replaced request is still decidable")
	}
	// Replaced requests do not count toward the open cap.
	for i := 0; i < MaxOpenPerSession+2; i++ {
		e.createIn(t, CreateInput{Key: "step-2", Actions: []ActionInput{act("c-notes", "update_page", map[string]any{"page_id": "p1", "v": 10 + i})}})
	}
}

func TestSameKeyAfterRunFollowsInstead(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	v1 := e.createIn(t, CreateInput{Key: "step-2", Actions: []ActionInput{act("c-notes", "update_page", map[string]any{"page_id": "p1"})}})
	approveAll(t, e, v1)
	v2 := e.createIn(t, CreateInput{Key: "step-2", Actions: []ActionInput{act("c-notes", "update_page", map[string]any{"page_id": "p2"})}})
	if v2.After != v1.ID || v2.Version != 2 {
		t.Fatalf("v2 after %q version %d, want after v1", v2.After, v2.Version)
	}
	old, _ := e.svc.ViewFor(ctx, v1.ID, "")
	if old.Status != StatusDone {
		t.Fatalf("v1 must keep its history, status = %s", old.Status)
	}
	if v2.AfterState != AfterOK {
		t.Fatalf("after state = %s", v2.AfterState)
	}
}

func TestAfterQueuesUntilEarlierFinishesOK(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	first := e.createIn(t, CreateInput{Key: "step-1", Actions: []ActionInput{act("c-notes", "update_page", map[string]any{"page_id": "p1"})}})
	second := e.createIn(t, CreateInput{Key: "step-2", After: "step-1", Actions: []ActionInput{act("c-apps", "update_setting", map[string]any{"app_id": "a1"})}})
	if second.After != first.ID || second.AfterState != AfterWaiting {
		t.Fatalf("second after %q state %q", second.After, second.AfterState)
	}
	// Approving step 2 first: nothing runs, its action is queued.
	out := approveAll(t, e, second)
	if len(e.cat.runs) != 0 || out.Items[0].Status != ItemQueued || out.Status != StatusPending {
		t.Fatalf("runs %+v item %s status %s", e.cat.runs, out.Items[0].Status, out.Status)
	}
	// Step 1 runs OK: step 2 follows on its own, in order.
	approveAll(t, e, first)
	if len(e.cat.runs) != 2 || e.cat.runs[0].op != "update_page" || e.cat.runs[1].op != "update_setting" {
		t.Fatalf("runs = %+v", e.cat.runs)
	}
	done, _ := e.svc.ViewFor(ctx, second.ID, "")
	if done.Status != StatusDone || done.Items[0].Status != ItemOK || done.Items[0].DecidedBy != "Owner A" {
		t.Fatalf("second = %s / %+v", done.Status, done.Items[0])
	}
}

func TestAfterSkippedWhenEarlierFails(t *testing.T) {
	e := newEnv(t)
	first := e.createIn(t, CreateInput{Key: "step-1", Actions: []ActionInput{act("c-notes", "update_page", map[string]any{"page_id": "p1"})}})
	second := e.createIn(t, CreateInput{Key: "step-2", After: first.ID, Actions: []ActionInput{
		act("c-apps", "update_setting", map[string]any{"app_id": "a1"}),
		act("c-apps", "update_setting", map[string]any{"app_id": "a2"}),
	}})
	// One action approved (queued), one still waiting for a person.
	if _, res, _ := e.svc.Decide(context.Background(), second.ID, "owner-a", []Decision{{ItemID: second.Items[0].ID, Mode: ModeOnce}}); !res[0].OK {
		t.Fatalf("decide = %+v", res)
	}
	e.cat.runErr = errors.New("upstream said no")
	approveAll(t, e, first)
	if len(e.cat.runs) != 1 {
		t.Fatalf("runs = %+v, step 2 must not run after step 1 failed", e.cat.runs)
	}
	out, _ := e.svc.ViewFor(context.Background(), second.ID, "")
	if out.Status != StatusDone || out.Items[0].Status != ItemSkipped || out.Items[1].Status != ItemSkipped {
		t.Fatalf("second = %s %s %s", out.Status, out.Items[0].Status, out.Items[1].Status)
	}
	if !strings.Contains(out.Items[0].Error, "step-1") {
		t.Fatalf("skip reason = %q", out.Items[0].Error)
	}
}

func TestSupersedesExplicitAndRefusesStarted(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	v1 := e.createIn(t, CreateInput{Actions: []ActionInput{act("c-notes", "update_page", map[string]any{"page_id": "p1"})}})
	v2 := e.createIn(t, CreateInput{Supersedes: v1.ID[:8], Actions: []ActionInput{act("c-notes", "update_page", map[string]any{"page_id": "p1", "x": 1})}})
	if v2.Version != 2 {
		t.Fatalf("version = %d", v2.Version)
	}
	approveAll(t, e, v2)
	if _, err := e.svc.Create(ctx, CreateInput{SessionID: "sess-1", AgentID: "agent-1", Reason: "r", Supersedes: v2.ID,
		Actions: []ActionInput{act("c-notes", "update_page", map[string]any{"page_id": "p9"})}}); err == nil || !strings.Contains(err.Error(), "after=") {
		t.Fatalf("superseding a request that ran = %v", err)
	}
	if _, err := e.svc.Create(ctx, CreateInput{SessionID: "sess-2", Reason: "r", Supersedes: v2.ID,
		Actions: []ActionInput{act("c-notes", "update_page", map[string]any{"page_id": "p9"})}}); err == nil {
		t.Fatal("another session superseded it")
	}
}

func TestSimilarPendingIsRefusedWithHowToReplace(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.createIn(t, CreateInput{Key: "step-2", Actions: []ActionInput{act("c-notes", "update_page", map[string]any{"page_id": "p1", "v": 1})}})
	_, err := e.svc.Create(ctx, CreateInput{SessionID: "sess-1", AgentID: "agent-1", Reason: "r",
		Actions: []ActionInput{act("c-notes", "update_page", map[string]any{"page_id": "p1", "v": 2})}})
	var sim *SimilarError
	if !errors.As(err, &sim) || sim.Key != "step-2" || !strings.Contains(err.Error(), `key="step-2"`) {
		t.Fatalf("err = %v", err)
	}
	// force keeps both on purpose; another target is not similar.
	e.createIn(t, CreateInput{Force: true, Actions: []ActionInput{act("c-notes", "update_page", map[string]any{"page_id": "p1", "v": 3})}})
	e.createIn(t, CreateInput{Actions: []ActionInput{act("c-notes", "update_page", map[string]any{"page_id": "p2"})}})
}

func TestTurnNoteListsSessionRequests(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if note := e.svc.TurnNote(ctx, "sess-1"); note != "" {
		t.Fatalf("note without requests = %q", note)
	}
	v1 := e.createIn(t, CreateInput{Key: "step-1", Actions: []ActionInput{act("c-notes", "update_page", map[string]any{"page_id": "p1"})}})
	e.createIn(t, CreateInput{Key: "step-1", Actions: []ActionInput{act("c-notes", "update_page", map[string]any{"page_id": "p1", "v": 2})}})
	note := e.svc.TurnNote(ctx, "sess-1")
	if !strings.Contains(note, "[access requests]") || !strings.Contains(note, "step-1 · ") || !strings.Contains(note, "v2") {
		t.Fatalf("note = %q", note)
	}
	if strings.Contains(note, short(v1.ID)) {
		t.Fatalf("replaced v1 still listed: %q", note)
	}
	if e.svc.TurnNote(ctx, "sess-2") != "" {
		t.Fatal("another session sees this session's requests")
	}
}

func TestMaxActionsFitsOnePlanStep(t *testing.T) {
	e := newEnv(t)
	var acts []ActionInput
	for i := 0; i < MaxActions; i++ {
		acts = append(acts, act("c-notes", "update_page", map[string]any{"page_id": "p", "n": i}))
	}
	v := e.createIn(t, CreateInput{Key: "big", Actions: acts})
	if len(v.Items) != MaxActions || MaxActions < 50 {
		t.Fatalf("items = %d max %d", len(v.Items), MaxActions)
	}
	if _, err := e.svc.Create(context.Background(), CreateInput{SessionID: "sess-1", Reason: "r", Force: true,
		Actions: append(acts, act("c-notes", "update_page", map[string]any{"page_id": "p", "n": -1}))}); err == nil {
		t.Fatal("over MaxActions accepted")
	}
}

func TestPendingInThreadFindsOpenCard(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	v := e.createIn(t, CreateInput{Actions: []ActionInput{act("c-notes", "update_page", map[string]any{"page_id": "p1"})}})
	_ = e.svc.store.setOrigin(ctx, v.ID, `{"slack_channel":"C01TESTCHAN","slack_thread":"1.2","slack_ts":"1.3"}`)
	if got := e.svc.PendingInThread(ctx, "C01TESTCHAN", "1.2"); len(got) != 1 || got[0] != v.ID {
		t.Fatalf("pending in thread = %v", got)
	}
	if got := e.svc.PendingInThread(ctx, "C01TESTCHAN", "9.9"); len(got) != 0 {
		t.Fatalf("other thread = %v", got)
	}
}

func TestDifferentKeysAreNotSimilar(t *testing.T) {
	e := newEnv(t)
	e.createIn(t, CreateInput{Key: "step-1", Actions: []ActionInput{act("c-chat", "post_message", map[string]any{"channel": "C1", "text": "a"})}})
	// Another named step to the same channel is a different step.
	e.createIn(t, CreateInput{Key: "step-2", Actions: []ActionInput{act("c-chat", "post_message", map[string]any{"channel": "C1", "text": "b"})}})
	// No key: still caught.
	if _, err := e.svc.Create(context.Background(), CreateInput{SessionID: "sess-1", AgentID: "agent-1", Reason: "r",
		Actions: []ActionInput{act("c-chat", "post_message", map[string]any{"channel": "C1", "text": "c"})}}); err == nil {
		t.Fatal("an unnamed repeat was not refused")
	}
}

func TestAfterAFailedRequestIsRefused(t *testing.T) {
	e := newEnv(t)
	first := e.createIn(t, CreateInput{Key: "step-1", Actions: []ActionInput{act("c-notes", "update_page", map[string]any{"page_id": "p1"})}})
	e.cat.runErr = errors.New("upstream said no")
	approveAll(t, e, first)
	_, err := e.svc.Create(context.Background(), CreateInput{SessionID: "sess-1", AgentID: "agent-1", Reason: "r", Key: "step-2", After: "step-1",
		Actions: []ActionInput{act("c-apps", "update_setting", map[string]any{"app_id": "a1"})}})
	if err == nil || !strings.Contains(err.Error(), "never run") {
		t.Fatalf("after a failed request = %v", err)
	}
}
