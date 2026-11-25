package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/yogasw/wick/internal/accessreq"
	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/connectors"
)

// A script that writes cannot ask for approval halfway: the first write
// the agent's access refuses ends it. wick_run_code's request_access
// turns that around. The script runs once as a dry run: reads run as
// usual, every write (a destructive op) is NOT executed but recorded, and
// returns a stub. The recorded writes become the actions of ONE access
// card, in call order; once approved they run one after another like any
// card. A script that needs a write's real answer to go on cannot be
// recorded this way: split it, or file the actions yourself.

// recordReq is wick_run_code's request_access argument.
type recordReq struct {
	Reason string
	Key    string
	After  string
	// ShowCard posts the card even when a rule approves every write.
	ShowCard bool
}

func recordReqOf(args map[string]any) (*recordReq, error) {
	raw, ok := args["request_access"]
	if !ok || raw == nil {
		return nil, nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, errors.New(`request_access must be an object: {"reason": "...", "key": "...", "after": "...", "show_card": false}`)
	}
	r := &recordReq{}
	r.Reason, _ = m["reason"].(string)
	r.Key, _ = m["key"].(string)
	r.After, _ = m["after"].(string)
	r.ShowCard, _ = m["show_card"].(bool)
	r.Reason = strings.TrimSpace(r.Reason)
	if r.Reason == "" {
		return nil, errors.New("request_access.reason is required: one or two sentences saying what the writes change and why, for the approver")
	}
	return r, nil
}

// writeRecorder collects the writes of a dry run.
type writeRecorder struct {
	mu   sync.Mutex
	acts []accessreq.ActionInput
}

func (w *writeRecorder) add(a accessreq.ActionInput) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.acts = append(w.acts, a)
	return len(w.acts)
}

func (w *writeRecorder) actions() []accessreq.ActionInput {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]accessreq.ActionInput(nil), w.acts...)
}

// toolIsWrite reports whether toolID names a destructive op, session
// connectors (sw_…) included. Unknown counts as a write: a dry run must
// never run something it could not classify.
func toolIsWrite(ctx context.Context, svc *connectors.Service, layout agentconfig.Layout, sessionID, toolID string) bool {
	connectorID, opKey, _, err := ParseToolIDFull(toolID)
	if err != nil {
		return true
	}
	if t, isSession, err := SessionInstanceForCaller(ctx, layout, sessionID, connectorID); isSession {
		if err != nil || t == nil {
			return true
		}
		destructive, found := opOf(svc, t.BaseKey, opKey)
		return destructive || !found
	}
	row, err := svc.Get(ctx, connectorID)
	if err != nil || row == nil {
		return true
	}
	destructive, found := opOf(svc, row.Key, opKey)
	return destructive || !found
}

// recordStub is what a recorded write returns to the script.
func recordStub(n int) string {
	b, _ := json.Marshal(map[string]any{"recorded": true, "action": n,
		"note": "dry run: this write was not executed; it is filed for approval with the others"})
	return string(b)
}

// fileRecorded turns the recorded writes into one access request.
func fileRecorded(ctx context.Context, sessionID string, rr *recordReq, acts []accessreq.ActionInput) (map[string]any, error) {
	if AccessRequests == nil {
		return nil, errors.New("access requests are not available on this wick")
	}
	if len(acts) == 0 {
		return map[string]any{"status": "nothing_to_approve", "note": "the script made no write, so no card was filed"}, nil
	}
	if len(acts) > accessreq.MaxActions {
		return nil, fmt.Errorf("the script recorded %d writes; one card holds at most %d. Split the work (e.g. by input range) and file each part with its own key, ordered with after", len(acts), accessreq.MaxActions)
	}
	v, err := AccessRequests.Create(ctx, accessreq.CreateInput{
		SessionID: sessionID, AgentID: connectors.AgentIDOf(ctx), Reason: rr.Reason, Key: rr.Key, After: rr.After, ShowCard: rr.ShowCard, Actions: acts,
	})
	if err != nil {
		return nil, err
	}
	out := accessSummary(v)
	if v.AutoRan {
		out["next"] = autoRanNext
		return out, nil
	}
	out["next"] = "The writes are filed as one card. Tell the person what you asked for and end your turn; approved writes run on their own, in order — do not run them yourself."
	return out, nil
}
