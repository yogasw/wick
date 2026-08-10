package main

import (
	"strings"
	"testing"

	"github.com/yogasw/wick/pkg/entity"
)

func TestSetupGuideOpensShowingOnlyTheChooser(t *testing.T) {
	// Every host's panel is in the markup — that is what lets CSS switch between
	// them with no request. What matters is that none is *visible* until a host is
	// picked, so the config page does not open on a wall of text.
	html := renderSetupGuide("")

	for _, g := range providerGuides {
		if !strings.Contains(html, ">"+g.Label+"<") {
			t.Errorf("host chooser is missing %q", g.Label)
		}
	}
	if strings.Contains(html, `" checked/>`) {
		t.Error("a host is pre-selected, so a panel is visible before anything was picked")
	}
	if !strings.Contains(html, ".wgid-panel{display:none}") {
		t.Error("panels are not hidden by default")
	}
	if !strings.Contains(html, "Pick your git host above.") {
		t.Error("no prompt telling the operator what to do first")
	}
}

func TestSetupGuidePreselectsAStoredHost(t *testing.T) {
	// A stored value should reopen on the host the operator last used.
	html := renderSetupGuide("bitbucket_cloud")

	if !strings.Contains(html, `id="wgid-bitbucket_cloud" checked`) {
		t.Error("the stored host is not pre-selected")
	}
	// Exactly one, or two panels would show at once.
	if n := strings.Count(html, `" checked/>`); n != 1 {
		t.Errorf("%d hosts pre-selected, want exactly 1", n)
	}
	// Its own content is present.
	for _, want := range []string{"Repositories: Read", "not your email", "App passwords"} {
		if !strings.Contains(html, want) {
			t.Errorf("guide for bitbucket_cloud is missing %q", want)
		}
	}
	// The hint is hidden once a host is chosen, so the prompt does not linger above
	// a panel that is already answering it.
	if !strings.Contains(html, ":checked~.wgid-hint{display:none}") {
		t.Error("no rule hiding the prompt once a host is picked")
	}
}

func TestSetupGuideEveryHostRenders(t *testing.T) {
	// A host present in the chooser but broken when picked would be worse than
	// absent, so exercise all of them.
	for _, g := range providerGuides {
		html := renderSetupGuide(g.Key)
		if !strings.Contains(html, "Create the token") {
			t.Errorf("%s: steps section missing", g.Key)
		}
		if !strings.Contains(html, ">"+g.Label+"<span") {
			t.Errorf("%s: panel is not titled with the host name", g.Key)
		}
		if !strings.Contains(html, g.Username) {
			t.Errorf("%s: username %q not shown", g.Key, g.Username)
		}
		if len(g.Steps) == 0 {
			t.Errorf("%s: no steps defined", g.Key)
		}
		if len(g.Scopes) == 0 {
			t.Errorf("%s: no scopes defined", g.Key)
		}
		// Every host needs at least one required permission, or the guide would
		// imply a token with no access is enough.
		required := 0
		for _, s := range g.Scopes {
			if s.Required {
				required++
			}
		}
		if required == 0 {
			t.Errorf("%s: no scope marked required", g.Key)
		}
	}
}

func TestSetupGuideUnknownHostShowsNothingSelected(t *testing.T) {
	// A stale or hand-edited value must not pre-select anything, leaving the
	// operator with the chooser rather than a half-empty guide.
	html := renderSetupGuide("bitbucket") // not a real key
	if strings.Contains(html, `" checked/>`) {
		t.Errorf("unknown host pre-selected something:\n%s", html)
	}
	if !strings.Contains(html, "Pick your git host above.") {
		t.Error("unknown host did not leave the picker prompt visible")
	}
}

