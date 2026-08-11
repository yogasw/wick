// setupui.go renders the credential setup guide shown inside the connector's own
// config page.
//
// The guide lives here rather than in the docs site because that is where the
// question actually gets asked: someone is looking at an empty Username field and
// needs to know what belongs in it. The answer differs per host in ways that are
// easy to get wrong — GitHub ignores the username, Bitbucket Cloud does not and
// rejects an email address — so a static description cannot say it without saying
// four things at once.
//
// Shape: every host's guide is rendered once and CSS reveals the picked one, so
// switching hosts costs no request and nothing flashes. See renderSetupGuide for
// why the interaction is a radio group rather than JavaScript.
//
// Styling follows the same rule as policyui.go — theme CSS variables, never
// Tailwind classes, because the manager's Tailwind build does not scan markup a
// connector returns at runtime. Class names here are all prefixed and every rule
// is namespaced under that prefix, since this markup renders inside the manager's
// own page and must not leak styles into it.
package main

import (
	"fmt"
	"strings"

	"github.com/yogasw/wick/pkg/connector"
)

// Brand marks, as inline SVG path data on a 24×24 viewBox. Inline because the
// manager's CSP blocks external assets — a linked logo would render as nothing at
// all. They are drawn with currentColor so one path serves both themes and picks
// up the selected state for free.
const (
	iconGitHub    = "M12 .3a12 12 0 0 0-3.8 23.4c.6.1.8-.3.8-.7v-2.6c-3.3.7-4-1.6-4-1.6-.6-1.4-1.4-1.8-1.4-1.8-1-.7 0-.7 0-.7 1.2.1 1.9 1.2 1.9 1.2 1 1.8 2.8 1.3 3.5 1 .1-.8.4-1.3.8-1.6-2.7-.3-5.5-1.3-5.5-6 0-1.2.5-2.3 1.2-3.1-.1-.3-.5-1.5.1-3.2 0 0 1-.3 3.3 1.2a11.5 11.5 0 0 1 6 0C16.2 4.6 17.2 5 17.2 5c.7 1.7.3 2.9.1 3.2.8.8 1.2 1.9 1.2 3.1 0 4.7-2.8 5.7-5.5 6 .4.4.8 1.1.8 2.2v3.3c0 .4.2.8.8.7A12 12 0 0 0 12 .3"
	iconBitbucket = "M2.3 3a.9.9 0 0 0-.9 1l3 17.2c.1.5.5.9 1 .9h14.1c.4 0 .8-.3.8-.7l1.2-6.9h-6.1l-.9 5.1H8.9L6.7 8h11.7l.5-3.1a.9.9 0 0 0-.9-1zm11.4 5H9.1l1.2 6.6h3.4z"
	iconGitLab    = "m23.6 13.4-1.3-4.1-2.7-8.2a.7.7 0 0 0-1.3 0l-2.6 8.2H8.3L5.7 1.1a.7.7 0 0 0-1.3 0L1.7 9.3.4 13.4a1 1 0 0 0 .3 1.1l11.3 8.2 11.3-8.2a1 1 0 0 0 .3-1.1"
)

// providerGuide is one host's credential recipe.
type providerGuide struct {
	Key   string
	Label string
	Blurb string

	// Icon is the host's mark as an inline SVG path. Inline because a CSP blocks
	// external assets and a runtime-fetched logo would silently render as nothing;
	// monochrome via currentColor so it follows the theme and the selected state.
	Icon string

	// Username is what literally goes in the Username field. Literal means it is
	// a fixed string to type verbatim rather than something personal to the user.
	Username        string
	UsernameLiteral bool
	UsernameNote    string

	TokenKind string
	Scopes    []guideScope
	Steps     []guideStep

	// Gotcha is the one thing most likely to waste an afternoon on this host.
	Gotcha string
}

// guideScope is one permission to grant.
//
// Name is the identifier the host itself uses — "write:repository:bitbucket", not
// "Repositories: Write". A UI label drifts when the vendor redesigns their page and
// cannot be searched for or copied; the scope string is what appears in the token's
// own summary, so it is what an operator can actually verify against.
//
// Access says whether the scope grants read or write, so "do I need this to push?"
// is answerable at a glance rather than inferred from prose.
type guideScope struct {
	Name     string
	Label    string // the vendor's UI wording, when it differs from the identifier
	Access   string // "read" or "write"
	Why      string
	Required bool
}

