package accessreq

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/entity"
)

// Limits that keep a card readable and an agent from flooding approvers.
const (
	MaxActions          = 10
	MaxOpenPerSession   = 5
	maxResultChars      = 1500
	maxParamChars       = 300
	runTimeout          = 5 * time.Minute
	wakeResultCharLimit = 1200
)

// ErrNotFound is returned for an unknown request, grant or rule.
var ErrNotFound = errors.New("not found")

// Service files, shows and decides access requests.
type Service struct {
	store *Store
	cat   Catalog
	dir   Directory

	mu   sync.RWMutex
	pubs []Publisher
	wake Waker
	url  func(id string) string

	// sync runs approved actions inline (tests); production runs them in
	// the background so a Slack click is answered within Slack's 3s.
	sync bool
	now  func() time.Time

	// script runs rule scripts (SetScriptRunner); cache keeps their
	// answers for a few minutes.
	script ScriptRunner
	cache  scriptCache
}

// New builds a service.
func New(store *Store, cat Catalog, dir Directory) *Service {
	return &Service{store: store, cat: cat, dir: dir, now: time.Now}
}

// AddPublisher registers a surface that draws requests.
func (s *Service) AddPublisher(p Publisher) {
	if p == nil {
		return
	}
	s.mu.Lock()
	s.pubs = append(s.pubs, p)
	s.mu.Unlock()
}

// SetWaker sets how outcomes reach the requesting session.
func (s *Service) SetWaker(w Waker) {
	s.mu.Lock()
	s.wake = w
	s.mu.Unlock()
}

// SetURL sets how a request's web page URL is built.
func (s *Service) SetURL(f func(id string) string) {
	s.mu.Lock()
	s.url = f
	s.mu.Unlock()
}

// HashParams is the stable hash an approval is bound to: a change to any
// parameter is a different action.
func HashParams(connectorID, opKey, accountID string, params map[string]any) string {
	b, _ := json.Marshal(params) // map keys marshal sorted
	h := sha256.Sum256([]byte(connectorID + "\x00" + opKey + "\x00" + accountID + "\x00" + string(b)))
	return hex.EncodeToString(h[:])
}

