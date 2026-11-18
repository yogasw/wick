package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/yogasw/wick/internal/accessreq"
	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/store"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/pkg/tool"
)

// Access requests on the web: the same accessreq.View the Slack card
// draws, as a page. Every decision is made by the service against the
// signed-in person's own access; this file only draws and forwards.

var globalAccess *accessreq.Service

// SetAccessRequests wires the access-request service. Call once at boot.
func SetAccessRequests(s *accessreq.Service) { globalAccess = s }

func registerAccessRoutes(r tool.Router) {
	r.GET("/access", accessListPage)
	r.GET("/access/rules", accessRulesPage)
	r.POST("/access/rules", accessRuleSave)
	r.POST("/access/rules/{rid}/delete", accessRuleDelete)
	r.POST("/access/facts", accessFactSave)
	r.POST("/access/facts/delete", accessFactDelete)
	r.POST("/access/simulate", accessSimulate)
	r.POST("/access/grants/{gid}/revoke", accessGrantRevoke)
	r.GET("/access/{rid}/view", accessRequestViewJSON)
	r.POST("/access/{rid}/decisions", accessRequestDecisionsJSON)
	r.GET("/access/{rid}", accessRequestPage)
	r.POST("/access/{rid}/decide", accessRequestDecide)
	r.POST("/access/{rid}/deny", accessRequestDeny)
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

type accessPageVM struct {
	Base    string
	Title   string
	Flash   string
	Error   string
	User    *entity.User
	View    *accessreq.View
	Deny    bool
	Closed  bool
	List    []accessreq.View
	Status  string
	Rules   []accessreq.RuleView
	Facts   []entity.AccessFact
	Grants  []entity.AccessGrant
	FactsOK bool
	Sim     *accessreq.SimulateResult
	SimIn   map[string]string
	Example string
	Facts2  []string
}

func renderAccess(c *tool.Ctx, name string, vm accessPageVM) {
	vm.Base = c.Base()
	vm.Flash = c.Query("ok")
	if vm.Error == "" {
		vm.Error = c.Query("err")
	}
	c.W.Header().Set("Content-Type", "text/html; charset=utf-8")
	c.W.Header().Set("Cache-Control", "no-store")
	if err := accessTemplates.ExecuteTemplate(c.W, name, vm); err != nil {
		http.Error(c.W, err.Error(), http.StatusInternalServerError)
	}
}

func accessRedirect(c *tool.Ctx, path, okMsg, errMsg string) {
	q := url.Values{}
	if okMsg != "" {
		q.Set("ok", okMsg)
	}
	if errMsg != "" {
		q.Set("err", errMsg)
	}
	target := c.Base() + path
	if len(q) > 0 {
		target += "?" + q.Encode()
	}
	c.Redirect(target, http.StatusSeeOther)
}

func accessRequestPage(c *tool.Ctx) {
	if !accessReady(c) {
		return
	}
	u := accessUser(c)
	if u == nil {
		return
	}
	v, err := globalAccess.ViewFor(c.Context(), c.PathValue("rid"), u.ID)
	if err != nil {
		c.NotFound()
		return
	}
	renderAccess(c, "request", accessPageVM{Title: "Access request", User: u, View: &v, Deny: c.Query("deny") == "1", Closed: c.Query("closed") == "1"})
}

func accessRequestDecide(c *tool.Ctx) {
	if !accessReady(c) || !accessSameOrigin(c) {
		return
	}
	u := accessUser(c)
	if u == nil {
		return
	}
	id := c.PathValue("rid")
	if err := c.R.ParseForm(); err != nil {
		accessRedirect(c, "/access/"+id, "", "could not read the form")
		return
	}
	note := strings.TrimSpace(c.R.PostForm.Get("note"))
	var ds []accessreq.Decision
	for k, vals := range c.R.PostForm {
		itemID, ok := strings.CutPrefix(k, "mode_")
		if !ok || len(vals) == 0 || vals[0] == "" {
			continue
		}
		d := accessreq.Decision{ItemID: itemID, Mode: vals[0]}
		if d.Mode == accessreq.ModeDeny {
			d.Note = note
		}
		ds = append(ds, d)
	}
	if len(ds) == 0 {
		accessRedirect(c, "/access/"+id, "", "pick a decision for at least one action")
		return
	}
	_, res, err := globalAccess.Decide(c.Context(), id, u.ID, ds)
	accessDecisionRedirect(c, id, res, err)
}

func accessDecisionRedirect(c *tool.Ctx, id string, res []accessreq.DecideResult, err error) {
	if err != nil {
		accessRedirect(c, "/access/"+id, "", err.Error())
		return
	}
	var failed []string
	okN := 0
	for _, r := range res {
		if r.OK {
			okN++
		} else {
			failed = append(failed, r.Error)
		}
	}
	msg := ""
	if okN > 0 {
		msg = "Saved. Approved actions are running with your access."
	}
	accessRedirect(c, "/access/"+id, msg, strings.Join(failed, " · "))
}

func accessRequestDeny(c *tool.Ctx) {
	if !accessReady(c) || !accessSameOrigin(c) {
		return
	}
	u := accessUser(c)
	if u == nil {
		return
	}
	id := c.PathValue("rid")
	_, res, err := globalAccess.DenyAll(c.Context(), id, u.ID, strings.TrimSpace(c.Form("note")))
	accessDecisionRedirect(c, id, res, err)
}

func accessListPage(c *tool.Ctx) {
	if !accessReady(c) {
		return
	}
	u := accessUser(c)
	if u == nil {
		return
	}
	status := c.Query("status")
	if status == "" {
		status = accessreq.StatusPending
	}
	if status == "all" {
		status = ""
	}
	list, err := globalAccess.List(c.Context(), status, 100)
	vm := accessPageVM{Title: "Access requests", User: u, Status: c.Query("status")}
	if err != nil {
		vm.Error = err.Error()
	}
	// Only requests the viewer has a part in: their own sessions, or
	// ones they may approve. Admins see everything.
	for _, v := range list {
		if u.IsAdmin() || accessInvolves(v, u.ID) {
			vm.List = append(vm.List, v)
		}
	}
	renderAccess(c, "list", vm)
}

func accessInvolves(v accessreq.View, userID string) bool {
	for _, it := range v.Items {
		for _, p := range it.Approvers {
			if p.ID == userID {
				return true
			}
		}
	}
	return false
}

// accessRuleExample is the new-rule template. The usual pair: staging
// apps may be written without a card, production apps never through this
// path (a second rule, effect deny, same ops, app.env equals production).
const accessRuleExample = `name: staging-apps-auto
effect: auto_approve        # or: require / deny
simulate: true              # log "would auto-approve" first; set false to act
when:
  ops: [apps.*, bots.*]
  kind: [read, write]       # delete is never auto-approved
  agents: [captain]         # optional: agent id or name
  all:
    - fact: app.env
      equals: staging
# Pair it with a deny rule for production:
#   name: prod-apps-never, effect: deny, same ops,
#   all: [{fact: app.env, equals: production}]
# app.env comes from the Facts table below (app code -> staging/production).
`

func accessRulesPage(c *tool.Ctx) {
	if !accessReady(c) {
		return
	}
	u := accessUser(c)
	if u == nil {
		return
	}
	vm := accessPageVM{Title: "Access rules", User: u, Example: accessRuleExample, Facts2: accessreq.FactNames(), FactsOK: u.IsAdmin()}
	var err error
	if vm.Rules, err = globalAccess.Rules(c.Context(), u.ID); err != nil {
		vm.Error = err.Error()
	}
	vm.Facts, _ = globalAccess.Facts(c.Context())
	grants, _ := globalAccess.Grants(c.Context(), "", "")
	for _, g := range grants {
		if u.IsAdmin() || g.ApproverID == u.ID {
			vm.Grants = append(vm.Grants, g)
		}
	}
	renderAccess(c, "rules", vm)
}

func accessRuleSave(c *tool.Ctx) {
	if !accessReady(c) || !accessSameOrigin(c) {
		return
	}
	u := accessUser(c)
	if u == nil {
		return
	}
	_, err := globalAccess.SaveRule(c.Context(), u.ID, strings.TrimSpace(c.Form("id")), c.Form("doc"), c.Form("reason"))
	if err != nil {
		accessRedirect(c, "/access/rules", "", err.Error())
		return
	}
	accessRedirect(c, "/access/rules", "Rule saved.", "")
}

func accessRuleDelete(c *tool.Ctx) {
	if !accessReady(c) || !accessSameOrigin(c) {
		return
	}
	u := accessUser(c)
	if u == nil {
		return
	}
	if err := globalAccess.DeleteRule(c.Context(), u.ID, c.PathValue("rid"), c.Form("reason")); err != nil {
		accessRedirect(c, "/access/rules", "", err.Error())
		return
	}
	accessRedirect(c, "/access/rules", "Rule deleted.", "")
}

func accessFactSave(c *tool.Ctx) {
	if !accessReady(c) || !accessSameOrigin(c) {
		return
	}
	u := accessUser(c)
	if u == nil {
		return
	}
	if err := globalAccess.SetFact(c.Context(), u.ID, c.Form("fact"), c.Form("subject"), c.Form("value")); err != nil {
		accessRedirect(c, "/access/rules", "", err.Error())
		return
	}
	accessRedirect(c, "/access/rules", "Fact saved.", "")
}

func accessFactDelete(c *tool.Ctx) {
	if !accessReady(c) || !accessSameOrigin(c) {
		return
	}
	u := accessUser(c)
	if u == nil {
		return
	}
	if err := globalAccess.DeleteFact(c.Context(), u.ID, c.Form("fact"), c.Form("subject")); err != nil {
		accessRedirect(c, "/access/rules", "", err.Error())
		return
	}
	accessRedirect(c, "/access/rules", "Fact deleted.", "")
}

func accessSimulate(c *tool.Ctx) {
	if !accessReady(c) || !accessSameOrigin(c) {
		return
	}
	u := accessUser(c)
	if u == nil {
		return
	}
	in := map[string]string{
		"connector": strings.TrimSpace(c.Form("connector")), "op": strings.TrimSpace(c.Form("op")),
		"agent": strings.TrimSpace(c.Form("agent")), "params": c.Form("params"), "session": strings.TrimSpace(c.Form("session")),
	}
	params := map[string]string{}
	for _, line := range strings.Split(in["params"], "\n") {
		k, v, ok := strings.Cut(line, "=")
		if ok && strings.TrimSpace(k) != "" {
			params[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	a := accessreq.Action{SessionID: in["session"], AgentID: in["agent"], ConnectorID: in["connector"], OpKey: in["op"], Params: params}
	res, err := globalAccess.Simulate(c.Context(), a, true)
	vm := accessPageVM{Title: "Access rules", User: u, Example: accessRuleExample, Facts2: accessreq.FactNames(), FactsOK: u.IsAdmin(), Sim: &res, SimIn: in}
	if err != nil {
		vm.Error = err.Error()
	}
	vm.Rules, _ = globalAccess.Rules(c.Context(), u.ID)
	vm.Facts, _ = globalAccess.Facts(c.Context())
	renderAccess(c, "rules", vm)
}

func accessGrantRevoke(c *tool.Ctx) {
	if !accessReady(c) || !accessSameOrigin(c) {
		return
	}
	u := accessUser(c)
	if u == nil {
		return
	}
	if err := globalAccess.RevokeGrant(c.Context(), c.PathValue("gid"), u.ID); err != nil {
		accessRedirect(c, "/access/rules", "", err.Error())
		return
	}
	accessRedirect(c, "/access/rules", "Grant revoked.", "")
}

var accessFuncs = template.FuncMap{
	"modeLabel": accessreq.ModeLabel,
	"icon": func(status string) string {
		switch status {
		case accessreq.ItemOK:
			return "✅"
		case accessreq.ItemError:
			return "❌"
		case accessreq.ItemDenied:
			return "⛔"
		case accessreq.ItemRunning:
			return "🔄"
		}
		return "⏳"
	},
	"names": func(ps []accessreq.Person) string {
		if len(ps) == 0 {
			return "nobody yet"
		}
		out := make([]string, 0, len(ps))
		for _, p := range ps {
			out = append(out, p.Name)
		}
		return strings.Join(out, ", ")
	},
	"short": func(id string) string {
		if len(id) > 8 {
			return id[:8]
		}
		return id
	},
	"add1": func(i int) int { return i + 1 },
	"agent": func(v accessreq.View) string {
		if v.AgentName != "" {
			return v.AgentName
		}
		return "The agent"
	},
	"hint": func(kind string) string {
		switch kind {
		case accessreq.KindSend:
			return ""
		case accessreq.KindDelete:
			return "Deletes: only \"Once\" is offered."
		}
		return ""
	},
}

var accessTemplates = template.Must(template.New("access").Funcs(accessFuncs).Parse(accessHTML))

const accessHTML = `
{{define "head"}}<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{.Title}} · wick</title>
<style>
:root{--bg:#f6f8f7;--surface:#fff;--fg:#14212b;--muted:#5b6b76;--border:#dbe3e0;--accent:#16a34a;--accent-fg:#fff;--danger:#dc2626;--warn:#b45309;--code:#eef2f0}
@media (prefers-color-scheme:dark){:root{--bg:#0b141b;--surface:#121e27;--fg:#e6edf0;--muted:#93a4ae;--border:#24333d;--accent:#22c55e;--accent-fg:#06240f;--danger:#f87171;--warn:#fbbf24;--code:#0d1820}}
*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--fg);font:14px/1.5 Inter,system-ui,-apple-system,Segoe UI,sans-serif}
a{color:var(--accent)}main{max-width:880px;margin:0 auto;padding:24px 16px 64px}
header{display:flex;gap:16px;align-items:center;justify-content:space-between;margin-bottom:16px;flex-wrap:wrap}
nav a{margin-right:16px;text-decoration:none;color:var(--muted)}nav a.on{color:var(--fg);font-weight:600}
h1{font-size:20px;margin:0}h2{font-size:16px;margin:24px 0 8px}
.card{background:var(--surface);border:1px solid var(--border);border-radius:12px;padding:16px;margin:12px 0}
.muted{color:var(--muted)}.small{font-size:12px}.row{display:flex;gap:8px;align-items:center;flex-wrap:wrap}
.claim{border-left:3px solid var(--warn);padding:8px 12px;background:var(--code);border-radius:8px;margin:8px 0}
.locked{border-left:3px solid var(--danger);padding:8px 12px;background:var(--code);border-radius:8px;margin-top:8px}
.flash{border:1px solid var(--accent);padding:8px 12px;border-radius:8px;margin:8px 0}.err{border-color:var(--danger);color:var(--danger)}
code,pre{background:var(--code);border-radius:6px;font:12px/1.5 ui-monospace,SFMono-Regular,Menlo,monospace}code{padding:1px 6px}pre{padding:8px 12px;overflow:auto;max-height:320px;white-space:pre-wrap;word-break:break-word}
table{width:100%;border-collapse:collapse}td,th{padding:6px 8px;border-bottom:1px solid var(--border);text-align:left;vertical-align:top}
.modes label{display:inline-flex;gap:6px;align-items:center;margin:4px 16px 4px 0;cursor:pointer}
button,.btn{font:inherit;border-radius:8px;padding:8px 16px;border:1px solid var(--border);background:var(--surface);color:var(--fg);cursor:pointer;text-decoration:none;display:inline-block}
button.primary{background:var(--accent);border-color:var(--accent);color:var(--accent-fg);font-weight:600}button.danger{border-color:var(--danger);color:var(--danger)}
textarea,input[type=text]{width:100%;font:inherit;background:var(--bg);color:var(--fg);border:1px solid var(--border);border-radius:8px;padding:8px}
textarea.code{font:12px/1.5 ui-monospace,Menlo,monospace;min-height:220px}
.badge{font-size:12px;border:1px solid var(--border);border-radius:999px;padding:1px 8px;color:var(--muted)}
.overlay{position:fixed;inset:0;background:rgba(0,0,0,.5);display:flex;align-items:center;justify-content:center;padding:16px}
.overlay .card{max-width:560px;width:100%}
</style></head><body><main>
<header><h1>{{.Title}}</h1><nav><a href="{{.Base}}/access">Requests</a><a href="{{.Base}}/access/rules">Rules &amp; grants</a></nav></header>
{{if .Flash}}<div class="flash">{{.Flash}}</div>{{end}}{{if .Error}}<div class="flash err">{{.Error}}</div>{{end}}
{{end}}

{{define "foot"}}</main></body></html>{{end}}

{{define "request"}}{{template "head" .}}{{with .View}}
<div class="card">
  <div class="row"><strong>🔐 {{agent .}} asks to run {{len .Items}} action(s)</strong><span class="badge">{{.Status}}</span><span class="badge">request {{short .ID}}</span></div>
  {{if .Requester}}<div class="muted small">Asked in a conversation of {{.Requester}} · {{.CreatedAt.Format "02 Jan 15:04"}}</div>{{end}}
  {{if .Reason}}<div class="claim"><div class="small muted">Agent's claim (not verified)</div>{{.Reason}}</div>{{end}}
</div>
{{if and (not .ViewerCanDecide) .ViewerReason (ne .ViewerReasonCode "already_decided") (ne .ViewerReasonCode "inactive") (not $.Closed)}}
<div class="overlay"><div class="card"><strong>⛔ You do not have access</strong>
<p>{{.ViewerReason}}</p>
<ul>{{range .Items}}<li>{{icon .Status}} <code>{{.ConnectorKey}}.{{.OpKey}}</code> {{.Target}}{{if eq .Status "pending"}}<br><span class="small muted">Who can: {{names .Approvers}}</span>{{end}}</li>{{end}}</ul>
<a class="btn" href="?closed=1#actions">See details</a></div></div>
{{end}}
{{if and $.Deny .ViewerCanDecide}}
<div class="overlay"><form class="card" method="post" action="{{$.Base}}/access/{{.ID}}/deny"><strong>Deny these actions?</strong>
<ul>{{range .Items}}{{if .CanDecide}}<li><code>{{.ConnectorKey}}.{{.OpKey}}</code> {{.Target}}</li>{{end}}{{end}}</ul>
<p class="small muted">Actions outside your access stay waiting for another approver.</p>
<label class="small">Reason (sent to the agent)<textarea name="note" rows="3"></textarea></label>
<div class="row" style="margin-top:12px"><button class="danger" type="submit">Deny</button><a class="btn" href="{{$.Base}}/access/{{.ID}}">Cancel</a></div></form></div>
{{end}}
<form method="post" action="{{$.Base}}/access/{{.ID}}/decide" id="actions">
{{range .Items}}
<div class="card">
  <div class="row"><span>{{icon .Status}}</span><strong>{{add1 .Idx}}. <code>{{.ConnectorKey}}.{{.OpKey}}</code></strong><span class="muted">{{if .ConnectorLabel}}{{.ConnectorLabel}}{{end}}</span><span class="badge">{{.Kind}}</span></div>
  {{if .Target}}<div style="margin-top:6px"><span class="muted">Target:</span> {{.Target}}</div>{{end}}
  {{if .CanDecide}}<div><span class="muted">Runs as:</span> your own account ✅</div>{{end}}
  <details style="margin-top:8px"><summary class="small muted">Parameters (secrets masked)</summary><pre>{{.ParamsJSON}}</pre></details>
  {{if .Note}}<div class="small muted" style="margin-top:6px">ℹ️ {{.Note}}</div>{{end}}
  {{if .CanDecide}}
    <div class="modes" style="margin-top:8px">{{$id := .ID}}{{range .Modes}}<label><input type="radio" name="mode_{{$id}}" value="{{.}}"> {{modeLabel .}}</label>{{end}}<label><input type="radio" name="mode_{{$id}}" value="deny"> Deny</label></div>
    {{with hint .Kind}}<div class="small muted">{{.}}</div>{{end}}
  {{else if eq .Status "pending"}}
    <div class="locked small">⛔ {{.LockedReason}}</div>
  {{else}}
    <div class="small" style="margin-top:8px">{{.LockedReason}}{{if .DecidedBy}} {{end}}{{if .Error}}<pre>{{.Error}}</pre>{{end}}{{if .Result}}<details><summary class="small muted">Result</summary><pre>{{.Result}}</pre></details>{{end}}</div>
  {{end}}
</div>
{{end}}
{{if .ViewerCanDecide}}
<div class="card"><label class="small">Note for the agent (sent with a deny)<textarea name="note" rows="2"></textarea></label>
<p class="small muted">Leave an action unselected to keep it waiting. Approved actions run right away with your access. "This session" covers the same operation on the same target only; standing access is set with rules.</p>
<div class="row"><button class="primary" type="submit">Save decisions</button><a class="btn" href="{{$.Base}}/access/{{.ID}}?deny=1">Deny…</a></div></div>
{{end}}
</form>
{{end}}{{template "foot" .}}{{end}}

{{define "list"}}{{template "head" .}}
<div class="row"><a class="btn" href="?status=pending">Pending</a><a class="btn" href="?status=done">Done</a><a class="btn" href="?status=all">All</a></div>
{{if not .List}}<div class="card muted">No requests here.</div>{{end}}
{{range .List}}<div class="card"><div class="row"><a href="{{$.Base}}/access/{{.ID}}"><strong>🔐 {{agent .}} · {{len .Items}} action(s)</strong></a><span class="badge">{{.Status}}</span><span class="muted small">{{.CreatedAt.Format "02 Jan 15:04"}}</span>{{if .Pending}}<span class="badge">{{.Pending}} waiting</span>{{end}}</div>
<div class="small">{{range .Items}}{{icon .Status}} <code>{{.ConnectorKey}}.{{.OpKey}}</code> {{.Target}}<br>{{end}}</div></div>{{end}}
{{template "foot" .}}{{end}}

{{define "rules"}}{{template "head" .}}
<p class="muted">Rules decide an action an agent cannot run on its own before a card is shown: <b>deny</b> beats <b>require</b> beats <b>auto_approve</b>. An auto-approved action runs with the rule owner's access, and only when the owner can run it themselves. A fact that cannot be determined never auto-approves. Your rules cover only your own sessions and agents; an admin's cover everyone.</p>
<h2>Rules</h2>
{{range .Rules}}<div class="card"><div class="row"><strong>{{.Name}}</strong><span class="badge">{{.Effect}}</span>{{if .Simulate}}<span class="badge">simulate</span>{{end}}{{if .Disabled}}<span class="badge">disabled</span>{{end}}<span class="muted small">owner {{.OwnerName}} · rev {{.Rev}}</span></div>
{{if .CanEdit}}<form method="post" action="{{$.Base}}/access/rules"><input type="hidden" name="id" value="{{.ID}}"><textarea class="code" name="doc">{{.YAML}}</textarea>
<input type="text" name="reason" placeholder="Why (kept in history)" style="margin-top:8px"><div class="row" style="margin-top:8px"><button class="primary" type="submit">Save</button></div></form>
<form method="post" action="{{$.Base}}/access/rules/{{.ID}}/delete" style="margin-top:8px"><button class="danger" type="submit">Delete</button></form>
{{else}}<pre>{{.YAML}}</pre>{{end}}</div>{{end}}
<div class="card"><strong>New rule</strong><form method="post" action="{{.Base}}/access/rules"><textarea class="code" name="doc">{{.Example}}</textarea>
<input type="text" name="reason" placeholder="Why (kept in history)" style="margin-top:8px"><div class="row" style="margin-top:8px"><button class="primary" type="submit">Create</button></div></form>
<p class="small muted">Facts: {{range .Facts2}}<code>{{.}}</code> {{end}}</p></div>

<h2>Simulate</h2>
<form class="card" method="post" action="{{.Base}}/access/simulate">
<div class="row"><input type="text" name="connector" placeholder="connector key or instance id" value="{{index .SimIn "connector"}}"><input type="text" name="op" placeholder="op key, e.g. update_page" value="{{index .SimIn "op"}}"><input type="text" name="agent" placeholder="agent id (optional)" value="{{index .SimIn "agent"}}"><input type="text" name="session" placeholder="session id (optional)" value="{{index .SimIn "session"}}"></div>
<textarea name="params" rows="3" placeholder="params, one key=value per line" style="margin-top:8px">{{index .SimIn "params"}}</textarea>
<div class="row" style="margin-top:8px"><button type="submit">Simulate</button>{{with .Sim}}<span>→ <b>{{.Effect}}</b>{{if .Rule}} by rule <b>{{.Rule}}</b>{{end}} · kind {{.Kind}}{{if .Simulated}} · would auto-approve (simulate): {{range .Simulated}}{{.}} {{end}}{{end}}</span>{{end}}</div></form>

<h2>Facts</h2>
<div class="card"><table><tr><th>Fact</th><th>Subject</th><th>Value</th><th></th></tr>
{{range .Facts}}<tr><td><code>{{.Fact}}</code></td><td>{{.Subject}}</td><td>{{.Value}}</td><td>{{if $.FactsOK}}<form method="post" action="{{$.Base}}/access/facts/delete"><input type="hidden" name="fact" value="{{.Fact}}"><input type="hidden" name="subject" value="{{.Subject}}"><button type="submit">Delete</button></form>{{end}}</td></tr>{{end}}
</table>
{{if .FactsOK}}<form method="post" action="{{.Base}}/access/facts" class="row" style="margin-top:8px"><input type="text" name="fact" placeholder="app.env" style="max-width:160px"><input type="text" name="subject" placeholder="app code / connector id" style="max-width:240px"><input type="text" name="value" placeholder="staging" style="max-width:160px"><button type="submit">Save fact</button></form>
{{else}}<p class="small muted">Only an admin can change facts.</p>{{end}}</div>

<h2>Standing grants</h2>
<div class="card">{{if not .Grants}}<span class="muted">None.</span>{{else}}<table><tr><th>Operation</th><th>Target</th><th>Scope</th><th>Since</th><th></th></tr>
{{range .Grants}}<tr><td><code>{{.ConnectorID}}</code> · <code>{{.OpKey}}</code></td><td class="small">{{.Target}}</td><td>{{.ScopeType}} {{short .ScopeID}}</td><td>{{.CreatedAt.Format "02 Jan 15:04"}}</td><td><form method="post" action="{{$.Base}}/access/grants/{{.ID}}/revoke"><button class="danger" type="submit">Revoke</button></form></td></tr>{{end}}
</table>{{end}}</div>
{{template "foot" .}}{{end}}
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
		"pending": v.Pending, "url": v.URL, "items": items, "viewer_can_decide": v.ViewerCanDecide,
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
