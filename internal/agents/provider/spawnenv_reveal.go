package provider

import "strings"

// UnmaskSpawnEnv returns a copy of storedEnv (the masked env captured in the
// spawn log) with secret values replaced by their live plaintext, resolved
// from the current instance config keyed by env KEY. Non-secret entries and
// unresolvable secrets pass through verbatim — order and every non-secret
// entry (including the per-spawn MAX_THINKING_TOKENS) are preserved exactly.
//
// It never reconstructs the env from scratch; it only unmasks in place. When
// the instance no longer exists (Find fails) it returns storedEnv unchanged —
// a stale spawn log must never fail the page it renders on.
func UnmaskSpawnEnv(t Type, name string, storedEnv []string) []string {
	ins, err := Find(t, name)
	if err != nil {
		return storedEnv
	}

	// live maps a secret env KEY to its current plaintext value.
	live := map[string]string{}
	for _, e := range ins.Env {
		k, v, ok := strings.Cut(e, "=")
		if ok && isSecretEnvKey(k) {
			live[k] = v
		}
	}
	// The 9router routing keys are what actually reaches the CLI, and they
	// differ from ins.Env — resolve them from the router9 auth key.
	if key := Router9AuthKey(ins); key != "" {
		live["ANTHROPIC_AUTH_TOKEN"] = key
		live["OPENAI_API_KEY"] = key
	}

	out := make([]string, 0, len(storedEnv))
	for _, e := range storedEnv {
		k, _, ok := strings.Cut(e, "=")
		if ok && isSecretEnvKey(k) {
			if v, has := live[k]; has && v != "" {
				out = append(out, k+"="+v)
				continue
			}
		}
		out = append(out, e)
	}
	return out
}
