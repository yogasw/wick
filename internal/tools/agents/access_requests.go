package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/yogasw/wick/internal/accessreq"
	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/store"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/pkg/tool"
)

// Access requests on the web: the card in the conversation (its JSON
// view and decisions) and each project's Access requests settings card
// (history, rules, grants, facts). There is no standalone page: an
// approver decides from the card where the request was made, and a
// project's managers audit and set rules from the project's settings.
// Every decision is made by the service; this file only forwards.

var globalAccess *accessreq.Service

// SetAccessRequests wires the access-request service. Call once at boot.
func SetAccessRequests(s *accessreq.Service) { globalAccess = s }

func registerAccessRoutes(r tool.Router) {
	r.GET("/access/{rid}/view", accessRequestViewJSON)
	r.POST("/access/{rid}/decisions", accessRequestDecisionsJSON)
	r.GET("/api/projects/{id}/access", projectOwnerMW(projectAccessJSON))
	r.POST("/api/projects/{id}/access/rules", projectOwnerMW(projectAccessRuleSave))
	r.POST("/api/projects/{id}/access/rules/{rid}/delete", projectOwnerMW(projectAccessRuleDelete))
	r.POST("/api/projects/{id}/access/simulate", projectOwnerMW(projectAccessSimulate))
	r.POST("/api/projects/{id}/access/grants/{gid}/revoke", projectOwnerMW(projectAccessGrantRevoke))
	r.POST("/api/projects/{id}/access/enabled", projectOwnerMW(projectAccessSetEnabled))
	// Workspace panel, Connector access (guarded by sessionAccessMW).
	r.GET("/sessions/{id}/access", sessionAccessJSON)
	r.POST("/sessions/{id}/access/policy", sessionAccessPolicySet)
	r.POST("/sessions/{id}/access/grants/{gid}/revoke", sessionAccessGrantRevoke)
}

func accessUser(c *tool.Ctx) *entity.User {
	u := login.GetUser(c.Context())
	if u == nil || u.ID == "" {
		c.Error(http.StatusUnauthorized, "sign in to wick first")
		return nil
	}
	return u
}

// accessSameOrigin refuses a form post that came from another site.
func accessSameOrigin(c *tool.Ctx) bool {
	o := c.R.Header.Get("Origin")
	if o == "" {
		o = c.R.Header.Get("Referer")
	}
	if o == "" {
		return true
	}
	u, err := url.Parse(o)
	if err != nil || !strings.EqualFold(u.Host, c.R.Host) {
		c.Error(http.StatusForbidden, "cross-site request refused")
		return false
	}
	return true
}

func accessReady(c *tool.Ctx) bool {
	if globalAccess == nil {
		c.Error(http.StatusServiceUnavailable, "access requests are not available on this wick")
		return false
	}
	return true
}

// accessRuleExample is the new-rule template: staging apps may be
// written without a card; production stays behind one (a second rule,
// effect deny). Rules apply to this project's sessions only.
const accessRuleExample = `name: staging-apps-auto
effect: auto_approve        # or: require / deny / approvers
simulate: true              # log "would auto-approve" first; set false to act
when:
  ops: [apps.*, bots.*]
  kind: [read, write]       # delete is never auto-approved
  agents: [captain]         # optional: agent id or name
  all:
    - fact: app             # the app code from the action or its connector
      in: [acme-stag, acme-dev]
# Pair it with a deny rule for production:
#   name: prod-apps-never, effect: deny, same ops,
#   all: [{fact: app, in: [acme-prod]}]
# A list kept elsewhere (a canvas, a sheet): use a script, see the examples.
`

// accessScriptExamples shows rules whose condition is a Code Mode script.
// The script runs as the rule owner with read-only connector access and
// returns true / false or {allow, reason}. Tool ids are placeholders.
const accessScriptExamples = `# 1. Who may approve: members of one chat channel (approvers: add | only)
name: channel-members-approve
effect: approvers
approvers: add              # add = they may approve too; only = every approver must pass
when:
  ops: [apps.*]
  script: |
    const r = await wick.call("conn:<chat-connector-id>/list_channel_members", { channel: "C01TESTCHAN" });
    const ids = r.members || [];
    return { allow: ids.includes(input.viewer.slack_id), reason: "not a member of the ops channel" };

# 2. Auto-approve when the app code is listed in a document
name: listed-apps-auto
effect: auto_approve
when:
  ops: [apps.*]
  kind: [write]
  script: |
    const doc = await wick.call("conn:<docs-connector-id>/read_canvas", { canvas_id: "F01TESTDOC" });
    const app = input.action.config.app_code || input.action.params.app_code;
    return !!app && String(doc.markdown || "").includes(app);

# input = {action: {connector_id, connector_key, op, kind, params, config, agent, session_id},
#          viewer: {wick_id, name, email, is_admin, slack_id}}   (viewer only for approvers rules)
# A failing or unclear script never allows. Answers are cached 5 minutes per rule revision.
`

// ----- web chat: the inline card -----

