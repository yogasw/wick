// Package mcp — workflow_template_test implementation.
//
// One-shot Go template renderer that AI clients call to verify a
// `{{...}}` snippet against a synthetic context without round-tripping
// through workflow_write_file + workflow_simulate. Errors are
// introspected: when a missing-key error fires inside a map lookup, the
// response lists the keys that ARE present at the offending path so
// the next attempt is informed instead of guessing again.
package mcp

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/yogasw/wick/internal/agents/workflow"
	wftemplate "github.com/yogasw/wick/internal/agents/workflow/template"
)

// TemplateTestInput is the payload for workflow_template_test.
//
// Context is a JSON-encoded RenderCtx-shaped object. Top-level keys
// "Event", "Node", "Env", "Secret", "Workflow", "Run", "Dataset" are
// projected onto the typed RenderCtx; unknown keys are ignored so the
// caller can hand in whatever they have without ceremony.
//
// SampleEvent picks a built-in synthetic event payload. When non-empty
// it OVERWRITES the .Event branch of context — the caller can use
// SampleEvent alone for the easy cases, or mix it with a hand-built
// .Node context for richer scenarios.
type TemplateTestInput struct {
	Template    string `json:"template"`
	Context     string `json:"context,omitempty"`
	SampleEvent string `json:"sample_event,omitempty"`
}

// TemplateTestResult is the workflow_template_test response.
//
// On success Rendered carries the output and Error is empty. On
// failure Error explains the failure, and when the failure is a
// missing-key error wick introspects the context at the offending
// path and lists the keys that ARE present.
type TemplateTestResult struct {
	OK            bool     `json:"ok"`
	Rendered      string   `json:"rendered,omitempty"`
	Error         string   `json:"error,omitempty"`
	At            string   `json:"at,omitempty"`
	AvailableKeys []string `json:"available_keys,omitempty"`
	Hint          string   `json:"hint,omitempty"`
}

// TemplateTest renders `in.Template` against the resolved context and
// returns a structured result.
func (m *Ops) TemplateTest(in TemplateTestInput) (TemplateTestResult, error) {
	if strings.TrimSpace(in.Template) == "" {
		return TemplateTestResult{}, fmt.Errorf("template is required")
	}
	rctx, ctxRaw, err := m.resolveTemplateContext(in)
	if err != nil {
		return TemplateTestResult{}, err
	}
	rendered, rerr := wftemplate.Render(in.Template, rctx)
	if rerr == nil {
		return TemplateTestResult{OK: true, Rendered: rendered}, nil
	}
	res := TemplateTestResult{
		OK:    false,
		Error: rerr.Error(),
	}
	// missingkey=error fires "map has no entry for key \"X\"" — when we
	// can locate it inside the template, introspect ctxRaw and dump
	// sibling keys so the caller knows what's actually available.
	if path, missingKey, ok := locateMissingKey(in.Template, rerr.Error()); ok {
		res.At = path
		keys := availableKeysAt(ctxRaw, path)
		sort.Strings(keys)
		res.AvailableKeys = keys
		if guess := bestMatch(missingKey, keys); guess != "" {
			res.Hint = fmt.Sprintf("did you mean %q?", guess)
		}
	}
	return res, nil
}

