package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"strings"

	"github.com/yogasw/wick/pkg/connector"
)

// getPolicy exposes the effective configuration without mutating it.
func getPolicy(c *connector.Ctx) (any, error) { return policyFor(c) }

// validatePolicy is a dry run: proposed JSON never grants permissions.
func validatePolicy(c *connector.Ctx) (any, error) {
	p, err := loadPolicy(c.Input("policy_json"))
	if err != nil {
		return map[string]any{"valid": false, "error": err.Error()}, nil
	}
	return map[string]any{"valid": true, "policy": p}, nil
}

// checkPolicy uses the same verdict engine as execution; it does not touch files or run commands.
func checkPolicy(c *connector.Ctx) (any, error) {
	p, err := policyFor(c)
	if err != nil {
		return nil, err
	}
	op := c.Input("operation")
	var verdict, reason string
	if op == "exec" || op == "exec_shell" {
		args, e := parseArgs(c.Input("args_json"))
		if e != nil {
			return nil, e
		}
		verdict, reason = p.commandVerdict(c.Input("command"), args, op == "exec_shell")
	} else {
		n, e := absolutePath(c.Input("path"))
		if e != nil {
			return nil, e
		}
		verdict, reason = p.fileVerdict(op, n)
	}
	return map[string]any{"verdict": verdict, "reason": reason, "executed": false}, nil
}

// policyEditorView keeps the submitted draft separate from the persisted
// policy. Preview and formatting must never return config writes.
type policyEditorView struct {
	Raw     string
	Policy  Policy
	Valid   bool
	Notice  string
	Problem string
	Tab     string
}

// overview is available only through the manager's config UI. The existing
// fields bridge persists the hidden policy_json key with Wick's normal
// authorization and validation; no agent-facing write operation is added.
func overview(c *connector.Ctx) (any, error) {
	saved := c.Cfg("policy_json")
	if strings.TrimSpace(saved) == "" {
		saved = defaultPolicy
	}
	return policyEditor(saved, c.Input("raw_json"), c.Input("browser"))
}

// policyEditor processes a single user action. Keep the draft's original
// paths and optional fields when formatting, rather than serializing the
// normalized runtime Policy, which would expand home aliases.
func policyEditor(saved, draft, action string) (map[string]any, error) {
	v := policyEditorView{Raw: draft, Tab: "raw"}
	switch action {
	case "raw", "preview", "format", "apply":
		if action == "preview" {
			v.Tab = "overview"
		}
	case "defaults":
		v.Raw = defaultPolicy
		v.Notice = "Default-deny draft loaded. Apply to save."
	case "reset":
		v.Raw = saved
		v.Notice = "Draft reloaded from stored policy."
	default:
		v.Raw = saved
		v.Tab = "overview"
	}

	if len(v.Raw) > 128*1024 {
		return nil, fmt.Errorf("policy draft exceeds 128 KiB")
	}
	p, err := loadPolicy(v.Raw)
	if strings.TrimSpace(v.Raw) == "" || strings.TrimSpace(v.Raw) == "{}" {
		err = fmt.Errorf("enter a complete versioned policy object")
	}
	if err != nil {
		v.Problem = err.Error()
		v.Tab = "raw"
		return renderPolicyResponse(v), nil
	}
	v.Policy, v.Valid = p, true

	if action == "format" || action == "apply" || action == "defaults" || action == "reset" || action == "" {
		var buf bytes.Buffer
		if err := json.Indent(&buf, []byte(strings.TrimSpace(v.Raw)), "", "  "); err != nil {
			return nil, fmt.Errorf("format policy: %w", err)
		}
		v.Raw = buf.String()
	}
	switch action {
	case "preview":
		v.Notice = "Valid draft preview. No changes saved."
	case "format":
		v.Notice = "JSON formatted. No changes saved."
	case "apply":
		// The manager writes fields after this response. Do not claim the
		// database commit succeeded before the manager confirms it.
		v.Notice = "Policy validated and submitted. Reload stored policy to verify."
		resp := renderPolicyResponse(v)
		resp["fields"] = map[string]string{"policy_json": v.Raw}
		return resp, nil
	}
	return renderPolicyResponse(v), nil
}

func renderPolicyResponse(v policyEditorView) map[string]any {
	return map[string]any{"html": renderPolicyEditor(v)}
}

// renderPolicyEditor uses the same generic HTML bridge as Git Policy Manager.
// A changed textarea ID intentionally replaces its DOM node after formatting
// or resetting; the core otherwise preserves the operator's live value.
func renderPolicyEditor(v policyEditorView) string {
	var b strings.Builder
	b.WriteString(policyEditorCSS)
	b.WriteString(`<section class="ha-policy"><div class="ha-head"><div><strong>Host Access Policy</strong><div class="ha-muted">One JSON policy for filesystem, terminal and system permissions.</div></div></div>`)
	if v.Problem != "" {
		fmt.Fprintf(&b, `<p class="ha-error" role="alert">%s</p>`, html.EscapeString(v.Problem))
	} else if v.Notice != "" {
		fmt.Fprintf(&b, `<p class="ha-notice" role="status">%s</p>`, html.EscapeString(v.Notice))
	}
	b.WriteString(`<div class="ha-actions">`)
	writePolicyButton(&b, "preview", "Overview", v.Tab == "overview")
	writePolicyButton(&b, "raw", "Raw JSON", v.Tab == "raw")
	b.WriteString(`</div>`)
	if v.Tab == "overview" && v.Valid {
		renderPolicySummary(&b, v.Policy)
	}
	hidden := ""
	if v.Tab == "overview" {
		hidden = " hidden"
	}
	fmt.Fprintf(&b, `<div%s><label for="ha-raw-%s">Policy JSON</label><textarea id="ha-raw-%s" name="raw_json" rows="18" spellcheck="false">%s</textarea></div>`,
		hidden, digest([]byte(v.Raw)), digest([]byte(v.Raw)), html.EscapeString(v.Raw))
	b.WriteString(`<div class="ha-actions">`)
	writePolicyButton(&b, "format", "Format JSON", false)
	writePolicyButton(&b, "preview", "Validate / Preview", false)
	writePolicyButton(&b, "apply", "Validate & Apply", true)
	writePolicyButton(&b, "reset", "Reload stored", false)
	writePolicyButton(&b, "defaults", "Default-deny draft", false)
	b.WriteString(`</div><p class="ha-muted">Only Apply changes permissions. Terminal trust is not an OS sandbox. Keep configuration access restricted to trusted administrators.</p></section>`)
	return b.String()
}

