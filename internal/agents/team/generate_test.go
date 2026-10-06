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
		"tagline":       "  The   Critic of every pull request ever written, twice over ",
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
	if d.Tagline != "The Critic of every pull request ever written, twi" {
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

// Settings' one ✨ panel: the user's instruction plus the fields to change;
// the rest stay as they are.
func TestPersonaImprove(t *testing.T) {
	k := PersonaKindSpec()
	if err := k.Validate(aigen.Input{Text: "lebih formal", Fields: map[string]string{"target": "improve", "system_prompt": "Cek log."}}); err == nil {
		t.Fatal("improve with no field to update accepted")
	}
	in := aigen.Input{Text: "lebih formal, tambah aturan jangan tebak", Fields: map[string]string{
		"target": "improve", "update": "tagline, system_prompt, bogus", "system_prompt": "Cek log.", "tagline": "Log hunter",
	}}
	if err := k.Validate(in); err != nil {
		t.Fatal(err)
	}
	req, err := k.Build(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Rewrite only these fields: tagline, system_prompt.", "USER INSTRUCTION:\nlebih formal", "CURRENT TAGLINE:\nLog hunter"} {
		if !strings.Contains(req.Prompt, want) {
			t.Errorf("prompt lacks %q", want)
		}
	}
	if strings.Contains(req.Prompt, "bogus") || strings.Contains(req.Prompt, "USER BRIEF") {
		t.Error("unknown field or brief label leaked into the prompt")
	}
}

func TestPersonaFromResultAccessAndMentions(t *testing.T) {
	d, err := PersonaFromResult(wfprovider.StructuredResult{OK: true, Parsed: map[string]any{
		"name": "Notifier", "system_prompt": "x",
		"connectors":       []any{"slack", "loki"},
		"write_connectors": []any{"slack", "github"},
		"mention_from":     "LIST",
		"mention_allow":    []any{"@Log-Hunter", "log-hunter", ""},
	}})
	if err != nil {
		t.Fatal(err)
	}
	// write is a subset of the suggestions: github was never suggested
	if len(d.WriteConnectors) != 1 || d.WriteConnectors[0] != "slack" {
		t.Errorf("write %v", d.WriteConnectors)
	}
	if d.MentionFrom != "list" || len(d.MentionAllow) != 1 || d.MentionAllow[0] != "log-hunter" {
		t.Errorf("mention %q %v", d.MentionFrom, d.MentionAllow)
	}

	// an allow list only means something with "list"; an unknown policy is dropped
	d, _ = PersonaFromResult(wfprovider.StructuredResult{OK: true, Parsed: map[string]any{
		"name": "x", "system_prompt": "x", "mention_from": "everyone", "mention_allow": []any{"a"},
	}})
	if d.MentionFrom != "" || len(d.MentionAllow) != 0 || d.WriteConnectors == nil {
		t.Errorf("got %q %v %v", d.MentionFrom, d.MentionAllow, d.WriteConnectors)
	}
}

func TestPersonaPromptNamesAgentsAndCaptain(t *testing.T) {
	p := personaPrompt(aigen.Input{Text: "lead the team", Fields: map[string]string{
		"agents": "log-hunter: reads logs", "captain": "true",
	}})
	for _, want := range []string{"AVAILABLE AGENTS:\nlog-hunter: reads logs", "THIS AGENT IS THE CAPTAIN", "write_connectors", "mention_from"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt lacks %q", want)
		}
	}
	if p := personaPrompt(aigen.Input{Text: "x"}); !strings.Contains(p, "AVAILABLE AGENTS: (none yet") || strings.Contains(p, "THIS AGENT IS THE CAPTAIN") {
		t.Errorf("no-agents prompt:\n%s", p)
	}
}
