package manager

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yogasw/wick/internal/configs"
	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/internal/pkg/adminscope"
	"github.com/yogasw/wick/pkg/connector"
	wickentity "github.com/yogasw/wick/pkg/entity"
	"github.com/yogasw/wick/pkg/tool"
)

// testMetaModule mirrors apiDetailModule but gives each op a small input
// schema so the test-meta projection (input fields) can be asserted.
func testMetaModule(key string) connector.Module {
	return connector.Module{
		Meta: connector.Meta{
			Key:         key,
			Name:        "Slack",
			Icon:        "💬",
			DefaultTags: []tool.DefaultTag{},
		},
		Operations: []connector.Category{
			connector.Cat("", "",
				connector.Operation{
					Key:         "send",
					Name:        "Send",
					Description: "Send a message",
					Input: []entity.Config{
						{Key: "channel", Type: "text", Required: true, Description: "target channel"},
						{Key: "text", Type: "textarea"},
					},
					Execute: noopExec,
				},
				connector.Operation{
					Key:         "del",
					Name:        "Delete",
					Description: "Delete a message",
					Destructive: true,
					Execute:     noopExec,
				},
			),
		},
	}
}

func newTestMetaHandler(t *testing.T) (*Handler, *connectors.Service) {
	t.Helper()
	svc := newConnectorsSvcForAPI(t, []connector.Module{testMetaModule("slack")})
	return &Handler{connectors: svc}, svc
}