// Create files a request. Actions the rules deny are refused up front;
// actions an auto_approve rule covers run at once and show on the card as
// decided by that rule.
func (s *Service) Create(ctx context.Context, in CreateInput) (View, error) {
	ctx = WithSession(ctx, in.SessionID)
	if strings.TrimSpace(in.SessionID) == "" {
		return View{}, errors.New("an access request needs a session: call it from an agent session")
	}
	if !s.Enabled(in.SessionID) {
		return View{}, ErrOff
	}
	if len(in.Actions) == 0 {
		return View{}, errors.New("actions is empty: list at least one {tool_id, params}")
	}
	if len(in.Actions) > MaxActions {
		return View{}, fmt.Errorf("at most %d actions per request", MaxActions)
	}
	if n, err := s.store.countOpen(ctx, in.SessionID); err == nil && n >= MaxOpenPerSession {
		return View{}, fmt.Errorf("this session already has %d open access requests; wait for a decision or cancel one", n)
	}
	now := s.now()
	req := entity.AccessRequest{
		ID:              uuid.NewString(),
		SessionID:       in.SessionID,
		AgentID:         in.AgentID,
		RequesterUserID: s.sessionOwner(in.SessionID),
		ProjectID:       s.sessionProject(in.SessionID),
		Reason:          strings.TrimSpace(in.Reason),
		Status:          StatusPending,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if s.dir != nil && in.AgentID != "" {
		req.AgentName = s.dir.AgentName(ctx, in.AgentID)
	}
	asker := s.lastAsker(in.SessionID)
	if asker != nil {
		if b, err := json.Marshal(asker); err == nil {
			req.AskedBy = string(b)
		}
	}
	items := make([]entity.AccessRequestItem, 0, len(in.Actions))
	seen := map[string]bool{}
	type auto struct {
		idx  int
		rule entity.AccessRule
	}
	var autos []auto
	for i, a := range in.Actions {
		meta, err := s.cat.Describe(ctx, a.ConnectorID, a.OpKey, overrideOf(a.Params))
		if err != nil {
			return View{}, fmt.Errorf("action %d: %w", i+1, err)
		}
		if ok, why := s.cat.Requestable(ctx, in.SessionID, in.AgentID, a.ConnectorID); !ok {
			return View{}, fmt.Errorf("action %d (%s.%s): %s", i+1, meta.ConnectorKey, a.OpKey, why)
		}
		if a.Params == nil {
			a.Params = map[string]any{}
		}
		hash := HashParams(a.ConnectorID, a.OpKey, a.AccountID, a.Params)
		if seen[hash] {
			return View{}, fmt.Errorf("action %d repeats an earlier action with the same parameters", i+1)
		}
		seen[hash] = true
		kind := ClassifyOp(a.OpKey, meta.Destructive)
		act := Action{SessionID: in.SessionID, AgentID: in.AgentID, AgentName: req.AgentName,
			ConnectorID: a.ConnectorID, ConnectorKey: meta.ConnectorKey, OpKey: a.OpKey, Kind: kind, Params: stringify(a.Params),
			AskedBy: asker}
		if sc, ok := s.cat.(SubjectConfigs); ok {
			act.Config = sc.SubjectConfig(ctx, a.ConnectorID)
		}
		v := s.Evaluate(ctx, act)
		if v.Effect == EffectDeny {
			return View{}, fmt.Errorf("action %d (%s.%s) is denied by rule %q", i+1, meta.ConnectorKey, a.OpKey, v.Rule.Name)
		}
		params, _ := json.Marshal(a.Params)
		item := entity.AccessRequestItem{
			ID: uuid.NewString(), RequestID: req.ID, Idx: i, ConnectorID: a.ConnectorID,
			ConnectorKey: meta.ConnectorKey, ConnectorLabel: meta.ConnectorLabel, OpKey: a.OpKey, OpName: meta.OpName,
			AccountID: a.AccountID, Params: string(params), ParamsHash: hash, Kind: kind, Status: ItemPending,
			// A "require" rule, or an action that names no clear target,
			// is approved one call at a time, never for a while.
			OnceOnly: v.Effect == EffectRequire || !HasTarget(act.Params),
		}
		if len(v.Simulated) > 0 {
			item.Note = "would auto-approve (simulate): " + strings.Join(v.Simulated, ", ")
		}
		if v.Effect == EffectAutoApprove {
			autos = append(autos, auto{idx: i, rule: *v.Rule})
		}
		items = append(items, item)
	}
	if err := s.store.createRequest(ctx, &req, items); err != nil {
		return View{}, fmt.Errorf("save request: %w", err)
	}
	if len(autos) > 0 {
		var run []entity.AccessRequestItem
		for _, a := range autos {
			it := items[a.idx]
			by := "rule:" + a.rule.Name
			if ok, err := s.store.claimItem(ctx, it.ID, ItemRunning, ModeOnce, by, "auto-approved by rule "+a.rule.Name, s.now()); err == nil && ok {
				it.Status, it.Mode, it.DecidedBy = ItemRunning, ModeOnce, by
				run = append(run, it)
			}
		}
		autoOwner := map[string]string{}
		for _, a := range autos {
			autoOwner[items[a.idx].ID] = a.rule.OwnerID
		}
		s.execute(req, run, func(it entity.AccessRequestItem) string { return autoOwner[it.ID] }, "")
	}
	s.publish(ctx, req.ID)
	return s.ViewFor(ctx, req.ID, "")
}

// ViewFor computes the request as viewerID sees it ("" = the public card).
func (s *Service) ViewFor(ctx context.Context, id, viewerID string) (View, error) {
	req, items, err := s.store.request(ctx, id)
	if err != nil {
		return View{}, err
	}
	return s.view(ctx, req, items, viewerID), nil
}

func (s *Service) sessionOwner(sessionID string) string {
	if s.dir == nil {
		return ""
	}
	return s.dir.SessionOwner(sessionID)
}

// SessionProjects is the optional part of a Directory that names a
// session's project.
type SessionProjects interface {
	SessionProject(sessionID string) string
}

func (s *Service) sessionProject(sessionID string) string {
	if sp, ok := s.dir.(SessionProjects); ok && sessionID != "" {
		return sp.SessionProject(sessionID)
	}
	return ""
}

// ProjectSwitches is the optional part of a Directory that says whether
// a project turned access requests on. Off (the default) means none of
// this runs for its sessions: no cards, no rules, no grants, no
// overrides — a refused op stays refused as it always was.
type ProjectSwitches interface {
	AccessRequestsOn(projectID string) bool
}

// Enabled reports whether access requests are on for a session.
// A Directory that cannot tell leaves them on (tests, simple setups).
func (s *Service) Enabled(sessionID string) bool {
	sw, ok := s.dir.(ProjectSwitches)
	if !ok {
		return true
	}
	pid := s.sessionProject(sessionID)
	return pid != "" && sw.AccessRequestsOn(pid)
}

// lastAsker names the person behind the session's latest turn, nil when
// none or unknown.
func (s *Service) lastAsker(sessionID string) *Asker {
	if sa, ok := s.dir.(SessionAskers); ok && sessionID != "" {
		return sa.LastAsker(sessionID)
	}
	return nil
}

// ErrOff refuses a request in a project that did not turn them on.
var ErrOff = errors.New("access requests are off for this session's project; the project's owner can turn them on in the project settings (Access requests). Until then, an action outside your access cannot be requested")

func (s *Service) owners(ctx context.Context, sessionID, agentID string) (sessionOwner, agentOwner string) {
	if s.dir == nil {
		return "", ""
	}
	sessionOwner = s.dir.SessionOwner(sessionID)
	if agentID != "" {
		agentOwner = s.dir.AgentOwner(ctx, agentID)
	}
	return sessionOwner, agentOwner
}

// approverRole reports whether viewerID is one of the people who may
// decide this request at all: an admin, the session's owner, or the
// agent's owner.
func (s *Service) approverRole(ctx context.Context, req *entity.AccessRequest, viewer Person) bool {
	if viewer.IsAdmin {
		return true
	}
	if viewer.ID == req.RequesterUserID && viewer.ID != "" {
		return true
	}
	if req.AgentID != "" && s.dir != nil && s.dir.AgentOwner(ctx, req.AgentID) == viewer.ID {
		return true
	}
	return false
}

// candidates are everyone with an approver role on the request.
func (s *Service) candidates(ctx context.Context, req *entity.AccessRequest) []Person {
	if s.dir == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []Person
	add := func(p Person, ok bool) {
		if !ok || p.ID == "" || seen[p.ID] || !p.Approved {
			return
		}
		seen[p.ID] = true
		out = append(out, p)
	}
	if req.RequesterUserID != "" {
		add(s.dir.User(ctx, req.RequesterUserID))
	}
	if req.AgentID != "" {
		if o := s.dir.AgentOwner(ctx, req.AgentID); o != "" {
			add(s.dir.User(ctx, o))
		}
	}
	for _, a := range s.dir.Admins(ctx) {
		add(a, true)
	}
	return out
}

func (s *Service) view(ctx context.Context, req *entity.AccessRequest, items []entity.AccessRequestItem, viewerID string) View {
	ctx = WithSession(ctx, req.SessionID)
	v := View{
		ID: req.ID, SessionID: req.SessionID, AgentID: req.AgentID, AgentName: req.AgentName,
		Reason: req.Reason, Status: req.Status, CreatedAt: req.CreatedAt, ViewerID: viewerID,
	}
	if s.dir != nil && req.RequesterUserID != "" {
		if p, ok := s.dir.User(ctx, req.RequesterUserID); ok {
			v.Requester = p.Name
		}
	}
	if req.AskedBy != "" {
		var a Asker
		if json.Unmarshal([]byte(req.AskedBy), &a) == nil && a.ID != "" {
			v.AskedBy = &a
		}
	}
	if req.Origin != "" {
		_ = json.Unmarshal([]byte(req.Origin), &v.Origin)
	}
	s.mu.RLock()
	if s.url != nil {
		v.URL = s.url(req.ID)
	}
	s.mu.RUnlock()

	var viewer Person
	viewerKnown := false
	if viewerID != "" && s.dir != nil {
		viewer, viewerKnown = s.dir.User(ctx, viewerID)
	}
	isRole := viewerKnown && viewer.Approved && s.approverRole(ctx, req, viewer)
	anyRole := isRole
	cands := s.candidates(ctx, req)
	agentOwner := ""
	if req.AgentID != "" && s.dir != nil {
		agentOwner = s.dir.AgentOwner(ctx, req.AgentID)
	}

	for _, it := range items {
		iv := ItemView{
			ID: it.ID, Idx: it.Idx, ConnectorID: it.ConnectorID, ConnectorKey: it.ConnectorKey,
			ConnectorLabel: it.ConnectorLabel, OpKey: it.OpKey, OpName: it.OpName, Kind: it.Kind,
			Status: it.Status, Mode: it.Mode, DecidedAt: it.DecidedAt, Note: it.Note,
			Result: it.Result, Error: it.Error,
		}
		iv.DecidedBy = s.personName(ctx, it.DecidedBy)
		var params map[string]any
		_ = json.Unmarshal([]byte(it.Params), &params)
		iv.Params, iv.ParamsJSON = maskParams(params)
		iv.Target = targetSummary(params)
		ov := overrideOf(params)
		for _, c := range cands {
			if ok, _ := s.cat.CanRun(ctx, c.ID, it.ConnectorID, it.OpKey, ov); ok {
				iv.Approvers = append(iv.Approvers, c)
			}
		}
		if it.Status == ItemPending {
			v.Pending++
		}
		if it.Status == ItemRunning {
			v.Pending++
		}
		if it.Status == ItemPending && req.Status == StatusPending {
			act := s.actionOfItem(ctx, req, it)
			rules := s.approverRules(ctx, act)
			iv.approverNote = approverRuleNote(rules)
			iv.ApproversText = approversText(rules, approverNames(iv.Approvers))
			if viewerID != "" && viewerKnown && viewer.Approved {
				role, why := s.itemRole(ctx, act, viewer, isRole, rules)
				iv.roleReason = why
				if role {
					anyRole = true
				}
				s.lockFor(ctx, req, it, &iv, viewer, viewerKnown, role, agentOwner)
			} else if viewerID != "" {
				s.lockFor(ctx, req, it, &iv, viewer, viewerKnown, isRole, agentOwner)
			}
		} else if viewerID != "" {
			s.lockFor(ctx, req, it, &iv, viewer, viewerKnown, isRole, agentOwner)
		}
		if iv.CanDecide {
			v.ViewerCanDecide = true
		}
		v.Items = append(v.Items, iv)
	}
	if viewerID != "" && !v.ViewerCanDecide {
		v.ViewerReason, v.ViewerReasonCode = s.viewerReason(req, v, viewerKnown, viewer, anyRole)
	}
	return v
}

// lockFor fills the viewer-specific part of an item: can they decide it,
// with which modes, or why not.
func (s *Service) lockFor(ctx context.Context, req *entity.AccessRequest, it entity.AccessRequestItem, iv *ItemView, viewer Person, known, isRole bool, agentOwner string) {
	names := approverNames(iv.Approvers)
	extra := iv.approverNote
	if iv.ApproversText != "" {
		names, extra = []string{iv.ApproversText}, ""
	}
	switch {
	case req.Status == StatusCancelled:
		iv.LockedCode, iv.LockedReason = ReasonInactive, "This request is no longer active."
	case it.Status != ItemPending:
		iv.LockedCode = ReasonDecided
		iv.LockedReason = decidedText(*iv)
	case !known || !viewer.Approved:
		iv.LockedCode, iv.LockedReason = ReasonNotLinked, "Your account is not linked to an approved wick user."
	case !isRole:
		iv.LockedCode = ReasonNotApprover
		if iv.roleReason != "" {
			iv.LockedReason = iv.roleReason + whoCan(names) + extra
		} else {
			iv.LockedReason = "You are not an approver for this request." + whoCan(names) + extra
		}
	default:
		ov := overrideOfItem(it)
		if ok, why := s.cat.CanRun(ctx, viewer.ID, it.ConnectorID, it.OpKey, ov); !ok {
			iv.LockedCode = ReasonApproverAccess
			iv.LockedReason = fmt.Sprintf("You are an approver, but your own access cannot run %s on %s", it.OpKey, labelOr(it.ConnectorLabel, it.ConnectorKey))
			if why != "" {
				iv.LockedReason += ": " + why
			}
			iv.LockedReason += "." + whoCan(names) + extra
			return
		}
		iv.CanDecide = true
		for _, m := range ModesFor(it.Kind) {
			if m != ModeOnce && it.OnceOnly {
				continue // approved one call at a time
			}
			if m == ModeAlways && (req.AgentID == "" || !(viewer.IsAdmin || viewer.ID == agentOwner)) {
				continue
			}
			iv.Modes = append(iv.Modes, m)
		}
	}
}

func (s *Service) viewerReason(req *entity.AccessRequest, v View, known bool, viewer Person, isRole bool) (string, string) {
	if req.Status == StatusCancelled {
		return "This request is no longer active.", ReasonInactive
	}
	if v.Pending == 0 {
		return "Every action in this request has been decided.", ReasonDecided
	}
	if !known || !viewer.Approved {
		return "Your account is not linked to an approved wick user, so you cannot approve this.", ReasonNotLinked
	}
	if !isRole {
		var all []Person
		seen := map[string]bool{}
		for _, it := range v.Items {
			for _, p := range it.Approvers {
				if !seen[p.ID] {
					seen[p.ID] = true
					all = append(all, p)
				}
			}
		}
		return "You do not have access to approve this request." + whoCan(approverNames(all)), ReasonNotApprover
	}
	return "Your own access cannot run any of the pending actions.", ReasonApproverAccess
}

func (s *Service) personName(ctx context.Context, id string) string {
	if id == "" {
		return ""
	}
	if r, ok := strings.CutPrefix(id, "rule:"); ok {
		return "rule " + r
	}
	if s.dir != nil {
		if p, ok := s.dir.User(ctx, id); ok && p.Name != "" {
			return p.Name
		}
	}
	return id
}

// DecideResult reports what happened to each decision.
type DecideResult struct {
	ItemID string `json:"item_id"`
	OK     bool   `json:"ok"`
	Code   string `json:"code,omitempty"`
	Error  string `json:"error,omitempty"`
}

// Decide applies viewerID's decisions. Each one is checked against the
// viewer's own access at the moment of the click; a decision on an item
// someone else already decided reports already_decided and changes
// nothing. Approved actions run as the viewer.
func (s *Service) Decide(ctx context.Context, id, viewerID string, ds []Decision) (View, []DecideResult, error) {
	req, items, err := s.store.request(ctx, id)
	if err != nil {
		return View{}, nil, err
	}
	v := s.view(ctx, req, items, viewerID)
	byID := map[string]ItemView{}
	for _, it := range v.Items {
		byID[it.ID] = it
	}
	rawByID := map[string]entity.AccessRequestItem{}
	for _, it := range items {
		rawByID[it.ID] = it
	}
	var results []DecideResult
	var run []entity.AccessRequestItem
	denied := 0
	for _, d := range ds {
		iv, ok := byID[d.ItemID]
		if !ok {
			results = append(results, DecideResult{ItemID: d.ItemID, Code: "unknown_item", Error: "no such action in this request"})
			continue
		}
		if !iv.CanDecide {
			results = append(results, DecideResult{ItemID: d.ItemID, Code: iv.LockedCode, Error: iv.LockedReason})
			continue
		}
		if d.Mode != ModeDeny && !contains(iv.Modes, d.Mode) {
			results = append(results, DecideResult{ItemID: d.ItemID, Code: ReasonModeNotAllowed,
				Error: fmt.Sprintf("mode %q is not offered for this action (offered: %s)", d.Mode, strings.Join(iv.Modes, ", "))})
			continue
		}
		status, mode := ItemRunning, d.Mode
		if d.Mode == ModeDeny {
			status, mode = ItemDenied, ""
		}
		ok, err := s.store.claimItem(ctx, d.ItemID, status, mode, viewerID, strings.TrimSpace(d.Note), s.now())
		if err != nil {
			results = append(results, DecideResult{ItemID: d.ItemID, Code: "error", Error: err.Error()})
			continue
		}
		if !ok {
			results = append(results, DecideResult{ItemID: d.ItemID, Code: ReasonDecided, Error: "someone already decided this action"})
			continue
		}
		results = append(results, DecideResult{ItemID: d.ItemID, OK: true})
		if d.Mode == ModeDeny {
			denied++
			continue
		}
		it := rawByID[d.ItemID]
		it.Status, it.Mode, it.DecidedBy = ItemRunning, mode, viewerID
		run = append(run, it)
	}
	decidedAny := len(run) > 0 || denied > 0
	if len(run) > 0 {
		// Show the decision at once (🔄 running) so the card does not look
		// stuck while the actions run; execute publishes again when done.
		s.publish(context.WithoutCancel(ctx), req.ID)
		s.execute(*req, run, func(entity.AccessRequestItem) string { return viewerID }, viewerID)
	} else if decidedAny {
		s.afterChange(context.WithoutCancel(ctx), req.ID, true)
	}
	out, err := s.ViewFor(ctx, id, viewerID)
	return out, results, err
}

// DenyAll denies every pending action viewerID may decide.
func (s *Service) DenyAll(ctx context.Context, id, viewerID, note string) (View, []DecideResult, error) {
	v, err := s.ViewFor(ctx, id, viewerID)
	if err != nil {
		return View{}, nil, err
	}
	var ds []Decision
	for _, it := range v.Items {
		if it.CanDecide {
			ds = append(ds, Decision{ItemID: it.ID, Mode: ModeDeny, Note: note})
		}
	}
	if len(ds) == 0 {
		return v, nil, nil
	}
	return s.Decide(ctx, id, viewerID, ds)
}

// Cancel withdraws a request from its own session.
func (s *Service) Cancel(ctx context.Context, id, sessionID string) error {
	req, _, err := s.store.request(ctx, id)
	if err != nil {
		return err
	}
	if req.SessionID != sessionID {
		return errors.New("this request belongs to another session")
	}
	ok, err := s.store.setRequestStatus(ctx, id, StatusPending, StatusCancelled)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("this request is no longer pending")
	}
	s.publish(ctx, id)
	return nil
}

