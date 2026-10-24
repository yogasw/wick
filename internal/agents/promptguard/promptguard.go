// Package promptguard keeps the version history of the prompts every
// agent spawn carries — the operator prompts (`system_prompt_security`,
// `system_prompt`, `system_prompt_team`) and each user's Team
// instructions — and the approval step an MCP change to an operator
// prompt has to pass.
//
// A person saving a prompt in the UI is applied at once and recorded as
// a version. An agent asking for a change over MCP only files a
// proposal: nothing changes until an admin clicks Accept on the approval
// card, in a browser session (see Decide). An agent running as an admin
// account that got talked into rewriting its own security rules
// therefore cannot finish the job alone.
package promptguard

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/pmezard/go-difflib/difflib"
	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/entity"
)

// ConfigOwner is the config owner of the operator prompts.
const ConfigOwner = "agents"

// The managed operator prompt keys, in the order they reach a spawn.
const (
	KeySecurity   = "system_prompt_security"
	KeySystem     = "system_prompt"
	KeyTeam       = "system_prompt_team"
	KeyTeamPrompt = "team_instructions"
)

// Statuses of a PromptChange.
const (
	StatusPending  = "pending"
	StatusApplied  = "applied"
	StatusDeclined = "declined"
)

// Via values of an applied version.
const (
	ViaUI       = "ui"
	ViaApproval = "approval"
	ViaBaseline = "baseline"
)

var (
	ErrUnknownKey  = errors.New("unknown prompt key: use system_prompt_security, system_prompt or system_prompt_team")
	ErrNoSession   = errors.New("a prompt change needs a chat session to show its approval card in; call it from an agent session")
	ErrUnchanged   = errors.New("the prompt already has exactly this value")
	ErrNotPending  = errors.New("request id no longer pending (timed out or already resolved)")
	ErrNotAdmin    = errors.New("only an admin may decide a prompt change")
	ErrStaleBase   = errors.New("the prompt changed since this was proposed — decline it and propose again")
	ErrNoVersion   = errors.New("no such version")
	ErrUnavailable = errors.New("prompt history is unavailable in this run mode")
)

// Managed reports whether key is an operator prompt this package guards.
func Managed(key string) bool {
	switch key {
	case KeySecurity, KeySystem, KeyTeam:
		return true
	}
	return false
}

// ConfigTarget is the history target of an operator prompt key.
func ConfigTarget(key string) string { return ConfigOwner + "/" + key }

// TeamTarget is the history target of userID's Team instructions.
func TeamTarget(userID string) string { return "team/" + userID }

// Configs is the slice of configs.Service the guard reads and writes.
type Configs interface {
	GetOwned(owner, key string) string
	SetOwned(ctx context.Context, owner, key, value string) error
}

// Card is what Notify posts: the approval card of a proposal, or the
// settled state of one.
type Card struct {
	SessionID string
	Text      string
	Extras    map[string]string
}

// Service records versions and settles proposals.
type Service struct {
	db  *gorm.DB
	cfg Configs
	now func() time.Time

	mu sync.Mutex // serialises version numbering

	// Notify posts an approval card into a session; wired by the agents
	// tool. Nil: a proposal is refused (nobody could approve it).
	Notify func(Card)
}

// New returns a Service over db and cfg.
func New(db *gorm.DB, cfg Configs) *Service {
	return &Service{db: db, cfg: cfg, now: time.Now}
}

// Actor is who makes or proposes a change.
type Actor struct {
	UserID string
	Name   string
	// Agent is the "@handle" of the proposing agent, "" for a person.
	Agent string
}

type applyKey struct{}

// applying is what Decide puts on the context of the config write, so
// OnConfigChange records the accepted proposal instead of a UI save.
type applying struct{ change *entity.PromptChange }

