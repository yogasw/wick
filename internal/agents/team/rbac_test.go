package team

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/entity"
)

func rbacStore(t *testing.T) *Store {
	t.Helper()
	db := testDB(t)
	if err := db.AutoMigrate(&entity.AgentRBAC{}); err != nil {
		t.Fatal(err)
	}
	return NewStore(db)
}

func TestRBACDocValidate(t *testing.T) {
	if err := DefaultRBACDoc().Validate(); err != nil {
		t.Fatalf("default doc invalid: %v", err)
	}
	bad := RBACDoc{
		Roles: map[string]RBACRole{"owner": {Rank: 50, Lend: "read"}, "Ops!": {Rank: 120, Lend: "maybe"}},
		Rules: []RBACRule{{Match: "user"}, {Match: "group", Role: "ghost"}},
		Lend:  map[string]map[string]RBACLevel{"c1": {"nobody": {Level: "read"}, "Ops!": {Level: LevelPick}}},
	}
	err := bad.Validate()
	if err == nil {
		t.Fatal("bad doc accepted")
	}
	for _, want := range []string{"built in", "rank must be 1-99", "lend must be off, read or all", "needs the user id", "match must be user or shared", "unknown role \"ghost\"", "lend c1: unknown role \"nobody\"", "pick needs at least one op"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
}

func TestRBACDocAcceptsGuestLendAndCoOwner(t *testing.T) {
	d := DefaultRBACDoc()
	d.Rules = append([]RBACRule{{Match: MatchUser, Value: "user-a", Role: RoleOwner}, {Match: MatchUser, Value: "user-z", Role: RoleGuest}}, d.Rules...)
	d.Lend["music"] = map[string]RBACLevel{RoleGuest: {Level: LevelAll}}
	if err := d.Validate(); err != nil {
		t.Fatalf("guest lend / co-owner refused: %v", err)
	}
	if !d.HasCoOwner() {
		t.Fatal("co-owner rule not seen")
	}
	if got := d.RoleFor("user-owner", "user-a", nil); got != RoleOwner {
		t.Fatalf("co-owner role = %q", got)
	}
	if got := d.RoleFor("user-owner", "user-z", func() bool { return true }); got != RoleGuest {
		t.Fatalf("user ruled guest = %q", got)
	}
	// Guest has no default: a row without guest lends it nothing, and a
	// role's default does not reach guests.
	d.Lend["docs"] = map[string]RBACLevel{}
	if lv := d.LendLevel(RoleGuest, "docs"); lv.Level != LevelOff {
		t.Fatalf("guest borrowed docs by default: %s", lv.Level)
	}
	if lv := d.LendLevel(RoleGuest, "music"); lv.Level != LevelAll {
		t.Fatalf("guest on music = %s", lv.Level)
	}
}

func TestRolesScopeGuestLendAndCoOwner(t *testing.T) {
	ctx := context.Background()
	d := DefaultRBACDoc()
	d.Lend["docs"] = map[string]RBACLevel{RoleGuest: {Level: LevelAll}}
	s := rolesScope(d)
	for _, w := range []struct {
		caller, conn string
		lent         bool
		level        string
	}{
		{"user-g", "docs", true, LevelAll},  // lent to guest by name, writes included
		{"", "docs", true, LevelAll},        // an unknown caller is a guest too
		{"user-g", "docs", false, LevelOff}, // still nothing of their own
		{"user-g", "tickets", true, LevelOff},
	} {
		if lv, _, _ := s.CallerLevel(ctx, w.caller, w.conn, w.lent); lv != w.level {
			t.Errorf("CallerLevel(%q, %s, lent=%v) = %s, want %s", w.caller, w.conn, w.lent, lv, w.level)
		}
	}
	if got := s.LentIDs(); strings.Join(got, ",") != "docs" {
		t.Fatalf("LentIDs with a guest lend = %v", got)
	}

	// A co-owner borrows every connector of the agent; others still only
	// what the table lends them.
	d.Rules = append([]RBACRule{{Match: MatchUser, Value: "user-a", Role: RoleOwner}}, d.Rules...)
	s = rolesScope(d, "user-m")
	if got := s.LentIDs(); strings.Join(got, ",") != "chat,docs,tickets" {
		t.Fatalf("LentIDs with a co-owner = %v", got)
	}
	if lv, _, _ := s.CallerLevel(ctx, "user-a", "tickets", true); lv != LevelAll {
		t.Fatalf("co-owner on tickets = %s", lv)
	}
	if lv, _, _ := s.CallerLevel(ctx, "user-m", "tickets", true); lv != LevelOff {
		t.Fatalf("member borrowed tickets through the co-owner lend: %s", lv)
	}
}

func TestRBACLevelJSON(t *testing.T) {
	in := `{"c1":{"member":"read","trusted":{"level":"pick","ops":["get","list"]}}}`
	var m map[string]map[string]RBACLevel
	if err := json.Unmarshal([]byte(in), &m); err != nil {
		t.Fatal(err)
	}
	if m["c1"]["member"].Level != LevelRead || m["c1"]["trusted"].Level != LevelPick || len(m["c1"]["trusted"].Ops) != 2 {
		t.Fatalf("decoded %+v", m)
	}
	out, _ := json.Marshal(m)
	if string(out) != `{"c1":{"member":"read","trusted":{"level":"pick","ops":["get","list"]}}}` {
		t.Fatalf("round trip = %s", out)
	}
}

func TestRBACRoleFor(t *testing.T) {
	d := DefaultRBACDoc()
	d.Rules = append([]RBACRule{{Match: MatchUser, Value: "user-b", Role: "trusted"}, {Match: MatchUser, Value: "user-x", Role: "deleted"}}, d.Rules...)
	shared := func(v bool) func() bool { return func() bool { return v } }
	cases := []struct {
		caller string
		shared bool
		want   string
	}{
		{"user-owner", false, RoleOwner},
		{"user-b", false, "trusted"},
		{"user-c", true, "member"},
		{"user-c", false, RoleGuest},
		{"user-x", true, RoleGuest}, // first match names a role that no longer exists
		{"", true, RoleGuest},
		{internalPrincipal, true, RoleGuest},
	}
	for _, c := range cases {
		if got := d.RoleFor("user-owner", c.caller, shared(c.shared)); got != c.want {
			t.Errorf("RoleFor(%q, shared=%v) = %q, want %q", c.caller, c.shared, got, c.want)
		}
	}
}

func TestRBACStoreRevisions(t *testing.T) {
	ctx := context.Background()
	st := rbacStore(t)
	d, rev, err := st.RBAC(ctx, "agent-1")
	if err != nil || rev != 0 || d.Roles["member"].Lend != LevelRead {
		t.Fatalf("default = %+v rev %d err %v", d, rev, err)
	}
	d.Lend["c1"] = map[string]RBACLevel{"member": {Level: LevelRead}}
	if rev, err = st.SetRBAC(ctx, "agent-1", d, 0, "user-owner", "lend docs"); err != nil || rev != 1 {
		t.Fatalf("first save rev %d err %v", rev, err)
	}
	if _, err := st.SetRBAC(ctx, "agent-1", d, 0, "user-owner", "stale"); !errors.Is(err, ErrRBACConflict) {
		t.Fatalf("stale save err = %v, want conflict", err)
	}
	bad := d
	bad.Rules = []RBACRule{{Match: "nope", Role: "member"}}
	if _, err := st.SetRBAC(ctx, "agent-1", bad, 1, "user-owner", ""); err == nil {
		t.Fatal("invalid doc saved")
	}
	d2, rev, _ := st.RBAC(ctx, "agent-1")
	if rev != 1 || d2.Lend["c1"]["member"].Level != LevelRead {
		t.Fatalf("in force = %+v rev %d", d2, rev)
	}
	hist, err := st.RBACHistory(ctx, "agent-1", 10)
	if err != nil || len(hist) != 1 || hist[0].Reason != "lend docs" || hist[0].UpdatedBy != "user-owner" {
		t.Fatalf("history = %+v err %v", hist, err)
	}
}

func rolesScope(doc RBACDoc, sharedWith ...string) *Scope {
	p := entity.AgentPersona{ID: "agent-1", OwnerUserID: "user-owner", AccessMode: AccessRoles,
		AllowedConnectors: EncodeGrants([]ConnectorGrant{{ConnectorID: "chat", Level: LevelAll, Lend: true}, {ConnectorID: "docs", Level: LevelAll}, {ConnectorID: "tickets", Level: LevelAll}})}
	shared := map[string]bool{}
	for _, u := range sharedWith {
		shared[u] = true
	}
	return ScopeOf(p, reachOf("docs", "chat", "tickets")).WithRoles(doc, func(_ context.Context, _, uid string) bool { return shared[uid] })
}

func TestRolesScopeLendsFromDocument(t *testing.T) {
	ctx := context.Background()
	d := DefaultRBACDoc()
	d.Rules = append([]RBACRule{{Match: MatchUser, Value: "user-t", Role: "trusted"}}, d.Rules...)
	d.Lend["docs"] = map[string]RBACLevel{}                                                      // both roles at their default
	d.Lend["tickets"] = map[string]RBACLevel{"member": {Level: LevelPick, Ops: []string{"get"}}} // narrowed for members
	s := rolesScope(d, "user-m")

	// The grant's own Lend flag is ignored in By role access.
	if _, lent := s.LentBy("chat"); lent {
		t.Fatal("chat lent through the grant flag")
	}
	if got := s.LentIDs(); strings.Join(got, ",") != "docs,tickets" {
		t.Fatalf("LentIDs = %v", got)
	}
	// Picked connectors keep their checklist level.
	if s.Level("chat") != LevelAll {
		t.Fatalf("chat level = %s", s.Level("chat"))
	}

	type want struct {
		caller, conn string
		lent         bool
		level        string
	}
	for _, w := range []want{
		{"user-owner", "docs", true, LevelAll},
		{"user-t", "docs", true, LevelAll},     // trusted borrows at all
		{"user-m", "docs", true, LevelRead},    // member: read by default
		{"user-m", "tickets", true, LevelPick}, // narrowed to picked ops
		{"user-m", "chat", true, LevelOff},     // not in the lend table
		{"user-m", "chat", false, LevelAll},    // own access is kept
		{"user-g", "docs", false, LevelOff},    // guest: nothing, own access included
		{"user-g", "docs", true, LevelOff},     // and no lend that does not name guest
		{"", "docs", false, LevelOff},
	} {
		lv, _, _ := s.CallerLevel(ctx, w.caller, w.conn, w.lent)
		if lv != w.level {
			t.Errorf("CallerLevel(%s, %s, lent=%v) = %s, want %s", w.caller, w.conn, w.lent, lv, w.level)
		}
	}

	// A role whose writes need approval reads only, even when lent at all.
	d.Lend["docs"] = map[string]RBACLevel{"member": {Level: LevelAll}}
	s = rolesScope(d, "user-m")
	lv, _, why := s.CallerLevel(ctx, "user-m", "docs", true)
	if lv != LevelRead || !strings.Contains(why, "approval") {
		t.Fatalf("approval role = %s (%s)", lv, why)
	}
}

// By role keeps the checklist as the agent's ceiling: a connector the
// owner has but the agent does not list is off for everyone, the owner and
// a co-owner included, and is never lent.
func TestRolesScopeCappedAtChecklist(t *testing.T) {
	p := entity.AgentPersona{ID: "agent-1", OwnerUserID: "user-owner", AccessMode: AccessRoles,
		AllowedConnectors: EncodeGrants([]ConnectorGrant{{ConnectorID: "music", Level: LevelAll}})}
	d := DefaultRBACDoc()
	d.Rules = append([]RBACRule{{Match: MatchUser, Value: "user-a", Role: RoleOwner}}, d.Rules...)
	d.Lend["repo"] = map[string]RBACLevel{RoleGuest: {Level: LevelAll}}
	s := ScopeOf(p, reachOf("music", "repo")).WithRoles(d, nil)
	if s.Level("music") != LevelAll || s.Level("repo") != LevelOff {
		t.Fatalf("music %s, repo %s", s.Level("music"), s.Level("repo"))
	}
	if _, lent := s.LentBy("repo"); lent {
		t.Fatal("a connector outside the checklist was lent")
	}
	if got := s.LentIDs(); strings.Join(got, ",") != "music" {
		t.Fatalf("LentIDs = %v", got)
	}
}

// A guest who has a connector of their own still gets the guest lend in
// the preview, as at run time (their own access is off, the lend is not).
func TestEffectiveGuestWithOwnAccessBorrows(t *testing.T) {
	d := DefaultRBACDoc()
	d.Lend["docs"] = map[string]RBACLevel{RoleGuest: {Level: LevelAll}}
	s := rolesScope(d)
	rows := Effective(s, effCatalog(), EffectiveCaller{UserID: "user-g", Reaches: func(string) bool { return true },
		Level: func(id string, lent bool) (string, []string) {
			lv, ops, _ := s.CallerLevel(context.Background(), "user-g", id, lent)
			return lv, ops
		}}, nil)
	if docs := rowOf(rows, "docs"); docs.RunsAs != RunsAsOwner || len(docs.Ops) == 0 {
		t.Fatalf("guest with own access on docs = %+v, want the guest lend", docs)
	}
	if chat := rowOf(rows, "chat"); chat.RunsAs != RunsAsNone {
		t.Fatalf("guest kept own access on chat: %+v", chat)
	}
}

func TestNonRoleScopePermitsEveryCaller(t *testing.T) {
	p := entity.AgentPersona{ID: "a", OwnerUserID: "o", AccessMode: AccessOwner}
	s := ScopeOf(p, reachOf("docs")).WithRoles(DefaultRBACDoc(), nil)
	if s.InRoles() {
		t.Fatal("owner-mode scope took roles")
	}
	if lv, _, _ := s.CallerLevel(context.Background(), "", "docs", true); lv != LevelAll {
		t.Fatalf("non-role scope limited a caller: %s", lv)
	}
}

func TestEffectiveAppliesRole(t *testing.T) {
	d := DefaultRBACDoc()
	d.Lend["docs"] = map[string]RBACLevel{}
	s := rolesScope(d, "user-m")
	cat := effCatalog()
	member := EffectiveCaller{UserID: "user-m", Level: func(id string, lent bool) (string, []string) {
		lv, ops, _ := s.CallerLevel(context.Background(), "user-m", id, lent)
		return lv, ops
	}}
	rows := Effective(s, cat, member, nil)
	docs := rowOf(rows, "docs")
	if docs.RunsAs != RunsAsOwner || strings.Join(docs.Ops, ",") != "read" {
		t.Fatalf("member on docs = %+v, want lent with read only", docs)
	}
	if chat := rowOf(rows, "chat"); chat.RunsAs != RunsAsNone {
		t.Fatalf("member got chat without a lend or own access: %+v", chat)
	}
	guest := Effective(s, cat, EffectiveCaller{Level: func(id string, lent bool) (string, []string) {
		lv, ops, _ := s.CallerLevel(context.Background(), "", id, lent)
		return lv, ops
	}}, nil)
	for _, r := range guest {
		if r.RunsAs != RunsAsNone {
			t.Fatalf("guest reaches %s", r.ConnectorID)
		}
	}
}