// execute runs approved items as runAs(item) and records the outcome.
// grantBy, when set, is who leaves a standing grant for session/always
// modes.
func (s *Service) execute(req entity.AccessRequest, items []entity.AccessRequestItem, runAs func(entity.AccessRequestItem) string, grantBy string) {
	work := func() {
		ctx, cancel := context.WithTimeout(WithSession(context.Background(), req.SessionID), runTimeout)
		defer cancel()
		for _, it := range items {
			var params map[string]any
			_ = json.Unmarshal([]byte(it.Params), &params)
			user := runAs(it)
			runID, result, err := s.cat.Run(ctx, user, it.ConnectorID, it.OpKey, it.AccountID, params)
			status, errText := ItemOK, ""
			if err != nil {
				status, errText = ItemError, truncate(err.Error(), maxResultChars)
			}
			if ferr := s.store.finishItem(ctx, it.ID, status, runID, truncate(result, maxResultChars), errText); ferr != nil {
				log.Warn().Err(ferr).Str("item", it.ID).Msg("accessreq: record result failed")
			}
			if err == nil && grantBy != "" {
				s.leaveGrant(ctx, req, it, grantBy)
			}
		}
		s.afterChange(ctx, req.ID, true)
	}
	if s.sync {
		work()
		return
	}
	go work()
}

