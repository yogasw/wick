package slack

import (
	"testing"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
)

func TestShortcutActionMapping(t *testing.T) {
	var empty agentconfig.SlackChannelConfig
	if got := shortcutAction(empty, "session_secret"); got != ShortcutActionSessionSecret {
		t.Fatalf("default key: got %q", got)
	}
	if got := shortcutAction(empty, "/session_secret"); got != ShortcutActionSessionSecret {
		t.Fatalf("slash form of the default key: got %q", got)
	}
	if got := shortcutAction(empty, "other"); got != "" {
		t.Fatalf("unmapped key must stay with workflows: got %q", got)
	}
	custom := agentconfig.SlackChannelConfig{Shortcuts: `[{"key":"/a","action":"session_secret"}]`}
	if got := shortcutAction(custom, "a"); got != ShortcutActionSessionSecret {
		t.Fatalf("custom key: got %q", got)
	}
	if got := shortcutAction(custom, "session_secret"); got != "" {
		t.Fatalf("a custom list replaces the default: got %q", got)
	}
}

func TestSecretModalBuilds(t *testing.T) {
	for _, askThread := range []bool{false, true} {
		req, err := toModalRequest(secretModal("sess|C01TESTCHAN|1.2", askThread))
		if err != nil {
			t.Fatalf("modal does not decode: %v", err)
		}
		if req.CallbackID != secretCallback || req.PrivateMetadata != "sess|C01TESTCHAN|1.2" {
			t.Fatalf("callback/meta lost: %+v", req)
		}
		if n := len(req.Blocks.BlockSet); n > 100 || n < 2*secretRows {
			t.Fatalf("unexpected block count %d", n)
		}
	}
}

func TestStateChecked(t *testing.T) {
	values := map[string]any{secretPlainBlk: map[string]any{"rows": map[string]any{
		"selected_options": []any{map[string]any{"value": "plain2"}},
	}}}
	got := stateChecked(values, secretPlainBlk, "rows")
	if !got["plain2"] || got["plain1"] {
		t.Fatalf("got %v", got)
	}
}

func TestConfigModalBuilds(t *testing.T) {
	fields := []ConfigField{
		{Key: "base_url", Type: "text", Value: "https://stag.example.com"},
		{Key: "api_key", Type: "secret", Placeholder: "Leave empty to keep current"},
		{Key: "mode", Type: "dropdown", Options: []string{"read", "write"}, Value: "read"},
	}
	req, err := toModalRequest(configModal("Acme API", "s|sw_1|C01TESTCHAN|1.2", fields))
	if err != nil {
		t.Fatalf("modal does not decode: %v", err)
	}
	if req.CallbackID != cfgSaveCallback || len(req.Blocks.BlockSet) != 4 {
		t.Fatalf("got callback %q, %d blocks", req.CallbackID, len(req.Blocks.BlockSet))
	}
}