type guideStep struct {
	Title string
	Body  string
	Path  string // click path, rendered as a trail; optional
}

// providerGuides is the full set. Every value here is a claim about an external
// product's UI, so each one is worded as what to look for rather than an exact
// label that upstream may rename.
var providerGuides = []providerGuide{
	{
		Key:             "github",
		Icon:            iconGitHub,
		Label:           "GitHub",
		Blurb:           "github.com or GitHub Enterprise",
		Username:        "x-access-token",
		UsernameLiteral: true,
		UsernameNote:    "Literal. GitHub checks only the token, so this is a placeholder.",
		TokenKind:       "Personal access token",
		Scopes: []guideScope{
			{Name: "repo", Access: "write", Required: true,
				Why: "Classic token, private repositories."},
			{Name: "public_repo", Access: "write", Required: false,
				Why: "Classic token, public repositories only."},
			{Name: "contents:write", Label: "Contents: Read and write", Access: "write", Required: false,
				Why: "Fine-grained token instead of repo. Use contents:read for read-only."},
		},
		Steps: []guideStep{
			{Title: "Open token settings", Body: "Avatar → Settings.",
				Path: "github.com → avatar → Settings → Developer settings → Personal access tokens"},
			{Title: "Choose a token type", Body: "Classic is simplest. Fine-grained limits it to chosen repositories."},
			{Title: "Pick the permissions", Body: "Classic: tick repo. Fine-grained: Contents → Read and write."},
			{Title: "Set an expiry", Body: "Shortest you can live with. Expiry shows up as an auth error."},
			{Title: "Copy the token", Body: "Shown once. Paste it into the Token field."},
		},
	},
	{
		Key:             "bitbucket_cloud",
		Icon:            iconBitbucket,
		Label:           "Bitbucket Cloud",
		Blurb:           "bitbucket.org",
		Username:        "your Bitbucket username",
		UsernameLiteral: false,
		UsernameNote:    "NOT your email. Bitbucket checks the username too — email is the usual cause of a 401.",
		TokenKind:       "App Password",
		Scopes: []guideScope{
			{Name: "read:repository:bitbucket", Label: "Repositories: Read", Access: "read", Required: true,
				Why: "Clone and fetch."},
			{Name: "write:repository:bitbucket", Label: "Repositories: Write", Access: "write", Required: false,
				Why: "Push. Omit for a read-only connector."},
		},
		Steps: []guideStep{
			{Title: "Find your username", Body: "This is the Username value — not your email.",
				Path: "bitbucket.org → avatar → Personal settings → Account settings → Username"},
			{Title: "Create an App Password", Body: "Name it something recognisable.",
				Path: "Personal settings → App passwords → Create app password"},
			{Title: "Tick the permissions", Body: "Repositories → Read, plus Write to push."},
			{Title: "Copy the password", Body: "Shown once. Paste it into the Token field."},
			{Title: "Fill in both fields", Body: "Both fields are checked here, unlike GitHub."},
		},
		Gotcha: "With an API token instead, the username becomes x-bitbucket-api-token-auth.",
	},
	{
		Key:             "bitbucket_server",
		Icon:            iconBitbucket,
		Label:           "Bitbucket Server",
		Blurb:           "self-hosted or Data Center",
		Username:        "your Bitbucket username",
		UsernameLiteral: false,
		UsernameNote:    "Your sign-in username.",
		TokenKind:       "HTTP access token",
		Scopes: []guideScope{
			{Name: "PROJECT_READ", Label: "Project read", Access: "read", Required: true,
				Why: "See the project."},
			{Name: "REPO_READ", Label: "Repository read", Access: "read", Required: true,
				Why: "Clone and fetch."},
			{Name: "REPO_WRITE", Label: "Repository write", Access: "write", Required: false,
				Why: "Push. Omit for a read-only connector."},
		},
		Steps: []guideStep{
			{Title: "Open your profile", Body: "Avatar → Manage account."},
			{Title: "Create an HTTP access token", Body: "Repository-scoped tokens live in that repository's settings.",
				Path: "Manage account → HTTP access tokens → Create token"},
			{Title: "Set the permissions", Body: "Repository read, plus write to push."},
			{Title: "Copy the token", Body: "Shown once. Paste it into the Token field."},
			{Title: "Check the host names", Body: "If SSH and HTTPS use different hostnames, map them under Remote — otherwise a push targets a host that does not exist."},
		},
		Gotcha: "No access tokens on older versions — use a password, ideally a service account.",
	},
	{
		Key:             "gitlab",
		Icon:            iconGitLab,
		Label:           "GitLab",
		Blurb:           "gitlab.com or self-managed",
		Username:        "oauth2",
		UsernameLiteral: true,
		UsernameNote:    "Literal, for a personal access token. Your username also works.",
		TokenKind:       "Personal / project / group token",
		Scopes: []guideScope{
			{Name: "read_repository", Access: "read", Required: true,
				Why: "Clone and fetch."},
			{Name: "write_repository", Access: "write", Required: false,
				Why: "Push. The broad api scope is not needed."},
		},
		Steps: []guideStep{
			{Title: "Open access tokens", Body: "Avatar → Edit profile.",
				Path: "gitlab.com → avatar → Edit profile → Access tokens"},
			{Title: "Choose the token's reach", Body: "Personal covers everything you can see; project or group tokens are limited to it."},
			{Title: "Tick the scopes", Body: "read_repository, plus write_repository to push. The api scope is not needed."},
			{Title: "Copy the token", Body: "Shown once. Paste it into the Token field."},
		},
	},
}

