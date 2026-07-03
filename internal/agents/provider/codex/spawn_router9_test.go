package codex

import (
	"strings"
	"testing"

	provider "github.com/yogasw/wick/internal/agents/provider"
)

// TestRouter9Args verifies the codex 9router arg/env builder: provider
// wiring + concrete --model injected, key exported as env (never argv),
// and missing / "auto" models refused.
func TestRouter9Args(t *testing.T) {
	t.Setenv("WICK_PORT", "9425")

	t.Run("off when Use9router false", func(t *testing.T) {
		args, env, err := router9Args(&provider.Instance{})
		if err != nil || args != nil || env != nil {
			t.Fatalf("expected all-nil when off, got args=%v env=%v err=%v", args, env, err)
		}
	})

	t.Run("nil instance", func(t *testing.T) {
		if a, e, err := router9Args(nil); a != nil || e != nil || err != nil {
			t.Fatalf("nil instance should be inert, got %v %v %v", a, e, err)
		}
	})

	t.Run("injects wiring; unset subagent is skipped", func(t *testing.T) {
		args, env, err := router9Args(&provider.Instance{
			Type:          provider.TypeCodex,
			Use9router:    true,
			Router9Models: map[string]string{"model": "cc/claude-opus-4-6"},
		})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		joined := strings.Join(args, " ")
		for _, want := range []string{
			"--model cc/claude-opus-4-6",
			"model_provider=9router",
			"model_providers.9router.base_url='http://127.0.0.1:9425/9router/v1'",
			"model_providers.9router.wire_api=responses",
			"auth_mode=apikey",
		} {
			if !strings.Contains(joined, want) {
				t.Errorf("args missing %q\n  got: %s", want, joined)
			}
		}
		// Unset subagent slot must NOT emit agents.subagent.model.
		if strings.Contains(joined, "agents.subagent.model") {
			t.Errorf("unset subagent should be skipped, got: %s", joined)
		}
		if strings.Contains(joined, "--model auto") {
			t.Errorf("args must never contain --model auto: %s", joined)
		}
		if len(env) != 1 || !strings.HasPrefix(env[0], "OPENAI_API_KEY=") {
			t.Errorf("env should carry OPENAI_API_KEY, got %v", env)
		}
		if strings.Contains(joined, "sk_9router") {
			t.Errorf("key must never appear in argv: %s", joined)
		}
	})

	t.Run("explicit subagent slot", func(t *testing.T) {
		args, _, err := router9Args(&provider.Instance{
			Type:          provider.TypeCodex,
			Use9router:    true,
			Router9Models: map[string]string{"model": "cc/main", "subagent": "cc/sub"},
		})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if !strings.Contains(strings.Join(args, " "), "agents.subagent.model='cc/sub'") {
			t.Errorf("explicit subagent not used: %v", args)
		}
	})

	t.Run("allows auto primary (user combo)", func(t *testing.T) {
		args, _, err := router9Args(&provider.Instance{Type: provider.TypeCodex, Use9router: true, Router9Models: map[string]string{"model": "auto"}})
		if err != nil {
			t.Fatalf("auto combo should be allowed: %v", err)
		}
		if !strings.Contains(strings.Join(args, " "), "--model auto") {
			t.Errorf("expected --model auto in args: %v", args)
		}
	})

	t.Run("empty model is allowed and omits --model", func(t *testing.T) {
		args, env, err := router9Args(&provider.Instance{Type: provider.TypeCodex, Use9router: true})
		if err != nil {
			t.Fatalf("empty model should be allowed: %v", err)
		}
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "--model") {
			t.Errorf("empty model should omit --model, got: %s", joined)
		}
		// Provider wiring + key still present.
		if !strings.Contains(joined, "model_provider=9router") {
			t.Errorf("provider wiring missing: %s", joined)
		}
		if len(env) != 1 || !strings.HasPrefix(env[0], "OPENAI_API_KEY=") {
			t.Errorf("env should carry OPENAI_API_KEY, got %v", env)
		}
	})

	t.Run("errors when WICK_PORT unset", func(t *testing.T) {
		t.Setenv("WICK_PORT", "")
		if _, _, err := router9Args(&provider.Instance{Type: provider.TypeCodex, Use9router: true, Router9Models: map[string]string{"model": "cc/x"}}); err == nil {
			t.Fatal("expected error when WICK_PORT unset")
		}
	})
}
