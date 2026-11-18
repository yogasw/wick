package accessreq

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/pkg/postgres"
)

type run struct {
	user, connector, op string
	params              map[string]any
}

type fakeCat struct {
	mu      sync.Mutex
	ops     map[string]ActionMeta // connectorID.op
	can     map[string]bool       // user|connector
	runs    []run
	runErr  error
	refuse  string // connector id Requestable refuses
	resultF string
	// configure lists user|connector pairs who may configure (override).
	configure map[string]bool
}

func (f *fakeCat) Describe(_ context.Context, cid, op string, ov map[string]string) (ActionMeta, error) {
	if _, bad := ov[""]; bad {
		return ActionMeta{}, errors.New("unreadable override")
	}
	m, ok := f.ops[cid+"."+op]
	if !ok {
		return ActionMeta{}, errors.New("unknown operation")
	}
	return m, nil
}

func (f *fakeCat) Requestable(_ context.Context, _, _, cid string) (bool, string) {
	if cid == f.refuse {
		return false, "not in the agent's access"
	}
	return true, ""
}

func (f *fakeCat) CanRun(_ context.Context, user, cid, _ string, ov map[string]string) (bool, string) {
	if len(ov) > 0 && !f.configure[user+"|"+cid] {
		return false, "may not configure"
	}
	if f.can[user+"|"+cid] {
		return true, ""
	}
	return false, "no access"
}

func (f *fakeCat) Run(_ context.Context, user, cid, op, _ string, params map[string]any) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runs = append(f.runs, run{user, cid, op, params})
	if f.runErr != nil {
		return "run-1", "", f.runErr
	}
	return "run-1", `{"ok":true}`, nil
}

type fakeDir struct {
	users      map[string]Person
	sessionOwn map[string]string
	agentOwn   map[string]string
	askers     map[string]*Asker
}

func (d fakeDir) LastAsker(sid string) *Asker { return d.askers[sid] }

func (d fakeDir) User(_ context.Context, id string) (Person, bool) {
	p, ok := d.users[id]
	return p, ok
}

func (d fakeDir) Admins(context.Context) []Person {
	var out []Person
	for _, p := range d.users {
		if p.IsAdmin {
			out = append(out, p)
		}
	}
	return out
}

func (d fakeDir) SessionOwner(s string) string { return d.sessionOwn[s] }
func (d fakeDir) SessionProject(s string) string {
	switch s {
	case "sess-1", "sess-2":
		return "proj-1"
	case "sess-h":
		return "proj-2"
	}
	return ""
}
func (d fakeDir) AgentOwner(_ context.Context, a string) string { return d.agentOwn[a] }
func (d fakeDir) AgentName(_ context.Context, a string) string {
	if a == "" {
		return ""
	}
	return "@" + a
}

type env struct {
	svc   *Service
	cat   *fakeCat
	woken []string
	mu    sync.Mutex
}

func newEnv(t *testing.T) *env {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: postgres.NewLogLevel("silent")})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	postgres.Migrate(db)
	cat := &fakeCat{
		ops: map[string]ActionMeta{
			"c-notes.update_page":   {ConnectorKey: "notes", ConnectorLabel: "Notes", OpName: "Update page", Destructive: true},
			"c-notes.delete_page":   {ConnectorKey: "notes", ConnectorLabel: "Notes", Destructive: true},
			"c-chat.post_message":   {ConnectorKey: "chat", ConnectorLabel: "Chat", Destructive: true},
			"c-apps.update_setting": {ConnectorKey: "apps", ConnectorLabel: "Apps", Destructive: true},
		},
		can: map[string]bool{
			"owner-a|c-notes": true, "owner-a|c-chat": true, "owner-a|c-apps": true,
			"admin-z|c-notes": true, "admin-z|c-apps": true,
		},
	}
	dir := fakeDir{
		users: map[string]Person{
			"owner-a":  {ID: "owner-a", Name: "Owner A", Approved: true},
			"admin-z":  {ID: "admin-z", Name: "Admin Z", Approved: true, IsAdmin: true},
			"stranger": {ID: "stranger", Name: "Stranger", Approved: true},
			"pending":  {ID: "pending", Name: "Pending", Approved: false},
			"helper-b": {ID: "helper-b", Name: "Helper B", Approved: true},
		},
		sessionOwn: map[string]string{"sess-1": "owner-a", "sess-2": "owner-a", "sess-h": "helper-b"},
		agentOwn:   map[string]string{"agent-1": "owner-a"},
		askers:     map[string]*Asker{"sess-1": {ID: "U-TEST-1", Name: "Person One", Channel: "slack"}},
	}
	e := &env{cat: cat}
	e.svc = New(NewStore(db), cat, dir)
	e.svc.sync = true
	e.svc.now = func() time.Time { return time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC) }
	e.svc.SetWaker(func(_ context.Context, sid, text string) error {
		e.mu.Lock()
		e.woken = append(e.woken, sid+"::"+text)
		e.mu.Unlock()
		return nil
	})
	return e
}