func guideByKey(key string) *providerGuide {
	for i := range providerGuides {
		if providerGuides[i].Key == key {
			return &providerGuides[i]
		}
	}
	return nil
}

// setupGuideInput drives the guide widget. Provider carries the picked host; the
// html= convention also sends the field's current value as "browser".
type setupGuideInput struct {
	Browser  string `wick:"desc=Current field value, supplied by the config UI."`
	Provider string `wick:"desc=Git host to show the credential guide for."`
}

// doSetupGuide renders the guide. It runs once, when the config page loads —
// switching hosts afterwards is handled entirely in CSS, so this op is not called
// again. With no host pre-selected the page opens showing just the chooser rather
// than four hosts' instructions at once.
func doSetupGuide(c *connector.Ctx) (any, error) {
	picked := strings.TrimSpace(c.Input("provider"))
	if picked == "" {
		picked = strings.TrimSpace(c.Input("browser"))
	}
	return map[string]any{"html": renderSetupGuide(picked)}, nil
}

// renderSetupGuide renders every host's guide once and lets CSS decide which one
// is visible.
//
// Why no backend round-trip: this content is static — four fixed recipes that
// never depend on config, credentials or network state. Re-running the op on each
// click meant an HTTP call to /test and a full markup replacement just to reveal
// text the browser already could have had, which made the panel visibly flash.
//
// Why CSS and not JavaScript: the manager renders this markup through Svelte's
// {@html}, and a <script> inserted that way is never executed by the browser. So
// the interaction is a hidden radio group plus ":checked ~ .panel" sibling rules —
// no JS, no request, instant, and keyboard-navigable for free.
//
// Scoping: every class name carries the wgid prefix and every rule is namespaced
// under it, because this markup lands in the middle of the manager's own page and
// must not style anything outside itself.
//
// Size: all four panels ship on every render, which is the cost of switching hosts
// without a request. The style block is written per position (:nth-of-type) rather
// than per host name so it does not grow with the host list, and the copy is kept
// terse for the same reason.
func renderSetupGuide(picked string) string {
	const p = "wgid" // class prefix, kept short because it repeats on every element

	var b strings.Builder

	// The style block is written once and is host-agnostic: the selected-tab and
	// visible-panel rules use :checked + nth-of-type positioning rather than one
	// pair of rules per host, which keeps the markup from growing with the number
	// of hosts. Adding a fifth host costs a row of data and nothing else.
	fmt.Fprintf(&b, `<style>
.%[1]s-w{color:%[2]s;font-size:13px}
.%[1]s-r{position:absolute;opacity:0;width:0;height:0;pointer-events:none}
.%[1]s-tabs{display:flex;gap:6px;flex-wrap:wrap;margin-bottom:10px}
.%[1]s-tab{cursor:pointer;display:flex;align-items:center;gap:6px;padding:6px 10px;border-radius:6px;border:1px solid %[3]s;font-size:12px;user-select:none}
.%[1]s-tab:hover{border-color:%[4]s}
.%[1]s-tab svg{width:15px;height:15px;flex:0 0 auto;opacity:.75}
.%[1]s-panel{display:none}
.%[1]s-hint{border:1px dashed %[3]s;border-radius:6px;padding:12px;text-align:center;opacity:.6;font-size:12px}
.%[1]s-ttl{display:flex;align-items:center;gap:7px;font-weight:600;font-size:13px;margin-bottom:8px}
.%[1]s-ttl svg{width:17px;height:17px;color:%[4]s}
.%[1]s-ttl span{font-weight:400;opacity:.6;font-size:11px}
.%[1]s-grid{display:grid;grid-template-columns:1fr 1fr;gap:8px;align-items:start}
@media (max-width:900px){.%[1]s-grid{grid-template-columns:1fr}}
.%[1]s-card{border:1px solid %[3]s;border-radius:6px;padding:10px;background:%[5]s}
.%[1]s-card+.%[1]s-card,.%[1]s-grid+.%[1]s-card{margin-top:8px}
.%[1]s-h{font-weight:600;font-size:12px;margin-bottom:7px;opacity:.85}
.%[1]s-row{margin-bottom:7px}
.%[1]s-k{opacity:.55;font-size:11px}
.%[1]s-v{font-family:monospace;font-size:12px}
.%[1]s-lit{color:%[4]s;font-weight:600}
.%[1]s-note{opacity:.7;font-size:11px;margin-top:1px}
.%[1]s-scope{display:flex;gap:7px;margin-bottom:6px}
.%[1]s-tick{font-family:monospace;color:%[4]s}
.%[1]s-acc{font-family:monospace;font-size:10px;text-transform:uppercase;letter-spacing:.04em;padding:1px 4px;border-radius:3px;border:1px solid;vertical-align:1px}
.%[1]s-rd{color:%[4]s;border-color:%[4]s}
.%[1]s-wr{color:#D9A03C;border-color:#D9A03C}
.%[1]s-lbl{font-size:11px;opacity:.6}
.%[1]s-opt{color:inherit;opacity:.5}
.%[1]s-step{display:flex;gap:8px;margin-bottom:8px}
.%[1]s-num{flex:0 0 auto;width:18px;height:18px;border-radius:50%%;border:1px solid %[3]s;color:%[4]s;display:flex;align-items:center;justify-content:center;font-family:monospace;font-size:10px}
.%[1]s-st{font-weight:600;font-size:12px}
.%[1]s-path{margin-top:3px;font-family:monospace;font-size:11px;background:%[6]s;border:1px solid %[3]s;border-radius:4px;padding:4px 6px;overflow-x:auto;white-space:nowrap}
.%[1]s-call{border:1px solid %[3]s;border-left:2px solid %[4]s;border-radius:4px;padding:8px 10px;margin-top:8px;font-size:12px;background:%[5]s}
`, p, uiText, uiBorder, uiOK, uiPanel, uiSunken)

	// One rule group per host, keyed by id.
	//
	// This was briefly written with :nth-of-type to keep the sheet host-agnostic,
	// which was wrong: nth-of-type counts every sibling of the same TAG, and the
	// panels share <div> with .wgid-tabs and .wgid-hint. Panel one is actually the
	// third div, so no rule ever matched and picking a host revealed nothing.
	// Explicit ids cost a few hundred bytes and cannot drift when the markup gains
	// a wrapper.
	for _, g := range providerGuides {
		k := esc(g.Key)
		fmt.Fprintf(&b, `#%[1]s-%[2]s:checked~.%[1]s-tabs>label[for="%[1]s-%[2]s"]{border-color:%[3]s;background:%[3]s1a;font-weight:600}
#%[1]s-%[2]s:checked~.%[1]s-tabs>label[for="%[1]s-%[2]s"] svg{opacity:1;color:%[3]s}
#%[1]s-%[2]s:checked~.%[1]s-hint{display:none}
#%[1]s-%[2]s:checked~#%[1]s-p-%[2]s{display:block}
`, p, k, uiOK)
	}
	fmt.Fprintf(&b, `</style><div class="%s-w">`, p)

	// Sprite first, before the tabs and panels that <use> its symbols. Referencing
	// a symbol defined later is legal per spec, but "define before use" removes any
	// chance of a silently blank icon — and a blank icon is indistinguishable from
	// a broken widget. Sibling position is free here because the reveal rules
	// target ids rather than counting siblings.
	b.WriteString(svgSprite())

	// Radios come first so every later element is a sibling ~ can reach.
	for _, g := range providerGuides {
		checked := ""
		if g.Key == picked {
			checked = " checked"
		}
		fmt.Fprintf(&b, `<input class="%[1]s-r" type="radio" name="%[1]s-h" id="%[1]s-%[2]s"%[3]s/>`,
			p, esc(g.Key), checked)
	}

	// Icon tabs. Labels, not buttons: the click flips the radio natively, and with
	// no data-op attribute the manager's handler ignores it — so nothing is sent to
	// the backend. title= carries the host name for hover and for screen readers,
	// since the tab itself is just a mark.
	fmt.Fprintf(&b, `<div class="%s-tabs">`, p)
	for _, g := range providerGuides {
		fmt.Fprintf(&b, `<label class="%[1]s-tab" for="%[1]s-%[2]s" title="%[3]s — %[4]s">%[5]s%[3]s</label>`,
			p, esc(g.Key), esc(g.Label), esc(g.Blurb), svgIcon(iconKey(g)))
	}
	fmt.Fprintf(&b, `</div><div class="%s-hint">Pick your git host above.</div>`, p)

	for _, g := range providerGuides {
		renderGuidePanel(&b, p, g)
	}

	fmt.Fprintf(&b, `</div>`)
	return b.String()
}

