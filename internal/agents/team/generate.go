package team

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/yogasw/wick/internal/agents/aigen"
	wfprovider "github.com/yogasw/wick/internal/agents/workflow/provider"
)

// PersonaKind is the aigen kind behind the Team app's ✨ buttons: the New
// agent wizard's one-line brief and Settings › Persona's Generate /
// Improve. One kind serves all three; Input.Fields["target"] says which
// part the caller will use:
//
//	"all"           a whole persona from Input.Text (the brief)
//	"system_prompt" a system prompt; rewrites Fields["system_prompt"]
//	                keeping its intent when there is one
//	"description"   the tagline and one-line description
//	"improve"       update the current persona following Input.Text (the
//	                user's instruction); Fields["update"] lists the fields
//	                to change (name, tagline, description, system_prompt),
//	                the rest are kept as they are
//
// Fields may also carry name, tagline, description, system_prompt (the
// current values) and connectors (comma-separated keys the owner has, so
// suggestions name real ones). The answer is always the full shape; the
// caller picks the fields it asked for.
const PersonaKind = "agent-persona"

// personaTargets are the accepted Fields["target"] values.
var personaTargets = []string{"all", "system_prompt", "description", "improve"}

// personaUpdatable are the fields an "improve" job may be asked to change.
var personaUpdatable = []string{"name", "tagline", "description", "system_prompt"}

// personaUpdates is Fields["update"] as known field names, in order.
func personaUpdates(in aigen.Input) []string {
	var out []string
	for _, f := range strings.Split(in.Fields["update"], ",") {
		if f = strings.TrimSpace(f); slices.Contains(personaUpdatable, f) && !slices.Contains(out, f) {
			out = append(out, f)
		}
	}
	return out
}

// hexColor is the avatar color shape the avatar component draws.
var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// maxSuggestedConnectors caps the connector suggestions of one draft.
const maxSuggestedConnectors = 6

// PersonaDraft is the result of a PersonaKind job.
type PersonaDraft struct {
	Name         string `json:"name"`
	Handle       string `json:"handle"`
	Tagline      string `json:"tagline"`
	Description  string `json:"description"`
	SystemPrompt string `json:"system_prompt"`
	AvatarShape  string `json:"avatar_shape,omitempty"`
	AvatarColor  string `json:"avatar_color,omitempty"`
	// Connectors are suggestions only — keys the wizard offers on the
	// Access step. Nothing is granted from them.
	Connectors []string `json:"connectors"`
}

var personaSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"name":          map[string]any{"type": "string"},
		"handle":        map[string]any{"type": "string"},
		"tagline":       map[string]any{"type": "string"},
		"description":   map[string]any{"type": "string"},
		"system_prompt": map[string]any{"type": "string"},
		"avatar_shape":  map[string]any{"type": "string", "enum": AvatarShapes},
		"avatar_color":  map[string]any{"type": "string"},
		"connectors":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
	},
	"required": []string{"name", "handle", "tagline", "description", "system_prompt"},
}

const personaPromptHead = `You write the persona of an AI agent that works inside a team chat app. Reply with the JSON object only.

Rules:
- Write in the language of the user's brief (English when it is English or unclear).
- system_prompt: a concise persona, 4-10 short lines: focus, how it works, what it hands back, tone. Address the agent as "you". NEVER put the agent's name or handle in it — the app adds an identity block with the current name. Do not claim tools, data or access the agent may not have; say what to do when something is outside its access.
- name: a short human-friendly name (1-2 words). handle: lowercase-kebab of the name, 2-31 chars of a-z, 0-9, "-".
- tagline: a short label people know it by, at most 50 characters, e.g. "The Critic", "Log Hunter", "Si Kritikus".
- description: one sentence on what it is for.
- avatar_shape: one of circle, squircle, triangle, diamond. avatar_color: a #rrggbb color that fits.
- connectors: keys from AVAILABLE CONNECTORS that the agent would likely need (empty when none fit). Never invent a key.
`

// PersonaKindSpec is the aigen.Kind for PersonaKind.
func PersonaKindSpec() aigen.Kind {
	return aigen.Kind{
		Name:     PersonaKind,
		MaxInput: 4 * 1024,
		Validate: validatePersonaInput,
		Build: func(in aigen.Input) (wfprovider.StructuredRequest, error) {
			return wfprovider.StructuredRequest{Prompt: personaPrompt(in), Schema: personaSchema}, nil
		},
		Finish: func(res wfprovider.StructuredResult) (any, error) { return PersonaFromResult(res) },
	}
}

func validatePersonaInput(in aigen.Input) error {
	target := personaTarget(in)
	if !slices.Contains(personaTargets, target) {
		return fmt.Errorf("unknown target %q", target)
	}
	brief := strings.TrimSpace(in.Text)
	if target == "all" && brief == "" {
		return errors.New("describe what the agent should do first")
	}
	if target == "improve" && len(personaUpdates(in)) == 0 {
		return errors.New("pick at least one field to update")
	}
	if target != "all" && brief == "" && strings.TrimSpace(in.Fields["system_prompt"]) == "" &&
		strings.TrimSpace(in.Fields["description"]) == "" && strings.TrimSpace(in.Fields["name"]) == "" {
		return errors.New("write a name, description or system prompt first")
	}
	return nil
}

