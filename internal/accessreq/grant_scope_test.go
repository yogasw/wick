package accessreq

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

const requireNotes = `name: notes-always-card
effect: require
when:
  ops: [notes.update_page]
`

func TestHourGrantExpires(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	v := e.create(t, act("c-notes", "update_page", map[string]any{"page_id": "p1"}))
	if _, res, _ := e.svc.Decide(ctx, v.ID, "owner-a", []Decision{{ItemID: v.Items[0].ID, Mode: ModeHour}}); len(res) != 1 || !res[0].OK {
		t.Fatalf("hour: %+v", res)
	}
	a := Action{SessionID: "sess-1", ConnectorID: "c-notes", ConnectorKey: "notes", OpKey: "update_page", Kind: KindWrite,
		Params: map[string]string{"page_id": "p1"}}
	if runAs, _, _ := e.svc.Check(ctx, a); runAs != "owner-a" {
		t.Fatalf("inside the hour: %q", runAs)
	}
	b := a
	b.Params = map[string]string{"page_id": "p2"}
	if runAs, _, _ := e.svc.Check(ctx, b); runAs != "" {
		t.Fatalf("another target inside the hour: %q", runAs)
	}
	if gs, _ := e.svc.Grants(ctx, ModeSession, "sess-1"); len(gs) != 1 || gs[0].ExpiresAt == nil {
		t.Fatalf("grants = %+v", gs)
	}
	base := e.svc.now()
	e.svc.now = func() time.Time { return base.Add(GrantHour + time.Minute) }
	if runAs, _, _ := e.svc.Check(ctx, a); runAs != "" {
		t.Fatalf("after the hour: %q", runAs)
	}
	if gs, _ := e.svc.Grants(ctx, ModeSession, "sess-1"); len(gs) != 0 {
		t.Fatalf("expired grant still listed: %+v", gs)
	}
}

func TestRequireRuleApprovesOnceOnly(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	// A grant left before the rule existed...
	v := e.create(t, act("c-notes", "update_page", map[string]any{"page_id": "p1"}))
	e.svc.Decide(ctx, v.ID, "owner-a", []Decision{{ItemID: v.Items[0].ID, Mode: ModeSession}})
	saveRule(t, e, "owner-a", requireNotes)
	a := Action{SessionID: "sess-1", ConnectorID: "c-notes", ConnectorKey: "notes", OpKey: "update_page", Kind: KindWrite,
		Params: map[string]string{"page_id": "p1"}}
	// ...no longer covers the call.
	if runAs, _, _ := e.svc.Check(ctx, a); runAs != "" {
		t.Fatalf("grant used under a require rule: %q", runAs)
	}
	v2 := e.create(t, act("c-notes", "update_page", map[string]any{"page_id": "p2"}))
	ov, _ := e.svc.ViewFor(ctx, v2.ID, "owner-a")
	if len(ov.Items[0].Modes) != 1 || ov.Items[0].Modes[0] != ModeOnce {
		t.Fatalf("require offered %v", ov.Items[0].Modes)
	}
	if _, res, _ := e.svc.Decide(ctx, v2.ID, "owner-a", []Decision{{ItemID: v2.Items[0].ID, Mode: ModeHour}}); len(res) != 1 || res[0].OK {
		t.Fatalf("hour accepted under require: %+v", res)
	}
}

func TestNoTargetLeavesNoGrant(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	v := e.create(t, act("c-chat", "post_message", map[string]any{"text": "to everyone"}))
	if v.Items[0].Modes != nil && len(v.Items[0].Modes) > 1 {
		t.Fatalf("modes = %v", v.Items[0].Modes)
	}
	if _, res, _ := e.svc.Decide(ctx, v.ID, "owner-a", []Decision{{ItemID: v.Items[0].ID, Mode: ModeSession}}); len(res) != 1 || res[0].OK {
		t.Fatalf("session accepted without a target: %+v", res)
	}
	if !HasTarget(map[string]string{"room_id": "r1"}) || HasTarget(map[string]string{"text": "x"}) || HasTarget(map[string]string{"room_id": " "}) {
		t.Fatal("HasTarget")
	}
}