func TestSetupGuideStylesThroughThemeTokens(t *testing.T) {
	// Runtime-returned markup is not scanned by the manager's Tailwind build, so a
	// utility class would be purged and render unstyled. Prefixed classes defined
	// in the widget's own <style> block are fine — the purge only removes classes
	// Tailwind itself would have generated.
	for _, key := range []string{"", "github", "gitlab"} {
		html := renderSetupGuide(key)
		if !strings.Contains(html, "var(--color-") {
			t.Errorf("renderSetupGuide(%q) does not style through theme CSS variables", key)
		}
		// Any class used must be one this widget defines.
		for _, frag := range strings.Split(html, `class="`)[1:] {
			classes := frag[:strings.IndexByte(frag, '"')]
			for _, c := range strings.Fields(classes) {
				if !strings.HasPrefix(c, "wgid-") {
					t.Errorf("renderSetupGuide(%q) uses class %q, which the widget does not define", key, c)
				}
			}
		}
	}
}

func TestSetupGuideNeverPutsInputIntoMarkup(t *testing.T) {
	// The picked value is only ever compared against a known key to decide the
	// checked attribute — it is never interpolated. So there is no injection path
	// at all, which is stronger than escaping it would be. Pin that property: if
	// someone starts echoing the value, this fails.
	for _, hostile := range []string{
		`" onmouseover="alert(1)`,
		`<script>alert(1)</script>`,
		`wgid-github" checked x="`,
	} {
		html := renderSetupGuide(hostile)
		if strings.Contains(html, "alert(1)") || strings.Contains(html, "onmouseover") {
			t.Errorf("input %q reached the markup:\n%s", hostile, html)
		}
		if strings.Contains(html, `" checked/>`) {
			t.Errorf("input %q managed to select a host", hostile)
		}
	}
}

func TestSetupGuideSwitchesHostsWithoutTheBackend(t *testing.T) {
	// The guide is static content, so switching hosts must not call the backend.
	// It used to, and every click cost an HTTP round-trip plus a full markup
	// replacement — which made the panel visibly flash.
	html := renderSetupGuide("")

	if strings.Contains(html, "data-op") {
		t.Error("markup carries data-op, so clicking would call the backend again")
	}

	// A radio per host, and a label pointing at each, is what makes the CSS
	// :checked ~ sibling rules work.
	for _, g := range providerGuides {
		if !strings.Contains(html, `type="radio"`) {
			t.Fatal("no radio inputs — nothing for the CSS rules to hang off")
		}
		if !strings.Contains(html, `id="wgid-`+g.Key+`"`) {
			t.Errorf("%s: no radio input", g.Key)
		}
		if !strings.Contains(html, `for="wgid-`+g.Key+`"`) {
			t.Errorf("%s: no label targeting its radio", g.Key)
		}
		if !strings.Contains(html, `#wgid-`+g.Key+`:checked~#wgid-p-`+g.Key+`{display:block}`) {
			t.Errorf("%s: no CSS rule revealing its panel", g.Key)
		}
		// The rule targets an id, so that id must actually exist on the panel.
		if !strings.Contains(html, `id="wgid-p-`+g.Key+`"`) {
			t.Errorf("%s: reveal rule targets #wgid-p-%s but no element carries it", g.Key, g.Key)
		}
	}
}

func TestSetupGuideRevealRulesDoNotUsePositionalSelectors(t *testing.T) {
	// This shipped broken once. The rules were written with :nth-of-type to keep the
	// stylesheet host-agnostic, but nth-of-type counts every sibling of the same TAG
	// — and the panels are <div>s alongside .wgid-tabs and .wgid-hint, so panel one
	// is really the third div. No rule matched, and picking a host revealed nothing
	// at all while every unit test still passed.
	//
	// Targeting ids instead cannot drift when the markup gains a wrapper.
	html := renderSetupGuide("")
	if strings.Contains(html, "nth-of-type") {
		t.Error("reveal rules use nth-of-type, which counts unrelated siblings of the same tag")
	}
	if strings.Contains(html, "nth-child") {
		t.Error("reveal rules use nth-child, which is just as fragile as nth-of-type here")
	}

	// Every id a reveal rule targets must exist, and vice versa: a rule with no
	// element, or an element with no rule, both mean a dead host.
	for _, g := range providerGuides {
		rule := `#wgid-` + g.Key + `:checked~#wgid-p-` + g.Key
		if !strings.Contains(html, rule) {
			t.Errorf("%s: missing reveal rule %s", g.Key, rule)
		}
	}
}

