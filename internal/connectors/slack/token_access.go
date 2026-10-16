package slack

import (
	"fmt"
	"html"
	"strings"

	"github.com/yogasw/wick/pkg/connector"
)

// ── Token & access panel ─────────────────────────────────────────────
//
// token_access backs the `html=token_access` widget on the config page. It
// answers three questions the form alone cannot: who each stored token
// really is (a user token pasted into bot_token still authenticates and
// posts as that person), which token operations actually run with, and
// what each token can do per operation.
//
// Read-only: one auth.test per stored token, nothing is written. Token
// values are never rendered — at most the "xoxb-" / "xoxp-" prefix.

// TokenAccessInput: the manager's html widget always passes the current
// field value as "browser"; it is unused here.
type TokenAccessInput struct {
	Browser string `wick:"desc=Unused."`
}

// Theme tokens. The manager's palette is exposed as "R G B" triplets, so
// every colour goes through rgb(); the dark variants hang off html.dark.
// No Tailwind class is used — Go strings are not scanned, so they would be
// purged from the built CSS.
const (
	taText     = "rgb(var(--color-black-900))"
	taTextDark = "rgb(var(--color-white-100))"
	taMuted    = "rgb(var(--color-black-700))"
	taMutedDk  = "rgb(var(--color-black-600))"
	taBorder   = "rgb(var(--color-white-300))"
	taBorderDk = "rgb(var(--color-navy-600))"
	taPos      = "#27B199"
	taNeg      = "#E5484D"
	taCau      = "#D4A72C"
)

// tokenFields is the order the token cards render in.
var tokenFields = []string{"bot_token", "user_token"}

// tokenProbe is what auth.test told us about one stored token.
type tokenProbe struct {
	Field     string
	Set       bool
	Prefix    string // "xoxb-" / "xoxp-" / "" when unrecognised
	IsBot     bool
	WrongType bool
	Err       string
	User      string
	UserID    string
	Team      string
	BotID     string
	Scopes    map[string]struct{}
}

func (p tokenProbe) usable() bool { return p.Set && p.Err == "" }

func (p tokenProbe) kindLabel() string {
	switch {
	case p.IsBot:
		return "bot"
	default:
		return "user"
	}
}

// identity is "name (U123)" — what a person recognises in Slack.
func (p tokenProbe) identity() string {
	switch {
	case p.User != "" && p.UserID != "":
		return p.User + " (" + p.UserID + ")"
	case p.User != "":
		return p.User
	default:
		return p.UserID
	}
}

// activeTokenField mirrors pickToken: the field operations read for the
// current auth_mode, falling back to the legacy `token` key when that
// field is empty. "" + error when auth_mode is unknown.
func activeTokenField(c *connector.Ctx) (mode, field string, err error) {
	mode = strings.TrimSpace(c.Cfg("auth_mode"))
	if mode == "" {
		mode = "bot_token"
	}
	if mode != "bot_token" && mode != "user_token" {
		return mode, "", fmt.Errorf("unknown auth_mode %q", mode)
	}
	if strings.TrimSpace(c.Cfg(mode)) == "" && strings.TrimSpace(c.Cfg("token")) != "" {
		return mode, "token", nil
	}
	return mode, mode, nil
}

