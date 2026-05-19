// Package mcp — workflow_validate hint augmentation.
//
// parse.Validate already covers structural errors well; this layer
// wraps the result with did-you-mean suggestions for the failure
// modes AI authors hit repeatedly:
//
//   - lowercase JSON keys ("channel" / "channelname" instead of
//     PascalCase ChannelName) when calling set_triggers
//   - misspelt match keys (channe_id instead of channel_id)
//   - templates pointing at .Event.Foo when the supported root is
//     .Event.Payload
//
// Hints are advisory — surfaced alongside the original Error.Message
// so existing callers keep working. The Ops.ValidateRich method is
// the workflow_validate handler going forward; Ops.Validate stays as
// the low-level shim.
package mcp

import (
	"strings"

	"github.com/yogasw/wick/internal/agents/workflow/parse"
)

// ValidateRichResult is the augmented response for workflow_validate.
//
// Errors and Warnings carry the same Path + Message as parse.Result
// plus an optional DidYouMean / Hint pair. Hint is a human-readable
// remediation pointer the caller can show verbatim; DidYouMean lists
// likely-intended values for "unknown field" / "unknown key" errors.
type ValidateRichResult struct {
	OK       bool        `json:"ok"`
	Errors   []ErrorHint `json:"errors,omitempty"`
	Warnings []ErrorHint `json:"warnings,omitempty"`
}

// ErrorHint extends parse.Error with optional remediation pointers.
type ErrorHint struct {
	Path        string   `json:"path"`
	Message     string   `json:"message"`
	DidYouMean  []string `json:"did_you_mean,omitempty"`
	Hint        string   `json:"hint,omitempty"`
}

// ValidateRich runs parse.Validate and decorates each Error with
// did-you-mean / hint pointers. Used by workflow_validate.
func (m *Ops) ValidateRich(id string) ValidateRichResult {
	base := m.Validate(id)
	out := ValidateRichResult{OK: base.OK}
	for _, e := range base.Errors {
		out.Errors = append(out.Errors, m.decorateError(e))
	}
	for _, w := range base.Warnings {
		out.Warnings = append(out.Warnings, m.decorateError(w))
	}
	return out
}

// decorateError adds did-you-mean / hint when the message matches a
// known failure shape.
func (m *Ops) decorateError(e parse.Error) ErrorHint {
	h := ErrorHint{Path: e.Path, Message: e.Message}
	lowerMsg := strings.ToLower(e.Message)
	lowerPath := strings.ToLower(e.Path)

	switch {
	case strings.Contains(lowerMsg, "unknown field"):
		key := extractQuoted(e.Message)
		if guess := bestMatchAmong(key, knownTriggerJSONKeys()); guess != "" {
			h.DidYouMean = []string{guess}
		}
		if isJSONPascalContext(e.Path) {
			h.Hint = "Trigger JSON keys use PascalCase (Type, ChannelName, Event, EntryNode, MatchEnabled, Match). YAML uses lowercase."
		}

	case strings.Contains(lowerMsg, "unknown key") || strings.Contains(lowerMsg, "unknown match"):
		key := extractQuoted(e.Message)
		// Look up the event's MatchSchema for nearby keys.
		matchKeys := m.matchKeysForPath(e.Path)
		if guess := bestMatchAmong(key, matchKeys); guess != "" {
			h.DidYouMean = []string{guess}
		}
		if len(matchKeys) > 0 {
			h.Hint = "Match filter keys come from the event's match_schema. Call workflow_node_detail(\"channel:<channel>.<event>\") for the full list."
		}

	case strings.Contains(lowerMsg, "is required"):
		h.Hint = "Required field is missing. Read the schema via workflow_node_detail for the node type."

	case strings.Contains(lowerMsg, "must be a go identifier"):
		h.Hint = "Use letters, digits, and underscores only. No dash, no dot. Required for Go template field access ({{.Node.<id>.…}})."

	case strings.Contains(lowerMsg, "entry_node"):
		h.Hint = "entry_node must reference an existing node id. Add the node first via workflow_add_node or workflow_write_file."

	case strings.Contains(lowerPath, ".match") && strings.Contains(lowerMsg, "string"):
		h.Hint = "Picker fields (channel_id, user) accept [{id, name}] objects, not bare strings. Use workflow_picker_resolve to get valid IDs."
	}
	return h
}

// extractQuoted returns the first `"X"` substring inside s, or "".
func extractQuoted(s string) string {
	i := strings.IndexByte(s, '"')
	if i < 0 {
		return ""
	}
	j := strings.IndexByte(s[i+1:], '"')
	if j < 0 {
		return ""
	}
	return s[i+1 : i+1+j]
}

// bestMatchAmong returns the closest candidate to `target` by simple
// substring then Levenshtein. Empty when nothing's within distance 3.
func bestMatchAmong(target string, candidates []string) string {
	if target == "" || len(candidates) == 0 {
		return ""
	}
	lt := strings.ToLower(target)
	for _, c := range candidates {
		lc := strings.ToLower(c)
		if lc == lt {
			return c
		}
	}
	for _, c := range candidates {
		lc := strings.ToLower(c)
		if strings.Contains(lc, lt) || strings.Contains(lt, lc) {
			return c
		}
	}
	best := ""
	bestDist := 4
	for _, c := range candidates {
		d := levenshtein(lt, strings.ToLower(c))
		if d < bestDist {
			bestDist = d
			best = c
		}
	}
	return best
}

// knownTriggerJSONKeys is the canonical PascalCase key list for
// workflow_set_triggers JSON. Used to suggest fixes when the AI sends
// lowercase forms.
func knownTriggerJSONKeys() []string {
	return []string{
		"Type", "Schedule", "Timezone",
		"ChannelName", "Event", "EventKey", "Target",
		"EntryNode",
		"Path", "SecretRef",
		"Label",
		"At",
		"SourceWorkflow", "Severity", "NodeTypes",
		"MatchEnabled", "Match",
	}
}

// matchKeysForPath returns the MatchSchema field keys for the event
// the error path points at. Best-effort — parses "triggers[N].match"
// then looks up the workflow's trigger N to discover the event key,
// then asks the integration registry. Returns nil on any miss.
func (m *Ops) matchKeysForPath(path string) []string {
	if m.Integration == nil {
		return nil
	}
	// Path shapes: "triggers[0].match.channel_id" or just "triggers[0].match"
	idx := strings.Index(path, "triggers[")
	if idx < 0 {
		return nil
	}
	rest := path[idx+len("triggers["):]
	end := strings.IndexByte(rest, ']')
	if end < 0 {
		return nil
	}
	// We don't have the workflow id at this layer — caller resolves
	// keys generically across all registered events. List the union
	// of MatchSchema keys; collisions across events are fine because
	// the suggester picks the best string-distance match.
	seen := map[string]struct{}{}
	for _, ev := range m.Integration.Events() {
		for _, c := range ev.MatchSchema {
			seen[c.Key] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	return out
}

// isJSONPascalContext reports whether the error path likely refers to
// the JSON trigger shape (workflow_set_triggers) rather than the YAML
// shape (workflow_write_file). Heuristic: triggers[N].Field with a
// PascalCase or unknown-case field name suggests JSON input.
func isJSONPascalContext(path string) bool {
	return strings.Contains(path, "triggers[")
}
