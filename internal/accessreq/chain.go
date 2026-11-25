package accessreq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/entity"
)

// A plan step often needs more than one card: a payload fixed after
// review, or a second step that must wait for the first. These helpers
// keep that straight without the agent having to remember it:
//
//   - key: the agent names a step once ("deploy-step-2"). Filing the same
//     key again replaces the earlier card while nothing of it ran
//     (Version + 1, the old card is marked "replaced by"), or follows it
//     (After) once something already ran, so the run history stays.
//   - supersedes: replace another untouched request explicitly.
//   - after: approve whenever, run only once the earlier request finished
//     OK; skipped when it did not.

// After states shown on a card.
const (
	AfterWaiting = "waiting"
	AfterOK      = "ok"
	AfterFailed  = "failed"
)

// plan is how a new request fits with the session's earlier ones.
type plan struct {
	version    int
	key        string
	supersedes *entity.AccessRequest
	after      string
}

// untouched: still pending, and no action of it decided yet.
func untouched(req *entity.AccessRequest, items []entity.AccessRequestItem) bool {
	if req.Status != StatusPending {
		return false
	}
	for _, it := range items {
		if it.Status != ItemPending {
			return false
		}
	}
	return true
}

// resolveRef finds a request of the session by id, id prefix (8+ chars)
// or key (its newest version).
func (s *Service) resolveRef(ctx context.Context, sessionID, ref string) (*entity.AccessRequest, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, ErrNotFound
	}
	if r, _, err := s.store.request(ctx, ref); err == nil {
		if r.SessionID != sessionID {
			return nil, ErrNotFound
		}
		return r, nil
	}
	if len(ref) >= 8 {
		if r, err := s.store.requestByPrefix(ctx, sessionID, ref); err == nil {
			return r, nil
		}
	}
	return s.store.latestByKey(ctx, sessionID, ref)
}

// planFor works out version, replacement and ordering for a new request.
func (s *Service) planFor(ctx context.Context, in CreateInput) (plan, error) {
	p := plan{version: 1, key: strings.TrimSpace(in.Key)}
	if ref := strings.TrimSpace(in.Supersedes); ref != "" {
		old, err := s.resolveRef(ctx, in.SessionID, ref)
		if err != nil {
			return p, fmt.Errorf("supersedes %q: no such request in this session", ref)
		}
		_, items, err := s.store.request(ctx, old.ID)
		if err != nil {
			return p, err
		}
		if !untouched(old, items) {
			return p, fmt.Errorf("supersedes %q: that request is %s and part of it was already decided, so it cannot be replaced; file the new one with after=%q instead", ref, old.Status, old.ID)
		}
		p.supersedes, p.version = old, old.Version+1
		if p.key == "" {
			p.key = old.Key
		}
		p.after = old.After
	} else if p.key != "" {
		old, err := s.store.latestByKey(ctx, in.SessionID, p.key)
		switch {
		case errors.Is(err, ErrNotFound):
		case err != nil:
			return p, err
		default:
			_, items, err := s.store.request(ctx, old.ID)
			if err != nil {
				return p, err
			}
			p.version = old.Version + 1
			switch {
			case untouched(old, items):
				p.supersedes, p.after = old, old.After
			case old.Status == StatusPending || old.Status == StatusDone:
				// Something of it ran or is decided: keep its history and
				// follow it instead.
				p.after = old.ID
			}
		}
	}
	if ref := strings.TrimSpace(in.After); ref != "" {
		prev, err := s.resolveRef(ctx, in.SessionID, ref)
		if err != nil {
			return p, fmt.Errorf("after %q: no such request in this session", ref)
		}
		if p.supersedes != nil && prev.ID == p.supersedes.ID {
			return p, fmt.Errorf("after %q: a request cannot run after the one it replaces", ref)
		}
		p.after = prev.ID
	}
	return p, nil
}

// actionSig is what makes two actions "the same thing": connector, op and
// target.
func actionSig(connectorID, opKey string, params map[string]string) string {
	return connectorID + "\x00" + opKey + "\x00" + TargetKey(params)
}

