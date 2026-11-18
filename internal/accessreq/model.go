// Package accessreq lets an agent ask a human for connector actions its
// own access cannot run, one card per batch, decided action by action.
//
// The flow never blocks an agent turn: wick_request_access files the
// request and returns "pending"; the card shows wherever the session
// lives (a Slack thread, the web); an approver opens it, picks a mode per
// action (once, an hour, this session, or deny); wick runs the approved
// actions with the APPROVER's access and wakes the session with the
// result.
//
// Everything that decides (who may click, what each action is, why a row
// is locked) is computed here into a View. Channels only draw a View and
// pass clicks back, so a Slack card, the web page and any later channel
// cannot disagree about who may approve what.
package accessreq

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/yogasw/wick/internal/entity"
)

// Request statuses.
const (
	StatusPending   = "pending"
	StatusDone      = "done"
	StatusCancelled = "cancelled"
)

// Item statuses.
const (
	ItemPending = "pending"
	ItemRunning = "running"
	ItemOK      = "ok"
	ItemError   = "error"
	ItemDenied  = "denied"
)

// Modes an approver picks per action. ModeDeny is a decision, not a
// stored mode.
const (
	ModeOnce = "once"
	// The timed modes cover the same operation on the same target (and
	// the same config override, if any) for a while, in this session.
	ModeHour    = "hour"
	Mode8h      = "8h"
	Mode24h     = "24h"
	ModeSession = "session"
	ModeAlways  = "always"
	ModeDeny    = "deny"
)

// Kinds of action. They decide which modes are offered: anything that
// sends outward cannot become a standing "always", and a delete is only
// ever approved once.
const (
	KindRead   = "read"
	KindWrite  = "write"
	KindSend   = "send"
	KindDelete = "delete"
)

// Reason codes a locked row or a refused decision carries. The text next
// to them is for people; the code is for tests and channels that style
// by code.
const (
	ReasonNotLinked      = "not_linked"
	ReasonNotApprover    = "not_approver"
	ReasonApproverAccess = "approver_lacks_access"
	ReasonDecided        = "already_decided"
	ReasonInactive       = "inactive"
	ReasonModeNotAllowed = "mode_not_allowed"
)

// ClassifyOp names the kind of an operation from its key and its own
// destructive flag. A non-destructive op only reads. Among writes, a key
// that deletes or sends outward is classed as such; the rest is write.
func ClassifyOp(opKey string, destructive bool) string {
	if !destructive {
		return KindRead
	}
	k := strings.ToLower(opKey)
	for _, w := range []string{"delete", "remove", "destroy", "drop", "purge", "revoke", "archive", "uninstall", "wipe"} {
		if strings.Contains(k, w) {
			return KindDelete
		}
	}
	for _, w := range []string{"send", "post", "publish", "reply", "broadcast", "notify", "message", "email", "invite", "share", "forward", "dm"} {
		if strings.Contains(k, w) {
			return KindSend
		}
	}
	return KindWrite
}

// ModesFor lists the modes offered for a kind, safest first. deny is
// always allowed and not listed.
//
// "Always" is deliberately not offered on a card: standing access is a
// rule (project settings: deny / require / auto_approve with facts such as
// app.env), written on purpose, never one click on a card. The timed
// modes (an hour, the rest of the session) cover the same operation on
// the same target only, in this session.
func ModesFor(kind string) []string {
	if kind == KindDelete {
		return []string{ModeOnce}
	}
	return []string{ModeOnce, ModeHour, Mode8h, Mode24h, ModeSession}
}

// GrantHour is how long a ModeHour grant lasts.
const GrantHour = time.Hour

// modeTTL is how long a timed mode's grant lasts (0 = as long as its
// scope).
func modeTTL(m string) time.Duration {
	switch m {
	case ModeHour:
		return GrantHour
	case Mode8h:
		return 8 * time.Hour
	case Mode24h:
		return 24 * time.Hour
	}
	return 0
}

// ActionInput is one action an agent asks for.
type ActionInput struct {
	ConnectorID string
	OpKey       string
	AccountID   string
	Params      map[string]any
}

// CreateInput is a new request.
type CreateInput struct {
	SessionID string
	AgentID   string
	Reason    string
	Actions   []ActionInput
}

// Decision is one approver choice on one item.
type Decision struct {
	ItemID string
	// Mode is once, session, always or deny.
	Mode string
	// Note is the approver's reason for a deny, passed to the agent.
	Note string
}

// ActionMeta describes a connector operation for the card.
type ActionMeta struct {
	ConnectorKey   string
	ConnectorLabel string
	OpName         string
	Destructive    bool
}

// Person is a wick user as the card shows them.
type Person struct {
	ID       string
	Name     string
	IsAdmin  bool
	Approved bool
}

// Catalog is the connector side the service needs. The real one wraps
// connectors.Service; tests use a fake.
type Catalog interface {
	// Describe resolves a connector instance and one of its operations.
	// override is the action's per-call config override (nil for none).
	Describe(ctx context.Context, connectorID, opKey string, override map[string]string) (ActionMeta, error)
	// Requestable reports whether the session's agent may ask for actions
	// on this connector at all: it must already be in its access (at least
	// read). A connector its owner switched off cannot be asked for.
	Requestable(ctx context.Context, sessionID, agentID, connectorID string) (bool, string)
	// CanRun reports whether userID can run opKey on connectorID with
	// their OWN access (no agent scope, no lend). why explains a no.
	// With an override the user must also be allowed to configure the
	// instance.
	CanRun(ctx context.Context, userID, connectorID, opKey string, override map[string]string) (ok bool, why string)
	// Run executes the action as userID with their own access and
	// returns the run id and the response.
	Run(ctx context.Context, userID, connectorID, opKey, accountID string, params map[string]any) (runID, result string, err error)
}