// probeToken runs auth.test with one stored token. The call goes through
// the normal doSlack path on a throwaway Ctx that holds only that token,
// so the request is built exactly like every operation's.
func probeToken(c *connector.Ctx, field string) tokenProbe {
	p := tokenProbe{Field: field}
	token := strings.TrimSpace(c.Cfg(field))
	if token == "" {
		return p
	}
	p.Set = true
	switch kind := trimRotatingPrefix(token); {
	case strings.HasPrefix(kind, "xoxb-"):
		p.Prefix, p.IsBot = "xoxb-", true
	case strings.HasPrefix(kind, "xoxp-"):
		p.Prefix = "xoxp-"
	}

	sub := connector.NewCtx(c.Context(), c.InstanceID(),
		map[string]string{"auth_mode": "bot_token", "bot_token": token}, nil, c.HTTP, nil, nil)
	raw, header, err := slackGetWithHeaders(sub, "auth.test", nil)
	if err != nil {
		// Never echo a token back, even if an upstream error quoted it.
		p.Err = strings.ReplaceAll(err.Error(), token, "[redacted]")
	} else {
		m, _ := raw.(map[string]any)
		p.User, p.UserID = strAt(m, "user"), strAt(m, "user_id")
		p.Team, p.BotID = strAt(m, "team"), strAt(m, "bot_id")
		// auth.test is the authority on what the token is; the prefix is
		// only the fallback when the call failed.
		p.IsBot = p.BotID != ""
		p.Scopes = map[string]struct{}{}
		for _, s := range parseScopeHeader(header.Get("X-OAuth-Scopes")) {
			p.Scopes[s] = struct{}{}
		}
	}
	switch field {
	case "bot_token":
		p.WrongType = !p.IsBot
	case "user_token":
		p.WrongType = p.IsBot || (p.Err != "" && p.Prefix != "xoxp-")
	}
	return p
}

// tokenAccess renders the panel. Errors are part of the markup, never a
// failed op — one bad token must not hide the other's card.
func tokenAccess(c *connector.Ctx) (any, error) {
	mode, active, modeErr := activeTokenField(c)

	fields := append([]string{}, tokenFields...)
	if active == "token" {
		fields = append(fields, "token")
	}
	probes := make(map[string]tokenProbe, len(fields))
	for _, f := range fields {
		probes[f] = probeToken(c, f)
	}

	return map[string]any{"html": renderTokenAccess(c, mode, active, modeErr, fields, probes, c.ConnectedAccounts())}, nil
}