func TestSetupGuideSpriteIsDefinedBeforeItIsUsed(t *testing.T) {
	// Every icon is a <use href="#…"> pointing at a symbol in the sprite. Referring
	// to a symbol defined later in the document is legal per spec, but a blank icon
	// is indistinguishable from a broken widget, so the sprite goes first and the
	// question never arises. Sibling order is free to change because the reveal
	// rules target ids rather than counting siblings.
	html := renderSetupGuide("")
	sprite := strings.Index(html, `<svg width="0"`)
	firstUse := strings.Index(html, `<use href="#wgid-i-`)
	if sprite < 0 {
		t.Fatal("no icon sprite")
	}
	if firstUse < 0 {
		t.Fatal("no icon references")
	}
	if sprite > firstUse {
		t.Error("an icon is referenced before the sprite defines it")
	}

	// Every referenced symbol must exist, or that icon renders blank.
	for _, g := range providerGuides {
		id := `id="wgid-i-` + iconKey(g) + `"`
		if !strings.Contains(html, id) {
			t.Errorf("%s references a symbol the sprite does not define (%s)", g.Key, id)
		}
	}
}

func TestEveryWidgetButtonIsTypeButton(t *testing.T) {
	// One shared rule for every widget this connector renders into the manager's
	// config form: a typeless <button> defaults to type="submit", which submits the
	// form, reloads it and wipes the operator's input while the op never runs. This
	// really happened to the test panel, so cover all three widgets in one place.
	markups := map[string]string{
		"setup guide (empty)":    renderSetupGuide(""),
		"setup guide (picked)":   renderSetupGuide("github"),
		"policy manager":         renderPolicyManager(testCtx(map[string]string{}), nil),
		"test panel (empty)":     renderTestPanel(nil),
		"test panel (with rows)": renderTestPanel(&testReport{Checks: []testCheck{{Status: "ok", Name: "x"}}}),
	}
	for name, html := range markups {
		for _, frag := range strings.Split(html, "<button")[1:] {
			i := strings.IndexByte(frag, '>')
			if i < 0 {
				t.Errorf("%s: unterminated <button", name)
				continue
			}
			if tag := frag[:i]; !strings.Contains(tag, `type="button"`) {
				t.Errorf("%s: <button%s> has no type=\"button\" and would submit the form", name, tag)
			}
		}
	}
}

func TestSetupGuideMarkupCannotRunScripts(t *testing.T) {
	// The manager renders this through Svelte's {@html}, which never executes an
	// inserted <script>. A script here would look like it worked in review and do
	// nothing in the browser, so the interaction has to stay CSS-only.
	html := renderSetupGuide("github")
	for _, forbidden := range []string{"<script", "onclick=", "onchange=", "javascript:"} {
		if strings.Contains(strings.ToLower(html), forbidden) {
			t.Errorf("markup contains %q, which {@html} will not run", forbidden)
		}
	}
}

func TestSetupGuideStylesAreScopedToTheWidget(t *testing.T) {
	// This markup renders inside the manager's own settings page, so an unprefixed
	// selector would restyle the host page.
	html := renderSetupGuide("github")
	start := strings.Index(html, "<style>")
	end := strings.Index(html, "</style>")
	if start < 0 || end < 0 {
		t.Fatal("no style block found")
	}
	for _, line := range strings.Split(html[start+7:end], "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, "{") {
			continue
		}
		// An at-rule such as "@container (max-width:620px){.wgid-grid{…}}" is a
		// wrapper, not a selector — the selectors that matter are the ones nested
		// inside it, so unwrap before checking.
		if strings.HasPrefix(line, "@") {
			if i := strings.Index(line, "{"); i >= 0 {
				line = strings.TrimSpace(line[i+1:])
			}
		}
		selector := line[:strings.Index(line, "{")]
		if !strings.Contains(selector, "wgid") {
			t.Errorf("selector %q is not scoped to the widget", selector)
		}
	}
}