func personaTarget(in aigen.Input) string {
	if t := strings.TrimSpace(in.Fields["target"]); t != "" {
		return t
	}
	return "all"
}

// personaPrompt assembles the one prompt of a PersonaKind job.
func personaPrompt(in aigen.Input) string {
	var b strings.Builder
	b.WriteString(personaPromptHead)
	b.WriteString("\nTASK: ")
	switch personaTarget(in) {
	case "system_prompt":
		if strings.TrimSpace(in.Fields["system_prompt"]) != "" {
			b.WriteString("Improve CURRENT SYSTEM PROMPT: rewrite it tighter and clearer, keeping its intent, language and every concrete rule it states. Fill the other fields from it too.")
		} else {
			b.WriteString("Write the system_prompt for the agent described below. Fill the other fields too.")
		}
	case "description":
		b.WriteString("Write the tagline and the one-sentence description for the agent described below (its system prompt is the main source). Fill the other fields too.")
	case "improve":
		upd := personaUpdates(in)
		b.WriteString("Update the CURRENT persona below following the USER INSTRUCTION (when there is none, make it tighter and clearer). Rewrite only these fields: " +
			strings.Join(upd, ", ") + ". Keep their language and every concrete rule the instruction does not change. Return every other field exactly as it is now.")
	default:
		b.WriteString("Create the whole persona from the user's brief.")
	}
	b.WriteString("\n")
	for _, f := range []struct{ key, label string }{
		{"name", "CURRENT NAME"},
		{"tagline", "CURRENT TAGLINE"},
		{"description", "CURRENT DESCRIPTION"},
		{"system_prompt", "CURRENT SYSTEM PROMPT"},
	} {
		if v := strings.TrimSpace(in.Fields[f.key]); v != "" {
			fmt.Fprintf(&b, "\n%s:\n%s\n", f.label, v)
		}
	}
	if c := strings.TrimSpace(in.Fields["connectors"]); c != "" {
		fmt.Fprintf(&b, "\nAVAILABLE CONNECTORS: %s\n", c)
	} else {
		b.WriteString("\nAVAILABLE CONNECTORS: (none — leave connectors empty)\n")
	}
	if t := strings.TrimSpace(in.Text); t != "" {
		label := "USER BRIEF"
		if personaTarget(in) == "improve" {
			label = "USER INSTRUCTION"
		}
		fmt.Fprintf(&b, "\n%s:\n%s\n", label, t)
	}
	return b.String()
}

// PersonaFromResult validates and normalizes a PersonaKind answer: the
// handle is made a valid one, the tagline cut to MaxTagline, a bad avatar
// dropped, and connector keys deduplicated. Whether a suggested key exists
// is the caller's check against its own catalog.
func PersonaFromResult(res wfprovider.StructuredResult) (PersonaDraft, error) {
	if !res.OK || res.Parsed == nil {
		msg := res.Error
		if msg == "" {
			msg = "no structured result"
		}
		return PersonaDraft{}, fmt.Errorf("generate failed: %s", msg)
	}
	str := func(k string) string {
		s, _ := res.Parsed[k].(string)
		return strings.TrimSpace(s)
	}
	d := PersonaDraft{
		Name:         str("name"),
		Tagline:      cutRunes(strings.Join(strings.Fields(str("tagline")), " "), MaxTagline),
		Description:  str("description"),
		SystemPrompt: str("system_prompt"),
		Connectors:   []string{},
	}
	if d.SystemPrompt == "" && d.Description == "" && d.Tagline == "" {
		return PersonaDraft{}, errors.New("generate returned an empty persona — try a more specific brief")
	}
	d.Handle = slugHandle(str("handle"))
	if d.Handle == "" {
		d.Handle = slugHandle(d.Name)
	}
	if s := str("avatar_shape"); slices.Contains(AvatarShapes, s) {
		d.AvatarShape = s
	}
	if c := str("avatar_color"); hexColor.MatchString(c) {
		d.AvatarColor = strings.ToLower(c)
	}
	if arr, ok := res.Parsed["connectors"].([]any); ok {
		for _, v := range arr {
			k, _ := v.(string)
			k = strings.TrimSpace(k)
			if k != "" && !slices.Contains(d.Connectors, k) && len(d.Connectors) < maxSuggestedConnectors {
				d.Connectors = append(d.Connectors, k)
			}
		}
	}
	return d, nil
}

// slugHandle turns any text into a valid handle, or "" when nothing of it
// survives.
func slugHandle(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(strings.TrimPrefix(strings.TrimSpace(s), "@")) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case b.Len() > 0 && !dash:
			b.WriteByte('-')
			dash = true
		}
	}
	h := strings.Trim(b.String(), "-")
	if len(h) > 31 {
		h = strings.TrimRight(h[:31], "-")
	}
	if ValidateHandle(h) != nil {
		return ""
	}
	return h
}

func cutRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return strings.TrimSpace(string([]rune(s)[:n]))
}
