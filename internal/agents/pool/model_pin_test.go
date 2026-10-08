package pool

import (
	"context"
	"testing"

	"github.com/yogasw/wick/internal/agents/provider"
)

type pinSets struct{}

func (pinSets) Sets(context.Context, provider.Instance) ([]provider.ModelChoice, error) {
	return nil, nil
}

func (pinSets) Expand(context.Context, provider.Instance, []string) ([]provider.ModelChoice, error) {
	return nil, nil
}

func (pinSets) Resolve(provider.Instance, []string, string) (provider.SpawnPin, error) {
	return provider.SpawnPin{}, nil
}

func TestDropForeignModelPin(t *testing.T) {
	provider.RegisterModelSets("pintest", pinSets{})
	cases := []struct {
		name, prov, pin string
		want            bool
	}{
		{"empty pin", "claude/work", "", false},
		{"wick provider keeps registry id", "wick/main", "m_0370951f-68d", false},
		{"flat cli model", "claude/work", "opus", false},
		{"registry id on a flat provider", "claude/work", "m_0370951f-68d", true},
		{"leaf pin on a flat provider", "claude/work", "entry@gpt-5", true},
		{"grouped pin on a grouped provider", "pintest/yoga", "opencode-zen@opencode-zen/claude-opus-5-5", false},
		{"registry id on a grouped provider", "pintest/yoga", "m_0370951f-68d", true},
	}
	for _, c := range cases {
		if got := dropForeignModelPin(c.prov, c.pin); got != c.want {
			t.Errorf("%s: dropForeignModelPin(%q, %q) = %v, want %v", c.name, c.prov, c.pin, got, c.want)
		}
	}
}