// OnConfigChange records a write of a managed config row as a version.
// Wired as the configs service's change hook, so every write path (the
// settings page, the manager UI, an accepted proposal) lands in history.
func (s *Service) OnConfigChange(ctx context.Context, owner, key, before, after string, actor Actor) {
	if s == nil || owner != ConfigOwner || !Managed(key) || before == after {
		return
	}
	if ap, ok := ctx.Value(applyKey{}).(applying); ok && ap.change != nil && ap.change.Target == ConfigTarget(key) {
		return // Decide records it with the proposal's details
	}
	_, _ = s.record(ctx, ConfigTarget(key), before, &entity.PromptChange{
		Value: after, Via: ViaUI, Actor: actor.Name, ActorUserID: actor.UserID,
	})
}

// OnTeamPrompt records a save of userID's Team instructions.
func (s *Service) OnTeamPrompt(ctx context.Context, userID, before, after string) {
	if s == nil || before == after {
		return
	}
	_, _ = s.record(ctx, TeamTarget(userID), before, &entity.PromptChange{
		Value: after, Via: ViaUI, Actor: userID, ActorUserID: userID,
	})
}

// record writes ch as the next applied version of target. When target
// has no history yet and before is not empty, before is recorded first
// as version 1 ("baseline"), so the value history started from can be
// rolled back to.
func (s *Service) record(ctx context.Context, target, before string, ch *entity.PromptChange) (*entity.PromptChange, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	db := s.db.WithContext(ctx)
	last, err := s.lastVersion(db, target)
	if err != nil {
		return nil, err
	}
	if last == 0 && before != "" {
		base := &entity.PromptChange{
			ID: uuid.NewString(), Target: target, Version: 1, Value: before,
			Status: StatusApplied, Via: ViaBaseline, Actor: "value before history", At: s.now().UTC(),
		}
		if err := db.Create(base).Error; err != nil {
			return nil, err
		}
		last = 1
	}
	if ch.ID == "" {
		ch.ID = uuid.NewString()
	}
	ch.Target = target
	ch.Version = last + 1
	ch.Status = StatusApplied
	if ch.At.IsZero() {
		ch.At = s.now().UTC()
	}
	if err := db.Save(ch).Error; err != nil {
		return nil, err
	}
	return ch, nil
}

func (s *Service) lastVersion(db *gorm.DB, target string) (int, error) {
	var n *int
	err := db.Model(&entity.PromptChange{}).
		Where("target = ? AND status = ?", target, StatusApplied).
		Select("MAX(version)").Scan(&n).Error
	if err != nil || n == nil {
		return 0, err
	}
	return *n, nil
}

// Current is the live value of an operator prompt and its version (0
// when it has no history yet).
func (s *Service) Current(ctx context.Context, key string) (string, int, error) {
	if !Managed(key) {
		return "", 0, ErrUnknownKey
	}
	n, err := s.lastVersion(s.db.WithContext(ctx), ConfigTarget(key))
	return s.cfg.GetOwned(ConfigOwner, key), n, err
}

// ProposeInput is one change asked for over MCP.
type ProposeInput struct {
	Key       string
	Value     string
	Reason    string
	SessionID string
	Actor     Actor
	// RollbackOf restores that version's value; Value is ignored.
	RollbackOf int
}