func renderTokenAccess(c *connector.Ctx, mode, active string, modeErr error, fields []string, probes map[string]tokenProbe, accounts []connector.AccountRef) string {
	const p = "wsta" // class prefix: this markup lands inside the manager page
	esc := html.EscapeString
	var b strings.Builder

	fmt.Fprintf(&b, `<style>
.%[1]s-w{color:%[2]s;font-size:13px;line-height:1.45}
html.dark .%[1]s-w{color:%[3]s}
.%[1]s-top{display:flex;align-items:center;margin-bottom:8px}
.%[1]s-re{margin-left:auto;font-size:11px;color:%[4]s;text-decoration:underline;cursor:pointer}
html.dark .%[1]s-re,html.dark .%[1]s-mut,html.dark .%[1]s-acc,html.dark .%[1]s-kv span:nth-child(odd),html.dark .%[1]s-w th{color:%[5]s}
.%[1]s-use{display:flex;gap:8px;align-items:flex-start;padding:9px 11px;border-radius:8px;margin-bottom:10px;border:1px solid %[8]s;background:%[8]s1f}
.%[1]s-use.bad{border-color:%[9]s;background:%[9]s1f}
.%[1]s-toks{display:grid;grid-template-columns:1fr 1fr;gap:8px;margin-bottom:10px}
@media (max-width:900px){.%[1]s-toks{grid-template-columns:1fr}}
.%[1]s-tok{border:1px solid %[6]s;border-radius:8px;padding:9px 11px;font-size:12.5px}
html.dark .%[1]s-tok,html.dark .%[1]s-w td,html.dark .%[1]s-w th{border-color:%[7]s}
.%[1]s-tok.on{outline:2px solid %[8]s;outline-offset:-1px}
.%[1]s-tok h4{margin:0 0 6px;font:600 12px ui-monospace,Menlo,monospace;display:flex;gap:6px;align-items:center;flex-wrap:wrap}
.%[1]s-pill{border-radius:999px;padding:1px 7px;font:700 10px system-ui,sans-serif}
.%[1]s-pos{background:%[8]s24;color:%[8]s}.%[1]s-neg{background:%[9]s21;color:%[9]s}
.%[1]s-cau{background:%[10]s24;color:%[10]s}.%[1]s-pm{background:rgba(139,148,167,.15);color:%[4]s}
.%[1]s-kv{display:grid;grid-template-columns:78px 1fr;gap:2px 8px}
.%[1]s-kv span:nth-child(odd){color:%[4]s}
.%[1]s-warn{margin-top:7px;color:%[9]s;font-size:12px}
.%[1]s-mut{color:%[4]s;font-size:11.5px}
.%[1]s-acc{font-size:12.5px;margin-bottom:10px;color:%[4]s}
.%[1]s-acc b{color:inherit;font-weight:600}
.%[1]s-w table{width:100%%;border-collapse:collapse;font-size:12.5px}
.%[1]s-w th{text-align:left;font-weight:600;color:%[4]s;padding:5px 8px;border-bottom:1px solid %[6]s}
.%[1]s-w td{padding:5px 8px;border-bottom:1px solid %[6]s;vertical-align:top}
.%[1]s-w tr.cat td{font-weight:600;background:rgba(127,127,127,.06)}
.%[1]s-w td.op{font:12px ui-monospace,Menlo,monospace;padding-left:22px}
.%[1]s-miss{color:%[9]s;font-size:11.5px}
.%[1]s-w code{font-size:12px}
</style><div class="%[1]s-w">`, p, taText, taTextDark, taMuted, taMutedDk, taBorder, taBorderDk, taPos, taNeg, taCau)

	fmt.Fprintf(&b, `<div class="%[1]s-top"><a href="#" class="%[1]s-re" data-op="token_access">↻ Recheck</a></div>`, p)

	// In-use banner.
	cur, hasCur := probes[active]
	switch {
	case modeErr != nil:
		fmt.Fprintf(&b, `<div class="%s-use bad">⚠️ <span>%s — operations on this instance fail until auth_mode is bot_token or user_token.</span></div>`, p, esc(modeErr.Error()))
	case !hasCur || !cur.Set:
		fmt.Fprintf(&b, `<div class="%s-use bad">⚠️ <span>auth_mode is <code>%s</code> but <code>%s</code> is not set — operations on this instance fail until it is filled.</span></div>`, p, esc(mode), esc(mode))
	case cur.Err != "":
		fmt.Fprintf(&b, `<div class="%s-use bad">⚠️ <span>The token in use (<code>%s</code>) failed auth.test: %s</span></div>`, p, esc(active), esc(cur.Err))
	case cur.WrongType:
		fmt.Fprintf(&b, `<div class="%s-use bad">⚠️ <span>Operations on this instance run as <b>%s</b> — a <b>%s</b> token, although auth_mode is <code>%s</code>.%s</span></div>`,
			p, esc(cur.identity()), cur.kindLabel(), esc(mode), wrongTypeConsequence(cur))
	default:
		fmt.Fprintf(&b, `<div class="%s-use">✅ <span>Operations on this instance run as <b>%s</b> — a %s token (<code>%s</code>).</span></div>`,
			p, esc(cur.identity()), cur.kindLabel(), esc(active))
	}

	// Token cards.
	fmt.Fprintf(&b, `<div class="%s-toks">`, p)
	for _, f := range fields {
		writeTokenCard(&b, p, probes[f], f == active && modeErr == nil, mode)
	}
	b.WriteString(`</div>`)

	// Personal accounts.
	fmt.Fprintf(&b, `<div class="%s-acc">Personal accounts (OAuth) — used only when a tool_id names <code>@account</code>: `, p)
	if len(accounts) == 0 {
		b.WriteString(`none connected.`)
	} else {
		for i, a := range accounts {
			if i > 0 {
				b.WriteString(", ")
			}
			name := a.DisplayName
			if name == "" {
				name = a.ID
			}
			b.WriteString(`<b>@` + esc(name) + `</b>`)
			if who := c.UserName(a.WickUserID); who != "" {
				b.WriteString(` (wick: ` + esc(who) + `)`)
			}
		}
		b.WriteString(`.`)
	}
	b.WriteString(`</div>`)

	writeAccessTable(&b, p, active, modeErr == nil, fields, probes)

	b.WriteString(`</div>`)
	return b.String()
}

