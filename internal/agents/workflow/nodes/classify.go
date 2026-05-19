package nodes

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	"github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/engine"
	"github.com/yogasw/wick/internal/agents/workflow/integration"
	"github.com/yogasw/wick/internal/agents/workflow/provider"
	"github.com/yogasw/wick/internal/agents/workflow/template"
	"github.com/yogasw/wick/pkg/wickdocs"
)

type classifyNodeSchema struct {
	OutputCases     string `wick:"required;key=output_cases;desc=Enum labels the LLM must pick from (YAML list)"`
	Input           string `wick:"required;key=input;desc=Text to classify — use expression e.g. {{index .Event.Payload \"text\"}}"`
	Provider        string `wick:"key=provider;desc=Provider name (optional, uses default)"`
	PromptFile      string `wick:"key=prompt_file;desc=Optional prompt file path to override default classify prompt"`
	FuzzyMatch      bool   `wick:"key=fuzzy_match;desc=Allow partial/fuzzy label matching"`
	RetryOnMismatch int    `wick:"key=retry_on_mismatch;desc=Retry count when LLM returns unrecognized label"`
}

func (e *ClassifyExecutor) Descriptor() engine.NodeDescriptor {
	return engine.NodeDescriptor{
		Description: "Classify natural-language input into an enum via LLM. Route downstream via case: labels matching verdict.",
		WhenToUse:   "Input is free text and needs to be bucketed into a small set of cases.",
		Example:     "- id: triage\n  type: classify\n  output_cases: [bug, feature, question]\n  input: '{{index .Event.Payload \"text\"}}'\n  provider: claude",
		Schema:      integration.StructSchema(classifyNodeSchema{}),
		Output: map[string]string{
			"verdict":    "string — matched case label",
			"confidence": "float — 0.0–1.0",
			"reasoning":  "string — LLM explanation",
		},
		Docs: wickdocs.Docs{
			OutputShape: map[string]string{
				"verdict":    "Matched output_cases label. Edge `case:` filters route on this value. Falls back to \"default\" after retries fail.",
				"confidence": "0.0–1.0 score from the provider's structured output. 0 when the provider didn't return one.",
				"reasoning":  "Short explanation. Useful as Slack reply or audit log; not a routing input.",
				"raw":        "Raw provider response — debugging only.",
				"fuzzy":      "True when the verdict was resolved via fuzzy match instead of exact.",
			},
			TemplateableFields: []string{"input", "prompt_file"},
			Quirks: []string{
				"output_cases is a YAML list — each entry becomes a JSON Schema enum value passed to the provider's structured output.",
				"6-layer reliability stack: structured_output → normalize → exact → fuzzy → retry_on_mismatch → confidence_threshold fallback to \"default\".",
				"verdict \"default\" fires when no enum match after all retries, OR when confidence < confidence_threshold. Add a \"default\" case in your downstream branch to catch it.",
				"fuzzy_match enables Levenshtein + substring fallback — useful when the model occasionally returns variants like \"bugs\" instead of \"bug\".",
				"retry_on_mismatch tightens the system prompt on each retry. Costs more tokens; keep ≤2 unless the model is unreliable.",
			},
			PairWith:     []string{"branch", "switch", "agent"},
			CommonPitfalls: []string{
				"Don't add \"default\" to output_cases — the engine reserves \"default\" as the fallback verdict. Add it as a downstream edge case, not an input enum.",
				"Don't use classify for boolean/structured routing — use branch with an expression instead. Classify is for free-text input.",
			},
			InputSample:  `{"output_cases":["bug","feature","question"],"input":"{{index .Event.Payload \"text\"}}","provider":"claude","fuzzy_match":true,"retry_on_mismatch":1}`,
			OutputSample: `{"verdict":"bug","confidence":0.92,"reasoning":"User reports authentication failure after deploy — concrete defect, reproducible."}`,
			Examples: []wickdocs.Example{
				{
					Name: "triage_support_intent",
					YAML: `- id: triage
  type: classify
  provider: claude
  output_cases: [bug, feature, question]
  input: '{{.Node.trigger.payload.text}}'
  fuzzy_match: true`,
				},
			},
		},
	}
}

// ClassifyExecutor implements the 6-layer reliability stack:
//
//  1. structured_output (defer to provider.StructuredCall)
//  2. normalize          — lowercase, trim, strip punct/quotes
//  3. exact match        — verdict ∈ output_cases?
//  4. fuzzy_match        — Levenshtein/substring against cases
//  5. retry_on_mismatch  — stricter system prompt, re-ask
//  6. confidence_threshold — < threshold → default
type ClassifyExecutor struct {
	Providers *provider.Registry
}

// NewClassifyExecutor wires the executor.
func NewClassifyExecutor(reg *provider.Registry) *ClassifyExecutor {
	return &ClassifyExecutor{Providers: reg}
}