func TestAPIConnectorTestMeta(t *testing.T) {
	h, svc := newTestMetaHandler(t)
	row, err := svc.Create(t.Context(), "slack", "Prod", map[string]string{}, "u-admin")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	req := adminReq(t, http.MethodGet, "/manager/api/connectors/slack/"+row.ID+"/test-meta", nil)
	req.SetPathValue("key", "slack")
	req.SetPathValue("id", row.ID)
	rec := httptest.NewRecorder()
	h.apiConnectorTestMeta(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got testMetaJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Key != "slack" || got.ID != row.ID || got.Label != "Prod" {
		t.Errorf("identity wrong: %+v", got)
	}
	if len(got.Ops) != 2 {
		t.Fatalf("ops len = %d, want 2", len(got.Ops))
	}
	byKey := map[string]testOpJSON{}
	for _, op := range got.Ops {
		byKey[op.Key] = op
	}
	send, ok := byKey["send"]
	if !ok {
		t.Fatalf("send op missing")
	}
	if len(send.Input) != 2 {
		t.Fatalf("send input len = %d, want 2: %+v", len(send.Input), send.Input)
	}
	if send.Input[0].Key != "channel" || !send.Input[0].Required {
		t.Errorf("channel input wrong: %+v", send.Input[0])
	}
	if send.Input[1].Type != "textarea" {
		t.Errorf("text input type = %q, want textarea", send.Input[1].Type)
	}
	del, ok := byKey["del"]
	if !ok {
		t.Fatalf("del op missing")
	}
	if !del.Destructive {
		t.Errorf("del op should be destructive")
	}
	if len(del.Input) != 0 {
		t.Errorf("del op should have no input: %+v", del.Input)
	}
}

func TestAPIConnectorTestMetaUnknownKey(t *testing.T) {
	h, _ := newTestMetaHandler(t)
	req := adminReq(t, http.MethodGet, "/manager/api/connectors/nope/x/test-meta", nil)
	req.SetPathValue("key", "nope")
	req.SetPathValue("id", "x")
	rec := httptest.NewRecorder()
	h.apiConnectorTestMeta(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestAPIConnectorTestMetaRowNotFound(t *testing.T) {
	h, _ := newTestMetaHandler(t)
	req := adminReq(t, http.MethodGet, "/manager/api/connectors/slack/missing/test-meta", nil)
	req.SetPathValue("key", "slack")
	req.SetPathValue("id", "missing")
	rec := httptest.NewRecorder()
	h.apiConnectorTestMeta(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

// The "Run as" dropdown must only offer accounts the caller may actually run
// as. Service.Execute refuses an AccountID the caller cannot see, so listing
// the whole pool offered options that fail on Run — and disclosed who else
// had connected an account on the row.
func TestAPIConnectorTestMetaHidesOtherUsersAccounts(t *testing.T) {
	h, svc := newTestMetaHandler(t)
	row, err := svc.Create(t.Context(), "slack", "Shared", map[string]string{}, "u-owner")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.SetAccessPolicy(t.Context(), row.ID, connectors.AccessPolicy{EnableSSO: true, MultiAccount: true}); err != nil {
		t.Fatalf("set policy: %v", err)
	}
	if err := svc.SaveAccount(t.Context(), row.ID, "u-alice", "ext-a", "alice", "tok-a"); err != nil {
		t.Fatalf("save alice: %v", err)
	}
	if err := svc.SaveAccount(t.Context(), row.ID, "u-bob", "ext-b", "bob", "tok-b"); err != nil {
		t.Fatalf("save bob: %v", err)
	}

	metaFor := func(u *entity.User) testMetaJSON {
		t.Helper()
		req := pathReq(t, http.MethodGet, "/x", "slack", row.ID, nil)
		req = req.WithContext(login.WithUser(req.Context(), u, nil))
		rec := httptest.NewRecorder()
		h.apiConnectorTestMeta(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
		var got testMetaJSON
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return got
	}

	bob := metaFor(&entity.User{ID: "u-bob", Role: entity.RoleUser})
	if len(bob.Accounts) != 1 || bob.Accounts[0].DisplayName != "bob" {
		t.Fatalf("bob should only be offered his own account, got %+v", bob.Accounts)
	}
	owner := metaFor(&entity.User{ID: "u-owner", Role: entity.RoleUser})
	if len(owner.Accounts) != 2 {
		t.Fatalf("the instance owner should see both accounts, got %+v", owner.Accounts)
	}

	// A shared pool is the row opting in, so everyone sees everything again.
	if err := svc.SetAccessPolicy(t.Context(), row.ID, connectors.AccessPolicy{EnableSSO: true, MultiAccount: true, AllowOthersSeeAccounts: true}); err != nil {
		t.Fatalf("set policy: %v", err)
	}
	shared := metaFor(&entity.User{ID: "u-bob", Role: entity.RoleUser})
	if len(shared.Accounts) != 2 {
		t.Fatalf("shared pool should offer both accounts, got %+v", shared.Accounts)
	}
}

// newTestMetaHandlerCfgs is newTestMetaHandler plus the configs service, so a
// test can flip admin_see_all_connectors — the knob that decides whether being
// an admin is, by itself, enough to run as somebody else's account.
func newTestMetaHandlerCfgs(t *testing.T) (*Handler, *connectors.Service, *configs.Service) {
	t.Helper()
	db := newAPISQLite(t)
	cfgsSvc := configs.NewService(db)
	if err := cfgsSvc.Bootstrap(t.Context()); err != nil {
		t.Fatalf("configs bootstrap: %v", err)
	}
	svc := connectors.NewServiceFromDB(db)
	svc.SetConfigs(cfgsSvc)
	if err := svc.Bootstrap(t.Context(), []connector.Module{testMetaModule("slack")}); err != nil {
		t.Fatalf("connectors bootstrap: %v", err)
	}
	return &Handler{connectors: svc}, svc, cfgsSvc
}

// The "Run as" list answers to the same two switches the rest of the account
// surface does, so what the dropdown offers matches what Run will accept:
//
//   - admin_see_all_connectors off → an admin is scoped like anybody else and
//     is offered only the account they connected themselves.
//   - Others can see connected accounts (AllowOthersSeeAccounts) on → the row
//     opted into a shared pool, so everyone who sees the row is offered all of
//     its accounts.
func TestAPIConnectorTestMetaAccountsFollowAdminKnobAndPoolPolicy(t *testing.T) {
	h, svc, cfgs := newTestMetaHandlerCfgs(t)
	ctx := t.Context()
	// Owned by someone else, so the admin's only way in is the knob.
	row, err := svc.Create(ctx, "slack", "Shared", map[string]string{}, "u-owner")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.SetAccessPolicy(ctx, row.ID, connectors.AccessPolicy{EnableSSO: true, MultiAccount: true}); err != nil {
		t.Fatalf("set policy: %v", err)
	}
	if err := svc.SaveAccount(ctx, row.ID, "u-owner", "ext-o", "owner", "tok-o"); err != nil {
		t.Fatalf("save owner account: %v", err)
	}
	if err := svc.SaveAccount(ctx, row.ID, "u-admin", "ext-ad", "admin", "tok-ad"); err != nil {
		t.Fatalf("save admin account: %v", err)
	}

	names := func(u *entity.User) []string {
		t.Helper()
		req := pathReq(t, http.MethodGet, "/x", "slack", row.ID, nil)
		req = req.WithContext(login.WithUser(req.Context(), u, nil))
		rec := httptest.NewRecorder()
		h.apiConnectorTestMeta(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
		var got testMetaJSON
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		out := make([]string, 0, len(got.Accounts))
		for _, a := range got.Accounts {
			out = append(out, a.DisplayName)
		}
		return out
	}

	admin := &entity.User{ID: "u-admin", Role: entity.RoleAdmin}

	// Knob on (the default): being an admin is enough for the whole pool.
	if got := names(admin); len(got) != 2 {
		t.Fatalf("admin with the knob on should be offered both accounts, got %v", got)
	}

	// Knob off: the admin keeps only the account they connected themselves —
	// the same answer Execute gives, so the dropdown stops offering a run that
	// would be refused.
	if err := cfgs.EnsureOwned(ctx, "agents", wickentity.Config{Key: adminscope.KeyAdminSeeAllConnectors, Value: "true", Type: "bool"}); err != nil {
		t.Fatalf("ensure knob: %v", err)
	}
	if err := cfgs.SetOwned(ctx, "agents", adminscope.KeyAdminSeeAllConnectors, "false"); err != nil {
		t.Fatalf("set knob: %v", err)
	}
	if svc.AdminSeesAllConnectors() {
		t.Fatalf("knob should read false")
	}
	got := names(admin)
	if len(got) != 1 || got[0] != "admin" {
		t.Fatalf("with the knob off the admin should only be offered their own account, got %v", got)
	}

	// The row opting into a shared pool is the other way in, and it applies
	// with the knob still off — the policy is the row's to give.
	if err := svc.SetAccessPolicy(ctx, row.ID, connectors.AccessPolicy{EnableSSO: true, MultiAccount: true, AllowOthersSeeAccounts: true}); err != nil {
		t.Fatalf("set policy: %v", err)
	}
	if got := names(admin); len(got) != 2 {
		t.Fatalf("a shared pool should offer both accounts, got %v", got)
	}
}

// The invariant that makes "offered but refused" impossible: what the Run-as
// dropdown lists is exactly what Execute's own gate would accept, for the
// same person, under every combination of the switches that govern it.
//
// This is the bug it pins. The dropdown offered accounts belonging to other
// people; clicking Run then answered "account … is not accessible: it
// belongs to another user and this connector keeps connected accounts
// private". Both sides already called AccountVisibleTo — they disagreed
// because each built its OWN caller identity, and one of them decided
// "privileged" differently. They now share one constructor.
func TestRunAsListMatchesWhatExecuteWouldAccept(t *testing.T) {
	h, svc, cfgs := newTestMetaHandlerCfgs(t)
	ctx := t.Context()

	row, err := svc.Create(ctx, "slack", "Shared", map[string]string{}, "u-owner")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.SetAccessPolicy(ctx, row.ID, connectors.AccessPolicy{EnableSSO: true, MultiAccount: true}); err != nil {
		t.Fatalf("set policy: %v", err)
	}
	for _, who := range []string{"u-owner", "u-admin", "u-other"} {
		if err := svc.SaveAccount(ctx, row.ID, who, "ext-"+who, who, "tok-"+who); err != nil {
			t.Fatalf("save account %s: %v", who, err)
		}
	}
	fresh, err := svc.Get(ctx, row.ID)
	if err != nil {
		t.Fatalf("reload row: %v", err)
	}

	listed := func(u *entity.User) map[string]bool {
		t.Helper()
		req := pathReq(t, http.MethodGet, "/x", "slack", row.ID, nil)
		req = req.WithContext(login.WithUser(req.Context(), u, nil))
		rec := httptest.NewRecorder()
		h.apiConnectorTestMeta(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("test-meta status = %d: %s", rec.Code, rec.Body.String())
		}
		var got testMetaJSON
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		out := map[string]bool{}
		for _, a := range got.Accounts {
			out[a.ID] = true
		}
		return out
	}

	// What Execute's gate would say for the same person and account.
	executable := func(u *entity.User) map[string]bool {
		t.Helper()
		accs, err := svc.ListAccounts(ctx, row.ID)
		if err != nil {
			t.Fatal(err)
		}
		tags, err := svc.AccountTagIDs(ctx, accs)
		if err != nil {
			t.Fatal(err)
		}
		caller := svc.AccountAccessFor(*fresh, u.ID, u.IsAdmin(), nil)
		out := map[string]bool{}
		for _, a := range accs {
			if connectors.AccountVisibleTo(*fresh, a, tags[a.ID], caller) {
				out[a.ID] = true
			}
		}
		return out
	}

	same := func(name string, u *entity.User) {
		t.Helper()
		l, e := listed(u), executable(u)
		if len(l) != len(e) {
			t.Errorf("%s: dropdown offers %d accounts, Execute would accept %d", name, len(l), len(e))
		}
		for id := range l {
			if !e[id] {
				t.Errorf("%s: dropdown offers account %s that Execute refuses — this is the exact mismatch", name, id)
			}
		}
		for id := range e {
			if !l[id] {
				t.Errorf("%s: Execute accepts account %s the dropdown hides", name, id)
			}
		}
	}

	owner := &entity.User{ID: "u-owner"}
	admin := &entity.User{ID: "u-admin", Role: entity.RoleAdmin}
	other := &entity.User{ID: "u-other"}

	// Knob on: an admin administers every instance.
	same("owner, knob on", owner)
	same("admin, knob on", admin)
	same("stranger, knob on", other)

	// Knob off: the admin is scoped like anybody else. Ownership still counts,
	// which is the rule Yoga chose to keep.
	if err := cfgs.EnsureOwned(ctx, "agents", wickentity.Config{Key: adminscope.KeyAdminSeeAllConnectors, Value: "true", Type: "bool"}); err != nil {
		t.Fatalf("ensure knob: %v", err)
	}
	if err := cfgs.SetOwned(ctx, "agents", adminscope.KeyAdminSeeAllConnectors, "false"); err != nil {
		t.Fatalf("set knob: %v", err)
	}
	if svc.AdminSeesAllConnectors() {
		t.Fatal("the knob should read false")
	}
	same("owner, knob off", owner)
	same("admin, knob off", admin)
	same("stranger, knob off", other)

	// And the scoping is real, not vacuous: with the knob off the admin is
	// down to the one account they connected themselves.
	if got := listed(admin); len(got) != 1 {
		t.Errorf("admin with the knob off sees %d accounts, want only their own", len(got))
	}
	if got := listed(owner); len(got) != 3 {
		t.Errorf("the instance owner sees %d accounts, want all three", len(got))
	}
}