func TestSetupGuideMarkupStaysSmall(t *testing.T) {
	// Shipping every panel is what makes host switching free, so the markup is the
	// sum of all four. Roughly 16 KB, sent once when the page loads.
	//
	// The budget is a tripwire against accidental growth, not a target to shrink to:
	// the copy has already been trimmed and the icons deduplicated into a sprite,
	// which together bought about 2 KB — the rest is the actual content. If this
	// fails, first check whether something is being repeated per host that could be
	// written once (the style block is per position for exactly that reason).
	const budget = 18000

	html := renderSetupGuide("github")
	if len(html) > budget {
		t.Errorf("markup is %d bytes, over the %d budget — trim the copy rather than raising this",
			len(html), budget)
	}

	// The style block must not scale with the host list. Rules are written per
	// position, so four hosts cost four short rule groups, not four full sheets.
	styleEnd := strings.Index(html, "</style>")
	if styleEnd < 0 {
		t.Fatal("no style block")
	}
	if styleEnd > 4200 {
		t.Errorf("style block is %d bytes; it should be host-agnostic", styleEnd)
	}

	// The real invariant: adding a host must cost a panel, not another stylesheet.
	// Four hosts share three icon symbols, so the sprite must not repeat paths.
	if n := strings.Count(html, "<symbol"); n != 3 {
		t.Errorf("%d icon symbols, want 3 — Bitbucket Cloud and Server share one mark", n)
	}
	if strings.Count(html, iconGitHub) != 1 {
		t.Error("the GitHub path appears more than once; use the sprite instead of inlining it")
	}
}

func TestSetupGuideIsTheFirstConfigFieldInItsOwnGroup(t *testing.T) {
	// Groups render in field-declaration order, so declaring the guide first puts it
	// at the top of the page — read before filling anything in. Being alone in its
	// group is what gives it the full width for its two-column layout; beside a text
	// input it would be squeezed into half a row.
	configs := entity.StructToConfigs(Config{})
	if len(configs) == 0 {
		t.Fatal("no configs")
	}
	if first := configs[0]; first.Key != "setup_guide" {
		t.Errorf("first config field is %q, want setup_guide so its group renders at the top", first.Key)
	}

	// No other field may share its group, or the guide would sit next to inputs.
	var group string
	for _, c := range configs {
		if c.Key == "setup_guide" {
			group = c.Group
		}
	}
	if group == "" {
		t.Fatal("setup_guide has no group")
	}
	title := strings.SplitN(group, "|", 2)[0]
	for _, c := range configs {
		if c.Key == "setup_guide" {
			continue
		}
		if strings.SplitN(c.Group, "|", 2)[0] == title {
			t.Errorf("field %q shares the guide's group %q", c.Key, title)
		}
	}
}