// Propose records a pending change and posts its approval card. Nothing
// is applied here.
func (s *Service) Propose(ctx context.Context, in ProposeInput) (*entity.PromptChange, []string, error) {
	if s == nil {
		return nil, nil, ErrUnavailable
	}
	if !Managed(in.Key) {
		return nil, nil, ErrUnknownKey
	}
	if strings.TrimSpace(in.SessionID) == "" {
		return nil, nil, ErrNoSession
	}
	if s.Notify == nil {
		return nil, nil, ErrUnavailable
	}
	target := ConfigTarget(in.Key)
	cur, base, err := s.Current(ctx, in.Key)
	if err != nil {
		return nil, nil, err
	}
	value := in.Value
	if in.RollbackOf > 0 {
		v, err := s.Version(ctx, target, in.RollbackOf)
		if err != nil {
			return nil, nil, err
		}
		value = v.Value
	}
	if value == cur {
		return nil, nil, ErrUnchanged
	}
	if base == 0 && cur != "" {
		// Pin the current value as version 1 now, so the proposal has a
		// base to be checked against when it is accepted.
		s.mu.Lock()
		base, err = s.lastVersion(s.db.WithContext(ctx), target)
		if err == nil && base == 0 {
			err = s.db.WithContext(ctx).Create(&entity.PromptChange{
				ID: uuid.NewString(), Target: target, Version: 1, Value: cur,
				Status: StatusApplied, Via: ViaBaseline, Actor: "value before history", At: s.now().UTC(),
			}).Error
			base = 1
		}
		s.mu.Unlock()
		if err != nil {
			return nil, nil, err
		}
	}
	ch := &entity.PromptChange{
		ID: uuid.NewString(), Target: target, Value: value, Status: StatusPending,
		BaseVersion: base, RollbackOf: in.RollbackOf,
		Actor: in.Actor.Name, ActorUserID: in.Actor.UserID, ActorAgent: in.Actor.Agent,
		Reason: strings.TrimSpace(in.Reason), SessionID: in.SessionID, At: s.now().UTC(),
	}
	if err := s.db.WithContext(ctx).Create(ch).Error; err != nil {
		return nil, nil, err
	}
	diff := DiffLines(cur, value)
	what := "change " + in.Key
	if in.RollbackOf > 0 {
		what = fmt.Sprintf("roll %s back to version %d", in.Key, in.RollbackOf)
	}
	reason := ch.Reason
	if reason == "" {
		reason = "No reason given."
	}
	s.Notify(Card{
		SessionID: in.SessionID,
		Text:      "wants to " + what,
		Extras: map[string]string{
			"approval_id": ch.ID, "state": "pending", "type": CardType,
			"target": in.Key, "changes": strings.Join(diff, "\n"),
			"reason": reason + " · Only an admin, from the browser, can accept.",
		},
	})
	return ch, diff, nil
}

// CardType marks an approval_request card that carries a prompt change.
const CardType = "prompt_change"

// Proposal returns the proposal approvalID, if it is one.
func (s *Service) Proposal(ctx context.Context, approvalID string) (*entity.PromptChange, error) {
	if s == nil {
		return nil, ErrUnavailable
	}
	var ch entity.PromptChange
	if err := s.db.WithContext(ctx).Where("id = ?", approvalID).First(&ch).Error; err != nil {
		return nil, err
	}
	return &ch, nil
}

