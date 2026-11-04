package manager

import (
	"context"
	"net/http"
	"strings"

	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/entity"
)

// auditNames resolves ids to display names once per request.
type auditNames struct {
	h        *Handler
	ctx      context.Context
	conns    map[string]*entity.Connector
	users    map[string]string
	sessions map[string]string
	sessProj map[string]string
	projects map[string]string
}

func (h *Handler) newAuditNames(ctx context.Context) *auditNames {
	return &auditNames{h: h, ctx: ctx, conns: map[string]*entity.Connector{}, users: map[string]string{},
		sessions: map[string]string{}, sessProj: map[string]string{}, projects: map[string]string{}}
}

// connector is the instance row, nil for an id with no row (a session
// connector, or one since deleted).
func (n *auditNames) connector(id string) *entity.Connector {
	if c, ok := n.conns[id]; ok {
		return c
	}
	c, err := n.h.connectors.Get(n.ctx, id)
	if err != nil {
		c = nil
	}
	n.conns[id] = c
	return c
}

func (n *auditNames) user(id string) string {
	if v, ok := n.users[id]; ok {
		return v
	}
	n.users[id] = n.h.userLabel(n.ctx, id)
	return n.users[id]
}

func (n *auditNames) session(id string) string {
	if v, ok := n.sessions[id]; ok {
		return v
	}
	label := ""
	if n.h.places != nil {
		label = n.h.places.SessionLabel(id)
	}
	n.sessions[id] = label
	return label
}

// project is the name of the session's project ("" when none).
func (n *auditNames) project(sessionID string) (string, string) {
	pid, ok := n.sessProj[sessionID]
	if !ok {
		if n.h.places != nil {
			pid = n.h.places.SessionProject(sessionID)
		}
		n.sessProj[sessionID] = pid
	}
	if pid == "" {
		return "", ""
	}
	name, ok := n.projects[pid]
	if !ok {
		if n.h.places != nil {
			name = n.h.places.ProjectName(pid)
		}
		if name == "" {
			name = pid
		}
		n.projects[pid] = name
	}
	return pid, name
}

func (h *Handler) auditBreakdown(ctx context.Context, f connectors.AuditFilter, names *auditNames) auditBreakdownJSON {
	b, err := h.connectors.AuditBreakdown(ctx, f)
	if err != nil {
		return auditBreakdownJSON{}
	}
	instance := func(c connectors.AuditCount) auditCountJSON {
		row := auditCountJSON{Key: c.Key, Label: c.Key, Op: c.Op, Runs: c.Runs, Failed: c.Failed}
		if conn := names.connector(c.Key); conn != nil {
			row.Label, row.ConnectorKey = conn.Label, conn.Key
		}
		return row
	}
	plain := func(in []connectors.AuditCount, label func(string) string) []auditCountJSON {
		out := make([]auditCountJSON, 0, len(in))
		for _, c := range in {
			l := c.Key
			if label != nil {
				l = label(c.Key)
			}
			out = append(out, auditCountJSON{Key: c.Key, Label: l, Runs: c.Runs, Failed: c.Failed})
		}
		return out
	}
	out := auditBreakdownJSON{
		Users:   plain(b.Users, names.user),
		Sources: plain(b.Sources, nil),
		Errors:  plain(b.Errors, firstErrorLine),
	}
	for _, c := range b.Instances {
		out.Instances = append(out.Instances, instance(c))
	}
	for _, c := range b.Operations {
		out.Operations = append(out.Operations, instance(c))
	}
	for _, c := range b.Sessions {
		row := auditCountJSON{Key: c.Key, Label: names.session(c.Key), Runs: c.Runs, Failed: c.Failed}
		if row.Label == "" {
			row.Label = c.Key
		}
		_, row.Project = names.project(c.Key)
		out.Sessions = append(out.Sessions, row)
	}
	return out
}