func TestEveryConfigGroupStartsCollapsed(t *testing.T) {
	// The page opens clean — every group collapsed, including the setup guide — so
	// the operator expands only what they need.
	//
	// The flag is the THIRD PIPE SEGMENT of the group value
	// (Title|Description|collapsed), not a separate semicolon flag. Written as
	// ";collapsed;" it parses as an unknown flag and is silently dropped, so this
	// test asserts on the parsed shape rather than the presence of the word.
	//
	// Only a group's FIRST field carries the flag, so this collects first-seen order
	// the same way the UI does.
	firstOfGroup := map[string]string{} // group title -> its first field's raw tag
	var order []string
	for _, c := range entity.StructToConfigs(Config{}) {
		g := c.Group
		if g == "" {
			continue // ungrouped fields land in the default card
		}
		title := strings.SplitN(g, "|", 2)[0]
		if _, seen := firstOfGroup[title]; !seen {
			firstOfGroup[title] = g
			order = append(order, title)
		}
	}
	if len(order) < 5 {
		t.Fatalf("only %d groups found; the config layout changed shape", len(order))
	}

	for _, title := range order {
		raw := firstOfGroup[title]

		// The parsed form is what the UI acts on: exactly three pipe segments, the
		// third being "collapsed".
		parts := strings.Split(raw, "|")
		collapsed := len(parts) >= 3 && parts[2] == "collapsed"

		if !collapsed {
			// Catch the specific near-miss rather than only reporting absence, since
			// ";collapsed" looks right and does nothing.
			if strings.Contains(raw, ";collapsed") || strings.HasSuffix(raw, ";collapsed") {
				t.Errorf("group %q writes collapsed as a semicolon flag; it must be the third pipe segment: Title|Description|collapsed", title)
				continue
			}
			t.Errorf("group %q does not start collapsed; make its first field's tag Title|Description|collapsed", title)
		}
	}

	const guide = "Setting up credentials"
	if order[0] != guide {
		t.Errorf("first group is %q, want %q", order[0], guide)
	}
}

func TestSetupGuideOpIsRegisteredAsConfigOnly(t *testing.T) {
	found := false
	for _, cat := range Operations() {
		for _, op := range cat.Ops {
			if op.Key == "setup_guide" {
				found = true
				if !op.ConfigOnly {
					t.Error("setup_guide must be OpConfigOnly so it never reaches the MCP surface")
				}
			}
		}
	}
	if !found {
		t.Error("setup_guide is not registered in Operations()")
	}
}

func TestSetupGuideFieldIsWiredToTheOp(t *testing.T) {
	// The html= tag names an op by string; a rename on either side silently blanks
	// the widget.
	var tag string
	for _, cfg := range entity.StructToConfigs(Config{}) {
		if cfg.Key == "setup_guide" {
			tag = cfg.Options
		}
	}
	if tag == "" {
		t.Fatal("no setup_guide config field found")
	}
	if tag != "setup_guide" {
		t.Errorf("setup_guide field points at op %q, want setup_guide", tag)
	}
}

func TestSetupGuideDoesNotContradictTheAuthFields(t *testing.T) {
	// The guide and the Username/Token descriptions are two copies of the same
	// facts. They drift the moment one is edited alone, so pin the values that
	// actually break authentication when wrong.
	var usernameDesc string
	for _, cfg := range entity.StructToConfigs(Config{}) {
		if cfg.Key == "username" {
			usernameDesc = cfg.Description
		}
	}
	if usernameDesc == "" {
		t.Fatal("no username config field found")
	}

	for _, g := range providerGuides {
		if !g.UsernameLiteral {
			continue
		}
		// A literal username is the kind of value someone copies character for
		// character; it must appear in the field description too.
		if !strings.Contains(usernameDesc, g.Username) {
			t.Errorf("guide tells %s users to enter %q, but the username field description never mentions it",
				g.Label, g.Username)
		}
	}

	// Same drift risk on the token field: adding a host to the guide without
	// naming its permission there leaves the field description silently wrong.
	var tokenDesc string
	for _, cfg := range entity.StructToConfigs(Config{}) {
		if cfg.Key == "token" {
			tokenDesc = cfg.Description
		}
	}
	if tokenDesc == "" {
		t.Fatal("no token config field found")
	}
	for _, g := range providerGuides {
		if !strings.Contains(tokenDesc, g.Label) && !mentionsAnyScope(tokenDesc, g) {
			t.Errorf("token field description covers neither %s by name nor any of its scopes", g.Label)
		}
	}
}

// mentionsAnyScope reports whether the description names at least one of a host's
// required permissions, which is enough to show the two were written together.
func mentionsAnyScope(desc string, g providerGuide) bool {
	for _, s := range g.Scopes {
		if s.Required && strings.Contains(desc, s.Name) {
			return true
		}
	}
	return false
}