// resolveTemplateContext materialises a workflow.RenderCtx from one of
// three input forms: a raw Context JSON string, a SampleEvent preset
// name, or a combination (sample event wins on the .Event branch).
//
// Returns both the typed RenderCtx (for the renderer) and the parsed
// raw map (for available-keys introspection).
func (m *Ops) resolveTemplateContext(in TemplateTestInput) (workflow.RenderCtx, map[string]any, error) {
	raw := map[string]any{}
	if strings.TrimSpace(in.Context) != "" {
		if err := json.Unmarshal([]byte(in.Context), &raw); err != nil {
			return workflow.RenderCtx{}, nil, fmt.Errorf("context: %w", err)
		}
	}
	if in.SampleEvent != "" {
		sample, ok := sampleEventPayloads[in.SampleEvent]
		if !ok {
			return workflow.RenderCtx{}, nil, fmt.Errorf("unknown sample_event %q (available: %s)",
				in.SampleEvent, strings.Join(sortedSampleEventKeys(), ", "))
		}
		raw["Event"] = sample
	}
	rctx := workflow.RenderCtx{Node: map[string]any{}, Env: map[string]string{}, Secret: map[string]string{}, Dataset: map[string]any{}}
	if ev, ok := raw["Event"].(map[string]any); ok {
		rctx.Event = buildEventFromMap(ev)
		// Also expose the trigger-as-node shape that real runs publish:
		// .Node.<trigger-label>.payload.* so users can paste the same
		// template they would in a workflow yaml without rewriting.
		// We pick a generic label "trigger" so examples carry over.
		if _, exists := rctx.Node["trigger"]; !exists {
			triggerShape := map[string]any{
				"payload": sampleEventPayloadMap(ev),
				"type":    pickString(ev, "type"),
				"channel": pickString(ev, "channel"),
				"at":      pickString(ev, "at"),
			}
			rctx.Node["trigger"] = triggerShape
			// Mirror the trigger shape back into raw so the available-keys
			// introspection on `.Node.trigger.<x>` paths can resolve the
			// keys at the offending level.
			rawNode, _ := raw["Node"].(map[string]any)
			if rawNode == nil {
				rawNode = map[string]any{}
				raw["Node"] = rawNode
			}
			if _, ok := rawNode["trigger"]; !ok {
				rawNode["trigger"] = triggerShape
			}
		}
	}
	if n, ok := raw["Node"].(map[string]any); ok {
		for k, v := range n {
			rctx.Node[k] = v
		}
	}
	if e, ok := raw["Env"].(map[string]any); ok {
		for k, v := range e {
			rctx.Env[k] = fmt.Sprint(v)
		}
	}
	if s, ok := raw["Secret"].(map[string]any); ok {
		for k, v := range s {
			rctx.Secret[k] = fmt.Sprint(v)
		}
	}
	if w, ok := raw["Workflow"].(map[string]any); ok {
		rctx.Workflow = workflow.WorkflowRef{
			ID:   pickString(w, "ID"),
			Name: pickString(w, "Name"),
		}
		if v, ok := w["Version"].(float64); ok {
			rctx.Workflow.Version = int(v)
		}
	}
	if r, ok := raw["Run"].(map[string]any); ok {
		rctx.Run = workflow.RunRef{ID: pickString(r, "ID"), StartedAt: pickString(r, "StartedAt")}
	}
	if d, ok := raw["Dataset"].(map[string]any); ok {
		for k, v := range d {
			rctx.Dataset[k] = v
		}
	}
	return rctx, raw, nil
}

// buildEventFromMap projects a JSON-decoded `Event` map onto
// workflow.Event. Only fields the renderer reaches via
// {{.Event.<X>}} are mapped; the rest stay in .Event.Payload.
func buildEventFromMap(ev map[string]any) workflow.Event {
	out := workflow.Event{
		Type:    pickString(ev, "Type", "type"),
		Subtype: pickString(ev, "Subtype", "subtype"),
		Channel: pickString(ev, "Channel", "channel"),
	}
	if v, ok := ev["Payload"].(map[string]any); ok {
		out.Payload = v
	} else if v, ok := ev["payload"].(map[string]any); ok {
		out.Payload = v
	}
	return out
}

// sampleEventPayloadMap returns the payload map a sample event carries,
// regardless of whether the input map keyed it as "Payload" or "payload".
func sampleEventPayloadMap(ev map[string]any) map[string]any {
	if v, ok := ev["Payload"].(map[string]any); ok {
		return v
	}
	if v, ok := ev["payload"].(map[string]any); ok {
		return v
	}
	return map[string]any{}
}

func pickString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k].(string); ok {
			return v
		}
	}
	return ""
}

// sampleEventPayloads is the catalog of synthetic events the caller can
// inject via sample_event. Mirrors the canonical event types AI authors
// hit most.
var sampleEventPayloads = map[string]map[string]any{
	"slack.message": {
		"type":    "channel",
		"channel": "slack",
		"at":      "2026-05-19T10:32:17Z",
		"payload": map[string]any{
			"user":       "U02ABCDEF",
			"text":       "hi @bot can you check the staging deploy?",
			"channel_id": "C12345",
			"thread":     "1700001234.005600",
			"ts":         "1700001234.005600",
			"is_dm":      false,
		},
	},
	"slack.block_action": {
		"type":    "channel",
		"channel": "slack",
		"at":      "2026-05-19T10:34:02Z",
		"payload": map[string]any{
			"user":         "U02ABCDEF",
			"action_id":    "create_ticket",
			"block_id":     "actions1",
			"value":        "open",
			"channel_id":   "C12345",
			"message_ts":   "1700001234.005600",
			"trigger_id":   "123.4567.8abc",
			"response_url": "https://hooks.slack.com/actions/T123/x/y",
			"state":        nil,
		},
	},
	"slack.view_submission": {
		"type":    "channel",
		"channel": "slack",
		"at":      "2026-05-19T10:35:00Z",
		"payload": map[string]any{
			"user":             "U02ABCDEF",
			"callback_id":      "create_ticket_modal",
			"view_id":          "V0123ABCDE",
			"view_hash":        "1700001234.abc",
			"private_metadata": "src_msg=1700001000.001",
			"trigger_id":       "123.4567.8abc",
			"values": map[string]any{
				"subject_block": map[string]any{
					"subject_input": map[string]any{
						"type":  "plain_text_input",
						"value": "Payment refund issue",
					},
				},
				"prio_block": map[string]any{
					"prio_select": map[string]any{
						"type":            "static_select",
						"selected_option": map[string]any{"value": "high"},
					},
				},
			},
		},
	},
	"cron": {
		"type": "cron",
		"at":   "2026-05-19T08:00:00Z",
		"payload": map[string]any{
			"schedule": "0 8 * * *",
		},
	},
}

