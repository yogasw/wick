// Package tools is the central registry for every tool instance wick
// will mount. Downstream apps append to it via app.RegisterTool, and
// server.go walks All() at boot to validate, wire routes, and seed
// config rows.
//
// Three seed paths exist:
//   - init() seeds core tools that ship with every consumer (currently
//     encfields, since the MCP encrypt/decrypt redirects depend on it).
//   - RegisterBuiltins() seeds the in-house tools every downstream wick
//     app gets by default — currently the agents session manager. Called
//     from server.go / worker.go boot, before tools.All().
//   - RegisterLabSamples() seeds demo-only tools used by cmd/lab —
//     convert-text variants and the external-links grid. Downstream
//     apps never see these.
//
// To add a tool here:
//  1. Create internal/tools/<name>/ with a Register(r tool.Router) func
//     and, if the tool has runtime-editable config, a Config struct.
//  2. Append app.RegisterTool calls to RegisterBuiltins (default-on for
//     every wick app) or RegisterLabSamples (lab binary only).
package tools

import (
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/tags"
	pkgentity "github.com/yogasw/wick/pkg/entity"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	converttext "github.com/yogasw/wick/internal/tools/convert-text"
	agentstool "github.com/yogasw/wick/internal/tools/agents"
	providerstorage "github.com/yogasw/wick/internal/tools/provider-storage"
	"github.com/yogasw/wick/internal/tools/webtty"
	"github.com/yogasw/wick/internal/tools/encfields"
	"github.com/yogasw/wick/internal/tools/external"

	"github.com/yogasw/wick/pkg/tool"
)

// extra holds tool instances registered by downstream projects, plus
// the modules added by RegisterBuiltins / RegisterLabSamples. All()
// returns this slice verbatim.
var extra []tool.Module

// Register appends a fully-resolved Module record to the registry.
// Called from app.RegisterTool / app.RegisterToolNoConfig; do not call
// directly from app code.
func Register(m tool.Module) {
	extra = append(extra, m)
}

// init seeds tools that are part of wick's core surface and must be
// available to every downstream consumer, not just cmd/lab. encfields
// belongs here because the MCP layer's wick_encrypt / wick_decrypt
// meta-tools redirect to /tools/encfields — without it, those redirects
// 404 in any consumer binary.
func init() {
	extra = append(extra, tool.Module{
		Meta: tool.Tool{
			Key:               "encfields",
			Name:              "Encrypt / Decrypt",
			Description:       "Mint or reveal wick_enc_ tokens. Per-user keys — only you can decrypt your own tokens.",
			Icon:              "🔐",
			Category:          "Security",
			DefaultVisibility: entity.VisibilityPrivate,
			DefaultTags:       []tool.DefaultTag{tags.Security},
		},
		Register: encfields.Register,
	})
}

// RegisterBuiltins seeds in-house tools every downstream wick app gets
// by default. Called from internal/pkg/api/server.go (web) and
// internal/pkg/worker/server.go (worker) at boot, before tools.All().
//
// Idempotent on Meta.Key: re-calling appends nothing if the key was
// already registered (downstream main.go can also explicitly call
// app.RegisterTool with the same key without producing duplicates).
func RegisterBuiltins() {
	agentsConfigs := agentconfig.SeedGeneralConfig()
	agentsConfigs = append(agentsConfigs, agentconfig.SeedGateConfig()...)
	agentsConfigs = append(agentsConfigs, agentconfig.SeedSlackChannelConfig()...)
	agentsConfigs = append(agentsConfigs, agentconfig.SeedTelegramChannelConfig()...)
	agentsConfigs = append(agentsConfigs, agentconfig.SeedWorkspaceConfig()...)
	registerOnce(tool.Module{
		Meta: tool.Tool{
			Key:               "agents",
			Name:              "Agents",
			Description:       "Manage AI agent sessions, projects, and presets. Run Claude against your codebase in real-time.",
			Icon:              "✦",
			Category:          "AI",
			DefaultVisibility: entity.VisibilityPrivate,
			DefaultTags:       []tool.DefaultTag{tags.AI},
			FullScreen:        true,
		},
		Configs:  agentsConfigs,
		Register: agentstool.Register,
	})
	registerOnce(tool.Module{
		Meta: tool.Tool{
			Key:               "provider-storage",
			Name:              "Provider Storage",
			Description:       "Browse, restore, upload, and manage credential files synced from AI provider instances.",
			Icon:              "🗄",
			Category:          "System",
			DefaultVisibility: entity.VisibilityPrivate,
			DefaultTags:       []tool.DefaultTag{tags.System},
		},
		Register: providerstorage.Register,
	})
	registerOnce(tool.Module{
		Meta: tool.Tool{
			Key:               "webtty",
			Name:              "Web Terminal",
			Description:       "Browser-based terminal session. Requires gotty on PATH.",
			Icon:              ">_",
			Category:          "System",
			DefaultVisibility: entity.VisibilityPrivate,
			DefaultTags:       []tool.DefaultTag{tags.System},
		},
		Configs: pkgentity.StructToConfigs(webtty.Config{
			Enabled: true,
		}),
		Register: webtty.Register,
	})
}

// RegisterLabSamples seeds demo-only tools shipped with the cmd/lab
// binary — convert-text and the external-links grid. Downstream wick
// apps do not call this; their main.go registers the tools they need.
func RegisterLabSamples() {
	registerOnce(tool.Module{
		Meta: tool.Tool{
			Key:               "convert-text",
			Name:              "Convert Text",
			Description:       "Transform text between UPPERCASE, lowercase, Title Case, Sentence case, aLtErNaTiNg CaSe, or convert lines to/from literal \\n.",
			Icon:              "Aa",
			Category:          "Text",
			DefaultVisibility: entity.VisibilityPublic,
			DefaultTags:       []tool.DefaultTag{tags.Text},
		},
		Configs: pkgentity.StructToConfigs(converttext.Config{
			InitText: "hello world",
			InitType: "uppercase",
		}),
		Register: converttext.Register,
	})
	registerOnce(tool.Module{
		Meta: tool.Tool{
			Key:               "convert-text-alt",
			Name:              "Convert Text (Alt)",
			Description:       "Second instance of convert-text — same logic, different card. Useful as a template for per-team or per-purpose duplicates.",
			Icon:              "aA",
			Category:          "Text",
			DefaultVisibility: entity.VisibilityPublic,
			DefaultTags:       []tool.DefaultTag{tags.Text},
		},
		Configs: pkgentity.StructToConfigs(converttext.Config{
			InitText: "HELLO WORLD",
			InitType: "lowercase",
		}),
		Register: converttext.Register,
	})
	for _, e := range external.All() {
		registerOnce(e)
	}
}

// registerOnce is the internal de-dupe helper for the seed paths above.
// Web + worker both call RegisterBuiltins at boot, and a downstream
// main.go could legitimately re-register the same module key — neither
// case should produce duplicate rows in tools.All().
func registerOnce(m tool.Module) {
	for _, existing := range extra {
		if existing.Meta.Key == m.Meta.Key {
			return
		}
	}
	extra = append(extra, m)
}

// All returns every registered tool instance in registration order.
func All() []tool.Module {
	return extra
}
