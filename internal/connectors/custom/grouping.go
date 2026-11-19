package custom

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// GroupRules is the grouping recipe stored on an MCP server row
// (entity.CustomConnectorMCPServer.GroupRules). It sorts the server's tools
// into titled sections when the server ships no _meta.category of its own.
// Nothing here names individual tools except Pins, so the recipe keeps
// working as the server adds or renames tools: it is re-applied on every
// tools/list sync.
//
// For one tool the first match wins, in this order:
//  1. the server's own _meta.category;
//  2. Pins — exact tool name → group (exceptions only; they break on rename);
//  3. Rules — regex on the tool name or description → fixed group;
//  4. Templates — regex with a {group} slot; the captured text names the
//     group, so a new resource on the server gets its own group unasked;
//  5. Other — the catch-all section.
type GroupRules struct {
	// StripSuffix is cut from tool names before they are turned into the
	// display name ("-tool"). Op keys (and so tool_ids) never change.
	StripSuffix string            `json:"strip_suffix,omitempty"`
	Pins        map[string]string `json:"pins,omitempty"`
	Rules       []GroupRule       `json:"rules,omitempty"`
	Templates   []GroupTemplate   `json:"templates,omitempty"`
	// Titles maps a template capture ("managed_database") or a group name
	// to the label shown ("Managed Databases").
	Titles map[string]string `json:"titles,omitempty"`
	// Descriptions gives a section its one-line description, by title.
	Descriptions map[string]string `json:"descriptions,omitempty"`
	// Keywords adds search words to a tool, by tool name.
	Keywords map[string][]string `json:"keywords,omitempty"`
	// Other names the catch-all section; "Other" when empty.
	Other string `json:"other,omitempty"`
}

// GroupRule sends every tool whose name (or description) matches Pattern
// to Group.
type GroupRule struct {
	Match   string `json:"match,omitempty"` // "name" (default) or "description"
	Pattern string `json:"pattern"`
	Group   string `json:"group"`
}

// GroupTemplate derives the group from the tool itself: Pattern is a regex
// holding the literal slot {group}; whatever the slot captures becomes the
// group (through Titles, else humanized).
type GroupTemplate struct {
	From    string `json:"from,omitempty"` // "name" (default) or "description"
	Pattern string `json:"pattern"`
}

const groupSlot = "{group}"

// compiledRules is GroupRules with every regex compiled once.
type compiledRules struct {
	src       GroupRules
	rules     []*regexp.Regexp
	templates []*regexp.Regexp
}

// ParseGroupRules decodes and validates a stored recipe. Empty input is a
// nil recipe (no grouping beyond the server's own).
func ParseGroupRules(raw string) (*GroupRules, error) {
	if strings.TrimSpace(raw) == "" || strings.TrimSpace(raw) == "{}" {
		return nil, nil
	}
	var g GroupRules
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&g); err != nil {
		return nil, fmt.Errorf("group rules: %w", err)
	}
	if _, err := g.compile(); err != nil {
		return nil, err
	}
	return &g, nil
}

// Validate reports the first problem in the recipe: a bad regex, a rule
// without a group, a template without the {group} slot, an unknown match
// target.
func (g GroupRules) Validate() error {
	_, err := g.compile()
	return err
}

func (g GroupRules) compile() (*compiledRules, error) {
	c := &compiledRules{src: g}
	for i, r := range g.Rules {
		if strings.TrimSpace(r.Group) == "" {
			return nil, fmt.Errorf("rules[%d]: group is required", i)
		}
		if err := checkTarget(r.Match); err != nil {
			return nil, fmt.Errorf("rules[%d]: %w", i, err)
		}
		re, err := regexp.Compile(r.Pattern)
		if err != nil || r.Pattern == "" {
			return nil, fmt.Errorf("rules[%d]: invalid pattern %q", i, r.Pattern)
		}
		c.rules = append(c.rules, re)
	}
	for i, t := range g.Templates {
		if strings.Count(t.Pattern, groupSlot) != 1 {
			return nil, fmt.Errorf("templates[%d]: pattern must hold %s exactly once", i, groupSlot)
		}
		if err := checkTarget(t.From); err != nil {
			return nil, fmt.Errorf("templates[%d]: %w", i, err)
		}
		re, err := regexp.Compile(strings.Replace(t.Pattern, groupSlot, `(?P<group>[A-Za-z0-9_-]+?)`, 1))
		if err != nil {
			return nil, fmt.Errorf("templates[%d]: invalid pattern %q", i, t.Pattern)
		}
		c.templates = append(c.templates, re)
	}
	return c, nil
}