func (s *Service) leaveGrant(ctx context.Context, req entity.AccessRequest, it entity.AccessRequestItem, by string) {
	var params map[string]any
	_ = json.Unmarshal([]byte(it.Params), &params)
	if it.OnceOnly || !HasTarget(stringify(params)) {
		return
	}
	g := entity.AccessGrant{ID: uuid.NewString(), ConnectorID: it.ConnectorID, OpKey: it.OpKey,
		Target: TargetKey(stringify(params)), ProjectID: req.ProjectID, ApproverID: by, ItemID: it.ID, CreatedAt: s.now()}
	switch it.Mode {
	case ModeHour, Mode8h, Mode24h:
		exp := g.CreatedAt.Add(modeTTL(it.Mode))
		g.ScopeType, g.ScopeID, g.ExpiresAt = ModeSession, req.SessionID, &exp
	case ModeSession:
		g.ScopeType, g.ScopeID = ModeSession, req.SessionID
	case ModeAlways:
		if req.AgentID == "" {
			return
		}
		g.ScopeType, g.ScopeID = "agent", req.AgentID
	default:
		return
	}
	if err := s.store.addGrant(ctx, &g); err != nil {
		log.Warn().Err(err).Msg("accessreq: save grant failed")
	}
}

// afterChange redraws the card, closes a fully decided request and, when
// wake is set, tells the session what happened.
func (s *Service) afterChange(ctx context.Context, id string, wake bool) {
	if n, err := s.store.pendingCount(ctx, id); err == nil && n == 0 {
		_, _ = s.store.setRequestStatus(ctx, id, StatusPending, StatusDone)
	}
	s.publish(ctx, id)
	if wake {
		s.wakeSession(ctx, id)
	}
}

