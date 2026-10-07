package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	provider "github.com/yogasw/wick/internal/agents/provider"
)

// isolation.go keeps an opencode spawn to wick's MCP + the instance's own
// extras, and to a model someone actually chose.
//
// Config layers opencode merges (packages/opencode/src/config/config.ts):
// remote well-known (per auth entry — lives in the instance data dir),
// global Global.Path.config (moved per instance via XDG_CONFIG_HOME, see
// provider.OpencodeEnv), OPENCODE_CONFIG (blanked), project
// opencode.json(c) walking up from the cwd, `.opencode/` dirs up from the
// cwd and ~/.opencode (config/paths.ts), OPENCODE_CONFIG_DIR (blanked),
// then OPENCODE_CONFIG_CONTENT — ours, merged last.
//
// OPENCODE_DISABLE_PROJECT_CONFIG would drop the project layers, but it
// also drops the project's AGENTS.md rules (session/instruction.ts:81), so
// instead wick reads those files itself and, in its own last-merged layer,
// sets every MCP server they declare to enabled:false.

// jsoncComment strips // and /* */ comments outside strings; trailing
// commas are removed afterwards. Enough for config files, not a full JSONC.
var trailingComma = regexp.MustCompile(`,(\s*[}\]])`)

func stripJSONC(b []byte) []byte {
	var out []byte
	inStr, esc := false, false
	for i := 0; i < len(b); i++ {
		c := b[i]
		if inStr {
			out = append(out, c)
			if esc {
				esc = false
			} else if c == '\\' {
				esc = true
			} else if c == '"' {
				inStr = false
			}
			continue
		}
		if c == '"' {
			inStr = true
			out = append(out, c)
			continue
		}
		if c == '/' && i+1 < len(b) && b[i+1] == '/' {
			for i < len(b) && b[i] != '\n' {
				i++
			}
			out = append(out, '\n')
			continue
		}
		if c == '/' && i+1 < len(b) && b[i+1] == '*' {
			i += 2
			for i+1 < len(b) && !(b[i] == '*' && b[i+1] == '/') {
				i++
			}
			i++
			continue
		}
		out = append(out, c)
	}
	return trailingComma.ReplaceAll(out, []byte("$1"))
}

// configMCPNames returns the MCP server names one config file declares.
func configMCPNames(path string) []string {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var doc struct {
		MCP map[string]json.RawMessage `json:"mcp"`
	}
	if json.Unmarshal(stripJSONC(b), &doc) != nil {
		return nil
	}
	out := make([]string, 0, len(doc.MCP))
	for k := range doc.MCP {
		out = append(out, k)
	}
	return out
}