func checkTarget(t string) error {
	switch t {
	case "", "name", "description":
		return nil
	}
	return fmt.Errorf("unknown target %q (name or description)", t)
}

// Placement says where one tool landed and why — the preview shows it.
type Placement struct {
	Tool  string `json:"tool"`
	Group string `json:"group"`
	// By is "server", "pin", "rule[i]", "template[i]" or "other".
	By string `json:"by"`
}

func (c *compiledRules) otherTitle() string {
	if t := strings.TrimSpace(c.src.Other); t != "" {
		return t
	}
	return "Other"
}

func (c *compiledRules) title(capture string) string {
	if t, ok := c.src.Titles[capture]; ok && strings.TrimSpace(t) != "" {
		return t
	}
	one := singular(capture)
	if t, ok := c.src.Titles[one]; ok && strings.TrimSpace(t) != "" {
		return t
	}
	return groupLabel(one)
}

// singular folds a plain English plural ("services" → "service") so
// "list-services" and "get-service" share one group. Words ending in ss,
// us or is are left alone (status, address, analysis).
func singular(s string) string {
	l := strings.ToLower(s)
	if len(l) <= 3 || !strings.HasSuffix(l, "s") {
		return s
	}
	for _, keep := range []string{"ss", "us", "is"} {
		if strings.HasSuffix(l, keep) {
			return s
		}
	}
	return s[:len(s)-1]
}

// place decides the group of one tool. serverCategory is the tool's own
// _meta.category ("" when absent).
func (c *compiledRules) place(name, description, serverCategory string) Placement {
	if serverCategory != "" {
		return Placement{Tool: name, Group: serverCategory, By: "server"}
	}
	if g, ok := c.src.Pins[name]; ok && strings.TrimSpace(g) != "" {
		return Placement{Tool: name, Group: c.title(g), By: "pin"}
	}
	for i, r := range c.src.Rules {
		if c.rules[i].MatchString(pick(r.Match, name, description)) {
			return Placement{Tool: name, Group: r.Group, By: fmt.Sprintf("rule[%d]", i)}
		}
	}
	for i, t := range c.src.Templates {
		re := c.templates[i]
		m := re.FindStringSubmatch(pick(t.From, name, description))
		if m == nil {
			continue
		}
		if g := m[re.SubexpIndex("group")]; g != "" {
			return Placement{Tool: name, Group: c.title(g), By: fmt.Sprintf("template[%d]", i)}
		}
	}
	return Placement{Tool: name, Group: c.otherTitle(), By: "other"}
}

func pick(target, name, description string) string {
	if target == "description" {
		return description
	}
	return name
}

// groupLabel turns a capture ("prodlike_service", "image-build") into a
// readable label ("Prodlike Service", "Image Build").
func groupLabel(s string) string {
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == '_' || r == '-' || r == ' ' })
	for i, p := range parts {
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, " ")
}

// applyGroupRules fills in Category on ops that have none, appends keyword
// text to descriptions and strips the display-name suffix. ops and tools
// are parallel only by MCPSource.ToolName, so they are matched by name.
func applyGroupRules(ops []DefOp, g *GroupRules) []DefOp {
	if g == nil || len(ops) == 0 {
		return ops
	}
	c, err := g.compile()
	if err != nil {
		return ops
	}
	for i := range ops {
		name := ops[i].Key
		if ops[i].MCPSource != nil {
			name = ops[i].MCPSource.ToolName
		}
		p := c.place(name, ops[i].Description, ops[i].Category)
		ops[i].Category = p.Group
		if g.StripSuffix != "" && strings.HasSuffix(name, g.StripSuffix) {
			ops[i].Name = humanize(toFieldKey(strings.TrimSuffix(name, g.StripSuffix)))
		}
		if kw := g.Keywords[name]; len(kw) > 0 {
			ops[i].Description = strings.TrimRight(ops[i].Description, " \n") + "\nKeywords: " + strings.Join(kw, ", ")
		}
	}
	return ops
}

// otherLast moves the catch-all section to the end of the section list, so
// the named groups read first.
func otherLast(cats []DefCategory, g *GroupRules) []DefCategory {
	if g == nil {
		return cats
	}
	other := g.Other
	if strings.TrimSpace(other) == "" {
		other = "Other"
	}
	for i, c := range cats {
		if c.Title == other && i != len(cats)-1 {
			out := append(append([]DefCategory{}, cats[:i]...), cats[i+1:]...)
			return append(out, c)
		}
	}
	return cats
}

