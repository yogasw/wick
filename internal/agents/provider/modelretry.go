package provider

import (
	"fmt"
	"slices"
	"strings"
)

// modelretry.go is the opt-in (Instance.AutoRetryModel, omp/opencode) answer
// to a turn the account was refused its model on: instead of failing, the
// SAME message runs again on the next usable model — the one that last
// worked if different, else the next one in the instance's live list that
// is not known refused. At most maxModelRetries per message, and never once
// the agent produced text or ran a tool (that turn did real work; re-running
// it would repeat side effects). The refusal itself is still recorded by
// modelTurnWatch, so the picker greys the model out as before.

// maxModelRetries caps re-runs of one message across models.
const maxModelRetries = 2

// autoRetryModelOn reports whether ins retries refused-model turns.
func autoRetryModelOn(ins *Instance) bool {
	return ins != nil && ins.AutoRetryModel && SupportsAutoRetryModel(ins.Type)
}

// nextRetryModel picks the model to re-run a refused turn on: the model that
// last worked (for account key, then instance-wide) when it differs, else
// the next listed model after failed, wrapping. Models in tried or recorded
// refused are skipped. "" = nothing left to try.
func nextRetryModel(ins Instance, key, failed string, tried []string) string {
	skip := func(id string) bool {
		if id == "" || id == failed || slices.Contains(tried, id) {
			return true
		}
		if _, bad := ModelUnavailable(ins, key, id); bad {
			return true
		}
		_, bad := ModelUnavailable(ins, modelPrefix(id), id)
		return bad
	}
	for _, last := range []string{LastWorkedModel(ins, key), LastWorkedModel(ins, "")} {
		if !skip(last) {
			return last
		}
	}
	list := retryCandidates(ins)
	start := 0
	for i, id := range list {
		if id == failed {
			start = i + 1
			break
		}
	}
	for i := range list {
		if id := list[(start+i)%len(list)]; !skip(id) {
			return id
		}
	}
	return ""
}

// retryCandidates is the instance's model list in picker order: the live
// CLI list (cached only — this runs on the reader path and must not exec)
// when on, else the operator's manual list.
func retryCandidates(ins Instance) []string {
	var out []string
	if LiveModelsEnabled(ins) {
		for _, m := range LiveDefaultFirst(FilterLiveModels(ins, PeekCLIModels(ins)), ins.LiveModelDefault) {
			out = append(out, m.ID)
		}
		return out
	}
	for _, m := range ins.Models {
		out = append(out, m.ID)
	}
	return out
}

// retryPin re-addresses pin to model: a grouped pin keeps its provider /
// account path when model belongs to that provider, anything else becomes
// the flat model id (the spawn passes it as --model on Auto).
func retryPin(ins *Instance, pin, model string) string {
	if path, _, ok := DecodePin(pin); ok && len(path) > 0 && path[0] == modelPrefix(model) {
		next := EncodePin(path, model)
		if _, ok := ResolvePin(ins, next); ok {
			return next
		}
	}
	return model
}

// modelRetryNotice is the transcript line for a re-run on another model.
func modelRetryNotice(from, to string) string {
	return fmt.Sprintf("Model %s is not available for this account — retried with %s.", shortModel(from), shortModel(to))
}

func shortModel(id string) string {
	if i := strings.LastIndexByte(id, '/'); i >= 0 {
		return id[i+1:]
	}
	return id
}

// retryRefusedModel re-queues the current turn on the next model when w saw
// this turn refused its model and the instance opted in. Called by the
// reader on the turn's error and once more when the reader exits (a refusal
// with no Error event); produced = the agent already streamed text or ran a
// tool, which rules the retry out. Returns whether a retry was queued; a
// refusal on an opted-in instance that is not retried gets the usual "pick
// another model" line instead (observe leaves it to this call).
func (a *Agent) retryRefusedModel(w *modelTurnWatch, produced bool) bool {
	ins := a.cfg.Instance
	if !autoRetryModelOn(ins) {
		return false
	}
	model, key, ok := w.takeRefusal()
	if !ok {
		return false
	}
	next := ""
	if !produced {
		next = a.queueModelRetry(*ins, model, key)
	}
	if next == "" {
		w.announceRefusal()
		return false
	}
	a.mu.Lock()
	st := a.store
	a.mu.Unlock()
	if st != nil {
		_ = st.AppendNoticeTurn(modelRetryNotice(model, next))
	}
	return true
}

// queueModelRetry switches the agent onto the next model and puts the turn's
// message back at the head of the queue. Returns the model it moved to, ""
// when it did not (stopped, cap reached, nothing left).
func (a *Agent) queueModelRetry(ins Instance, failed, key string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	msg := a.turnMsg
	if msg == "" || a.stopped {
		return ""
	}
	// A retried turn may carry messages queued meanwhile after it (the
	// queue is joined into one turn), so it still counts as the same one.
	if a.modelRetryMsg == "" || !strings.HasPrefix(msg, a.modelRetryMsg) {
		a.modelRetryMsg, a.modelRetries, a.modelTried = msg, 0, nil
	}
	if a.modelRetries >= maxModelRetries {
		return ""
	}
	a.modelTried = append(a.modelTried, failed)
	next := nextRetryModel(ins, key, failed, a.modelTried)
	if next == "" {
		return ""
	}
	a.modelRetries++
	a.modelOverride = retryPin(a.cfg.Instance, a.spawnModelIDLocked(), next)
	a.pendingQueue = append([]string{msg}, a.pendingQueue...)
	return next
}

// spawnModelIDLocked is the pin the next spawn runs: a retry's model once
// one moved this agent off a refused model, else the session's pin. Caller
// holds a.mu.
func (a *Agent) spawnModelIDLocked() string {
	if a.modelOverride != "" {
		return a.modelOverride
	}
	// A pin this instance cannot run (chosen on another account, or refused
	// here since) would fail every turn with model_not_found: drop it for
	// this agent and say so; the spawn then runs the instance's default.
	if m, stale := StalePin(a.cfg.Instance, a.cfg.ModelID); stale {
		if a.store != nil {
			_ = a.store.AppendNoticeTurn(StalePinNotice(*a.cfg.Instance, m))
		}
		a.cfg.ModelID = ""
	}
	return a.cfg.ModelID
}
