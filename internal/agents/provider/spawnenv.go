package provider

import "strings"

// MaskSpawnEnv returns a copy of the KEY=VALUE env entries with
// secret-looking values masked, safe to write to the spawn log / show in
// the Backends UI. A value is masked when its key name suggests a
// credential (KEY/TOKEN/SECRET/PASSWORD/AUTH). Non-secret entries pass
// through so operators can still verify routing (e.g. ANTHROPIC_BASE_URL).
func MaskSpawnEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, e := range env {
		k, v, ok := strings.Cut(e, "=")
		if !ok {
			out = append(out, e)
			continue
		}
		if isSecretEnvKey(k) && v != "" {
			out = append(out, k+"="+maskValue(v))
			continue
		}
		out = append(out, e)
	}
	return out
}

func isSecretEnvKey(k string) bool {
	u := strings.ToUpper(k)
	for _, needle := range []string{"KEY", "TOKEN", "SECRET", "PASSWORD", "AUTH"} {
		if strings.Contains(u, needle) {
			return true
		}
	}
	return false
}

// maskValue keeps the first and last char, masking the middle so the
// operator can recognize which secret is set without exposing it:
// "sk_9router" → "s********r". Values ≤2 chars are fully masked.
func maskValue(v string) string {
	r := []rune(v)
	if len(r) <= 2 {
		return strings.Repeat("*", len(r))
	}
	return string(r[0]) + strings.Repeat("*", len(r)-2) + string(r[len(r)-1])
}