func (e *env) create(t *testing.T, acts ...ActionInput) View {
	t.Helper()
	v, err := e.svc.Create(context.Background(), CreateInput{SessionID: "sess-1", AgentID: "agent-1", Reason: "update the status", Actions: acts})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	return v
}

func act(cid, op string, params map[string]any) ActionInput {
	return ActionInput{ConnectorID: cid, OpKey: op, Params: params}
}

func TestApproveOnceRunsAsApproverAndWakes(t *testing.T) {
	e := newEnv(t)
	v := e.create(t, act("c-notes", "update_page", map[string]any{"page_id": "p1", "status": "done"}))
	if v.Status != StatusPending || v.Pending != 1 || len(v.Items) != 1 {
		t.Fatalf("new request = %+v", v)
	}
	out, res, err := e.svc.Decide(context.Background(), v.ID, "owner-a", []Decision{{ItemID: v.Items[0].ID, Mode: ModeOnce}})
	if err != nil || len(res) != 1 || !res[0].OK {
		t.Fatalf("decide: %v %+v", err, res)
	}
	if len(e.cat.runs) != 1 || e.cat.runs[0].user != "owner-a" || e.cat.runs[0].params["page_id"] != "p1" {
		t.Fatalf("runs = %+v, want one run as owner-a with the exact params", e.cat.runs)
	}
	if out.Items[0].Status != ItemOK || out.Status != StatusDone {
		t.Fatalf("after run: item %s request %s", out.Items[0].Status, out.Status)
	}
	if len(e.woken) != 1 || !strings.Contains(e.woken[0], "sess-1::") || !strings.Contains(e.woken[0], "ran OK") {
		t.Fatalf("woken = %v", e.woken)
	}
}

func TestStrangerSeesActionsButCannotDecide(t *testing.T) {
	e := newEnv(t)
	v := e.create(t, act("c-notes", "update_page", map[string]any{"page_id": "p1"}))
	sv, err := e.svc.ViewFor(context.Background(), v.ID, "stranger")
	if err != nil {
		t.Fatal(err)
	}
	if sv.ViewerCanDecide || sv.Items[0].CanDecide || sv.Items[0].LockedCode != ReasonNotApprover {
		t.Fatalf("stranger view = %+v", sv.Items[0])
	}
	if !strings.Contains(sv.Items[0].LockedReason, "Owner A") || !strings.Contains(sv.ViewerReason, "Who can") {
		t.Fatalf("reason should name who can: %q / %q", sv.Items[0].LockedReason, sv.ViewerReason)
	}
	_, res, _ := e.svc.Decide(context.Background(), v.ID, "stranger", []Decision{{ItemID: v.Items[0].ID, Mode: ModeOnce}})
	if len(res) != 1 || res[0].OK || res[0].Code != ReasonNotApprover {
		t.Fatalf("stranger decide = %+v", res)
	}
	if len(e.cat.runs) != 0 {
		t.Fatal("nothing may run on a refused decision")
	}
}

func TestUnapprovedUserIsNotLinked(t *testing.T) {
	e := newEnv(t)
	v := e.create(t, act("c-notes", "update_page", nil))
	sv, _ := e.svc.ViewFor(context.Background(), v.ID, "pending")
	if sv.Items[0].LockedCode != ReasonNotLinked {
		t.Fatalf("code = %q", sv.Items[0].LockedCode)
	}
}