func writePolicyButton(b *strings.Builder, action, label string, primary bool) {
	class := "ha-btn"
	if primary {
		class += " ha-primary"
	}
	fmt.Fprintf(b, `<button type="button" class="%s" data-op="policy_overview" data-arg="%s">%s</button>`, class, action, html.EscapeString(label))
}

// renderPolicySummary presents effective rules using the existing policy
// parser, rather than duplicating permission logic in browser JavaScript.
func renderPolicySummary(b *strings.Builder, p Policy) {
	b.WriteString(`<div class="ha-summary"><h4>Filesystem</h4>`)
	writePolicyRow(b, "Default", p.Filesystem.Default)
	for _, r := range p.Filesystem.Rules {
		fmt.Fprintf(b, `<div class="ha-rule"><strong>%s</strong>`, html.EscapeString(r.Path))
		recursive := r.Recursive == nil || *r.Recursive
		writePolicyRow(b, "Recursive", fmt.Sprint(recursive))
		writePolicyRow(b, "Allow", strings.Join(r.Allow, ", "))
		writePolicyRow(b, "Deny", strings.Join(r.Deny, ", "))
		writePolicyRow(b, "Approval", strings.Join(r.Approval, ", "))
		b.WriteString(`</div>`)
	}
	writePolicyRow(b, "Protected patterns", strings.Join(p.Filesystem.ProtectedPatterns, ", "))
	b.WriteString(`<h4>Terminal</h4>`)
	writePolicyRow(b, "Mode", p.Terminal.Mode)
	writePolicyRow(b, "Trusted host", fmt.Sprint(p.Terminal.TrustHost))
	writePolicyRow(b, "Shell", fmt.Sprint(p.Terminal.AllowShell))
	writePolicyRow(b, "Allow", strings.Join(p.Terminal.Allow, ", "))
	writePolicyRow(b, "Deny", strings.Join(p.Terminal.Deny, ", "))
	writePolicyRow(b, "Approval", strings.Join(p.Terminal.Approval, ", "))
	writePolicyRow(b, "Timeout (seconds)", fmt.Sprint(p.Terminal.TimeoutSeconds))
	b.WriteString(`<h4>System</h4>`)
	writePolicyRow(b, "Allow", strings.Join(p.System.Allow, ", "))
	b.WriteString(`</div>`)
}

func writePolicyRow(b *strings.Builder, label, value string) {
	if value == "" {
		value = "None"
	}
	fmt.Fprintf(b, `<div class="ha-row"><span>%s</span><code>%s</code></div>`,
		html.EscapeString(label), html.EscapeString(value))
}

// Runtime HTML is not scanned by Tailwind. Scope all CSS to this widget and
// use Wick's theme variables so every theme keeps the same contrast.
const policyEditorCSS = `<style>
.ha-policy{color:var(--color-black-900);background:var(--color-white-100);border:1px solid var(--color-navy-200);border-radius:10px;padding:14px}
.ha-policy .ha-head{margin-bottom:12px}
.ha-policy .ha-muted{font-size:12px;opacity:.75;margin:8px 0}
.ha-policy .ha-actions{display:flex;flex-wrap:wrap;gap:8px;margin:12px 0}
.ha-policy .ha-btn{border:1px solid var(--color-navy-200);background:var(--color-white-200);color:inherit;padding:7px 11px;border-radius:6px;cursor:pointer}
.ha-policy .ha-primary{background:#27B199;color:#102822;border-color:#27B199;font-weight:600}
.ha-policy .ha-btn:disabled{opacity:.5;cursor:wait}
.ha-policy .ha-btn:focus-visible,.ha-policy textarea:focus-visible{outline:2px solid #27B199;outline-offset:2px}
.ha-policy textarea{display:block;box-sizing:border-box;width:100%;resize:vertical;background:var(--color-white-200);color:inherit;border:1px solid var(--color-navy-200);border-radius:6px;padding:12px;font:12px/1.6 monospace}
.ha-policy .ha-summary h4{font-weight:600;margin:14px 0 7px}
.ha-policy .ha-rule{border:1px solid var(--color-navy-200);padding:10px;border-radius:6px;margin:6px 0;overflow-wrap:anywhere}
.ha-policy .ha-row{display:grid;grid-template-columns:minmax(90px,35%) 1fr;gap:8px;padding:4px 0;font-size:12px}
.ha-policy .ha-row code{white-space:pre-wrap;overflow-wrap:anywhere}
.ha-policy .ha-error{color:var(--color-red-500,#E5484D);overflow-wrap:anywhere}
.ha-policy .ha-notice{font-size:12px;overflow-wrap:anywhere}
</style>`
