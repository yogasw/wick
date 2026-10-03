package agents

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/yogasw/wick/internal/agents/askuser"
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