func TestApproverWithoutOwnAccessIsLocked(t *testing.T) {
	e := newEnv(t)
	// helper-b owns sess-h but cannot run anything on c-notes.
	v, err := e.svc.Create(context.Background(), CreateInput{SessionID: "sess-h", Actions: []ActionInput{act("c-notes", "update_page", nil)}})
	if err != nil {
		t.Fatal(err)
	}
	sv, _ := e.svc.ViewFor(context.Background(), v.ID, "helper-b")
	if sv.Items[0].LockedCode != ReasonApproverAccess || !strings.Contains(sv.Items[0].LockedReason, "Admin Z") {
		t.Fatalf("item = %+v", sv.Items[0])
	}
	av, _ := e.svc.ViewFor(context.Background(), v.ID, "admin-z")
	if !av.Items[0].CanDecide {
		t.Fatal("an admin who can run it may decide")
	}
}

func TestSecondDecisionIsAlreadyDecided(t *testing.T) {
	e := newEnv(t)
	v := e.create(t, act("c-notes", "update_page", nil))
	id := v.Items[0].ID
	if _, res, _ := e.svc.Decide(context.Background(), v.ID, "owner-a", []Decision{{ItemID: id, Mode: ModeDeny, Note: "not now"}}); !res[0].OK {
		t.Fatalf("first = %+v", res)
	}
	_, res, _ := e.svc.Decide(context.Background(), v.ID, "admin-z", []Decision{{ItemID: id, Mode: ModeOnce}})
	if res[0].OK || res[0].Code != ReasonDecided {
		t.Fatalf("second = %+v", res)
	}
	if len(e.cat.runs) != 0 {
		t.Fatal("denied action ran")
	}
	if len(e.woken) != 1 || !strings.Contains(e.woken[0], `denied by Owner A: "not now"`) {
		t.Fatalf("woken = %v", e.woken)
	}
}

func TestModesFollowKind(t *testing.T) {
	e := newEnv(t)
	v := e.create(t,
		act("c-notes", "delete_page", map[string]any{"page_id": "p1"}),
		act("c-chat", "post_message", map[string]any{"text": "hi"}),
		act("c-notes", "update_page", map[string]any{"page_id": "p2"}),
	)
	ov, _ := e.svc.ViewFor(context.Background(), v.ID, "owner-a")
	// A delete, and an action naming no target, are approved once only.
	want := [][]string{{ModeOnce}, {ModeOnce}, {ModeOnce, ModeHour, Mode8h, Mode24h, ModeSession}}
	for i, it := range ov.Items {
		if strings.Join(it.Modes, ",") != strings.Join(want[i], ",") {
			t.Errorf("item %d (%s) modes = %v, want %v", i, it.Kind, it.Modes, want[i])
		}
	}
	_, res, _ := e.svc.Decide(context.Background(), v.ID, "owner-a", []Decision{{ItemID: ov.Items[0].ID, Mode: ModeSession}})
	if res[0].OK || res[0].Code != ReasonModeNotAllowed {
		t.Fatalf("session on a delete = %+v", res)
	}
}

// Standing access is a rule, never a click: no card offers "always",
// and a forged "always" decision is refused, even from an admin.
func TestAlwaysIsNeverOffered(t *testing.T) {
	e := newEnv(t)
	v := e.create(t, act("c-notes", "update_page", nil))
	for _, who := range []string{"admin-z", "owner-a"} {
		sv, _ := e.svc.ViewFor(context.Background(), v.ID, who)
		if contains(sv.Items[0].Modes, ModeAlways) {
			t.Errorf("%s was offered always: %v", who, sv.Items[0].Modes)
		}
	}
	_, res, _ := e.svc.Decide(context.Background(), v.ID, "admin-z", []Decision{{ItemID: v.Items[0].ID, Mode: ModeAlways}})
	if res[0].OK || res[0].Code != ReasonModeNotAllowed {
		t.Fatalf("always accepted: %+v", res)
	}
}