// SimilarError refuses a request that repeats a pending one of the same
// session, so an agent does not file a second card by accident.
type SimilarError struct {
	ID      string
	Key     string
	Version int
	Same    int
}

func (e *SimilarError) Error() string {
	name := fmt.Sprintf("request %s", e.ID)
	fix := fmt.Sprintf("supersedes=%q", e.ID)
	if e.Key != "" {
		name = fmt.Sprintf("request %q (v%d, id %s)", e.Key, e.Version, e.ID)
		fix = fmt.Sprintf("key=%q", e.Key)
	}
	return fmt.Sprintf("%s is still pending with %d of these actions (same tool and target). To replace it, file again with %s; "+
		"to run after it, pass after=%q; to keep both cards on purpose, pass force=true", name, e.Same, fix, e.ID)
}

// similarPending finds a pending request of the session with actions on
// the same tool and target as acts.
func (s *Service) similarPending(ctx context.Context, sessionID, key string, acts []ActionInput) *SimilarError {
	want := map[string]bool{}
	for _, a := range acts {
		want[actionSig(a.ConnectorID, a.OpKey, stringify(a.Params))] = true
	}
	reqs, err := s.store.sessionRequests(ctx, sessionID, 50)
	if err != nil {
		return nil
	}
	for i := range reqs {
		r := reqs[i]
		if r.Status != StatusPending {
			continue
		}
		if key != "" && r.Key != "" && r.Key != key {
			continue // two named steps of a plan: different on purpose
		}
		_, items, err := s.store.request(ctx, r.ID)
		if err != nil {
			continue
		}
		same := 0
		for _, it := range items {
			if it.Status != ItemPending && it.Status != ItemQueued {
				continue
			}
			if want[actionSig(it.ConnectorID, it.OpKey, stringify(paramsOf(it)))] {
				same++
			}
		}
		if same > 0 {
			return &SimilarError{ID: r.ID, Key: r.Key, Version: r.Version, Same: same}
		}
	}
	return nil
}

// afterState is where the request req runs after stands.
func (s *Service) afterState(ctx context.Context, req *entity.AccessRequest) (string, *entity.AccessRequest) {
	if req.After == "" {
		return "", nil
	}
	id := req.After
	for hop := 0; hop < 10; hop++ {
		prev, items, err := s.store.request(ctx, id)
		if err != nil {
			return AfterFailed, nil
		}
		switch prev.Status {
		case StatusSuperseded:
			if prev.SupersededBy == "" {
				return AfterFailed, prev
			}
			id = prev.SupersededBy
			continue
		case StatusPending:
			return AfterWaiting, prev
		case StatusDone:
			for _, it := range items {
				if it.Status != ItemOK {
					return AfterFailed, prev
				}
			}
			return AfterOK, prev
		}
		return AfterFailed, prev
	}
	return AfterFailed, nil
}

// skipAll ends a request whose predecessor did not succeed: everything
// still open is skipped, nothing runs.
func (s *Service) skipAll(ctx context.Context, req *entity.AccessRequest, prev *entity.AccessRequest) {
	what := "the request it runs after"
	if prev != nil {
		what = "request " + refName(prev)
	}
	msg := "skipped: " + what + " did not finish OK, so this was not run"
	if err := s.store.moveItems(ctx, req.ID, []string{ItemPending, ItemQueued, ItemRunning}, ItemSkipped, msg); err != nil {
		log.Warn().Err(err).Str("request", req.ID).Msg("accessreq: skip items failed")
	}
	s.afterChange(ctx, req.ID, true)
}

