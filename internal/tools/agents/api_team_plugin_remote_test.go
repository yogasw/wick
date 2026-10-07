package agents

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/remote"
	"github.com/yogasw/wick/internal/agents/remote/pluginremote"
	"github.com/yogasw/wick/internal/entity"
	serviceplugin "github.com/yogasw/wick/internal/services/plugin"
	wickentity "github.com/yogasw/wick/pkg/entity"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
	"github.com/yogasw/wick/pkg/tool"
)

type sealCodec struct{}

func (sealCodec) EncryptSecret(p string) (string, error) { return "wick_cenc_test:" + p, nil }
func (sealCodec) DecryptSecret(t string) (string, error) {
	return strings.TrimPrefix(t, "wick_cenc_test:"), nil
}

var pluginRemoteTestDecl = []wickentity.Config{
	{Key: "api_key", IsSecret: true},
	{Key: "source"},
	{Key: "region", Required: true},
}

func TestMergePluginRemoteValues(t *testing.T) {
	if _, err := mergePluginRemoteValues(pluginRemoteTestDecl, nil, map[string]string{"source": "s"}); err == nil || !strings.Contains(err.Error(), `"region"`) {
		t.Fatalf("empty required: err = %v", err)
	}
	if _, err := mergePluginRemoteValues(pluginRemoteTestDecl, nil, map[string]string{"region": "id", "nope": "x"}); err == nil {
		t.Fatal("unknown key accepted")
	}
	old := map[string]string{"api_key": "wick_cenc_test:old", "source": "s", "region": "id"}
	for _, sent := range []string{"", serviceplugin.SecretMask} {
		got, err := mergePluginRemoteValues(pluginRemoteTestDecl, old, map[string]string{"api_key": sent, "source": ""})
		if err != nil || got["api_key"] != "wick_cenc_test:old" || got["region"] != "id" {
			t.Fatalf("sent %q: %v %v", sent, got, err)
		}
		if _, ok := got["source"]; ok {
			t.Fatalf("cleared source kept: %v", got)
		}
	}
	// Optional-only declaration (Jules): nothing filled is fine.
	if got, err := mergePluginRemoteValues(pluginRemoteTestDecl[:2], nil, nil); err != nil || len(got) != 0 {
		t.Fatalf("optional only = %v %v", got, err)
	}
}

func TestPluginRemoteFieldsMaskSecrets(t *testing.T) {
	fs := pluginRemoteFields(pluginRemoteTestDecl, map[string]string{"api_key": "wick_cenc_test:k", "source": "s"})
	b, _ := json.Marshal(fs)
	if strings.Contains(string(b), "wick_cenc_test") {
		t.Fatalf("secret leaked: %s", b)
	}
	if fs[0].Value != serviceplugin.SecretMask || !fs[0].HasValue || fs[1].Value != "s" || fs[2].HasValue || !fs[2].Required {
		t.Fatalf("fields = %+v", fs)
	}
	if empty := pluginRemoteFields(pluginRemoteTestDecl, nil); empty[0].Value != "" || empty[0].HasValue {
		t.Fatalf("unset secret = %+v", empty[0])
	}
}