func (s *Service) wakeSession(ctx context.Context, id string) {
	s.mu.RLock()
	w := s.wake
	s.mu.RUnlock()
	if w == nil {
		return
	}
	v, err := s.ViewFor(ctx, id, "")
	if err != nil {
		return
	}
	if err := w(ctx, v.SessionID, WakeText(v)); err != nil {
		log.Warn().Err(err).Str("request", id).Msg("accessreq: wake session failed")
	}
}

// WakeText is the message the requesting session receives: the outcome
// of every decided action, written by wick from the stored results.
func WakeText(v View) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[access request %s] Update on the actions you asked for:\n", short(v.ID))
	for _, it := range v.Items {
		fmt.Fprintf(&b, "%d. %s.%s", it.Idx+1, it.ConnectorKey, it.OpKey)
		if it.Target != "" {
			fmt.Fprintf(&b, " (%s)", it.Target)
		}
		switch it.Status {
		case ItemOK:
			fmt.Fprintf(&b, " — approved %s by %s, ran OK", modeLabel(it.Mode), it.DecidedBy)
			if it.Result != "" {
				fmt.Fprintf(&b, "\n   result: %s", truncate(it.Result, wakeResultCharLimit))
			}
		case ItemError:
			fmt.Fprintf(&b, " — approved %s by %s, FAILED: %s", modeLabel(it.Mode), it.DecidedBy, it.Error)
		case ItemDenied:
			fmt.Fprintf(&b, " — denied by %s", it.DecidedBy)
			if it.Note != "" {
				fmt.Fprintf(&b, ": %q", it.Note)
			}
		case ItemRunning:
			b.WriteString(" — approved, still running")
		default:
			b.WriteString(" — still waiting for a decision")
		}
		b.WriteString("\n")
	}
	switch {
	case v.Status == StatusCancelled:
		b.WriteString("The request was cancelled.")
	case v.Pending > 0:
		fmt.Fprintf(&b, "%d action(s) still pending; you will be told when they are decided. Do not re-run approved actions yourself.", v.Pending)
	default:
		b.WriteString("All actions are decided. Approved actions already ran: do not run them again.")
	}
	return b.String()
}