func TestSessionGrantCoversSameSessionOnly(t *testing.T) {
	e := newEnv(t)
	v := e.create(t, act("c-notes", "update_page", map[string]any{"page_id": "p1"}))
	e.svc.Decide(context.Background(), v.ID, "owner-a", []Decision{{ItemID: v.Items[0].ID, Mode: ModeSession}})
	ctx := context.Background()
	p1 := map[string]string{"page_id": "p1", "status": "later"}
	runAs, _, err := e.svc.Check(ctx, Action{SessionID: "sess-1", ConnectorID: "c-notes", OpKey: "update_page", Kind: KindWrite, Params: p1})
	if err != nil || runAs != "owner-a" {
		t.Fatalf("same session, same target, other values: %q %v", runAs, err)
	}
	if runAs, _, _ := e.svc.Check(ctx, Action{SessionID: "sess-1", ConnectorID: "c-notes", OpKey: "update_page", Kind: KindWrite, Params: map[string]string{"page_id": "p2"}}); runAs != "" {
		t.Fatalf("another target got %q", runAs)
	}
	if runAs, _, _ := e.svc.Check(ctx, Action{SessionID: "sess-2", ConnectorID: "c-notes", OpKey: "update_page", Kind: KindWrite, Params: p1}); runAs != "" {
		t.Fatalf("another session got %q", runAs)
	}
	if runAs, _, _ := e.svc.Check(ctx, Action{SessionID: "sess-1", ConnectorID: "c-notes", OpKey: "delete_page", Kind: KindDelete, Params: p1}); runAs != "" {
		t.Fatalf("another op got %q", runAs)
	}
	// A grant does not outlive the approver's access.
	delete(e.cat.can, "owner-a|c-notes")
	if runAs, _, _ := e.svc.Check(ctx, Action{SessionID: "sess-1", ConnectorID: "c-notes", OpKey: "update_page", Params: p1}); runAs != "" {
		t.Fatalf("grant used after the approver lost access: %q", runAs)
	}
}

func TestSessionGrantIsRevocable(t *testing.T) {
	e := newEnv(t)
	v := e.create(t, act("c-notes", "update_page", map[string]any{"page_id": "p1"}))
	e.svc.Decide(context.Background(), v.ID, "owner-a", []Decision{{ItemID: v.Items[0].ID, Mode: ModeSession}})
	ctx := context.Background()
	p1 := map[string]string{"page_id": "p1"}
	if runAs, _, _ := e.svc.Check(ctx, Action{SessionID: "sess-1", ConnectorID: "c-notes", OpKey: "update_page", Params: p1}); runAs != "owner-a" {
		t.Fatalf("session grant: %q", runAs)
	}
	gs, _ := e.svc.Grants(ctx, ModeSession, "sess-1")
	if len(gs) != 1 {
		t.Fatalf("grants = %d", len(gs))
	}
	if err := e.svc.RevokeGrant(ctx, gs[0].ID, "stranger"); err == nil {
		t.Fatal("a stranger revoked a grant")
	}
	if err := e.svc.RevokeGrant(ctx, gs[0].ID, "owner-a"); err != nil {
		t.Fatal(err)
	}
	if runAs, _, _ := e.svc.Check(ctx, Action{SessionID: "sess-1", ConnectorID: "c-notes", OpKey: "update_page", Params: p1}); runAs != "" {
		t.Fatalf("revoked grant still used: %q", runAs)
	}
}

func TestRunErrorShowsOnItem(t *testing.T) {
	e := newEnv(t)
	e.cat.runErr = errors.New("channel_not_found")
	v := e.create(t, act("c-chat", "post_message", map[string]any{"channel": "C1"}))
	out, _, _ := e.svc.Decide(context.Background(), v.ID, "owner-a", []Decision{{ItemID: v.Items[0].ID, Mode: ModeSession}})
	if out.Items[0].Status != ItemError || out.Items[0].Error != "channel_not_found" {
		t.Fatalf("item = %+v", out.Items[0])
	}
	if gs, _ := e.svc.Grants(context.Background(), "", ""); len(gs) != 0 {
		t.Fatal("a failed run must not leave a grant")
	}
}

