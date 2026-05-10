package agents

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/yogasw/wick/internal/agents/gate"
	"github.com/yogasw/wick/pkg/tool"
)

// approvalReq is the body shape for POST /sessions/{id}/approve.
// Decision must match one of gate.DecisionApprove* / DecisionBlock —
// any other value is treated as block by the underlying manager but
// rejected here for clarity.
type approvalReq struct {
	ID       string `json:"id"`        // pending request UUID minted by the gate binary
	Decision string `json:"decision"`  // approve_once | approve_session | approve_always | block
	MatchKey string `json:"match_key"` // hash echoed back; needed for session/always state
	Reason   string `json:"reason,omitempty"`
}

// notReadyApprovals is the gate-disabled guard. If the parent process
// failed to resolve the gate binary, every approval call returns
// 503 with a hint pointing the operator at the env var override.
func notReadyApprovals(c *tool.Ctx) bool {
	if globalApprovals == nil {
		c.JSON(http.StatusServiceUnavailable, map[string]string{
			"error": "agents gate disabled — build the app with `wick build` to produce the sibling sidecar + embedded fallback",
		})
		return true
	}
	return false
}

// approveCommand resolves one pending approval. The matchKey rides
// along so the manager can update its session/always sets without a
// second lookup; the front-end already has it from the SSE event.
func approveCommand(c *tool.Ctx) {
	if notReady(c) || notReadyApprovals(c) {
		return
	}
	sessionID := c.PathValue("id")
	var req approvalReq
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	switch req.Decision {
	case gate.DecisionApproveOnce, gate.DecisionApproveSession,
		gate.DecisionApproveAlways, gate.DecisionBlock:
	default:
		c.JSON(http.StatusBadRequest, map[string]string{
			"error": "decision must be approve_once | approve_session | approve_always | block",
		})
		return
	}
	if req.ID == "" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "id required"})
		return
	}

	// Snapshot the cmd before Resolve removes it from the pending set.
	var pendingCmd string
	if req.Decision == gate.DecisionApproveAlways {
		if pr, ok := globalApprovals.LookupPending(req.ID); ok {
			pendingCmd = pr.Cmd
		}
	}

	ok, err := globalApprovals.Resolve(sessionID, req.ID, req.Decision, req.Reason, req.MatchKey)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if !ok {
		c.JSON(http.StatusGone, map[string]string{
			"error": "request id no longer pending (timed out or already resolved)",
		})
		return
	}

	// Persist the approved command so future sessions hit the whitelist
	// directly without going through the socket.
	if pendingCmd != "" {
		appendToAllowedCmds(c.Context(), pendingCmd)
	}

	c.JSON(http.StatusOK, map[string]string{"status": "resolved"})
}

// approvalsSnapshot returns the per-session approval state — pending
// requests + session/always-approved keys. Used by the UI to
// rehydrate the modal after a tab reload + populate the Revoke
// panel.
func approvalsSnapshot(c *tool.Ctx) {
	if notReady(c) || notReadyApprovals(c) {
		return
	}
	sessionID := c.PathValue("id")
	out := map[string]any{
		"pending":          globalApprovals.PendingFor(sessionID),
		"session_approved": globalApprovals.SessionApprovedKeys(sessionID),
		"always_approved":  globalApprovals.AutoApproved(),
	}
	c.JSON(http.StatusOK, out)
}

// appendToAllowedCmds adds cmd as an exact pattern to the allowed_cmds
// config if not already present. Runs best-effort; errors are silently
// dropped — the always-allow hash in spec.json is already written so
// the gate binary still fast-paths the command on future invocations.
func appendToAllowedCmds(ctx context.Context, cmd string) {
	if globalConfigs == nil || cmd == "" {
		return
	}
	raw := globalConfigs.GetOwned("agents", "allowed_cmds")
	var rows []map[string]string
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &rows)
	}
	for _, r := range rows {
		if r["pattern"] == cmd {
			return // already present
		}
	}
	rows = append(rows, map[string]string{"pattern": cmd})
	data, err := json.Marshal(rows)
	if err != nil {
		return
	}
	_ = globalConfigs.SetOwned(ctx, "agents", "allowed_cmds", string(data))
}

// revokeApproval drops one matchKey from the session set, the
// always-allow set, or both — depending on which scope was
// specified. Front-end's Revoke button on the Approved-commands
// panel hits this.
func revokeApproval(c *tool.Ctx) {
	if notReady(c) || notReadyApprovals(c) {
		return
	}
	sessionID := c.PathValue("id")
	matchKey := c.PathValue("matchKey")
	scope := c.Query("scope") // "session" | "always" | "" (= both)

	switch scope {
	case "session":
		globalApprovals.RevokeSession(sessionID, matchKey)
	case "always":
		if err := globalApprovals.RevokeAlways(sessionID, matchKey); err != nil {
			c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	case "", "both":
		globalApprovals.RevokeSession(sessionID, matchKey)
		if err := globalApprovals.RevokeAlways(sessionID, matchKey); err != nil {
			c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	default:
		c.JSON(http.StatusBadRequest, map[string]string{
			"error": "scope must be session | always | both",
		})
		return
	}
	c.JSON(http.StatusOK, map[string]string{"status": "revoked"})
}
