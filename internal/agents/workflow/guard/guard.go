// Package guard reviews a workflow body for safety violations before
// publish. Rule-based pre-flight (cheap, deterministic) per §17 of the
// design doc; the AI ephemeral-reviewer hook is left for callers to plug
// in via a custom Rule that spawns an agent.
package guard

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/yogasw/wick/internal/agents/workflow"
)

// Mode controls how violations are surfaced.
const (
	ModeOff   = "off"
	ModeWarn  = "warn"
	ModeBlock = "block"
)

// Severity levels.
const (
	SevLow      = "low"
	SevMedium   = "medium"
	SevHigh     = "high"
	SevCritical = "critical"
)

// Violation is one problem the guard found.
type Violation struct {
	Rule     string `json:"rule"`
	Node     string `json:"node,omitempty"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

// Report is the aggregate verdict.
type Report struct {
	OK          bool        `json:"ok"`
	Violations  []Violation `json:"violations,omitempty"`
	ContentHash string      `json:"content_hash"`
}

// Config is the runtime knob.
type Config struct {
	Mode             string   `wick:"mode,dropdown=off|warn|block,default=warn,desc=Guard verdict policy"`
	NetworkAllowlist []string // host substrings; only checked when set
}

// Rule is one inspection function. Returning empty slice means
// "nothing to flag for this rule". Implementations should be cheap and
// pure — they receive a defensive copy of the workflow.
type Rule interface {
	Name() string
	Check(w workflow.Workflow) []Violation
}

// Guard wires a set of rules.
type Guard struct {
	Rules  []Rule
	Config Config
}

// New returns a guard preloaded with the default rule set per §17.
func New(cfg Config) *Guard {
	return &Guard{
		Config: cfg,
		Rules: []Rule{
			&DestructiveShellRule{},
			&PromptInjectionRule{},
			&PlaintextSecretRule{},
			&UnparameterizedSQLRule{},
			&NetworkAllowlistRule{AllowedHosts: cfg.NetworkAllowlist},
		},
	}
}

// Review runs every rule.
func (g *Guard) Review(ctx context.Context, w workflow.Workflow) Report {
	rep := Report{ContentHash: ContentHash(w)}
	for _, r := range g.Rules {
		rep.Violations = append(rep.Violations, r.Check(w)...)
	}
	rep.OK = len(rep.Violations) == 0
	return rep
}

// Apply enforces the configured mode. Returns nil if the workflow can
// publish; otherwise an error describing the block.
func (g *Guard) Apply(report Report, override *workflow.Override) error {
	mode := g.Config.Mode
	if mode == "" {
		mode = ModeWarn
	}
	if report.OK || mode == ModeOff {
		return nil
	}
	if mode == ModeWarn {
		return nil
	}
	if override != nil && override.Reason != "" {
		return nil
	}
	return fmt.Errorf("guard blocked: %d violation(s); override required", len(report.Violations))
}

// ContentHash produces a deterministic hash of the workflow's
// governance-relevant payload (graph + triggers + env schema).
func ContentHash(w workflow.Workflow) string {
	h := sha256.New()
	fmt.Fprintf(h, "version:%d\n", w.Version)
	for _, n := range w.Graph.Nodes {
		fmt.Fprintf(h, "node:%s:%s\n", n.ID, n.Type)
		fmt.Fprintf(h, "  prompt:%s\n  cmd:%s\n  url:%s\n  sql:%s\n", n.Prompt, joinStr(n.Command, " "), n.URL, n.SQL)
	}
	for _, e := range w.Graph.Edges {
		fmt.Fprintf(h, "edge:%s->%s case:%s\n", e.From, e.To, e.Case)
	}
	for _, t := range w.Triggers {
		fmt.Fprintf(h, "trigger:%s:%s\n", t.Type, t.Schedule+t.Path+t.ChannelName)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func joinStr(xs []string, sep string) string {
	out := ""
	for i, x := range xs {
		if i > 0 {
			out += sep
		}
		out += x
	}
	return out
}