// legendFromRules turns the recipe's section descriptions into the legend
// shape toolsToCats already reads.
func legendFromRules(g *GroupRules, legend []MCPCategory) []MCPCategory {
	if g == nil || len(g.Descriptions) == 0 {
		return legend
	}
	out := append([]MCPCategory{}, legend...)
	titles := make([]string, 0, len(g.Descriptions))
	for t := range g.Descriptions {
		titles = append(titles, t)
	}
	sort.Strings(titles)
	for _, t := range titles {
		out = append(out, MCPCategory{Title: t, Description: g.Descriptions[t]})
	}
	return out
}

// GroupPreview is what grouping_preview returns: every section with its
// tools, the placements, the catch-all content and stale pins.
type GroupPreview struct {
	Groups []PreviewGroup `json:"groups"`
	// Other lists the tools no rule caught.
	Other []string `json:"other"`
	// OrphanPins are pinned tool names the server no longer lists.
	OrphanPins []string    `json:"orphan_pins,omitempty"`
	Placements []Placement `json:"placements"`
	Total      int         `json:"total"`
}

// PreviewGroup is one section of a preview.
type PreviewGroup struct {
	Title string   `json:"title"`
	Tools []string `json:"tools"`
}

// PreviewTool is the minimum a preview needs about a live tool.
type PreviewTool struct {
	Name        string
	Description string
	Category    string
}

// PreviewGroups dry-runs a recipe against a live tool list.
func PreviewGroups(tools []PreviewTool, g GroupRules) (GroupPreview, error) {
	c, err := g.compile()
	if err != nil {
		return GroupPreview{}, err
	}
	out := GroupPreview{Total: len(tools)}
	idx := map[string]int{}
	seen := map[string]bool{}
	for _, t := range tools {
		seen[t.Name] = true
		p := c.place(t.Name, t.Description, strings.TrimSpace(t.Category))
		out.Placements = append(out.Placements, p)
		if p.By == "other" {
			out.Other = append(out.Other, t.Name)
		}
		i, ok := idx[p.Group]
		if !ok {
			i = len(out.Groups)
			idx[p.Group] = i
			out.Groups = append(out.Groups, PreviewGroup{Title: p.Group})
		}
		out.Groups[i].Tools = append(out.Groups[i].Tools, t.Name)
	}
	// Catch-all last, like the live module.
	if i, ok := idx[c.otherTitle()]; ok && i != len(out.Groups)-1 {
		og := out.Groups[i]
		out.Groups = append(out.Groups[:i], out.Groups[i+1:]...)
		out.Groups = append(out.Groups, og)
	}
	for name := range g.Pins {
		if !seen[name] {
			out.OrphanPins = append(out.OrphanPins, name)
		}
	}
	sort.Strings(out.OrphanPins)
	return out, nil
}

// SuggestGroupRules guesses a recipe from the tool list: a common name
// suffix to strip, a "Requires <x>." style permission template when most
// descriptions carry one, and a <verb>-<resource> name template. It never
// writes anything; the caller previews and saves.
func SuggestGroupRules(tools []PreviewTool) GroupRules {
	var g GroupRules
	if len(tools) == 0 {
		return g
	}
	// Common suffix, e.g. "-tool" / "_tool".
	for _, suf := range []string{"-tool", "_tool", "-api", "_api"} {
		n := 0
		for _, t := range tools {
			if strings.HasSuffix(t.Name, suf) {
				n++
			}
		}
		if n*10 >= len(tools)*8 {
			g.StripSuffix = suf
			break
		}
	}
	// Permission-style descriptions: "Requires domain.view".
	perm := regexp.MustCompile("Requires `?([a-z][a-z0-9_]*)\\.[a-z_]+")
	n := 0
	for _, t := range tools {
		if perm.MatchString(t.Description) {
			n++
		}
	}
	if n*2 >= len(tools) {
		g.Templates = append(g.Templates, GroupTemplate{From: "description", Pattern: "Requires `?{group}\\."})
	}
	// <verb>-<resource>[-suffix] names.
	sep := "-"
	if strings.Count(tools[0].Name, "_") > strings.Count(tools[0].Name, "-") {
		sep = "_"
	}
	suffix := regexp.QuoteMeta(g.StripSuffix)
	g.Templates = append(g.Templates, GroupTemplate{
		From:    "name",
		Pattern: "^[a-z]+" + regexp.QuoteMeta(sep) + "{group}" + suffix + "$",
	})
	g.Other = "Other"
	return g
}

// ErrNoGroupRules is returned when a server row holds no recipe.
var ErrNoGroupRules = errors.New("no group rules set")