// Directory is who people and sessions are.
type Directory interface {
	User(ctx context.Context, id string) (Person, bool)
	Admins(ctx context.Context) []Person
	// SessionOwner is the wick user a session belongs to.
	SessionOwner(sessionID string) string
	// AgentOwner is the owner of a Team agent, "" when unknown.
	AgentOwner(ctx context.Context, agentID string) string
	// AgentName is the agent's display name, "" when unknown.
	AgentName(ctx context.Context, agentID string) string
}

// Asker is the person whose message the agent was answering when it
// asked, as the channel resolved it (never what the agent typed).
type Asker struct {
	ID         string `json:"id"`
	Name       string `json:"name,omitempty"`
	Channel    string `json:"channel,omitempty"`
	WickUserID string `json:"wick_user_id,omitempty"`
}

// SessionAskers is the optional part of a Directory that names the
// person behind a session's latest turn (nil when none: a schedule,
// another agent).
type SessionAskers interface {
	LastAsker(sessionID string) *Asker
}

// Publisher draws a request on the surfaces that show it. Publish is
// called when the request is filed and again after every change; each
// publisher decides whether the request's session is one it serves.
type Publisher interface {
	Publish(ctx context.Context, v View) (origin map[string]string, err error)
}

// Waker delivers the outcome to the requesting session, waking it.
type Waker func(ctx context.Context, sessionID, text string) error

// View is the neutral picture of a request for one viewer. Channels draw
// it; they never decide access themselves.
type View struct {
	ID        string
	SessionID string
	AgentID   string
	AgentName string
	// Requester is the session's owner.
	Requester string
	// AskedBy is the person whose message the agent was answering (nil
	// when none).
	AskedBy *Asker
	// Reason is the agent's claim, unverified.
	Reason    string
	Status    string
	CreatedAt time.Time
	// URL is the web page for this request ("" when the public URL is not
	// known).
	URL   string
	Items []ItemView
	// ViewerID is who this view was computed for ("" = nobody in
	// particular: a card posted to a thread).
	ViewerID string
	// ViewerCanDecide is true when at least one pending item is the
	// viewer's to decide.
	ViewerCanDecide bool
	// ViewerReason explains, when the viewer can decide nothing, why.
	ViewerReason     string
	ViewerReasonCode string
	// Origin is where the card was posted (publisher-defined keys).
	Origin map[string]string
	// Pending counts items still waiting.
	Pending int
}

// ItemView is one action as a viewer sees it.
type ItemView struct {
	ID             string
	Idx            int
	ConnectorID    string
	ConnectorKey   string
	ConnectorLabel string
	OpKey          string
	OpName         string
	Kind           string
	// Target is a one-line, server-computed summary of what the action
	// touches, built from its real parameters.
	Target string
	// Params lists every parameter, secrets masked, long values cut.
	Params []Param
	// ParamsJSON is the full masked parameter object.
	ParamsJSON string
	Status     string
	Mode       string
	DecidedBy  string
	DecidedAt  *time.Time
	Note       string
	Result     string
	Error      string
	// ApproversText, when set, is who may approve as the card says it
	// (from an approvers rule's label); empty = list Approvers by name.
	ApproversText string
	// Modes the viewer may pick for this item (empty when locked).
	Modes []string
	// CanDecide is true when the viewer may decide this item now.
	CanDecide bool
	// LockedReason / LockedCode say why not.
	LockedReason string
	LockedCode   string
	// Approvers are the people who could decide this item.
	Approvers []Person

	// approverNote names approvers rules that let more people in;
	// roleReason is an "only" rule's refusal. Internal to the view.
	approverNote string
	roleReason   string
}

// Param is one masked parameter.
type Param struct {
	Name  string
	Value string
	Long  bool
}

// ConfigOverrideParam is the reserved action parameter holding a per-call
// config override ({"mode": "write"}). Only someone who may configure the
// connector approves it; a timed approval covers the same override only.
const ConfigOverrideParam = "config_override"

// overrideOf reads the override from stored or incoming params.
func overrideOf(params map[string]any) map[string]string {
	raw, ok := params[ConfigOverrideParam]
	if !ok || raw == nil {
		return nil
	}
	var m map[string]any
	switch x := raw.(type) {
	case map[string]any:
		m = x
	case string:
		if json.Unmarshal([]byte(x), &m) != nil {
			return map[string]string{"": x} // unreadable: Describe refuses it
		}
	default:
		return map[string]string{"": fmt.Sprint(x)}
	}
	if len(m) == 0 {
		return nil
	}
	return stringify(m)
}

// overrideOfAction reads it from an Action's string params.
func overrideOfAction(a Action) map[string]string {
	raw, ok := a.Params[ConfigOverrideParam]
	if !ok || raw == "" {
		return nil
	}
	return overrideOf(map[string]any{ConfigOverrideParam: raw})
}

// overrideOfItem reads it from a stored item.
func overrideOfItem(it entity.AccessRequestItem) map[string]string {
	var params map[string]any
	_ = json.Unmarshal([]byte(it.Params), &params)
	return overrideOf(params)
}

type sessionCtxKey struct{}

// WithSession tells the Catalog which session an action belongs to, so a
// session's own connector (sw_…) can be found.
func WithSession(ctx context.Context, sessionID string) context.Context {
	if sessionID == "" {
		return ctx
	}
	return context.WithValue(ctx, sessionCtxKey{}, sessionID)
}

// SessionOf is the session WithSession set ("" when none).
func SessionOf(ctx context.Context) string {
	v, _ := ctx.Value(sessionCtxKey{}).(string)
	return v
}