// AccessWebPublisher draws a request into its session's thread as a
// kind:"access_request" system turn, re-recorded on every change under the
// same request_id; the conversation view folds them into one card. Every
// session has a thread, so the card shows on the web whatever channel the
// session came from.
type AccessWebPublisher struct{}

// Publish satisfies accessreq.Publisher.
func (AccessWebPublisher) Publish(_ context.Context, v accessreq.View) (map[string]string, error) {
	if v.SessionID == "" || globalLayout == (agentconfig.Layout{}) {
		return nil, nil
	}
	b, err := json.Marshal(accessViewDTO(v))
	if err != nil {
		return nil, err
	}
	text := fmt.Sprintf("%s asks to run %d action(s)", accessAgentLabel(v), len(v.Items))
	emitSystemEvent(v.SessionID, store.KindAccessRequest, text, map[string]string{
		"request_id": v.ID, "state": v.Status, "pending": strconv.Itoa(v.Pending),
		"agent": v.AgentName, "view": string(b), "api": "/tools/agents/access/" + v.ID,
	})
	return nil, nil
}

func accessAgentLabel(v accessreq.View) string {
	if v.AgentName != "" {
		return v.AgentName
	}
	return "The agent"
}

// accessViewDTO is a View as JSON for the web (snake_case, no ids of
// people beyond what the card shows).
func accessViewDTO(v accessreq.View) map[string]any {
	items := make([]map[string]any, 0, len(v.Items))
	for _, it := range v.Items {
		names := make([]string, 0, len(it.Approvers))
		for _, p := range it.Approvers {
			names = append(names, p.Name)
		}
		params := make([]map[string]any, 0, len(it.Params))
		for _, p := range it.Params {
			params = append(params, map[string]any{"name": p.Name, "value": p.Value, "long": p.Long})
		}
		m := map[string]any{
			"id": it.ID, "n": it.Idx + 1, "connector_key": it.ConnectorKey, "connector_label": it.ConnectorLabel,
			"op_key": it.OpKey, "op_name": it.OpName, "kind": it.Kind, "target": it.Target, "status": it.Status,
			"mode": it.Mode, "decided_by": it.DecidedBy, "note": it.Note, "error": it.Error, "result": it.Result,
			"approvers": names, "can_decide": it.CanDecide, "modes": it.Modes, "locked_reason": it.LockedReason,
			"locked_code": it.LockedCode, "params": params, "params_json": it.ParamsJSON,
		}
		if it.Modes == nil {
			m["modes"] = []string{}
		}
		items = append(items, m)
	}
	return map[string]any{
		"id": v.ID, "agent": accessAgentLabel(v), "requester": v.Requester, "reason": v.Reason, "status": v.Status,
		"pending": v.Pending, "items": items, "viewer_can_decide": v.ViewerCanDecide,
		"viewer_reason": v.ViewerReason, "viewer_reason_code": v.ViewerReasonCode,
		"created_at": v.CreatedAt,
	}
}

func accessRequestViewJSON(c *tool.Ctx) {
	if !accessReady(c) {
		return
	}
	u := accessUser(c)
	if u == nil {
		return
	}
	v, err := globalAccess.ViewFor(c.Context(), c.PathValue("rid"), u.ID)
	if err != nil {
		c.JSON(http.StatusNotFound, map[string]string{"error": "access request not found"})
		return
	}
	c.JSON(http.StatusOK, accessViewDTO(v))
}

type accessDecisionsBody struct {
	Decisions []struct {
		ItemID string `json:"item_id"`
		Mode   string `json:"mode"`
	} `json:"decisions"`
	Note    string `json:"note"`
	DenyAll bool   `json:"deny_all"`
}

func accessRequestDecisionsJSON(c *tool.Ctx) {
	if !accessReady(c) || !accessSameOrigin(c) {
		return
	}
	u := accessUser(c)
	if u == nil {
		return
	}
	var body accessDecisionsBody
	if err := c.BindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	id := c.PathValue("rid")
	note := strings.TrimSpace(body.Note)
	var (
		v   accessreq.View
		res []accessreq.DecideResult
		err error
	)
	if body.DenyAll {
		v, res, err = globalAccess.DenyAll(c.Context(), id, u.ID, note)
	} else {
		ds := make([]accessreq.Decision, 0, len(body.Decisions))
		for _, d := range body.Decisions {
			if d.ItemID == "" || d.Mode == "" {
				continue
			}
			dd := accessreq.Decision{ItemID: d.ItemID, Mode: d.Mode}
			if d.Mode == accessreq.ModeDeny {
				dd.Note = note
			}
			ds = append(ds, dd)
		}
		if len(ds) == 0 {
			c.JSON(http.StatusBadRequest, map[string]string{"error": "pick a decision for at least one action"})
			return
		}
		v, res, err = globalAccess.Decide(c.Context(), id, u.ID, ds)
	}
	if err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if res == nil {
		res = []accessreq.DecideResult{}
	}
	c.JSON(http.StatusOK, map[string]any{"view": accessViewDTO(v), "results": res})
}

// ----- project settings: Access requests card -----

