package airouter

import (
	"fmt"
	"os"
	"strings"

	"github.com/yogasw/wick/internal/agents/provider"
)

// Init wires the provider-side spawn injection so agent spawners can route
// through a router without importing this package (which would be an import
// cycle — airouter imports provider). Call once at boot, after RegisterBuiltins.
func Init() {
	provider.SetRouterSpawn(spawnContribution)
	provider.SetRouterSlots(routerSlots)
	provider.SetRouterKeyResolver(func(ins *provider.Instance) string {
		rt, ok := Get(routerID(ins.AIRouterProvider))
		if !ok {
			return ""
		}
		return resolveKey(rt, *ins)
	})
}

// spawnContribution resolves an instance's selected router and builds the CLI
// args + env. When the user set a raw-config override it is used verbatim (a
// full manual replacement of the generated config); otherwise the router's
// SpawnHook generates it. Backs provider.RouterSpawnContribution.
func spawnContribution(ins *provider.Instance, t provider.Type) (provider.RouterContribution, error) {
	id := routerID(ins.AIRouterProvider)
	rt, ok := Get(id)
	if !ok {
		return provider.RouterContribution{}, fmt.Errorf("airouter: unknown router %q", id)
	}
	if rt.Desc.Hook == nil {
		return provider.RouterContribution{}, fmt.Errorf("airouter: %s cannot route spawns", id)
	}
	key := resolveKey(rt, *ins)

	// Full manual override: the user edited the effective config in the UI, so
	// use it VERBATIM — a true replacement of the generated config, not an
	// append. The box was seeded (admin-only view) with the real config
	// including the resolved key, so it carries everything: base URL, models,
	// provider wiring, and the auth token. Removing a line unsets it. Skips the
	// hook + base-URL generation entirely — this IS the config.
	if raw := strings.TrimSpace(ins.AIRouterRawConfig); raw != "" {
		args, env := parseRawConfig(raw)
		return provider.RouterContribution{Args: args, Env: env}, nil
	}

	base := baseURL(id)
	if base == "" {
		return provider.RouterContribution{}, fmt.Errorf("airouter: WICK_PORT unset — cannot resolve %s proxy base URL", id)
	}
	args, env, err := rt.Desc.Hook.Contribute(t, *ins, base, key)
	if err != nil {
		return provider.RouterContribution{}, err
	}
	return provider.RouterContribution{Args: args, Env: env}, nil
}

// parseRawConfig turns the editable config box back into CLI args + env, keying
// off each line's prefix so it round-trips the exact shape the preview renders
// (see aiRouterConfigPreview): "-c key=val" → a codex -c override, "--flag val"
// → a CLI flag, everything else → a KEY=VALUE env var. Blank lines and
// #comments are skipped. Type-agnostic — the line prefix carries the intent.
func parseRawConfig(raw string) (args, env []string) {
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		switch {
		case strings.HasPrefix(line, "-c "):
			args = append(args, "-c", strings.TrimSpace(line[len("-c "):]))
		case strings.HasPrefix(line, "--"):
			if flag, val, ok := strings.Cut(line, " "); ok {
				args = append(args, flag, strings.TrimSpace(val))
			} else {
				args = append(args, line)
			}
		default:
			env = append(env, line)
		}
	}
	return args, env
}

// routerSlots backs provider.RouterSlots.
func routerSlots(id string, t provider.Type) []provider.RouterSlot {
	rt, ok := Get(routerID(id))
	if !ok || rt.Desc.Hook == nil {
		return nil
	}
	return rt.Desc.Hook.Slots(t)
}

// routerID resolves the effective router id, defaulting to 9router (for
// back-compat with instances configured before the provider field existed),
// then to the first registered router.
func routerID(id string) string {
	if id != "" {
		return id
	}
	for _, rid := range IDs() {
		if rid == "9router" {
			return rid
		}
	}
	if ids := IDs(); len(ids) > 0 {
		return ids[0]
	}
	return "9router"
}

// baseURL returns the wick-origin API base for router id, e.g.
// "http://127.0.0.1:9425/airouter/9router/v1". Empty when WICK_PORT is unset.
func baseURL(id string) string {
	port := strings.TrimSpace(os.Getenv("WICK_PORT"))
	if port == "" {
		return ""
	}
	return "http://127.0.0.1:" + port + "/airouter/" + id + "/v1"
}

// resolveKey returns the plaintext API key for an instance: the decrypted
// custom key when set, else the router's default. Decryption failure falls
// back to the default rather than leaking the raw token.
func resolveKey(rt *Router, ins provider.Instance) string {
	def := ""
	if rt.Desc.Hook != nil {
		def = rt.Desc.Hook.DefaultKey()
	}
	tok := strings.TrimSpace(ins.AIRouterAPIKey)
	if tok == "" {
		return def
	}
	if secretDecrypter == nil {
		return tok
	}
	plain, err := secretDecrypter(tok)
	if err != nil || strings.TrimSpace(plain) == "" {
		return def
	}
	return plain
}

// secretDecrypter turns a stored wick_cenc_/wick_enc_ token back into
// plaintext. nil until wired; when nil, tokens pass through unchanged (safe
// for dev/tests where encryption is disabled).
var secretDecrypter func(string) (string, error)

// SetSecretDecrypter wires the boot-time secret decrypter used to unwrap a
// stored router API key at spawn. Backed by configs.Service.DecryptSecret.
func SetSecretDecrypter(fn func(string) (string, error)) { secretDecrypter = fn }