// auditOptions builds the connector → instance → operation pickers from
// the runs matching f without its own three filters, so picking one
// level still lists its siblings.
func (h *Handler) auditOptions(ctx context.Context, f connectors.AuditFilter, names *auditNames) []auditConnectorJSON {
	f.ConnectorKey, f.ConnectorID, f.OperationKey = "", "", ""
	pairs, err := h.connectors.AuditOptions(ctx, f, auditOptionsLimit)
	if err != nil {
		return nil
	}
	var out []auditConnectorJSON
	byKey := map[string]int{}
	inst := map[string][2]int{}
	for _, p := range pairs {
		key, label := "session", p.Key
		if c := names.connector(p.Key); c != nil {
			key, label = c.Key, c.Label
		}
		ki, ok := byKey[key]
		if !ok {
			ki = len(out)
			byKey[key] = ki
			out = append(out, auditConnectorJSON{Key: key})
		}
		out[ki].Runs += p.Runs
		pos, ok := inst[p.Key]
		if !ok {
			pos = [2]int{ki, len(out[ki].Instances)}
			inst[p.Key] = pos
			out[ki].Instances = append(out[ki].Instances, auditInstanceJSON{ID: p.Key, Label: label})
		}
		in := &out[pos[0]].Instances[pos[1]]
		in.Runs += p.Runs
		in.Ops = append(in.Ops, p.Op)
	}
	return out
}

// auditRunDetailJSON is one connector call in full, for the audit page's
// row detail: the exact time, who, from which session and project, and
// the request and response wick recorded.
type auditRunDetailJSON struct {
	auditRunJSON
	AccountID   string `json:"account_id"`
	HTTPStatus  int    `json:"http_status"`
	IPAddress   string `json:"ip_address"`
	UserAgent   string `json:"user_agent"`
	ParentRunID string `json:"parent_run_id"`
	EndedAt     string `json:"ended_at"`
	ErrorFull   string `json:"error_full"`
	Request     string `json:"request"`
	Response    string `json:"response"`
}

// apiAuditRun serves GET /manager/api/runs/{runID}: one connector call
// with everything stored for it. Admin-only (gated by the route), like
// the connector History page that already shows the same payloads.
func (h *Handler) apiAuditRun(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	run, err := h.connectors.GetRun(ctx, r.PathValue("runID"))
	if err != nil || run == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "run not found"})
		return
	}
	names := h.newAuditNames(ctx)
	out := auditRunDetailJSON{
		auditRunJSON: auditRunJSON{
			ID: run.ID, ConnectorID: run.ConnectorID, OperationKey: run.OperationKey,
			Source: string(run.Source), Status: string(run.Status), UserID: run.UserID,
			SessionID: run.SessionID, LatencyMs: run.LatencyMs, StartedAt: run.StartedAt.Format(timeRFC3339),
			Error: firstErrorLine(run.ErrorMsg),
		},
		AccountID: run.AccountID, HTTPStatus: run.HTTPStatus, IPAddress: run.IPAddress, UserAgent: run.UserAgent,
		ErrorFull: run.ErrorMsg, Request: run.RequestJSON, Response: run.ResponseJSON,
	}
	if run.UserID != "" {
		out.UserName = names.user(run.UserID)
	}
	if run.SessionID != "" {
		out.SessionLabel = names.session(run.SessionID)
		out.ProjectID, out.ProjectName = names.project(run.SessionID)
	}
	if c := names.connector(run.ConnectorID); c != nil {
		out.ConnectorKey, out.ConnectorName = c.Key, c.Label
	}
	if run.ParentRunID != nil {
		out.ParentRunID = *run.ParentRunID
	}
	if run.EndedAt != nil {
		out.EndedAt = run.EndedAt.Format(timeRFC3339)
	}
	writeJSON(w, http.StatusOK, out)
}

// firstErrorLine is the first line of an error, for lists.
func firstErrorLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}
