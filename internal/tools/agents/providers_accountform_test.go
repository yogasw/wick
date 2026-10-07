package agents

import (
	"testing"

	"github.com/yogasw/wick/internal/agents/provider"
)

// Submitting only the data dir must not wipe the saved model: without
// one, opencode spawns refuse to run.
func TestApplyAccountFormKeepsOpencodeModel(t *testing.T) {
	_, c := adminCtx(t, "/tools/agents/providers?opencode_data_dir=/data/oc", nil)
	ins := provider.Instance{Type: provider.TypeOpencode, Name: "oc",
		OpencodeConfig: &provider.OpencodeConfig{DataDir: "/old", Model: "openai/gpt-5", AllowHosted: true}}
	if msg := applyAccountForm(&ins, c); msg != "" {
		t.Fatal(msg)
	}
	got := ins.OpencodeConfig
	if got.DataDir != "/data/oc" || got.Model != "openai/gpt-5" || !got.AllowHosted {
		t.Fatalf("config = %+v, want new data dir with model kept", got)
	}

	fresh := provider.Instance{Type: provider.TypeOpencode, Name: "new"}
	if msg := applyAccountForm(&fresh, c); msg != "" || fresh.OpencodeConfig == nil || fresh.OpencodeConfig.DataDir != "/data/oc" {
		t.Fatalf("new instance: %q %+v", msg, fresh.OpencodeConfig)
	}
}