// svgIcon wraps path data in a minimal inline SVG.
// svgIcon references a symbol defined once in the sprite rather than repeating the
// path. Each mark is used twice (tab and panel title), and the paths are the single
// largest thing in this markup, so inlining them eight times cost more than the
// prose did.
func svgIcon(key string) string {
	return `<svg viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><use href="#wgid-i-` + key + `"/></svg>`
}

// svgSprite defines each distinct mark once. Bitbucket Cloud and Server share one,
// so four hosts need three symbols.
func svgSprite() string {
	var b strings.Builder
	b.WriteString(`<svg width="0" height="0" style="position:absolute" aria-hidden="true">`)
	seen := map[string]bool{}
	for _, g := range providerGuides {
		if seen[g.Icon] {
			continue
		}
		seen[g.Icon] = true
		fmt.Fprintf(&b, `<symbol id="wgid-i-%s" viewBox="0 0 24 24"><path d="%s"/></symbol>`,
			esc(g.Key), g.Icon)
	}
	b.WriteString(`</svg>`)
	return b.String()
}

// iconKey returns the sprite key for a host: the first host that introduced this
// mark, so shared marks resolve to one symbol.
func iconKey(g providerGuide) string {
	for _, o := range providerGuides {
		if o.Icon == g.Icon {
			return o.Key
		}
	}
	return g.Key
}