// Execute runs the classify node.
func (e *ClassifyExecutor) Execute(ctx context.Context, n workflow.Node, rc *workflow.RunContext) (workflow.NodeOutput, error) {
	if e.Providers == nil {
		return workflow.NodeOutput{}, fmt.Errorf("classify: no provider registry configured")
	}
	prov, err := e.Providers.Get(n.Provider)
	if err != nil {
		return workflow.NodeOutput{}, err
	}
	rctx := rc.RenderCtx()
	prompt, err := template.Render(n.Prompt, rctx)
	if err != nil {
		return workflow.NodeOutput{}, err
	}
	systemPrompt := classifySystemPrompt(n.OutputCases, false)
	maxRetry := n.RetryOnMismatch

	var lastResult provider.StructuredResult
	for attempt := 0; attempt <= maxRetry; attempt++ {
		req := provider.StructuredRequest{
			Prompt:       prompt,
			SystemPrompt: systemPrompt,
			Schema:       classifySchema(n.OutputCases),
			Preset:       n.Preset,
			SessionID:    classifySessionID(n, rc),
		}
		res, err := prov.StructuredCall(ctx, req)
		if err != nil {
			return workflow.NodeOutput{}, fmt.Errorf("classify call: %w", err)
		}
		lastResult = res
		verdict, confidence, reasoning := extractClassify(res)
		verdict = normalize(verdict, n.Normalize)
		matched, fuzzy := matchVerdict(verdict, n.OutputCases, n.FuzzyMatch)
		if matched != "" {
			if n.ConfidenceThreshold > 0 && confidence > 0 && confidence < n.ConfidenceThreshold {
				return workflow.NodeOutput{Verdict: "default", Confidence: confidence, Reasoning: reasoning + " (low confidence fallback)"}, nil
			}
			return workflow.NodeOutput{
				Verdict:    matched,
				Confidence: confidence,
				Reasoning:  reasoning,
				Fields:     map[string]any{"raw": res.Raw, "fuzzy": fuzzy, "usage": res.Usage},
			}, nil
		}
		systemPrompt = classifySystemPrompt(n.OutputCases, true)
	}
	verdict, confidence, reasoning := extractClassify(lastResult)
	return workflow.NodeOutput{
		Verdict:    "default",
		Confidence: confidence,
		Reasoning:  fmt.Sprintf("no enum match after %d retries (last verdict=%q): %s", maxRetry, verdict, reasoning),
		Fields:     map[string]any{"raw": lastResult.Raw, "usage": lastResult.Usage},
	}, nil
}

func classifySystemPrompt(cases []string, strict bool) string {
	enum := strings.Join(cases, ", ")
	if strict {
		return "You MUST output a JSON object {\"verdict\": <one of: " + enum + ">, \"confidence\": <0..1>, \"reasoning\": <short>}. No prose, no markdown."
	}
	return "Output a JSON object with fields verdict (one of: " + enum + "), confidence (0..1), reasoning (short)."
}

func classifySchema(cases []string) map[string]any {
	caseAny := []any{}
	for _, c := range cases {
		caseAny = append(caseAny, c)
	}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"verdict":    map[string]any{"type": "string", "enum": caseAny},
			"confidence": map[string]any{"type": "number"},
			"reasoning":  map[string]any{"type": "string"},
		},
		"required": []any{"verdict"},
	}
}

func classifySessionID(n workflow.Node, rc *workflow.RunContext) string {
	switch n.Session {
	case workflow.SessionRoot:
		return fmt.Sprintf("workflow_%s_run_%s_root", rc.Workflow.ID, rc.RunID)
	case workflow.SessionPersistent:
		return fmt.Sprintf("workflow_%s_persistent", rc.Workflow.ID)
	}
	return ""
}

func extractClassify(r provider.StructuredResult) (verdict string, confidence float64, reasoning string) {
	if r.Parsed == nil {
		return "", 0, r.Raw
	}
	if v, ok := r.Parsed["verdict"].(string); ok {
		verdict = v
	}
	if c, ok := r.Parsed["confidence"].(float64); ok {
		confidence = c
	}
	if rs, ok := r.Parsed["reasoning"].(string); ok {
		reasoning = rs
	}
	return
}

func normalize(s string, flag *bool) string {
	if flag != nil && !*flag {
		return strings.TrimSpace(s)
	}
	s = strings.TrimSpace(s)
	s = strings.ToLower(s)
	if (strings.HasPrefix(s, `"`) && strings.HasSuffix(s, `"`)) ||
		(strings.HasPrefix(s, `'`) && strings.HasSuffix(s, `'`)) {
		s = s[1 : len(s)-1]
	}
	s = strings.TrimFunc(s, func(r rune) bool {
		return unicode.IsPunct(r) || unicode.IsSpace(r)
	})
	return s
}

func matchVerdict(verdict string, cases []string, fuzzy bool) (string, bool) {
	if verdict == "" {
		return "", false
	}
	for _, c := range cases {
		if c == verdict {
			return c, false
		}
	}
	if !fuzzy {
		return "", false
	}
	for _, c := range cases {
		if c == "default" {
			continue
		}
		if strings.Contains(verdict, c) || strings.Contains(c, verdict) {
			return c, true
		}
	}
	best := ""
	bestDist := 3
	for _, c := range cases {
		d := levenshtein(verdict, c)
		if d < bestDist {
			bestDist = d
			best = c
		}
	}
	if best != "" {
		return best, true
	}
	return "", false
}

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