func TestSecretsMaskedInView(t *testing.T) {
	e := newEnv(t)
	v := e.create(t, act("c-apps", "update_setting", map[string]any{"app_code": "acme-stag", "api_token": "abc123", "header": "Bearer xyz"}))
	js := v.Items[0].ParamsJSON
	if strings.Contains(js, "abc123") || strings.Contains(js, "xyz") || !strings.Contains(js, "acme-stag") {
		t.Fatalf("params json = %s", js)
	}
	if !strings.Contains(v.Items[0].Target, "app_code=acme-stag") {
		t.Fatalf("target = %q", v.Items[0].Target)
	}
}

func TestCreateValidation(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if _, err := e.svc.Create(ctx, CreateInput{SessionID: "sess-1"}); err == nil {
		t.Error("empty actions accepted")
	}
	if _, err := e.svc.Create(ctx, CreateInput{Actions: []ActionInput{act("c-notes", "update_page", nil)}}); err == nil {
		t.Error("no session accepted")
	}
	if _, err := e.svc.Create(ctx, CreateInput{SessionID: "sess-1", Actions: []ActionInput{act("c-notes", "nope", nil)}}); err == nil {
		t.Error("unknown op accepted")
	}
	e.cat.refuse = "c-notes"
	if _, err := e.svc.Create(ctx, CreateInput{SessionID: "sess-1", Actions: []ActionInput{act("c-notes", "update_page", nil)}}); err == nil {
		t.Error("connector outside the agent's access accepted")
	}
	e.cat.refuse = ""
	dup := act("c-notes", "update_page", map[string]any{"a": 1})
	if _, err := e.svc.Create(ctx, CreateInput{SessionID: "sess-1", Actions: []ActionInput{dup, dup}}); err == nil {
		t.Error("duplicate action accepted")
	}
	for i := 0; i < MaxOpenPerSession; i++ {
		e.create(t, act("c-notes", "update_page", map[string]any{"n": i}))
	}
	if _, err := e.svc.Create(ctx, CreateInput{SessionID: "sess-1", Actions: []ActionInput{act("c-notes", "update_page", nil)}}); err == nil {
		t.Error("open request cap not enforced")
	}
}

func TestCancelOnlyFromOwnSession(t *testing.T) {
	e := newEnv(t)
	v := e.create(t, act("c-notes", "update_page", nil))
	if err := e.svc.Cancel(context.Background(), v.ID, "sess-2"); err == nil {
		t.Fatal("another session cancelled it")
	}
	if err := e.svc.Cancel(context.Background(), v.ID, "sess-1"); err != nil {
		t.Fatal(err)
	}
	sv, _ := e.svc.ViewFor(context.Background(), v.ID, "owner-a")
	if sv.ViewerCanDecide || sv.Items[0].LockedCode != ReasonInactive {
		t.Fatalf("cancelled request still decidable: %+v", sv.Items[0])
	}
}

func TestClassifyOp(t *testing.T) {
	cases := map[string]string{"get_page": KindRead, "update_page": KindWrite, "delete_branch": KindDelete, "send_message": KindSend, "post_reply": KindSend}
	for op, want := range cases {
		if got := ClassifyOp(op, op != "get_page"); got != want {
			t.Errorf("%s = %s, want %s", op, got, want)
		}
	}
}

func TestHashBindsParams(t *testing.T) {
	a := HashParams("c", "op", "", map[string]any{"x": 1, "y": "2"})
	b := HashParams("c", "op", "", map[string]any{"y": "2", "x": 1})
	c := HashParams("c", "op", "", map[string]any{"x": 2, "y": "2"})
	if a != b || a == c {
		t.Fatal("hash must ignore key order and change with any value")
	}
}

func TestTargetKey(t *testing.T) {
	a := TargetKey(map[string]string{"page_id": "p1", "status": "x"})
	b := TargetKey(map[string]string{"status": "y", "page_id": "p1"})
	if a != b || a != "page_id=p1" {
		t.Fatalf("target keys %q %q", a, b)
	}
	n1 := TargetKey(map[string]string{"text": "hi"})
	n2 := TargetKey(map[string]string{"text": "bye"})
	if n1 == n2 || !strings.HasPrefix(n1, "params:") {
		t.Fatalf("no target must bind every param: %q %q", n1, n2)
	}
}

type recPub struct {
	mu       sync.Mutex
	statuses []string
}