// Decide settles a pending proposal. decider must be an admin; the
// caller (the approval card's HTTP handler) is responsible for making
// sure it is a person in a browser, not a token. Accepting writes the
// value through the configs service and records it as the next version.
func (s *Service) Decide(ctx context.Context, approvalID string, decider *entity.User, deciderName string, accept bool) (*entity.PromptChange, error) {
	if s == nil {
		return nil, ErrUnavailable
	}
	if decider == nil || !decider.IsAdmin() {
		return nil, ErrNotAdmin
	}
	ch, err := s.Proposal(ctx, approvalID)
	if err != nil || ch.Status != StatusPending {
		return nil, ErrNotPending
	}
	key := strings.TrimPrefix(ch.Target, ConfigOwner+"/")
	now := s.now().UTC()
	if !accept {
		res := s.db.WithContext(ctx).Model(&entity.PromptChange{}).
			Where("id = ? AND status = ?", ch.ID, StatusPending).
			Updates(map[string]any{"status": StatusDeclined, "decided_by": deciderName, "decided_at": now})
		if res.Error != nil || res.RowsAffected == 0 {
			return nil, ErrNotPending
		}
		ch.Status, ch.DecidedBy, ch.DecidedAt = StatusDeclined, deciderName, &now
		return ch, nil
	}
	if !Managed(key) {
		return nil, ErrUnknownKey
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	db := s.db.WithContext(ctx)
	last, err := s.lastVersion(db, ch.Target)
	if err != nil {
		return nil, err
	}
	if last != ch.BaseVersion {
		return nil, ErrStaleBase
	}
	// Claim the row first: two clicks race to one apply.
	res := db.Model(&entity.PromptChange{}).
		Where("id = ? AND status = ?", ch.ID, StatusPending).
		Updates(map[string]any{"status": StatusApplied, "version": last + 1, "via": ViaApproval, "decided_by": deciderName, "decided_at": now})
	if res.Error != nil || res.RowsAffected == 0 {
		return nil, ErrNotPending
	}
	actx := context.WithValue(ctx, applyKey{}, applying{change: ch})
	if err := s.cfg.SetOwned(actx, ConfigOwner, key, ch.Value); err != nil {
		db.Model(&entity.PromptChange{}).Where("id = ?", ch.ID).
			Updates(map[string]any{"status": StatusPending, "version": 0, "via": "", "decided_by": "", "decided_at": nil})
		return nil, err
	}
	ch.Status, ch.Version, ch.Via, ch.DecidedBy, ch.DecidedAt = StatusApplied, last+1, ViaApproval, deciderName, &now
	return ch, nil
}

// Version returns version n of target.
func (s *Service) Version(ctx context.Context, target string, n int) (*entity.PromptChange, error) {
	var ch entity.PromptChange
	err := s.db.WithContext(ctx).
		Where("target = ? AND status = ? AND version = ?", target, StatusApplied, n).
		First(&ch).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNoVersion
	}
	if err != nil {
		return nil, err
	}
	return &ch, nil
}

// Entry is one row of History: a change with its diff against the
// version before it (for a proposal: against the version it was made on).
type Entry struct {
	entity.PromptChange
	Diff []string `json:"diff"`
}

// History lists target's changes newest first, at most limit (0 = 20):
// applied versions, plus pending and declined proposals.
func (s *Service) History(ctx context.Context, target string, limit int) ([]Entry, error) {
	if s == nil {
		return nil, ErrUnavailable
	}
	if limit <= 0 {
		limit = 20
	}
	var rows []entity.PromptChange
	if err := s.db.WithContext(ctx).Where("target = ?", target).
		Order("at DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	var applied []entity.PromptChange
	if err := s.db.WithContext(ctx).Where("target = ? AND status = ?", target, StatusApplied).
		Find(&applied).Error; err != nil {
		return nil, err
	}
	byVersion := make(map[int]string, len(applied))
	for _, a := range applied {
		byVersion[a.Version] = a.Value
	}
	out := make([]Entry, 0, len(rows))
	for _, r := range rows {
		prev := ""
		switch {
		case r.Status == StatusApplied:
			prev = byVersion[r.Version-1]
		default:
			prev = byVersion[r.BaseVersion]
		}
		out = append(out, Entry{PromptChange: r, Diff: DiffLines(prev, r.Value)})
	}
	return out, nil
}

// DiffLines is a unified line diff of before → after, "+" and "-" lines
// only (no headers, no context), capped so a full rewrite stays readable.
func DiffLines(before, after string) []string {
	const max = 200
	ud := difflib.UnifiedDiff{
		A: difflib.SplitLines(ensureNL(before)), B: difflib.SplitLines(ensureNL(after)), Context: 0,
	}
	text, err := difflib.GetUnifiedDiffString(ud)
	if err != nil {
		return nil
	}
	var out []string
	for _, l := range strings.Split(text, "\n") {
		if strings.HasPrefix(l, "+++") || strings.HasPrefix(l, "---") || strings.HasPrefix(l, "@@") {
			continue
		}
		if strings.HasPrefix(l, "+") || strings.HasPrefix(l, "-") {
			out = append(out, l)
		}
	}
	if len(out) > max {
		out = append(out[:max], fmt.Sprintf("… %d more lines", len(out)-max))
	}
	return out
}

func ensureNL(s string) string {
	if s == "" || strings.HasSuffix(s, "\n") {
		return s
	}
	return s + "\n"
}