// wrongTypeConsequence spells out what a misplaced token means in practice.
func wrongTypeConsequence(t tokenProbe) string {
	if t.Field == "bot_token" {
		return " Messages sent through this connector appear as that person."
	}
	return " Operations that need a user identity act as the bot instead."
}

func writeTokenCard(b *strings.Builder, p string, t tokenProbe, inUse bool, mode string) {
	esc := html.EscapeString
	cls := p + "-tok"
	if inUse {
		cls += " on"
	}
	fmt.Fprintf(b, `<div class="%s"><h4>%s `, cls, esc(t.Field))
	if inUse {
		fmt.Fprintf(b, `<span class="%[1]s-pill %[1]s-pos">in use</span> `, p)
	} else {
		fmt.Fprintf(b, `<span class="%[1]s-pill %[1]s-pm">not in use</span> `, p)
	}
	switch {
	case !t.Set:
		fmt.Fprintf(b, `<span class="%[1]s-pill %[1]s-pm">not set</span>`, p)
	case t.Err != "":
		fmt.Fprintf(b, `<span class="%[1]s-pill %[1]s-neg">error</span>`, p)
	case t.WrongType:
		fmt.Fprintf(b, `<span class="%[1]s-pill %[1]s-neg">wrong type</span>`, p)
	}
	b.WriteString(`</h4>`)

	if !t.Set {
		fmt.Fprintf(b, `<div class="%s-mut">Not set.</div></div>`, p)
		return
	}

	typ := "unrecognised prefix"
	if t.Prefix != "" {
		typ = t.Prefix
	}
	if t.Err == "" || t.Prefix != "" {
		typ += " (" + t.kindLabel() + " token)"
	}
	fmt.Fprintf(b, `<div class="%s-kv"><span>Type</span><span>%s</span>`, p, esc(typ))
	if t.Err != "" {
		b.WriteString(`</div>`)
		fmt.Fprintf(b, `<div class="%s-warn">auth.test failed: %s</div></div>`, p, esc(t.Err))
		return
	}
	bot := "— (not a bot)"
	if t.BotID != "" {
		bot = t.BotID
	}
	fmt.Fprintf(b, `<span>Identity</span><span>%s</span><span>Bot ID</span><span>%s</span><span>Workspace</span><span>%s</span><span>Scopes</span><span>%d granted</span></div>`,
		esc(t.identity()), esc(bot), esc(firstNonEmpty(t.Team, "—")), len(t.Scopes))

	switch {
	case t.WrongType && t.Field == "bot_token":
		fmt.Fprintf(b, `<div class="%s-warn">This field must hold a Bot User OAuth Token (xoxb-); it currently posts as %s. Replace the value or reset it.</div>`, p, esc(t.identity()))
	case t.WrongType && t.Field == "user_token":
		fmt.Fprintf(b, `<div class="%s-warn">This field must hold a User OAuth Token (xoxp-); it currently acts as the bot %s. Replace the value or reset it.</div>`, p, esc(t.identity()))
	case !inUse && t.Field != "token":
		fmt.Fprintf(b, `<div class="%s-mut" style="margin-top:7px">Used only when auth_mode is %s.</div>`, p, esc(t.Field))
	}
	b.WriteString(`</div>`)
}