func (p *recPub) Publish(_ context.Context, v View) (map[string]string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(v.Items) > 0 {
		p.statuses = append(p.statuses, v.Items[0].Status)
	}
	return nil, nil
}

func TestDecideShowsRunningBeforeResult(t *testing.T) {
	e := newEnv(t)
	pub := &recPub{}
	e.svc.AddPublisher(pub)
	v := e.create(t, act("c-notes", "update_page", map[string]any{"page_id": "p1"}))
	pub.statuses = nil
	if _, _, err := e.svc.Decide(context.Background(), v.ID, "owner-a", []Decision{{ItemID: v.Items[0].ID, Mode: ModeOnce}}); err != nil {
		t.Fatal(err)
	}
	if len(pub.statuses) < 2 || pub.statuses[0] != ItemRunning || pub.statuses[len(pub.statuses)-1] != ItemOK {
		t.Fatalf("card states = %v, want running first and ok last", pub.statuses)
	}
}

func TestPruneFinishedKeepsPendingAndRecent(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	old := e.create(t, act("c-notes", "update_page", map[string]any{"page_id": "p1"}))
	open := e.create(t, act("c-notes", "update_page", map[string]any{"page_id": "p2"}))
	if ok, err := e.svc.store.setRequestStatus(ctx, old.ID, StatusPending, StatusCancelled); err != nil || !ok {
		t.Fatalf("cancel: %v %v", ok, err)
	}
	// 31 days later only the finished one goes.
	later := time.Now().Add(RequestRetention + 24*time.Hour)
	e.svc.now = func() time.Time { return later }
	n, err := e.svc.PruneFinished(ctx)
	if err != nil || n != 1 {
		t.Fatalf("pruned %d, %v", n, err)
	}
	if _, _, err := e.svc.store.request(ctx, old.ID); err == nil {
		t.Fatal("finished request still stored")
	}
	if _, _, err := e.svc.store.request(ctx, open.ID); err != nil {
		t.Fatalf("pending request pruned: %v", err)
	}
}

func TestConfigOverrideOnceOnlyAndConfigurers(t *testing.T) {
	e := newEnv(t)
	e.cat.configure = map[string]bool{"admin-z|c-notes": true}
	ctx := context.Background()
	v := e.create(t, act("c-notes", "update_page", map[string]any{"page_id": "p1", ConfigOverrideParam: map[string]any{"mode": "write"}}))
	// The owner can run the op but may not configure the connector.
	ov, _ := e.svc.ViewFor(ctx, v.ID, "owner-a")
	if ov.Items[0].CanDecide {
		t.Fatalf("non-configurer may approve an override: %+v", ov.Items[0])
	}
	av, _ := e.svc.ViewFor(ctx, v.ID, "admin-z")
	if !av.Items[0].CanDecide || !contains(av.Items[0].Modes, ModeOnce) || !contains(av.Items[0].Modes, Mode8h) {
		t.Fatalf("configurer view = %+v", av.Items[0])
	}
	if len(av.Items[0].Approvers) != 1 || av.Items[0].Approvers[0].ID != "admin-z" {
		t.Fatalf("approvers = %+v", av.Items[0].Approvers)
	}
	_, res, _ := e.svc.Decide(ctx, v.ID, "admin-z", []Decision{{ItemID: v.Items[0].ID, Mode: ModeOnce}})
	if len(res) != 1 || !res[0].OK || len(e.cat.runs) != 1 {
		t.Fatalf("once = %+v runs %+v", res, e.cat.runs)
	}
	if _, ok := e.cat.runs[0].params[ConfigOverrideParam]; !ok {
		t.Fatal("the override did not reach the run")
	}
}

