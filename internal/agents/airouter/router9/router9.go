// Package router9 registers the 9router backend with airouter. 9router ships
// as an npm package (`npm i -g 9router`) and serves a web dashboard on a local
// port; it exposes an OpenAI-compatible /v1 API. init() registers it, so a
// blank-import of this package wires it into the registry.
package router9

import (
	"strconv"
	"strings"

	"github.com/yogasw/wick/internal/agents/airouter"
	"github.com/yogasw/wick/internal/agents/provider"
)

func init() {
	airouter.Register(airouter.Descriptor{
		ID:          "9router",
		DisplayName: "9router",
		Blurb:       "Install, run, and manage the 9router dashboard — embedded here, no extra exposed port.",
		NpmPackage:  "9router",
		BinName:     "9router",
		PrefPort:    20128,
		// /manifest.webmanifest → {"name":"9Router - AI Infrastructure ...","short_name":"9Router"}.
		IdentitySubstr: "9router",
		IconSVG:        iconSVG,
		Launch:      launch,
		Hook:        hook{},
	})
}

// iconSVG is the inner markup for the switcher tile (rendered inside an <svg>).
const iconSVG = `<circle cx="4" cy="4" r="2"></circle><circle cx="4" cy="12" r="2"></circle><path d="M13 4v3a2 2 0 0 1-2 2H6M6 6l-2 2 2 2" stroke-linecap="round" stroke-linejoin="round"></path>`

// launch builds the 9router CLI args for a chosen port: bind loopback, no
// browser, log to stdout (so wick can tail it), skip the interactive update
// check (which would otherwise make the detached process exit early).
func launch(port int) (args, env []string) {
	return []string{
		"--port", strconv.Itoa(port),
		"--host", "127.0.0.1",
		"--no-browser",
		"--log",
		"--skip-update",
	}, nil
}

// defaultKey is 9router's documented default credential — a bare "sk_9router"
// is accepted when the instance sets no custom key.
const defaultKey = "sk_9router"

// hook implements airouter.SpawnHook for 9router. It absorbs the per-agent-type
// wiring: claude via Anthropic gateway env, codex via -c model_provider args.
type hook struct{}

func (hook) DefaultKey() string { return defaultKey }

func (hook) Slots(t provider.Type) []provider.RouterSlot {
	switch t {
	case provider.TypeClaude:
		return []provider.RouterSlot{
			{Key: "opus", Label: "Claude Opus", Placeholder: "cc/claude-opus-4-6"},
			{Key: "sonnet", Label: "Claude Sonnet", Placeholder: "cc/claude-sonnet-4-6"},
			{Key: "haiku", Label: "Claude Haiku", Placeholder: "cc/claude-haiku-4-5"},
		}
	case provider.TypeCodex:
		return []provider.RouterSlot{
			{Key: "model", Label: "Model", Placeholder: "provider/model-id"},
			{Key: "subagent", Label: "Subagent Model", Placeholder: "defaults to main model"},
		}
	default:
		return nil
	}
}

// Contribute returns the CLI args + env an agent needs to route through
// 9router. Models are optional — an unset slot is omitted so 9router applies
// its own default. The key goes to env, never argv (argv is logged).
func (hook) Contribute(t provider.Type, ins provider.Instance, base, key string) (args, env []string, err error) {
	switch t {
	case provider.TypeClaude:
		// ANTHROPIC_AUTH_TOKEN (NOT _API_KEY — that is for direct Anthropic)
		// carries the 9router key; per-tier models map via ANTHROPIC_DEFAULT_*.
		env = []string{
			"ANTHROPIC_BASE_URL=" + base,
			"ANTHROPIC_AUTH_TOKEN=" + key,
		}
		if m := strings.TrimSpace(ins.AIRouterModels["opus"]); m != "" {
			env = append(env, "ANTHROPIC_DEFAULT_OPUS_MODEL="+m)
		}
		if m := strings.TrimSpace(ins.AIRouterModels["sonnet"]); m != "" {
			env = append(env, "ANTHROPIC_DEFAULT_SONNET_MODEL="+m)
		}
		if m := strings.TrimSpace(ins.AIRouterModels["haiku"]); m != "" {
			env = append(env, "ANTHROPIC_DEFAULT_HAIKU_MODEL="+m)
		}
		return nil, env, nil
	case provider.TypeCodex:
		// auth_mode=apikey + OPENAI_API_KEY (codex's default env key for the
		// provider). base_url is a TOML literal so Windows paths / colons pass
		// through verbatim.
		args = []string{
			"-c", "model_provider=9router",
			"-c", "model_providers.9router.name=9Router",
			"-c", "model_providers.9router.base_url=" + tomlLiteral(base),
			"-c", "model_providers.9router.wire_api=responses",
			"-c", "auth_mode=apikey",
		}
		if m := strings.TrimSpace(ins.AIRouterModels["model"]); m != "" {
			args = append(args, "--model", m)
		}
		if sub := strings.TrimSpace(ins.AIRouterModels["subagent"]); sub != "" {
			args = append(args, "-c", "agents.subagent.model="+tomlLiteral(sub))
			args = append(args, "-c", "agents.subagent.description="+tomlLiteral("Subagent model routed through 9router"))
		}
		env = []string{"OPENAI_API_KEY=" + key}
		return args, env, nil
	default:
		return nil, nil, nil
	}
}

// tomlLiteral wraps s in a TOML literal string (single quotes) so backslashes
// and other escape-prone chars pass through verbatim.
func tomlLiteral(s string) string {
	if !strings.Contains(s, "'") {
		return "'" + s + "'"
	}
	return `"` + strings.ReplaceAll(s, `\`, `\\`) + `"`
}
