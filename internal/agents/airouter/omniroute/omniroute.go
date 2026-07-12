// Package omniroute registers the OmniRoute backend with airouter. OmniRoute
// (https://github.com/diegosouzapw/OmniRoute) ships as an npm package
// (`npm i -g omniroute`) and serves a gateway + dashboard on a local port with
// an OpenAI-compatible /v1 endpoint that also translates the Anthropic and
// Gemini wire formats. init() registers it, so a blank-import wires it in.
package omniroute

import (
	"strconv"
	"strings"

	"github.com/yogasw/wick/internal/agents/airouter"
	"github.com/yogasw/wick/internal/agents/provider"
)

func init() {
	airouter.Register(airouter.Descriptor{
		ID:          "omniroute",
		DisplayName: "OmniRoute",
		Blurb:       "Install, run, and manage the OmniRoute gateway — aggregate many AI providers behind one embedded dashboard.",
		NpmPackage:  "omniroute",
		BinName:     "omniroute",
		// OmniRoute also defaults to 20128; airouter remaps to the next free
		// loopback port when 9router already holds it, so both run concurrently.
		PrefPort: 20128,
		// Distinguishes an externally-started OmniRoute from a 9router on the
		// same default port via /manifest.webmanifest →
		// {"name":"OmniRoute AI Gateway","short_name":"OmniRoute"}.
		IdentitySubstr: "omniroute",
		// OmniRoute's client-side routes that would otherwise escape the mount
		// prefix and hit the wick root (e.g. /home → wick's own /home). Common
		// ones (/dashboard, /providers, /endpoints, /login, …) are in
		// baseRewritePrefixes; these are OmniRoute-specific top-level pages.
		RoutePrefixes: []string{
			"/home", "/settings", "/logs", "/usage", "/combos",
			"/keys", "/analytics", "/models", "/playground",
		},
		IconSVG: iconSVG,
		Launch:  launch,
		Hook:    hook{},
	})
}

const iconSVG = `<circle cx="8" cy="8" r="2.2"></circle><circle cx="3" cy="3" r="1.6"></circle><circle cx="13" cy="3" r="1.6"></circle><circle cx="3" cy="13" r="1.6"></circle><circle cx="13" cy="13" r="1.6"></circle><path d="M6.4 6.4 4 4M9.6 6.4 12 4M6.4 9.6 4 12M9.6 9.6 12 12" stroke-linecap="round"></path>`

// launch configures OmniRoute's listen port. OmniRoute reads the port from the
// PORT env var (there is no documented --port flag), so we pass it as env and
// leave args empty. BROWSER=none suppresses any auto-open of a browser tab.
func launch(port int) (args, env []string) {
	return nil, []string{
		"PORT=" + strconv.Itoa(port),
		"BROWSER=none",
		"NO_UPDATE_NOTIFIER=1",
	}
}

// hook implements airouter.SpawnHook for OmniRoute. OmniRoute is
// OpenAI-compatible at /v1 and translates the Anthropic/Gemini formats, so the
// wiring mirrors 9router but points at the /airouter/omniroute/v1 base. Unlike
// 9router there is no bare default key — the user copies one from the OmniRoute
// dashboard (Endpoints) and sets it in the provider config.
type hook struct{}

func (hook) DefaultKey() string { return "" }

func (hook) Slots(t provider.Type) []provider.RouterSlot {
	switch t {
	case provider.TypeClaude:
		return []provider.RouterSlot{
			{Key: "opus", Label: "Claude Opus", Placeholder: "auto"},
			{Key: "sonnet", Label: "Claude Sonnet", Placeholder: "auto"},
			{Key: "haiku", Label: "Claude Haiku", Placeholder: "auto"},
		}
	case provider.TypeCodex:
		return []provider.RouterSlot{
			{Key: "model", Label: "Model", Placeholder: "auto"},
			{Key: "subagent", Label: "Subagent Model", Placeholder: "defaults to main model"},
		}
	default:
		return nil
	}
}

func (hook) Contribute(t provider.Type, ins provider.Instance, base, key string) (args, env []string, err error) {
	switch t {
	case provider.TypeClaude:
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
		// OmniRoute exposes the OpenAI Responses API at /v1 (it translates
		// chat/Anthropic/Gemini underneath). Codex 0.x dropped wire_api="chat"
		// ("no longer supported"), so it must be "responses".
		args = []string{
			"-c", "model_provider=omniroute",
			"-c", "model_providers.omniroute.name=OmniRoute",
			"-c", "model_providers.omniroute.base_url=" + tomlLiteral(base),
			"-c", "model_providers.omniroute.wire_api=responses",
			"-c", "auth_mode=apikey",
		}
		if m := strings.TrimSpace(ins.AIRouterModels["model"]); m != "" {
			args = append(args, "--model", m)
		}
		if sub := strings.TrimSpace(ins.AIRouterModels["subagent"]); sub != "" {
			args = append(args, "-c", "agents.subagent.model="+tomlLiteral(sub))
			args = append(args, "-c", "agents.subagent.description="+tomlLiteral("Subagent model routed through OmniRoute"))
		}
		env = []string{"OPENAI_API_KEY=" + key}
		return args, env, nil
	default:
		return nil, nil, nil
	}
}

func tomlLiteral(s string) string {
	if !strings.Contains(s, "'") {
		return "'" + s + "'"
	}
	return `"` + strings.ReplaceAll(s, `\`, `\\`) + `"`
}