// foreignMCPNames lists every MCP server name the non-wick config layers
// would bring into a spawn in workspace: opencode.json(c) and
// .opencode/opencode.json(c) from workspace up to /, plus ~/.opencode.
func foreignMCPNames(workspace, home string) []string {
	seen := map[string]bool{}
	add := func(dir string) {
		for _, f := range []string{"opencode.json", "opencode.jsonc", filepath.Join(".opencode", "opencode.json"), filepath.Join(".opencode", "opencode.jsonc")} {
			for _, n := range configMCPNames(filepath.Join(dir, f)) {
				seen[n] = true
			}
		}
	}
	if workspace != "" {
		dir := filepath.Clean(workspace)
		for {
			add(dir)
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	if home != "" {
		for _, f := range []string{"opencode.json", "opencode.jsonc"} {
			for _, n := range configMCPNames(filepath.Join(home, ".opencode", f)) {
				seen[n] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// ErrNoModel is returned when a spawn has no model to pass.
var ErrNoModel = errors.New("no model")

// hostedProviders are opencode's own hosted services (Zen and its Go
// subscription, core/src/plugin/provider/opencode.ts): a model under one
// of these sends the conversation to opencode's servers.
var hostedProviders = []string{"opencode", "opencode-go"}

func isHosted(model string) bool {
	return slices.Contains(hostedProviders, modelProvider(model))
}

func modelProvider(model string) string {
	p, _, _ := strings.Cut(model, "/")
	return p
}

// listModels is the instance's live model list for the spawn-time default;
// swapped in tests. The cached / harvested list first (no process); the
// CLI only when nothing is known at all — a first spawn with no model
// configured — bounded so a hung CLI can't stall it.
var listModels = func(ctx context.Context, ins provider.Instance) ([]provider.ModelSeed, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if m, _, err := provider.CachedCLIModels(ctx, ins, false); err == nil && len(m) > 0 {
		return m, nil
	}
	return provider.ListCLIModels(ctx, ins)
}

// resolveModel picks the --model value, first hit wins:
//
//  1. --model/-m in the instance or spawn args (operator-set, like claude)
//  2. the session pin (composer model picker)
//  3. the instance default: opencode_model, else the first curated entry
//     of the Detail "Models" list (the composer's picker list)
//  4. the first model `opencode models` reports for a provider the
//     instance is logged in to (non-hosted providers first)
//
// opencode without --model silently uses its hosted default (opencode/…),
// sending the conversation to opencode's servers, so an empty result
// refuses the spawn. Hosted models (opencode/…, opencode-go/…) need
// opencode_allow_hosted — unless the instance logged in to opencode's own
// service, which is the user choosing it on purpose.
func resolveModel(ctx context.Context, ins provider.Instance, opt provider.SpawnOptions, args []string) (model string, fromArgs bool, err error) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case (a == "--model" || a == "-m") && i+1 < len(args):
			model, fromArgs = args[i+1], true
		case strings.HasPrefix(a, "--model="):
			model, fromArgs = strings.TrimPrefix(a, "--model="), true
		}
	}
	if !fromArgs {
		if m := provider.ModelArgs(opt, nil); len(m) == 2 {
			model = m[1]
		} else {
			model = instanceDefault(ctx, ins)
		}
	}
	logins := loggedInProviders(ins)
	hostedOK := (ins.OpencodeConfig != nil && ins.OpencodeConfig.AllowHosted) || loggedInHosted(ins, logins)
	if model == "" && len(logins) > 0 {
		model = firstLoggedInModel(ctx, ins, logins, hostedOK)
	}
	if model == "" {
		if len(logins) == 0 {
			return "", false, fmt.Errorf("opencode instance %s: log in first (Providers → Connection) or pick a model (opencode_model) — refusing to fall back to opencode's hosted default: %w", ins.Name, ErrNoModel)
		}
		return "", false, fmt.Errorf("opencode instance %s: no model found for the logged-in provider (%s); pick one (opencode_model, e.g. openai/gpt-5.5) — without one opencode silently uses its hosted default: %w", ins.Name, strings.Join(logins, ", "), ErrNoModel)
	}
	if isHosted(model) && !hostedOK {
		return "", false, fmt.Errorf("opencode instance %s: model %s is opencode's hosted service (conversation goes to opencode's servers); enable opencode_allow_hosted on the instance to allow it", ins.Name, model)
	}
	return model, fromArgs, nil
}

// instanceDefault is the instance's own default model. In live mode
// (Model selection → Live from CLI) that is the pinned live default when the
// CLI still lists it, else the first model the live filter matches
// (provider.LiveDefaultModel, cached ~10 min). Otherwise, or when live
// yields nothing: opencode_model, else the first curated Models entry. The
// per-type seed list is not a choice anyone made, so it never counts.
func instanceDefault(ctx context.Context, ins provider.Instance) string {
	if provider.LiveModelsEnabled(ins) {
		lctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		m := provider.LiveDefaultModel(lctx, ins)
		cancel()
		if m != "" {
			return m
		}
	}
	if ins.OpencodeConfig != nil {
		if m := strings.TrimSpace(ins.OpencodeConfig.Model); m != "" {
			return m
		}
	}
	if ins.ModelSelect {
		for _, m := range ins.Models {
			if id := strings.TrimSpace(m.ID); id != "" {
				return id
			}
		}
	}
	return ""
}

// firstLoggedInModel returns the first live model under a provider the
// instance holds a login for. Non-hosted providers win over hosted ones;
// hosted ones only count when hostedOK. Without a login filter, a keyless
// opencode still lists Zen's free models, which nobody chose.
func firstLoggedInModel(ctx context.Context, ins provider.Instance, logins []string, hostedOK bool) string {
	seeds, err := listModels(ctx, ins)
	if err != nil {
		log.Warn().Err(err).Str("instance", ins.Name).Msg("agents.spawn: opencode models failed; no default model")
		return ""
	}
	var hosted string
	for _, s := range seeds {
		p := modelProvider(s.ID)
		if !slices.Contains(logins, p) {
			continue
		}
		if !isHosted(s.ID) {
			return s.ID
		}
		if hostedOK && hosted == "" {
			hosted = s.ID
		}
	}
	return hosted
}

// loggedInHosted reports whether the instance logged in to opencode's own
// service: an auth.json entry for it, or OPENCODE_API_KEY in its env.
func loggedInHosted(ins provider.Instance, logins []string) bool {
	for _, p := range hostedProviders {
		if slices.Contains(logins, p) {
			return true
		}
	}
	for _, e := range ins.Env {
		if k, v, ok := strings.Cut(e, "="); ok && k == "OPENCODE_API_KEY" && strings.TrimSpace(v) != "" {
			return true
		}
	}
	return false
}

// loggedInProviders lists the provider ids the instance's auth.json holds
// a login for, sorted. An OPENCODE_API_KEY in the instance env counts as
// an "opencode" login (opencode.ts reads it the same way).
func loggedInProviders(ins provider.Instance) []string {
	var out []string
	if p, err := provider.OpencodeAuthFile(ins); err == nil {
		if b, err := os.ReadFile(p); err == nil {
			var m map[string]json.RawMessage
			if json.Unmarshal(b, &m) == nil {
				for k := range m {
					out = append(out, k)
				}
			}
		}
	}
	for _, e := range ins.Env {
		if k, v, ok := strings.Cut(e, "="); ok && k == "OPENCODE_API_KEY" && strings.TrimSpace(v) != "" && !slices.Contains(out, "opencode") {
			out = append(out, "opencode")
		}
	}
	sort.Strings(out)
	return out
}
