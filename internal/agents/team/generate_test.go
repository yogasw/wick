package team

import (
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/aigen"
	wfprovider "github.com/yogasw/wick/internal/agents/workflow/provider"
)

func TestPersonaFromResultNormalizes(t *testing.T) {
	d, err := PersonaFromResult(wfprovider.StructuredResult{OK: true, Parsed: map[string]any{
		"name":          "Anton",
		"handle":        "@Anton The Critic!",
		"tagline":       "  The   Critic of every pull request ever written ",
		"description":   "Reviews changes.",
		"system_prompt": "You review code.",
		"avatar_shape":  "hexagon",
		"avatar_color":  "#FF8800",
		"connectors":    []any{"github", " github ", "", "loki", "a", "b", "c", "d", "e"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if d.Handle != "anton-the-critic" {
		t.Errorf("handle %q", d.Handle)
	}
	if d.Tagline != "The Critic of every pull request" {
		t.Errorf("tagline %q (%d)", d.Tagline, len([]rune(d.Tagline)))
	}
	if d.AvatarShape != "" || d.AvatarColor != "#ff8800" {
		t.Errorf("avatar %q %q", d.AvatarShape, d.AvatarColor)
	}
	if len(d.Connectors) != maxSuggestedConnectors || d.Connectors[0] != "github" || d.Connectors[1] != "loki" {
		t.Errorf("connectors %v", d.Connectors)
	}
}

func TestPersonaFromResultFallbacks(t *testing.T) {
	d, err := PersonaFromResult(wfprovider.StructuredResult{OK: true, Parsed: map[string]any{
		"name": "Log Hunter", "handle": "!!!", "system_prompt": "x",
	}})
	if err != nil || d.Handle != "log-hunter" || d.Connectors == nil {
		t.Fatalf("got %+v, %v", d, err)
	}
	if _, err := PersonaFromResult(wfprovider.StructuredResult{OK: true, Parsed: map[string]any{"name": "x"}}); err == nil {
		t.Fatal("empty persona accepted")
	}
	if _, err := PersonaFromResult(wfprovider.StructuredResult{Error: "boom"}); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("provider error lost: %v", err)
	}
}

func TestPersonaValidateAndPrompt(t *testing.T) {
	k := PersonaKindSpec()
	if err := k.Validate(aigen.Input{Text: "  "}); err == nil {
		t.Fatal("empty brief accepted")
	}
	if err := k.Validate(aigen.Input{Text: "x", Fields: map[string]string{"target": "avatar"}}); err == nil {
		t.Fatal("unknown target accepted")
	}
	if err := k.Validate(aigen.Input{Fields: map[string]string{"target": "description"}}); err == nil {
		t.Fatal("description with nothing to go on accepted")
	}
	in := aigen.Input{Fields: map[string]string{"target": "system_prompt", "system_prompt": "Cek log.", "connectors": "loki, github"}}
	if err := k.Validate(in); err != nil {
		t.Fatal(err)
	}
	req, err := k.Build(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Improve CURRENT SYSTEM PROMPT", "CURRENT SYSTEM PROMPT:\nCek log.", "AVAILABLE CONNECTORS: loki, github", "NEVER put the agent's name"} {
		if !strings.Contains(req.Prompt, want) {
			t.Errorf("prompt lacks %q", want)
		}
	}
	if req.Schema == nil {
		t.Fatal("no schema")
	}
}