func (s *Service) publish(ctx context.Context, id string) {
	s.mu.RLock()
	pubs := append([]Publisher(nil), s.pubs...)
	s.mu.RUnlock()
	if len(pubs) == 0 {
		return
	}
	req, items, err := s.store.request(ctx, id)
	if err != nil {
		return
	}
	v := s.view(ctx, req, items, "")
	origin := map[string]string{}
	for k, val := range v.Origin {
		origin[k] = val
	}
	changed := false
	for _, p := range pubs {
		o, err := p.Publish(ctx, v)
		if err != nil {
			log.Warn().Err(err).Str("request", id).Msg("accessreq: publish failed")
			continue
		}
		for k, val := range o {
			if origin[k] != val {
				origin[k] = val
				changed = true
			}
		}
	}
	if changed {
		b, _ := json.Marshal(origin)
		_ = s.store.setOrigin(ctx, id, string(b))
	}
}

// List returns recent requests (newest first) as the public card view.
func (s *Service) List(ctx context.Context, status string, limit int) ([]View, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	reqs, err := s.store.listRequests(ctx, status, limit)
	if err != nil {
		return nil, err
	}
	out := make([]View, 0, len(reqs))
	for i := range reqs {
		v, err := s.ViewFor(ctx, reqs[i].ID, "")
		if err == nil {
			out = append(out, v)
		}
	}
	return out, nil
}

// Check is the connector-side hook, asked only when an agent's own access
// refused an operation. It answers with who the call may run as instead
// (a standing grant's approver, or an auto_approve rule's owner), or an
// error when a deny rule matches. ("", "", nil) leaves the refusal as is.
func (s *Service) Check(ctx context.Context, a Action) (runAs, note string, err error) {
	ctx = WithSession(ctx, a.SessionID)
	if !s.Enabled(a.SessionID) {
		return "", "", nil
	}
	if a.AgentName == "" && a.AgentID != "" && s.dir != nil {
		a.AgentName = s.dir.AgentName(ctx, a.AgentID)
	}
	if a.AskedBy == nil {
		a.AskedBy = s.lastAsker(a.SessionID)
	}
	v := s.Evaluate(ctx, a)
	if v.Effect == EffectDeny {
		return "", "", fmt.Errorf("denied by access rule %q", v.Rule.Name)
	}
	scopes := map[string]string{ModeSession: a.SessionID, "agent": a.AgentID}
	grants, gerr := s.store.grants(ctx, a.ConnectorID, a.OpKey, scopes, s.now())
	// A standing grant never covers a call a "require" rule matches
	// (approved one call at a time) or one that names no clear target. A
	// call with a config override is covered only by a grant left for that
	// same override (the target key carries it), by someone who may still
	// configure the connector.
	if gerr == nil && v.Effect != EffectRequire && HasTarget(a.Params) {
		target := TargetKey(a.Params)
		ov := overrideOfAction(a)
		for _, g := range grants {
			if g.Target != target {
				continue
			}
			if ok, _ := s.cat.CanRun(ctx, g.ApproverID, a.ConnectorID, a.OpKey, ov); ok {
				return g.ApproverID, "grant " + g.ID, nil
			}
		}
	}
	if v.Effect == EffectAutoApprove {
		return v.Rule.OwnerID, "rule " + v.Rule.Name, nil
	}
	return "", "", nil
}

