package promptguard

import (
	"context"
	"errors"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/entity"
)

// fakeConfigs is the configs service in memory, calling the change hook
// the way configs.Service does.
type fakeConfigs struct {
	vals map[string]string
	hook func(ctx context.Context, owner, key, before, after string)
}

func (f *fakeConfigs) GetOwned(owner, key string) string { return f.vals[owner+"/"+key] }

func (f *fakeConfigs) SetOwned(ctx context.Context, owner, key, value string) error {
	before := f.vals[owner+"/"+key]
	f.vals[owner+"/"+key] = value
	if f.hook != nil && before != value {
		f.hook(ctx, owner, key, before, value)
	}
	return nil
}

func newTestService(t *testing.T) (*Service, *fakeConfigs, *[]Card) {
	t.Helper()
	dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&entity.PromptChange{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cfg := &fakeConfigs{vals: map[string]string{}}
	s := New(db, cfg)
	cfg.hook = func(ctx context.Context, owner, key, before, after string) {
		s.OnConfigChange(ctx, owner, key, before, after, Actor{UserID: "u-admin", Name: "Admin A"})
	}
	var cards []Card
	s.Notify = func(c Card) { cards = append(cards, c) }
	return s, cfg, &cards
}

var (
	admin = &entity.User{ID: "u-admin", Name: "Admin A", Role: entity.RoleAdmin}
	plain = &entity.User{ID: "u-plain", Name: "User B"}
)

// A UI save is a version at once; the value found before history began
// becomes version 1 so it can be rolled back to.
func TestUISaveRecordsBaselineAndVersion(t *testing.T) {
	ctx := context.Background()
	s, cfg, _ := newTestService(t)
	cfg.vals["agents/"+KeySecurity] = "old rules"
	if err := cfg.SetOwned(ctx, ConfigOwner, KeySecurity, "new rules"); err != nil {
		t.Fatal(err)
	}
	hist, err := s.History(ctx, ConfigTarget(KeySecurity), 0)
	if err != nil || len(hist) != 2 {
		t.Fatalf("history = %+v err=%v", hist, err)
	}
	if hist[0].Version != 2 || hist[0].Via != ViaUI || hist[0].Actor != "Admin A" || hist[0].Value != "new rules" {
		t.Errorf("latest = %+v", hist[0].PromptChange)
	}
	if hist[1].Version != 1 || hist[1].Via != ViaBaseline || hist[1].Value != "old rules" {
		t.Errorf("baseline = %+v", hist[1].PromptChange)
	}
	if strings.Join(hist[0].Diff, "|") != "-old rules|+new rules" {
		t.Errorf("diff = %q", hist[0].Diff)
	}
	// A key the guard does not manage leaves no trace.
	_ = cfg.SetOwned(ctx, ConfigOwner, "gate_enabled", "true")
	if h, _ := s.History(ctx, ConfigTarget("gate_enabled"), 0); len(h) != 0 {
		t.Errorf("unmanaged key recorded: %+v", h)
	}
}

// A proposal changes nothing until an admin accepts it; then it is the
// next version, credited to its proposer and its approver.
func TestProposeThenAccept(t *testing.T) {
	ctx := context.Background()
	s, cfg, cards := newTestService(t)
	ch, diff, err := s.Propose(ctx, ProposeInput{
		Key: KeySecurity, Value: "rule one\nrule two", Reason: "harden", SessionID: "sess-1",
		Actor: Actor{UserID: "u-admin", Name: "Admin A", Agent: "@helper"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.GetOwned(ConfigOwner, KeySecurity); got != "" {
		t.Fatalf("proposal applied before approval: %q", got)
	}
	if len(*cards) != 1 || (*cards)[0].SessionID != "sess-1" || (*cards)[0].Extras["type"] != CardType ||
		(*cards)[0].Extras["approval_id"] != ch.ID {
		t.Fatalf("card = %+v", *cards)
	}
	if len(diff) != 2 {
		t.Errorf("diff = %q", diff)
	}
	if _, err := s.Decide(ctx, ch.ID, plain, "User B", true); !errors.Is(err, ErrNotAdmin) {
		t.Fatalf("non-admin decide: err=%v", err)
	}
	res, err := s.Decide(ctx, ch.ID, admin, "Admin A", true)
	if err != nil {
		t.Fatal(err)
	}
	if res.Version != 1 || cfg.GetOwned(ConfigOwner, KeySecurity) != "rule one\nrule two" {
		t.Fatalf("after accept: %+v value=%q", res, cfg.GetOwned(ConfigOwner, KeySecurity))
	}
	hist, _ := s.History(ctx, ConfigTarget(KeySecurity), 0)
	if len(hist) != 1 || hist[0].Via != ViaApproval || hist[0].ActorAgent != "@helper" || hist[0].DecidedBy != "Admin A" {
		t.Fatalf("history = %+v", hist)
	}
	if _, err := s.Decide(ctx, ch.ID, admin, "Admin A", true); !errors.Is(err, ErrNotPending) {
		t.Errorf("second accept: err=%v", err)
	}
}

// A declined proposal stays in history and changes nothing; a proposal
// made before someone else's change cannot be accepted any more.
func TestDeclineAndStaleBase(t *testing.T) {
	ctx := context.Background()
	s, cfg, _ := newTestService(t)
	a, _, err := s.Propose(ctx, ProposeInput{Key: KeySystem, Value: "A", SessionID: "sess-1", Actor: Actor{Name: "Admin A"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Decide(ctx, a.ID, admin, "Admin A", false); err != nil {
		t.Fatal(err)
	}
	if cfg.GetOwned(ConfigOwner, KeySystem) != "" {
		t.Fatal("declined proposal applied")
	}
	b, _, _ := s.Propose(ctx, ProposeInput{Key: KeySystem, Value: "B", SessionID: "sess-1", Actor: Actor{Name: "Admin A"}})
	_ = cfg.SetOwned(ctx, ConfigOwner, KeySystem, "edited in the UI meanwhile")
	if _, err := s.Decide(ctx, b.ID, admin, "Admin A", true); !errors.Is(err, ErrStaleBase) {
		t.Fatalf("stale accept: err=%v", err)
	}
	if cfg.GetOwned(ConfigOwner, KeySystem) != "edited in the UI meanwhile" {
		t.Fatal("stale proposal overwrote a newer value")
	}
}

// A rollback proposes an earlier version's text; refused when the
// prompt already has it, and for an unknown key or no session.
func TestRollbackAndRefusals(t *testing.T) {
	ctx := context.Background()
	s, cfg, _ := newTestService(t)
	_ = cfg.SetOwned(ctx, ConfigOwner, KeyTeam, "v1")
	_ = cfg.SetOwned(ctx, ConfigOwner, KeyTeam, "v2")
	ch, _, err := s.Propose(ctx, ProposeInput{Key: KeyTeam, RollbackOf: 1, SessionID: "sess-1", Actor: Actor{Name: "Admin A"}})
	if err != nil || ch.Value != "v1" || ch.RollbackOf != 1 {
		t.Fatalf("rollback = %+v err=%v", ch, err)
	}
	if _, err := s.Decide(ctx, ch.ID, admin, "Admin A", true); err != nil {
		t.Fatal(err)
	}
	if cfg.GetOwned(ConfigOwner, KeyTeam) != "v1" {
		t.Fatal("rollback not applied")
	}
	if _, _, err := s.Propose(ctx, ProposeInput{Key: KeyTeam, Value: "v1", SessionID: "sess-1"}); !errors.Is(err, ErrUnchanged) {
		t.Errorf("unchanged: err=%v", err)
	}
	if _, _, err := s.Propose(ctx, ProposeInput{Key: "gate_enabled", Value: "x", SessionID: "sess-1"}); !errors.Is(err, ErrUnknownKey) {
		t.Errorf("unknown key: err=%v", err)
	}
	if _, _, err := s.Propose(ctx, ProposeInput{Key: KeyTeam, Value: "x"}); !errors.Is(err, ErrNoSession) {
		t.Errorf("no session: err=%v", err)
	}
	if _, _, err := s.Propose(ctx, ProposeInput{Key: KeyTeam, RollbackOf: 9, SessionID: "sess-1"}); !errors.Is(err, ErrNoVersion) {
		t.Errorf("missing version: err=%v", err)
	}
}

// Team instructions keep their own history per user.
func TestTeamPromptHistory(t *testing.T) {
	ctx := context.Background()
	s, _, _ := newTestService(t)
	s.OnTeamPrompt(ctx, "u1", "", "be brief")
	s.OnTeamPrompt(ctx, "u1", "be brief", "be brief and kind")
	s.OnTeamPrompt(ctx, "u2", "", "other user")
	h, _ := s.History(ctx, TeamTarget("u1"), 0)
	if len(h) != 2 || h[0].Version != 2 || h[1].Version != 1 {
		t.Fatalf("u1 history = %+v", h)
	}
}
