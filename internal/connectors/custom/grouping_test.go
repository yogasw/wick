package custom

import (
	"fmt"
	"strings"
	"testing"
)

// fixtureTools is a neutral catalog shaped like a large resource-style MCP
// server: <verb>-<resource>-tool names, most descriptions carrying a
// "Requires <resource>.<perm>" line, a few without.
func fixtureTools() []PreviewTool {
	var out []PreviewTool
	add := func(name, res string, perm bool) {
		d := "Does something with " + res + "."
		if perm {
			// Wrapped like real servers do: "Requires" ends the line.
			d += " Requires\n    " + res + ".view."
		}
		out = append(out, PreviewTool{Name: name, Description: d})
	}
	for _, r := range []string{"widget", "gadget", "region"} {
		for _, v := range []string{"list", "get", "create", "delete"} {
			name := v + "-" + r + "-tool"
			if v == "list" {
				name = v + "-" + r + "s-tool"
			}
			add(name, r, true)
		}
	}
	add("list-sprockets-tool", "sprocket", false)
	add("get-sprocket-tool", "sprocket", false)
	add("sync-everything-now-tool", "everything", false)
	return out
}

func TestSuggestAndPreviewGroupsByTemplate(t *testing.T) {
	tools := fixtureTools()
	g := SuggestGroupRules(tools)
	if g.StripSuffix != "-tool" {
		t.Fatalf("strip suffix = %q", g.StripSuffix)
	}
	if len(g.Templates) != 2 || g.Templates[0].From != "description" {
		t.Fatalf("templates = %+v", g.Templates)
	}
	p, err := PreviewGroups(tools, g)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	for _, gr := range p.Groups {
		got[gr.Title] = len(gr.Tools)
	}
	for _, want := range []string{"Widget", "Gadget", "Region"} {
		if got[want] != 4 {
			t.Fatalf("group %s = %d, all groups %v", want, got[want], got)
		}
	}
	// list-sprockets and get-sprocket fold into one group via the name template.
	if got["Sprocket"] != 2 {
		t.Fatalf("sprocket group = %d (%v)", got["Sprocket"], got)
	}
	if p.Total != len(tools) {
		t.Fatalf("total = %d", p.Total)
	}
}

func TestPlacementOrderAndOther(t *testing.T) {
	g := GroupRules{
		Pins:      map[string]string{"get-widget-tool": "Pinned", "gone-tool": "X"},
		Rules:     []GroupRule{{Pattern: "region", Group: "Places"}},
		Templates: []GroupTemplate{{From: "description", Pattern: "Requires\\s+{group}\\."}},
		Titles:    map[string]string{"gadget": "Gadgets & Co"},
	}
	tools := append(fixtureTools(), PreviewTool{Name: "odd-one", Description: "x", Category: "From Server"})
	p, err := PreviewGroups(tools, g)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]Placement{}
	for _, pl := range p.Placements {
		by[pl.Tool] = pl
	}
	cases := map[string][2]string{
		"get-widget-tool":    {"Pinned", "pin"},
		"list-regions-tool":  {"Places", "rule[0]"},
		"create-gadget-tool": {"Gadgets & Co", "template[0]"},
		"odd-one":            {"From Server", "server"},
		"get-sprocket-tool":  {"Other", "other"},
		"delete-widget-tool": {"Widget", "template[0]"},
	}
	for tool, want := range cases {
		if by[tool].Group != want[0] || by[tool].By != want[1] {
			t.Fatalf("%s placed %+v, want %v", tool, by[tool], want)
		}
	}
	if p.Groups[len(p.Groups)-1].Title != "Other" {
		t.Fatalf("Other must be last, got %s", p.Groups[len(p.Groups)-1].Title)
	}
	if len(p.OrphanPins) != 1 || p.OrphanPins[0] != "gone-tool" {
		t.Fatalf("orphan pins = %v", p.OrphanPins)
	}
}

func TestParseGroupRulesValidation(t *testing.T) {
	bad := []string{
		`{"rules":[{"pattern":"(","group":"A"}]}`,
		`{"rules":[{"pattern":"a"}]}`,
		`{"templates":[{"pattern":"no slot"}]}`,
		`{"templates":[{"pattern":"{group}-{group}"}]}`,
		`{"rules":[{"match":"title","pattern":"a","group":"A"}]}`,
		`{"unknown":1}`,
	}
	for _, raw := range bad {
		if _, err := ParseGroupRules(raw); err == nil {
			t.Fatalf("expected error for %s", raw)
		}
	}
	for _, raw := range []string{"", "{}", "  "} {
		g, err := ParseGroupRules(raw)
		if err != nil || g != nil {
			t.Fatalf("empty input %q: %v %v", raw, g, err)
		}
	}
}

func TestApplyGroupRulesKeepsKeysAndAddsKeywords(t *testing.T) {
	ops := []DefOp{
		{Key: "get_widget_tool", Name: "Get Widget Tool", Description: "d. Requires widget.view.", MCPSource: &MCPSource{ToolName: "get-widget-tool"}},
		{Key: "odd", Name: "Odd", Description: "x", Category: "Server Says", MCPSource: &MCPSource{ToolName: "odd"}},
	}
	g := &GroupRules{
		StripSuffix: "-tool",
		Templates:   []GroupTemplate{{From: "description", Pattern: "Requires {group}\\."}},
		Keywords:    map[string][]string{"get-widget-tool": {"fetch", "lookup"}},
	}
	out := applyGroupRules(ops, g)
	if out[0].Key != "get_widget_tool" {
		t.Fatal("op key must never change")
	}
	if out[0].Category != "Widget" || out[1].Category != "Server Says" {
		t.Fatalf("categories = %q, %q", out[0].Category, out[1].Category)
	}
	if strings.Contains(strings.ToLower(out[0].Name), "tool") {
		t.Fatalf("suffix not stripped from name: %s", out[0].Name)
	}
	if !strings.Contains(out[0].Description, "Keywords: fetch, lookup") {
		t.Fatalf("keywords missing: %s", out[0].Description)
	}
}

func TestOtherLast(t *testing.T) {
	cats := []DefCategory{{Title: "Other"}, {Title: "A"}, {Title: "B"}}
	out := otherLast(cats, &GroupRules{})
	got := fmt.Sprint(out[0].Title, out[1].Title, out[2].Title)
	if got != "ABOther" {
		t.Fatalf("order = %s", got)
	}
}