// writeAccessTable renders the per-operation ✓/✗ grid, grouped by the
// connector's own categories. The token in use is always the first
// column; the other stored tokens follow. A token that is unset or failed
// auth.test gets no column — its card already says why.
func writeAccessTable(b *strings.Builder, p, active string, activeOK bool, fields []string, probes map[string]tokenProbe) {
	esc := html.EscapeString
	cols := make([]string, 0, len(fields))
	if activeOK && probes[active].usable() {
		cols = append(cols, active)
	}
	for _, f := range fields {
		if f != active && probes[f].usable() {
			cols = append(cols, f)
		}
	}
	if len(cols) == 0 {
		fmt.Fprintf(b, `<div class="%s-mut">No working token to check operations against.</div>`, p)
		return
	}

	b.WriteString(`<table><thead><tr><th>Category / operation</th>`)
	for _, f := range cols {
		label := f
		if f == active && activeOK {
			label = "Token in use (" + f + ")"
		}
		b.WriteString(`<th>` + esc(label) + `</th>`)
	}
	b.WriteString(`</tr></thead><tbody>`)

	span := len(cols)
	for _, cat := range Operations() {
		var ops []connector.Operation
		for _, op := range cat.Ops {
			if op.ConfigOnly {
				continue
			}
			ops = append(ops, op)
		}
		if len(ops) == 0 {
			continue
		}

		type cell struct {
			ok      bool
			missing string
		}
		var ruled []connector.Operation
		var unruled []connector.Operation
		results := map[string][]cell{} // op key → one cell per column
		passed := make([]int, len(cols))
		for _, op := range ops {
			rule, has := opScopes[op.Key]
			if !has {
				unruled = append(unruled, op)
				continue
			}
			ruled = append(ruled, op)
			row := make([]cell, len(cols))
			for i, f := range cols {
				ok, missing := evalScopeRule(rule, probes[f].Scopes)
				row[i] = cell{ok: ok}
				if ok {
					passed[i]++
				} else {
					row[i].missing = formatMissingScopes(missing)
				}
			}
			results[op.Key] = row
		}

		allPass := true
		fmt.Fprintf(b, `<tr class="cat"><td>%s</td>`, esc(cat.Title))
		if len(ruled) == 0 {
			// A category of unjudgeable ops (Custom) says so on its one row.
			notes := make([]string, 0, len(unruled))
			for _, op := range unruled {
				notes = append(notes, esc(op.Key)+" — "+unruledNote(op.Key))
			}
			fmt.Fprintf(b, `<td colspan="%d"><span class="%s-mut">%s</span></td></tr>`, span, p, strings.Join(notes, "; "))
			continue
		}
		for i := range cols {
			mark := "✅"
			switch {
			case passed[i] == 0:
				mark, allPass = "❌", false
			case passed[i] < len(ruled):
				mark, allPass = "⚠️", false
			}
			fmt.Fprintf(b, `<td>%s %d/%d</td>`, mark, passed[i], len(ruled))
		}
		b.WriteString(`</tr>`)

		// A fully passing category stays collapsed to its one row.
		if !allPass {
			for _, op := range ruled {
				fmt.Fprintf(b, `<tr><td class="op">%s</td>`, esc(op.Key))
				for i := range cols {
					c := results[op.Key][i]
					if c.ok {
						b.WriteString(`<td>✅</td>`)
					} else {
						fmt.Fprintf(b, `<td>❌ <span class="%s-miss">%s</span></td>`, p, esc(c.missing))
					}
				}
				b.WriteString(`</tr>`)
			}
		}
		for _, op := range unruled {
			fmt.Fprintf(b, `<tr><td class="op">%s</td><td colspan="%d"><span class="%s-mut">%s</span></td></tr>`, esc(op.Key), span, p, unruledNote(op.Key))
		}
	}
	b.WriteString(`</tbody></table>`)
}

// unruledNote explains an op opScopes deliberately does not judge.
func unruledNote(key string) string {
	if key == "custom_api_call" {
		return "depends on the method called (Slack answers missing_scope)"
	}
	return "no static scope rule"
}