// Grants lists standing grants (all when scopeType is "").
func (s *Service) Grants(ctx context.Context, scopeType, scopeID string) ([]entity.AccessGrant, error) {
	return s.store.listGrants(ctx, scopeType, scopeID, s.now())
}

// RevokeGrant ends a standing grant. Its approver, the agent's owner or an
// admin may revoke it.
func (s *Service) RevokeGrant(ctx context.Context, id, by string) error {
	g, err := s.store.grant(ctx, id)
	if err != nil {
		return err
	}
	p, ok := Person{}, false
	if s.dir != nil {
		p, ok = s.dir.User(ctx, by)
	}
	allowed := by == g.ApproverID || (ok && p.IsAdmin)
	if !allowed && g.ScopeType == "agent" && s.dir != nil {
		allowed = s.dir.AgentOwner(ctx, g.ScopeID) == by
	}
	if !allowed && g.ScopeType == ModeSession {
		allowed = s.sessionOwner(g.ScopeID) == by
	}
	if !allowed {
		return errors.New("only the approver, the owner or an admin can revoke this grant")
	}
	return s.store.revokeGrant(ctx, id, by)
}

// ConnectorRemoved forgets a session connector that was removed: its
// grants in the session end and its pending actions are withdrawn, so a
// card can no longer approve them. The history stays.
func (s *Service) ConnectorRemoved(ctx context.Context, sessionID, connectorID, by string) error {
	if sessionID == "" || connectorID == "" {
		return nil
	}
	ctx = WithSession(ctx, sessionID)
	if by == "" {
		by = "connector removed"
	}
	if err := s.store.revokeConnectorGrants(ctx, sessionID, connectorID, by); err != nil {
		return err
	}
	items, err := s.store.pendingItemsFor(ctx, sessionID, connectorID)
	if err != nil {
		return err
	}
	touched := map[string]bool{}
	for _, it := range items {
		if ok, err := s.store.claimItem(ctx, it.ID, ItemDenied, "", "system", "the session connector was removed", s.now()); err == nil && ok {
			touched[it.RequestID] = true
		}
	}
	for id := range touched {
		s.afterChange(ctx, id, false)
	}
	return nil
}

// RevokeSessionGrant ends a "this session" grant from the session's own
// Workspace panel: whoever may use the session may take it back (the web
// layer checked the session access).
func (s *Service) RevokeSessionGrant(ctx context.Context, sessionID, id, by string) error {
	g, err := s.store.grant(ctx, id)
	if err != nil {
		return err
	}
	if g.ScopeType != ModeSession || g.ScopeID != sessionID {
		return errors.New("this grant does not belong to this session")
	}
	return s.store.revokeGrant(ctx, id, by)
}

// ----- helpers -----

func stringify(params map[string]any) map[string]string {
	out := make(map[string]string, len(params))
	for k, v := range params {
		switch x := v.(type) {
		case string:
			out[k] = x
		case nil:
			out[k] = ""
		default:
			b, _ := json.Marshal(x)
			out[k] = string(b)
		}
	}
	return out
}

var secretKeyHints = []string{"token", "secret", "password", "passwd", "apikey", "api_key", "auth", "cookie", "credential", "private_key", "session_key"}

var secretValuePrefixes = []string{"xoxb-", "xoxp-", "xoxa-", "xapp-", "ghp_", "gho_", "github_pat_", "sk-", "Bearer ", "bearer ", "AKIA", "glpat-"}

func isSecretKey(k string) bool {
	k = strings.ToLower(k)
	for _, h := range secretKeyHints {
		if strings.Contains(k, h) {
			return true
		}
	}
	return false
}

func maskValue(k, v string) string {
	if strings.HasPrefix(v, "wick_enc_") {
		return v
	}
	if isSecretKey(k) && v != "" {
		return "••••••"
	}
	for _, p := range secretValuePrefixes {
		if strings.HasPrefix(v, p) {
			return p + "••••••"
		}
	}
	return v
}

func maskAny(k string, v any) any {
	switch x := v.(type) {
	case string:
		return maskValue(k, x)
	case map[string]any:
		out := make(map[string]any, len(x))
		for kk, vv := range x {
			out[kk] = maskAny(kk, vv)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, vv := range x {
			out[i] = maskAny(k, vv)
		}
		return out
	}
	if isSecretKey(k) && v != nil {
		return "••••••"
	}
	return v
}

// maskParams lists the parameters for display with secrets masked.
func maskParams(params map[string]any) ([]Param, string) {
	masked, _ := maskAny("", params).(map[string]any)
	if masked == nil {
		masked = map[string]any{}
	}
	keys := make([]string, 0, len(masked))
	for k := range masked {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]Param, 0, len(keys))
	for _, k := range keys {
		var s string
		if str, ok := masked[k].(string); ok {
			s = str
		} else {
			b, _ := json.Marshal(masked[k])
			s = string(b)
		}
		p := Param{Name: k, Value: s}
		if len(s) > maxParamChars {
			p.Long = true
		}
		out = append(out, p)
	}
	b, _ := json.MarshalIndent(masked, "", "  ")
	return out, string(b)
}