func TestGrantNeverCoversAnOverride(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	v := e.create(t, act("c-notes", "update_page", map[string]any{"page_id": "p1"}))
	if _, res, _ := e.svc.Decide(ctx, v.ID, "owner-a", []Decision{{ItemID: v.Items[0].ID, Mode: ModeSession}}); len(res) != 1 || !res[0].OK {
		t.Fatalf("session grant: %+v", res)
	}
	a := Action{SessionID: "sess-1", ConnectorID: "c-notes", ConnectorKey: "notes", OpKey: "update_page", Kind: KindWrite,
		Params: map[string]string{"page_id": "p1"}}
	if runAs, _, _ := e.svc.Check(ctx, a); runAs == "" {
		t.Fatal("grant did not cover the same call")
	}
	a.Params[ConfigOverrideParam] = `{"mode":"write"}`
	if runAs, _, _ := e.svc.Check(ctx, a); runAs != "" {
		t.Fatalf("grant covered a call with an override: %q", runAs)
	}
}

// A timed approval of an override covers the same override on the same
// target only, while its approver may still configure the connector.
func TestTimedGrantCoversSameOverrideOnly(t *testing.T) {
	e := newEnv(t)
	e.cat.configure = map[string]bool{"admin-z|c-notes": true}
	ctx := context.Background()
	v := e.create(t, act("c-notes", "update_page", map[string]any{"page_id": "p1", ConfigOverrideParam: map[string]any{"mode": "write"}}))
	if _, res, _ := e.svc.Decide(ctx, v.ID, "admin-z", []Decision{{ItemID: v.Items[0].ID, Mode: Mode8h}}); len(res) != 1 || !res[0].OK {
		t.Fatalf("8h: %+v", res)
	}
	a := Action{SessionID: "sess-1", ConnectorID: "c-notes", ConnectorKey: "notes", OpKey: "update_page", Kind: KindWrite,
		Params: map[string]string{"page_id": "p1", ConfigOverrideParam: `{"mode": "write"}`}}
	if runAs, _, _ := e.svc.Check(ctx, a); runAs != "admin-z" {
		t.Fatalf("same override within 8h: %q", runAs)
	}
	other := Action{SessionID: "sess-1", ConnectorID: "c-notes", ConnectorKey: "notes", OpKey: "update_page", Kind: KindWrite,
		Params: map[string]string{"page_id": "p1", ConfigOverrideParam: `{"mode":"admin"}`}}
	if runAs, _, _ := e.svc.Check(ctx, other); runAs != "" {
		t.Fatalf("another override covered: %q", runAs)
	}
	plain := Action{SessionID: "sess-1", ConnectorID: "c-notes", ConnectorKey: "notes", OpKey: "update_page", Kind: KindWrite,
		Params: map[string]string{"page_id": "p1"}}
	if runAs, _, _ := e.svc.Check(ctx, plain); runAs != "" {
		t.Fatalf("override grant covered a plain call: %q", runAs)
	}
	base := e.svc.now()
	e.svc.now = func() time.Time { return base.Add(8*time.Hour + time.Minute) }
	if runAs, _, _ := e.svc.Check(ctx, a); runAs != "" {
		t.Fatalf("after 8h: %q", runAs)
	}
}

type switchDir struct {
	fakeDir
	on map[string]bool
}

func (d switchDir) AccessRequestsOn(pid string) bool { return d.on[pid] }

func TestProjectSwitchOffChangesNothing(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	saveRule(t, e, "owner-a", stagingAuto)
	e.svc.dir = switchDir{fakeDir: e.svc.dir.(fakeDir), on: map[string]bool{"proj-2": true}}
	// sess-1 is in proj-1, which is off: no request, and no rule applies.
	if _, err := e.svc.Create(ctx, CreateInput{SessionID: "sess-1", Actions: []ActionInput{act("c-notes", "update_page", nil)}}); !errors.Is(err, ErrOff) {
		t.Fatalf("create in an off project = %v", err)
	}
	a := Action{SessionID: "sess-1", ConnectorID: "c-apps", ConnectorKey: "apps", OpKey: "update_setting", Kind: KindWrite,
		Params: map[string]string{"app_code": "acme-stag"}}
	if runAs, _, err := e.svc.Check(ctx, a); runAs != "" || err != nil {
		t.Fatalf("check in an off project = %q %v", runAs, err)
	}
	e.svc.dir = switchDir{fakeDir: e.svc.dir.(switchDir).fakeDir, on: map[string]bool{"proj-1": true}}
	if !e.svc.Enabled("sess-1") || e.svc.Enabled("sess-h") {
		t.Fatal("switch not per project")
	}
}
