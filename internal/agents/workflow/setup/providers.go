package setup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	agentprovider "github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/workflow/provider"
)

// cliProvider adapts an agentprovider.Instance (claude / codex /
// gemini CLI) to the workflow provider.Provider interface. One-shot
// subprocess per call — `<bin> --output-format json --print <prompt>`
// for structured + agent calls.
//
// Claude lives behind the agent pool in production (see
// internal/docs/workflow/pool.md), but cliProvider stays as the
// fallback for codex/gemini and for any path that runs without the
// pool wired (tests, headless MCP).
type cliProvider struct {
	ins agentprovider.Instance
}

// nonPoolSem caps concurrent cliProvider.AgentCall invocations so a
// burst of workflow runs doesn't fork N subprocesses at once. Claude
// path uses the agent pool's MaxConcurrent (separate cap); this
// semaphore protects only the codex/gemini path. Size matches the
// pool default; tweak via wick.yml if you have a powerful machine.
var nonPoolSem = make(chan struct{}, 2)

// NewCLIProviders returns one workflow.Provider per healthy CLI
// runtime declared by the agent provider package. Caller registers
// each into the workflow Manager.
func NewCLIProviders() ([]provider.Provider, error) {
	all, err := agentprovider.Load()
	if err != nil {
		return nil, err
	}
	out := []provider.Provider{}
	for _, ins := range all {
		if ins.Disabled {
			continue
		}
		out = append(out, &cliProvider{ins: ins})
	}
	return out, nil
}

func (p *cliProvider) Name() string { return p.ins.Name }

func (p *cliProvider) Capabilities() provider.Capabilities {
	return provider.Capabilities{
		StructuredOutput: p.ins.Type == agentprovider.TypeClaude,
		Streaming:        false,
	}
}

// StructuredCall runs the CLI with `--output-format json --print`,
// parses the response, and returns the verdict/confidence/reasoning
// shape classify expects.
func (p *cliProvider) StructuredCall(ctx context.Context, req provider.StructuredRequest) (provider.StructuredResult, error) {
	prompt := strings.TrimSpace(req.SystemPrompt + "\n\n" + req.Prompt)
	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	bin := p.ins.Bin()
	args := []string{"--output-format", "json", "--print", prompt}
	if len(p.ins.ExtraArgs) > 0 {
		args = append(p.ins.ExtraArgs, args...)
	}
	start := time.Now()
	out, err := exec.CommandContext(cctx, bin, args...).Output()
	usage := provider.Usage{LatencyMs: time.Since(start).Milliseconds()}
	if err != nil {
		return provider.StructuredResult{Raw: string(out), OK: false, Error: err.Error(), Usage: usage}, nil
	}

	// claude --output-format json wraps the actual assistant response
	// in {"result": "...", "usage": {...}}. Best-effort unwrap.
	var wire map[string]any
	if jerr := json.Unmarshal(out, &wire); jerr == nil {
		if r, ok := wire["result"].(string); ok {
			parsed, perr := tryParseJSONObject(r)
			if perr != nil {
				return provider.StructuredResult{Raw: r, OK: false, Error: perr.Error(), Usage: usage}, nil
			}
			return provider.StructuredResult{Raw: r, Parsed: parsed, OK: true, Usage: usage}, nil
		}
	}
	// Fallback — treat whole stdout as raw JSON object.
	parsed, perr := tryParseJSONObject(string(out))
	if perr != nil {
		return provider.StructuredResult{Raw: string(out), OK: false, Error: perr.Error(), Usage: usage}, nil
	}
	return provider.StructuredResult{Raw: string(out), Parsed: parsed, OK: true, Usage: usage}, nil
}

// AgentCall runs the CLI with `--print` and returns stdout as text.
//
// Bounded by nonPoolSem so concurrent codex/gemini calls don't blow
// up the machine. Respects ctx.Done() while waiting for a slot. No
// hardcoded timeout — caller's ctx (engine.Run wraps with the
// workflow MaxDurationSec) carries the cancel signal end-to-end.
func (p *cliProvider) AgentCall(ctx context.Context, req provider.AgentRequest) (provider.AgentResult, error) {
	select {
	case nonPoolSem <- struct{}{}:
		defer func() { <-nonPoolSem }()
	case <-ctx.Done():
		return provider.AgentResult{}, ctx.Err()
	}
	bin := p.ins.Bin()
	args := append([]string(nil), p.ins.ExtraArgs...)
	args = append(args, "--print", req.Prompt)
	start := time.Now()
	out, err := exec.CommandContext(ctx, bin, args...).Output()
	usage := provider.Usage{LatencyMs: time.Since(start).Milliseconds()}
	if err != nil {
		return provider.AgentResult{Text: string(out), Usage: usage}, fmt.Errorf("%s: %w", p.ins.Name, err)
	}
	return provider.AgentResult{Text: strings.TrimSpace(string(out)), Usage: usage}, nil
}

// ListSkills returns the empty catalog — claude exposes skills via
// `~/.claude/skills/` discovery which is provider-specific; deferred
// for a follow-up adapter once a stable shape is needed.
func (p *cliProvider) ListSkills(ctx context.Context) ([]provider.Skill, error) {
	return []provider.Skill{}, nil
}

// tryParseJSONObject scans the input for a `{...}` slice and decodes
// it. Tolerates leading prose ("Here's the JSON:\n{...}").
func tryParseJSONObject(s string) (map[string]any, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, errors.New("empty output")
	}
	// Direct decode first.
	var out map[string]any
	if err := json.Unmarshal([]byte(s), &out); err == nil {
		return out, nil
	}
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start < 0 || end <= start {
		return nil, errors.New("no JSON object found in response")
	}
	if err := json.Unmarshal([]byte(s[start:end+1]), &out); err != nil {
		return nil, err
	}
	return out, nil
}