// renderGuidePanel writes one host's panel, hidden until its radio is checked.
//
// The two short cards sit side by side and the steps run full width underneath,
// which is why the guide is worth giving its own group at the bottom of the page.
func renderGuidePanel(b *strings.Builder, p string, g providerGuide) {
	fmt.Fprintf(b, `<div class="%[1]s-panel" id="%[1]s-p-%[2]s"><div class="%[1]s-ttl">%[3]s%[4]s<span>%[5]s</span></div>`,
		p, esc(g.Key), svgIcon(iconKey(g)), esc(g.Label), esc(g.Blurb))

	fmt.Fprintf(b, `<div class="%s-grid">`, p)

	// Left: what to type into the credential fields.
	fmt.Fprintf(b, `<div class="%[1]s-card"><div class="%[1]s-h">Enter above</div>`, p)
	lit := ""
	if g.UsernameLiteral {
		lit = " " + p + "-lit"
	}
	guideRow(b, p, "Username", g.Username, lit, g.UsernameNote)
	guideRow(b, p, "Token", g.TokenKind, "", "Stored encrypted, never on disk.")
	guideRow(b, p, "Auth method", "askpass", " "+p+"-lit", "Keeps the token out of the process list.")
	fmt.Fprintf(b, `</div>`)

	// Right: permissions.
	fmt.Fprintf(b, `<div class="%[1]s-card"><div class="%[1]s-h">Permissions</div>`, p)
	for _, s := range g.Scopes {
		mark, cls, pre := "○", " "+p+"-opt", "Optional — "
		if s.Required {
			mark, cls, pre = "✓", "", ""
		}

		// The scope identifier leads, because that is the string the operator ticks,
		// searches for, and later sees in the token's own summary.
		fmt.Fprintf(b, `<div class="%[1]s-scope"><span class="%[1]s-tick%[2]s">%[3]s</span><div>`,
			p, cls, mark)
		fmt.Fprintf(b, `<code class="%[1]s-v">%[2]s</code>`, p, esc(s.Name))

		// read / write, so "is this the one I need to push?" needs no reading.
		if s.Access != "" {
			accessCls := p + "-rd"
			if s.Access == "write" {
				accessCls = p + "-wr"
			}
			fmt.Fprintf(b, ` <span class="%[1]s-acc %[2]s">%[3]s</span>`, p, accessCls, esc(s.Access))
		}

		// The vendor's own wording, when their page says something different from the
		// scope name — otherwise the operator hunts for a checkbox labelled
		// "write:repository:bitbucket" that does not exist.
		if s.Label != "" && s.Label != s.Name {
			fmt.Fprintf(b, ` <span class="%[1]s-lbl">%[2]s</span>`, p, esc(s.Label))
		}

		fmt.Fprintf(b, `<div class="%[1]s-note">%[2]s%[3]s</div></div></div>`,
			p, esc(pre), esc(s.Why))
	}
	fmt.Fprintf(b, `</div></div>`)

	// Full width: the steps. Numbering is real — they are done in order.
	fmt.Fprintf(b, `<div class="%[1]s-card"><div class="%[1]s-h">Create the token</div>`, p)
	for i, s := range g.Steps {
		fmt.Fprintf(b, `<div class="%[1]s-step"><span class="%[1]s-num">%[2]d</span><div>`+
			`<div class="%[1]s-st">%[3]s</div><div class="%[1]s-note">%[4]s</div>`,
			p, i+1, esc(s.Title), esc(s.Body))
		if s.Path != "" {
			fmt.Fprintf(b, `<div class="%[1]s-path">%[2]s</div>`, p, esc(s.Path))
		}
		fmt.Fprintf(b, `</div></div>`)
	}
	fmt.Fprintf(b, `</div>`)

	if g.Gotcha != "" {
		fmt.Fprintf(b, `<div class="%[1]s-call"><b>Watch out — </b>%[2]s</div>`, p, esc(g.Gotcha))
	}
	fmt.Fprintf(b, `<div class="%[1]s-call"><b>Verify — </b>run Probe Remote — it lists the remote's branches and changes nothing.</div>`, p)

	fmt.Fprintf(b, `</div>`)
}

func guideRow(b *strings.Builder, p, label, value, extraClass, note string) {
	fmt.Fprintf(b, `<div class="%[1]s-row"><div class="%[1]s-k">%[2]s</div>`+
		`<div class="%[1]s-v%[3]s">%[4]s</div><div class="%[1]s-note">%[5]s</div></div>`,
		p, esc(label), extraClass, esc(value), esc(note))
}