// grantTargetKeys are the parameters that name WHAT an action touches.
// A standing grant is bound to their values: "this session" on
// update_page of page p1 does not cover page p2.
var grantTargetKeys = []string{
	"page_id", "database_id", "block_id", "parent_id",
	"channel", "channel_id", "conversation",
	"repo", "repository", "repo_slug", "project", "workspace", "branch", "branch_name",
	"app_code", "app_id", "appCode", "appId", "appid",
	"room_id", "ticket_id", "issue_id", "document_id", "sheet_id", "spreadsheet_id", "table_id", "file_id",
	"path", "url", "id", "email", "user_id",
}

// HasTarget reports whether an action names what it touches (one of the
// target parameters). One that does not ("resolve every room", a bulk
// filter) is only ever approved once.
func HasTarget(params map[string]string) bool {
	for _, k := range grantTargetKeys {
		if strings.TrimSpace(params[k]) != "" {
			return true
		}
	}
	return false
}

// TargetKey is the target signature of an action's parameters: the
// values of its target parameters in a fixed order, or, when it names
// none, a hash of all its parameters (exactly that action again).
func TargetKey(params map[string]string) string {
	var parts []string
	for _, k := range grantTargetKeys {
		if v, ok := params[k]; ok && strings.TrimSpace(v) != "" {
			parts = append(parts, k+"="+strings.TrimSpace(v))
		}
	}
	if len(parts) > 0 {
		// The config override an action runs with is part of what was
		// approved: a grant for mode=write covers mode=write only.
		if raw := params[ConfigOverrideParam]; raw != "" {
			b, _ := json.Marshal(overrideOf(map[string]any{ConfigOverrideParam: raw}))
			parts = append(parts, ConfigOverrideParam+"="+string(b))
		}
		return strings.Join(parts, "&")
	}
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, k := range keys {
		h.Write([]byte(k + "\x00" + params[k] + "\x00"))
	}
	return "params:" + hex.EncodeToString(h.Sum(nil))[:32]
}

var targetKeys = []string{"title", "name", "page_id", "database_id", "channel", "channel_id", "repo", "repository", "branch", "app_code", "app_id", "room_id", "path", "url", "id", "email", "query"}

// targetSummary is a short line saying what the action touches, built
// from its real parameters (never from the agent's description).
func targetSummary(params map[string]any) string {
	masked, _ := maskAny("", params).(map[string]any)
	var parts []string
	for _, k := range targetKeys {
		v, ok := masked[k]
		if !ok || v == nil {
			continue
		}
		s, isStr := v.(string)
		if !isStr {
			b, _ := json.Marshal(v)
			s = string(b)
		}
		if s == "" {
			continue
		}
		parts = append(parts, k+"="+truncate(s, 60))
		if len(parts) == 3 {
			break
		}
	}
	if len(parts) == 0 {
		keys := make([]string, 0, len(masked))
		for k := range masked {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			s, isStr := masked[k].(string)
			if !isStr {
				continue
			}
			parts = append(parts, k+"="+truncate(s, 60))
			if len(parts) == 2 {
				break
			}
		}
	}
	return strings.Join(parts, " · ")
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func contains(list []string, x string) bool {
	for _, v := range list {
		if v == x {
			return true
		}
	}
	return false
}

func labelOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func approverNames(ps []Person) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		n := p.Name
		if n == "" {
			n = p.ID
		}
		out = append(out, n)
	}
	return out
}

func whoCan(names []string) string {
	if len(names) == 0 {
		return " Nobody can approve it right now: ask an admin to give an approver access."
	}
	return " Who can: " + strings.Join(names, ", ") + "."
}

func modeLabel(m string) string {
	switch m {
	case ModeOnce:
		return "once"
	case ModeHour:
		return "for an hour"
	case Mode8h:
		return "for 8 hours"
	case Mode24h:
		return "for 24 hours"
	case ModeSession:
		return "for this session"
	case ModeAlways:
		return "always"
	}
	return m
}

// ModeLabel is the human label of a mode.
func ModeLabel(m string) string {
	switch m {
	case ModeOnce:
		return "Once"
	case ModeHour:
		return "1 hour"
	case Mode8h:
		return "8 hours"
	case Mode24h:
		return "24 hours"
	case ModeSession:
		return "This session"
	case ModeAlways:
		return "Always"
	case ModeDeny:
		return "Deny"
	}
	return m
}

func decidedText(it ItemView) string {
	at := ""
	if it.DecidedAt != nil {
		at = " at " + it.DecidedAt.Format("15:04")
	}
	switch it.Status {
	case ItemDenied:
		return fmt.Sprintf("Denied by %s%s.", it.DecidedBy, at)
	case ItemRunning:
		return fmt.Sprintf("Approved %s by %s%s, running now.", modeLabel(it.Mode), it.DecidedBy, at)
	case ItemOK:
		return fmt.Sprintf("Approved %s by %s%s. Result: OK.", modeLabel(it.Mode), it.DecidedBy, at)
	case ItemError:
		return fmt.Sprintf("Approved %s by %s%s. Result: error.", modeLabel(it.Mode), it.DecidedBy, at)
	}
	return "Already decided."
}