func projectAccessJSON(c *tool.Ctx) {
	if !accessReady(c) {
		return
	}
	u := accessUser(c)
	if u == nil {
		return
	}
	pv, err := globalAccess.ProjectAccess(c.Context(), c.PathValue("id"), u.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	reqs := projectAccessHistory(globalLayout, c.PathValue("id"), 50)
	rules := make([]map[string]any, 0, len(pv.Rules))
	for _, r := range pv.Rules {
		rules = append(rules, map[string]any{
			"id": r.ID, "name": r.Name, "effect": r.Effect, "simulate": r.Simulate, "disabled": r.Disabled,
			"rev": r.Rev, "owner": r.OwnerName, "can_edit": r.CanEdit,
			"yaml": r.YAML, "updated_at": r.UpdatedAt,
		})
	}
	grants := make([]map[string]any, 0, len(pv.Grants))
	for _, g := range pv.Grants {
		grants = append(grants, map[string]any{
			"id": g.ID, "connector_id": g.ConnectorID, "op_key": g.OpKey, "target": g.Target,
			"session_id": g.ScopeID, "approver": globalAccess.PersonName(c.Context(), g.ApproverID), "created_at": g.CreatedAt,
		})
	}
	enabled := false
	if p, err := project.Load(globalLayout, c.PathValue("id")); err == nil {
		enabled = p.Meta.AccessRequests
	}
	c.JSON(http.StatusOK, map[string]any{
		"enabled":  enabled,
		"requests": reqs, "rules": rules, "grants": grants, "fact_names": pv.FactNames,
		"rule_example": accessRuleExample, "script_examples": accessScriptExamples,
	})
}

type projectRuleBody struct {
	ID     string `json:"id"`
	Doc    string `json:"doc"`
	Reason string `json:"reason"`
}

func projectAccessRuleSave(c *tool.Ctx) {
	if !accessReady(c) || !accessSameOrigin(c) {
		return
	}
	u := accessUser(c)
	if u == nil {
		return
	}
	var body projectRuleBody
	if err := c.BindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	r, err := globalAccess.SaveProjectRule(c.Context(), u.ID, c.PathValue("id"), body.ID, body.Doc, body.Reason)
	if err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]any{"id": r.ID, "rev": r.Rev})
}

func projectAccessRuleDelete(c *tool.Ctx) {
	if !accessReady(c) || !accessSameOrigin(c) {
		return
	}
	u := accessUser(c)
	if u == nil {
		return
	}
	var body projectRuleBody
	_ = c.BindJSON(&body)
	if err := globalAccess.DeleteProjectRule(c.Context(), u.ID, c.PathValue("id"), c.PathValue("rid"), body.Reason); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]bool{"ok": true})
}

type projectSimulateBody struct {
	Connector string            `json:"connector"`
	Op        string            `json:"op"`
	Agent     string            `json:"agent"`
	Session   string            `json:"session"`
	Params    map[string]string `json:"params"`
	Rule      string            `json:"rule"`
	Viewer    string            `json:"viewer"`
}

func projectAccessSimulate(c *tool.Ctx) {
	if !accessReady(c) || !accessSameOrigin(c) {
		return
	}
	u := accessUser(c)
	if u == nil {
		return
	}
	var body projectSimulateBody
	if err := c.BindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	a := accessreq.Action{SessionID: strings.TrimSpace(body.Session), AgentID: strings.TrimSpace(body.Agent),
		ConnectorID: strings.TrimSpace(body.Connector), OpKey: strings.TrimSpace(body.Op), Params: body.Params}
	res, err := globalAccess.Simulate(c.Context(), a, true)
	if err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	out := map[string]any{"result": res}
	if body.Rule != "" {
		sr, serr := globalAccess.SimulateProjectScript(c.Context(), u.ID, c.PathValue("id"), body.Rule, a, strings.TrimSpace(body.Viewer))
		if serr != nil {
			out["script_error"] = serr.Error()
		} else {
			out["script"] = sr
		}
	}
	c.JSON(http.StatusOK, out)
}

func projectAccessGrantRevoke(c *tool.Ctx) {
	if !accessReady(c) || !accessSameOrigin(c) {
		return
	}
	u := accessUser(c)
	if u == nil {
		return
	}
	if err := globalAccess.RevokeProjectGrant(c.Context(), u.ID, c.PathValue("id"), c.PathValue("gid")); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]bool{"ok": true})
}

// projectAccessSetEnabled turns access requests on or off for a project.
// Off: its sessions behave as before access requests existed.
func projectAccessSetEnabled(c *tool.Ctx) {
	if !accessSameOrigin(c) {
		return
	}
	if accessUser(c) == nil {
		return
	}
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := c.BindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	id := c.PathValue("id")
	p, err := project.Load(globalLayout, id)
	if err != nil {
		c.JSON(http.StatusNotFound, map[string]string{"error": "project not found"})
		return
	}
	p.Meta.AccessRequests = body.Enabled
	if err := project.SaveMeta(globalLayout, id, p.Meta); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]bool{"enabled": body.Enabled})
}