func sortedSampleEventKeys() []string {
	out := make([]string, 0, len(sampleEventPayloads))
	for k := range sampleEventPayloads {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// missingKeyRx matches the Go text/template missing-key error.
// Example: `template: node:1:7: executing "node" at <.Node.x.payload.channel>: map has no entry for key "channel"`
var missingKeyRx = regexp.MustCompile(`at <([^>]+)>: map has no entry for key "([^"]+)"`)

// locateMissingKey extracts the dotted path and missing key name from
// a Render error, when it carries the standard text/template
// missing-key error shape. Returns ok=false for other errors.
func locateMissingKey(_, errMsg string) (path, key string, ok bool) {
	m := missingKeyRx.FindStringSubmatch(errMsg)
	if m == nil {
		return "", "", false
	}
	// Drop the trailing missing-segment so `path` points at the parent map.
	// e.g. ".Node.x.payload.channel" → ".Node.x.payload"
	expr := m[1]
	missing := m[2]
	if idx := strings.LastIndex(expr, "."); idx >= 0 {
		path = expr[:idx]
	} else {
		path = expr
	}
	return path, missing, true
}

// availableKeysAt walks a dotted path inside the raw context map and
// returns the keys present at the leaf level. Best-effort — handles
// the .Node.<id>... case used in practice; deeper template
// expressions (index, range, etc.) bail to an empty list.
func availableKeysAt(raw map[string]any, path string) []string {
	if raw == nil {
		return nil
	}
	parts := strings.Split(strings.TrimPrefix(path, "."), ".")
	cur := any(raw)
	for _, p := range parts {
		mp, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		nxt, ok := mp[p]
		if !ok {
			// Allow trigger-as-node shorthand: when the path includes
			// "Node.trigger.payload" but the raw context only set
			// "Event.payload", reach through to that branch.
			if p == "trigger" {
				ev, _ := raw["Event"].(map[string]any)
				cur = ev
				continue
			}
			return nil
		}
		cur = nxt
	}
	mp, ok := cur.(map[string]any)
	if !ok {
		return nil
	}
	keys := make([]string, 0, len(mp))
	for k := range mp {
		keys = append(keys, k)
	}
	return keys
}

// bestMatch returns the closest sibling key to `missing` by simple
// substring / Levenshtein heuristics. Empty string when no candidate
// is within distance 3.
func bestMatch(missing string, candidates []string) string {
	if len(candidates) == 0 {
		return ""
	}
	missing = strings.ToLower(missing)
	for _, c := range candidates {
		if strings.Contains(strings.ToLower(c), missing) || strings.Contains(missing, strings.ToLower(c)) {
			return c
		}
	}
	best := ""
	bestDist := 4
	for _, c := range candidates {
		d := levenshtein(missing, strings.ToLower(c))
		if d < bestDist {
			bestDist = d
			best = c
		}
	}
	return best
}

// levenshtein computes the edit distance between a and b. Small,
// self-contained implementation — used for did-you-mean hints across
// template_test, validate, and picker_resolve.
func levenshtein(a, b string) int {
	if a == b {
		return 0
	}
	la, lb := len(a), len(b)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	prev := make([]int, lb+1)
	curr := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			min3 := prev[j] + 1
			if curr[j-1]+1 < min3 {
				min3 = curr[j-1] + 1
			}
			if prev[j-1]+cost < min3 {
				min3 = prev[j-1] + cost
			}
			curr[j] = min3
		}
		prev, curr = curr, prev
	}
	return prev[lb]
}
