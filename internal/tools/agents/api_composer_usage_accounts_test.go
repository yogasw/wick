package agents

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/provider/logintty"
)

// opencode with two account folders logged in to openai: the rows come
// out of the one cached reading, and the session's pin marks its folder.
func TestComposerUsageAccountsMarksSessionAccount(t *testing.T) {
	data := t.TempDir()
	// Synthetic, non-credential values only.
	for dir, body := range map[string]string{
		filepath.Join(data, "opencode"):                   `{"openai":{"type":"oauth","access":"x"},"openrouter":{"type":"api","key":"x"}}`,
		filepath.Join(data, "accounts", "a2", "opencode"): `{"openai":{"type":"oauth","access":"y"}}`,
	} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ins := provider.Instance{Type: provider.TypeOpencode, Name: "oc", OpencodeConfig: &provider.OpencodeConfig{DataDir: data}}
	v := usageView{Known: true, Windows: []logintty.UsageWindow{
		{Key: "five_hour", Utilization: 10, Account: "main/openai"},
		{Key: "five_hour", Utilization: 80, Account: "a2/openai"},
	}}

	rows, rotation := composerUsageAccounts(ins, "openai/a2@openai/gpt-5", v)
	if len(rows) != 3 || rotation != "" {
		t.Fatalf("rows=%+v rotation=%q", rows, rotation)
	}
	byID := map[string]composerUsageAccountRow{}
	for _, r := range rows {
		byID[r.ID] = r
	}
	if !byID["a2/openai"].Current || byID["main/openai"].Current {
		t.Fatalf("current must be the pinned folder: %+v", rows)
	}
	if w := byID["a2/openai"].Windows; len(w) != 1 || w[0].Utilization != 80 {
		t.Fatalf("a2 windows = %+v", w)
	}
	if !byID["main/openrouter"].NoUsage || byID["main/openai"].NoUsage {
		t.Fatalf("no_usage flags: %+v", rows)
	}

	// No pin: Auto — nothing is marked, wick rotates.
	rows, rotation = composerUsageAccounts(ins, "", v)
	for _, r := range rows {
		if r.Current {
			t.Fatalf("auto marked %s current", r.ID)
		}
	}
	if rotation != "wick" {
		t.Fatalf("rotation = %q", rotation)
	}

	// Single-account types get no rows.
	if rows, _ := composerUsageAccounts(provider.Instance{Type: provider.TypeClaude}, "", v); rows != nil {
		t.Fatalf("claude rows = %+v", rows)
	}
}
