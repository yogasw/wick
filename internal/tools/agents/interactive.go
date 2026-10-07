package agents

import (
	"encoding/json"
	"sort"
	"strings"
	"sync"

	"github.com/yogasw/wick/internal/agents/askuser"
	"github.com/yogasw/wick/internal/agents/gate"
	"github.com/yogasw/wick/internal/agents/store"
)

// Interactive turns: an ask_user question and a gate approval are kept in
// the thread as system turns, not only shown above the composer while
// they wait. Each is recorded twice under one id — once when raised
// (extras.state = "pending") and once when settled — and the front-end
// folds the pair into one card, the same way mention_handoff rows fold
// by task_id.

// RecordAskRequest writes a pending ask_user question into its session's
// thread as a kind:"input_request" turn.
func RecordAskRequest(req askuser.AskRequest) {
	emitSystemEvent(req.SessionID, store.KindInputRequest, req.Question, inputRequestExtras(req, "pending", ""))
}

// RecordAskSettled writes how the question ended: answered (with the
// answer as shown to the user), timeout or cancelled.
func RecordAskSettled(req askuser.AskRequest, ans askuser.Answer, outcome string) {
	shown := ""
	text := req.Question
	switch outcome {
	case askuser.OutcomeAnswered:
		shown = answerLabel(req, ans)
		text = "answered: " + shown
	case askuser.OutcomeTimeout:
		text = "no answer in time"
	case askuser.OutcomeCancelled:
		text = "question withdrawn"
	}
	ex := inputRequestExtras(req, outcome, shown)
	if outcome == askuser.OutcomeAnswered {
		if b, err := json.Marshal(maskAnswer(req, ans)); err == nil {
			ex["answer_json"] = string(b)
		}
	}
	emitSystemEvent(req.SessionID, store.KindInputRequest, text, ex)
}

// inputRequestExtras carries the request itself (options, fields) as JSON
// so the card can be drawn from the thread alone. Prefilled values of
// secret fields are dropped before it is written.
func inputRequestExtras(req askuser.AskRequest, state, answer string) map[string]string {
	shape := req
	shape.Fields = append([]askuser.Field(nil), req.Fields...)
	for i := range shape.Fields {
		if isSecretField(shape.Fields[i]) {
			shape.Fields[i].Value = ""
		}
	}
	raw, _ := json.Marshal(shape)
	ex := map[string]string{
		"ask_id": req.ID, "state": state, "agent": req.AgentName,
		"question": req.Question, "request": string(raw),
	}
	if answer != "" {
		ex["answer"] = answer
	}
	return ex
}

func isSecretField(f askuser.Field) bool {
	return f.Type == "secret"
}

// maskAnswer is ans with every secret field's value replaced, so a typed
// credential never lands in conversation.jsonl.
func maskAnswer(req askuser.AskRequest, ans askuser.Answer) askuser.Answer {
	if len(ans.Values) == 0 {
		return ans
	}
	out := ans
	out.Values = make(map[string]string, len(ans.Values))
	for k, v := range ans.Values {
		out.Values[k] = v
	}
	for _, f := range req.Fields {
		if _, ok := out.Values[f.Key]; ok && isSecretField(f) {
			out.Values[f.Key] = "••••"
		}
	}
	return out
}

// answerLabel is the answer as the pill shows it: the clicked option's
// label, the typed text, or "key: value" pairs of a form (secrets masked).
func answerLabel(req askuser.AskRequest, ans askuser.Answer) string {
	if ans.Value != "" {
		for _, o := range req.Options {
			if o.Value == ans.Value && o.Label != "" {
				return o.Label
			}
		}
		return ans.Value
	}
	if strings.TrimSpace(ans.Text) != "" {
		return ans.Text
	}
	m := maskAnswer(req, ans).Values
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+": "+m[k])
	}
	if len(parts) == 0 {
		return "submitted"
	}
	return strings.Join(parts, ", ")
}

// Approval card decisions (the codex app-server enum), mapped onto the
// gate's own decisions so a card click resolves exactly as the modal does.
const (
	ApprovalAccept           = "accept"
	ApprovalAcceptForSession = "accept_for_session"
	ApprovalDecline          = "decline"
)

// gateDecision maps a card decision to the gate's. ok=false for anything
// else.
func gateDecision(d string) (string, bool) {
	switch d {
	case ApprovalAccept:
		return gate.DecisionApproveOnce, true
	case ApprovalAcceptForSession:
		return gate.DecisionApproveSession, true
	case ApprovalDecline:
		return gate.DecisionBlock, true
	}
	return "", false
}

// RecordApprovalRequest writes a gate approval prompt into its session's
// thread as a kind:"approval_request" turn — the card is made by the
// server, never by the agent.
func RecordApprovalRequest(sessionID string, r gate.ApprovalRequest) {
	if r.Probe {
		return
	}
	approvalCards.add(r.ID, sessionID, r.MatchKey)
	text := r.Tool
	if r.Cmd != "" {
		text = strings.TrimSpace(r.Tool + ": " + r.Cmd)
	}
	emitSystemEvent(sessionID, store.KindApprovalRequest, text, map[string]string{
		"approval_id": r.ID, "state": "pending", "agent": r.AgentName,
		"tool": r.Tool, "cmd": r.Cmd, "work_dir": r.WorkDir, "match_key": r.MatchKey,
	})
}

// RecordApprovalResolved writes how the prompt was settled under the same
// approval_id; decision is the gate's (approve_once, approve_session,
// approve_always, block, guide, or a timeout's block).
func RecordApprovalResolved(sessionID, requestID, decision string) {
	approvalCards.drop(requestID)
	emitSystemEvent(sessionID, store.KindApprovalRequest, approvalDecisionText(decision), map[string]string{
		"approval_id": requestID, "state": decision,
	})
}

func approvalDecisionText(decision string) string {
	switch decision {
	case gate.DecisionApproveOnce:
		return "accepted"
	case gate.DecisionApproveSession:
		return "accepted for this agent"
	case gate.DecisionApproveAlways:
		return "always allowed"
	case gate.DecisionGuide:
		return "declined with guidance"
	default:
		return "declined"
	}
}

// approvalIndex remembers which WICK session each open approval belongs
// to. The gate's own ApprovalRequest.SessionID is the CLI's session id
// (and in-process requests are not in the socket's pending list), so the
// session the daemon routed the prompt to is kept here, from OnRequest
// until OnResolved.
type approvalIndex struct {
	mu   sync.Mutex
	open map[string]approvalRef
}

type approvalRef struct{ sessionID, matchKey string }

var approvalCards = &approvalIndex{open: map[string]approvalRef{}}

func (x *approvalIndex) add(id, sessionID, matchKey string) {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.open[id] = approvalRef{sessionID, matchKey}
}

func (x *approvalIndex) drop(id string) {
	x.mu.Lock()
	defer x.mu.Unlock()
	delete(x.open, id)
}

// lookup returns the open approval id in sessionID, ok=false when it is
// settled or belongs to another session.
func (x *approvalIndex) lookup(sessionID, id string) (approvalRef, bool) {
	x.mu.Lock()
	defer x.mu.Unlock()
	ref, ok := x.open[id]
	return ref, ok && ref.sessionID == sessionID
}