func TestPluginRemoteSpawnerCarriesConfig(t *testing.T) {
	withAgentA2AWorld(t)
	pluginRemoteCodecOverride = sealCodec{}
	t.Cleanup(func() { pluginRemoteCodecOverride = nil })
	p := &entity.AgentPersona{OwnerUserID: "u1", Handle: "jules-a", ProjectID: "p-j", Kind: pluginremote.Kind, AllowedConnectors: "[]"}
	if err := globalTeam.Create(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	values, err := sealPluginRemoteValues(pluginRemoteTestDecl, map[string]string{"api_key": "plain-k", "source": "s"})
	if err != nil || values["api_key"] != "wick_cenc_test:plain-k" {
		t.Fatalf("sealed = %v %v", values, err)
	}
	if err := pluginRemoteStore().Save(pluginremote.Config{AgentID: p.ID, OwnerUserID: "u1", PluginKey: "jules", Values: values}); err != nil {
		t.Fatal(err)
	}
	var row entity.AgentChannel
	if err := globalDB.Where("type = ? AND name = ?", pluginremote.RowType, p.ID).First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if strings.Contains(row.Config, `"plain-k"`) {
		t.Fatalf("secret stored in plaintext: %s", row.Config)
	}
	sp, ok := pluginRemoteSpawner(*p).(remote.Spawner)
	if !ok {
		t.Fatalf("spawner = %T", pluginRemoteSpawner(*p))
	}
	src := sp.Source.(*pluginremote.Source)
	if src.AgentID != p.ID || src.Config["api_key"] != "plain-k" || src.Config["source"] != "s" {
		t.Fatalf("source = %s %v", src.AgentID, src.Config)
	}
}

// A new chat's session fields come from the plugin, are kept per session
// until the remote session exists, then answer 409.
func TestPluginSessionOptionsSaveThenLock(t *testing.T) {
	withAgentA2AWorld(t)
	seedTeamProject(t, "p-j", "u1")
	p := &entity.AgentPersona{OwnerUserID: "u1", Handle: "jules-s", ProjectID: "p-j", Kind: pluginremote.Kind, AllowedConnectors: "[]"}
	if err := globalTeam.Create(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if err := pluginRemoteStore().Save(pluginremote.Config{AgentID: p.ID, OwnerUserID: "u1", PluginKey: "jules",
		Values: map[string]string{"source": "o/r"}}); err != nil {
		t.Fatal(err)
	}
	pluginRemoteSessionFieldsOverride = func(cfg pluginremote.Config) ([]wickplugin.SessionField, error) {
		return []wickplugin.SessionField{{Key: "source", Label: "Repository", Default: cfg.Values["source"]}, {Key: "branch", Label: "Branch"}}, nil
	}
	t.Cleanup(func() { pluginRemoteSessionFieldsOverride = nil })
	seedAgentSession(t, *p, "s-new", false)
	u := &entity.User{ID: "u1"}
	base := "/api/team/agents/" + p.ID + "/plugin-remote/session-options"
	call := func(method, target string, body any, h func(*tool.Ctx)) (int, pluginSessionOptions) {
		w, c := teamReq(t, u, method, target, body, map[string]string{"id": p.ID})
		h(c)
		var out pluginSessionOptions
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}

	code, out := call("GET", base, nil, apiTeamPluginSessionOptionsGet)
	if code != 200 || len(out.Fields) != 2 || out.Fields[0].Default != "o/r" || out.Locked {
		t.Fatalf("draft get = %d %+v", code, out)
	}
	if code, _ := call("PUT", base, map[string]any{"options": map[string]string{"source": "x"}}, apiTeamPluginSessionOptionsPut); code != 400 {
		t.Fatalf("put without session = %d, want 400", code)
	}
	if code, _ := call("PUT", base+"?session_id=s-new", map[string]any{"options": map[string]string{"nope": "x"}}, apiTeamPluginSessionOptionsPut); code != 400 {
		t.Fatalf("put unknown key = %d, want 400", code)
	}
	code, out = call("PUT", base+"?session_id=s-new", map[string]any{"options": map[string]string{"source": "me/app", "branch": "dev"}}, apiTeamPluginSessionOptionsPut)
	if code != 200 || out.Values["source"] != "me/app" || out.Values["branch"] != "dev" || out.Locked {
		t.Fatalf("put = %d %+v", code, out)
	}
	if code, out = call("GET", base+"?session_id=s-new", nil, apiTeamPluginSessionOptionsGet); code != 200 || out.Values["branch"] != "dev" {
		t.Fatalf("get = %d %+v", code, out)
	}
	if code, _ := call("GET", base+"?session_id=missing", nil, apiTeamPluginSessionOptionsGet); code != 404 {
		t.Fatalf("unknown session = %d, want 404", code)
	}

	// The first turn created the remote session: the values lock.
	dir := globalLayout.SessionDir("s-new")
	_ = os.MkdirAll(dir, 0o700)
	if err := os.WriteFile(filepath.Join(dir, "plugin-remote.json"), []byte(`{"context_id":"c1","options":{"source":"me/app","branch":"dev"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, out = call("GET", base+"?session_id=s-new", nil, apiTeamPluginSessionOptionsGet); code != 200 || !out.Locked {
		t.Fatalf("locked get = %d %+v", code, out)
	}
	if code, _ := call("PUT", base+"?session_id=s-new", map[string]any{"options": map[string]string{"branch": "x"}}, apiTeamPluginSessionOptionsPut); code != 409 {
		t.Fatalf("put after start = %d, want 409", code)
	}
}
