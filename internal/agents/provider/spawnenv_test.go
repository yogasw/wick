package provider

import "testing"

func TestMaskSpawnEnv(t *testing.T) {
	got := MaskSpawnEnv([]string{
		"ANTHROPIC_BASE_URL=http://127.0.0.1:9425/9router/v1",
		"OPENAI_API_KEY=sk_9router",
		"ANTHROPIC_AUTH_TOKEN=abcdef",
		"PLAIN=value",
		"SHORT_TOKEN=ab",
		"NOEQ",
	})
	want := map[string]string{
		// non-secret keys pass through untouched
		"ANTHROPIC_BASE_URL":   "http://127.0.0.1:9425/9router/v1",
		"PLAIN":                "value",
		// secret keys: first+last kept, middle masked
		"OPENAI_API_KEY":       "s********r",
		"ANTHROPIC_AUTH_TOKEN": "a****f",
		// ≤2 chars fully masked
		"SHORT_TOKEN":          "**",
	}
	for _, e := range got {
		k, v, ok := cut(e)
		if !ok {
			if e != "NOEQ" {
				t.Errorf("no-eq entry mangled: %q", e)
			}
			continue
		}
		if w, exists := want[k]; exists && v != w {
			t.Errorf("%s = %q, want %q", k, v, w)
		}
	}
}

func cut(s string) (string, string, bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == '=' {
			return s[:i], s[i+1:], true
		}
	}
	return s, "", false
}