func TestConnectorRemovedEndsGrantsAndPending(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	v := e.create(t, act("c-notes", "update_page", map[string]any{"page_id": "p1"}))
	e.svc.Decide(ctx, v.ID, "owner-a", []Decision{{ItemID: v.Items[0].ID, Mode: ModeSession}})
	pending := e.create(t, act("c-notes", "update_page", map[string]any{"page_id": "p2"}), act("c-chat", "post_message", map[string]any{"channel": "C01TESTCHAN"}))
	if err := e.svc.ConnectorRemoved(ctx, "sess-1", "c-notes", ""); err != nil {
		t.Fatal(err)
	}
	if gs, _ := e.svc.Grants(ctx, ModeSession, "sess-1"); len(gs) != 0 {
		t.Fatalf("grants left: %+v", gs)
	}
	after, _ := e.svc.ViewFor(ctx, pending.ID, "owner-a")
	if after.Items[0].Status != ItemDenied || after.Items[1].Status != ItemPending {
		t.Fatalf("items = %s / %s", after.Items[0].Status, after.Items[1].Status)
	}
	// Another session's grants are untouched.
	if err := e.svc.ConnectorRemoved(ctx, "sess-2", "c-chat", ""); err != nil {
		t.Fatal(err)
	}
	if after, _ := e.svc.ViewFor(ctx, pending.ID, "owner-a"); after.Items[1].Status != ItemPending {
		t.Fatal("another session's removal touched this one")
	}
}

func TestAskedByRecordedAndInScript(t *testing.T) {
	e := newEnv(t)
	v := e.create(t, act("c-notes", "update_page", map[string]any{"page_id": "p1"}))
	if v.AskedBy == nil || v.AskedBy.ID != "U-TEST-1" {
		t.Fatalf("asked by = %+v", v.AskedBy)
	}
	in := scriptInput(Action{AskedBy: v.AskedBy}, nil)
	r, _ := in["requester"].(map[string]any)
	if r["id"] != "U-TEST-1" || r["channel"] != "slack" {
		t.Fatalf("requester = %v", in["requester"])
	}
	if in := scriptInput(Action{}, nil); in["requester"] != nil {
		t.Fatalf("no asker must read null: %v", in["requester"])
	}
}

func TestApproversText(t *testing.T) {
	only := approverRule{doc: RuleDoc{Approvers: ApproversOnly, ApproversLabel: "members of @ops"}}
	add := approverRule{doc: RuleDoc{Approvers: ApproversAdd, ApproversLabel: "the on-call"}}
	if got := approversText([]approverRule{only, add}, []string{"Owner A"}); got != "members of @ops" {
		t.Fatalf("only = %q", got)
	}
	if got := approversText([]approverRule{add}, []string{"Owner A"}); got != "Owner A, the on-call" {
		t.Fatalf("add = %q", got)
	}
	if got := approversText([]approverRule{{doc: RuleDoc{Approvers: ApproversOnly}}}, []string{"Owner A"}); got != "" {
		t.Fatalf("no label = %q", got)
	}
}

// A require rule whose script fails still requires a card (fails closed).
func TestRequireScriptErrorFailsClosed(t *testing.T) {
	e := newEnv(t)
	e.svc.SetScriptRunner(func(context.Context, string, string, map[string]any) (json.RawMessage, []string, error) {
		return nil, nil, errors.New("boom")
	})
	saveRule(t, e, "owner-a", "name: prod-card\neffect: require\nwhen:\n  ops: [notes.*]\n  script: return true\n")
	v := e.svc.Evaluate(context.Background(), Action{SessionID: "sess-1", ConnectorID: "c-notes", ConnectorKey: "notes", OpKey: "update_page", Kind: KindWrite})
	if v.Effect != EffectRequire {
		t.Fatalf("effect = %q", v.Effect)
	}
}