// releaseFollowers runs (or skips) the requests waiting on id once id has
// finished one way or the other.
func (s *Service) releaseFollowers(ctx context.Context, id string) {
	fs, err := s.store.followers(ctx, id)
	if err != nil || len(fs) == 0 {
		return
	}
	for i := range fs {
		f := fs[i]
		state, prev := s.afterState(ctx, &f)
		switch state {
		case AfterWaiting:
			continue
		case AfterFailed:
			s.skipAll(ctx, &f, prev)
			continue
		}
		_, items, err := s.store.request(ctx, f.ID)
		if err != nil {
			continue
		}
		var run []entity.AccessRequestItem
		for _, it := range items {
			if it.Status == ItemQueued {
				run = append(run, it)
			}
		}
		if len(run) == 0 {
			s.publish(ctx, f.ID) // its card now says "ready"
			continue
		}
		_ = s.store.moveItems(ctx, f.ID, []string{ItemQueued}, ItemRunning, "")
		for j := range run {
			run[j].Status = ItemRunning
		}
		s.publish(ctx, f.ID)
		owners := map[string]string{}
		for _, it := range run {
			owners[it.ID] = s.runAsOf(ctx, it.DecidedBy)
		}
		grantBy := ""
		for _, it := range run {
			if !strings.HasPrefix(it.DecidedBy, "rule:") {
				grantBy = it.DecidedBy
			}
		}
		s.execute(f, run, func(it entity.AccessRequestItem) string { return owners[it.ID] }, grantBy)
	}
}

// runAsOf is who a queued action runs as: its approver, or the owner of
// the rule that auto-approved it.
func (s *Service) runAsOf(ctx context.Context, decidedBy string) string {
	name, ok := strings.CutPrefix(decidedBy, "rule:")
	if !ok {
		return decidedBy
	}
	rules, err := s.store.rules(ctx)
	if err != nil {
		return ""
	}
	for _, r := range rules {
		if r.Name == name {
			return r.OwnerID
		}
	}
	return ""
}

// refName is how a request is named to people and agents: its key and
// version when it has one, else its short id.
func refName(r *entity.AccessRequest) string {
	if r.Key != "" {
		return fmt.Sprintf("%q v%d", r.Key, r.Version)
	}
	return short(r.ID)
}

// Ref is another request a view points to (the one it runs after, the
// one that replaced it).
type Ref struct {
	ID      string
	Key     string
	Version int
	Status  string
	Origin  map[string]string
	URL     string
}

// Name is the ref as people read it.
func (r Ref) Name() string {
	if r.Key != "" {
		return fmt.Sprintf("%s v%d", r.Key, r.Version)
	}
	return short(r.ID)
}

func (s *Service) refOf(ctx context.Context, id string) *Ref {
	if id == "" {
		return nil
	}
	r, _, err := s.store.request(ctx, id)
	if err != nil {
		return &Ref{ID: id}
	}
	ref := &Ref{ID: r.ID, Key: r.Key, Version: r.Version, Status: r.Status}
	if r.Origin != "" {
		_ = jsonUnmarshal(r.Origin, &ref.Origin)
	}
	s.mu.RLock()
	if s.url != nil {
		ref.URL = s.url(r.ID)
	}
	s.mu.RUnlock()
	return ref
}

// SessionRequests lists a session's requests, newest first, as the
// public view.
func (s *Service) SessionRequests(ctx context.Context, sessionID string, limit int) ([]View, error) {
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	reqs, err := s.store.sessionRequests(ctx, sessionID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]View, 0, len(reqs))
	for i := range reqs {
		if v, err := s.ViewFor(ctx, reqs[i].ID, ""); err == nil {
			out = append(out, v)
		}
	}
	return out, nil
}

// PendingInThread lists the pending requests whose card is in a Slack
// thread (for the "approve with the button" nudge).
func (s *Service) PendingInThread(ctx context.Context, chatID, threadTS string) []string {
	if chatID == "" || threadTS == "" {
		return nil
	}
	reqs, err := s.store.pendingInThread(ctx, chatID, threadTS)
	if err != nil {
		return nil
	}
	ids := make([]string, 0, len(reqs))
	for _, r := range reqs {
		ids = append(ids, r.ID)
	}
	return ids
}

func paramsOf(it entity.AccessRequestItem) map[string]any {
	var params map[string]any
	_ = json.Unmarshal([]byte(it.Params), &params)
	return params
}

func jsonUnmarshal(raw string, v any) error { return json.Unmarshal([]byte(raw), v) }
