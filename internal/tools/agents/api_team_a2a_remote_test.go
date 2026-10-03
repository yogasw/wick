package agents

import (
	"bufio"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/a2aremote"
	"github.com/yogasw/wick/internal/agents/a2aremote/a2aremotetest"
	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/team"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/tool"
)

type remoteTestCodec struct{}

func (remoteTestCodec) EncryptSecret(p string) (string, error) { return "wick_enc_" + strings.ToUpper(p), nil }
func (remoteTestCodec) DecryptSecret(t string) (string, error) {
	if !strings.HasPrefix(t, "wick_enc_") {
		return "", errors.New("bad token")
	}
	return strings.ToLower(strings.TrimPrefix(t, "wick_enc_")), nil
}

// withRemoteWorld is the A2A world plus a codec and an allowlist that
// lets the httptest host through.
func withRemoteWorld(t *testing.T, allow string) {
	t.Helper()
	withAgentA2AWorld(t)
	prevC, prevA := remoteCodecOverride, remoteAllowedHostsOverride
	remoteCodecOverride = remoteTestCodec{}
	remoteAllowedHostsOverride = func() string { return allow }
	t.Cleanup(func() { remoteCodecOverride, remoteAllowedHostsOverride = prevC, prevA })
}

func remoteCall(t *testing.T, u *entity.User, method, path string, body map[string]any, h func(*tool.Ctx)) (int, map[string]any, string) {
	t.Helper()
	id := strings.Split(strings.TrimPrefix(path, "/api/team/agents/"), "/")[0]
	w, c := teamReq(t, u, method, path, body, map[string]string{"id": id})
	h(c)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out, w.Body.String()
}

func TestRemoteAgentLifecycle(t *testing.T) {
	withRemoteWorld(t, "127.0.0.1")
	srv := a2aremotetest.New("Research Bot")
	srv.Bearer = "tok-abc"
	defer srv.Close()
	u := &entity.User{ID: "u1"}
	auth := map[string]any{"type": "bearer", "secret": "tok-abc"}

	code, prev, raw := remoteCall(t, u, http.MethodPost, "/api/team/a2a-remote/resolve", map[string]any{"url": srv.URL, "auth": auth}, apiTeamRemoteResolve)
	if code != http.StatusOK || prev["suggested_handle"] != "research-bot" || prev["host"] == "" {
		t.Fatalf("resolve: %d %s", code, raw)
	}
	code, out, raw := remoteCall(t, u, http.MethodPost, "/api/team/a2a-remote/test", map[string]any{"url": srv.URL, "auth": auth}, apiTeamRemoteTest)
	if code != http.StatusOK || out["ok"] != true || out["reply"] != "echo: ping" {
		t.Fatalf("test: %d %s", code, raw)
	}

	// Two creates from one card: the second handle gets -2.
	var ids []string
	for i, want := range []string{"research-bot", "research-bot-2"} {
		code, out, raw = remoteCall(t, u, http.MethodPost, "/api/team/a2a-remote", map[string]any{"url": srv.URL, "auth": auth, "usage": "me_and_my_agents"}, apiTeamRemoteCreate)
		if code != http.StatusOK || out["handle"] != want || out["kind"] != a2aremote.Kind {
			t.Fatalf("create %d: %d %s", i, code, raw)
		}
		if strings.Contains(raw, "tok-abc") || strings.Contains(raw, "TOK-ABC") || strings.Contains(raw, "wick_enc_") {
			t.Fatalf("create response leaks the secret: %s", raw)
		}
		rem := out["remote"].(map[string]any)
		if rem["auth_set"] != true || rem["auth_type"] != "bearer" || rem["usage"] != "mention" || out["mention_from"] != "all" || rem["timeout_sec"] != float64(120) {
			t.Fatalf("remote info: %s", raw)
		}
		ids = append(ids, out["id"].(string))
	}
	// A typed handle that clashes is refused rather than renamed.
	if code, _, _ = remoteCall(t, u, http.MethodPost, "/api/team/a2a-remote", map[string]any{"url": srv.URL, "auth": auth, "handle": "research-bot"}, apiTeamRemoteCreate); code != http.StatusConflict {
		t.Fatalf("typed clash: %d", code)
	}
	cfg, _, _ := remoteStore().Load(ids[0])
	if cfg.Auth.Secret != "wick_enc_TOK-ABC" {
		t.Fatalf("stored secret is not the encrypted token: %q", cfg.Auth.Secret)
	}

	// Refresh follows the card but keeps the handle and avatar.
	agent, _ := globalTeam.Get(t.Context(), ids[0])
	agent.Avatar = team.EncodeAvatar(team.Avatar{Shape: "circle", Color: "#123456"})
	if err := globalTeam.Update(t.Context(), &agent); err != nil {
		t.Fatal(err)
	}
	srv.SetVersion("2.0.0")
	base := "/api/team/agents/" + ids[0] + "/a2a-remote"
	code, out, raw = remoteCall(t, u, http.MethodPost, base+"/refresh-card", nil, apiTeamRemoteRefresh)
	if code != http.StatusOK || out["card"].(map[string]any)["version"] != "2.0.0" || strings.Contains(raw, "TOK") {
		t.Fatalf("refresh: %d %s", code, raw)
	}
	after, _ := globalTeam.Get(t.Context(), ids[0])
	if after.Handle != "research-bot" || after.Avatar != agent.Avatar {
		t.Fatalf("refresh changed handle/avatar: %+v", after)
	}

	// Settings: limits validated, auth replaced only when sent.
	if code, _, _ = remoteCall(t, u, http.MethodPatch, base, map[string]any{"timeout_sec": 99999}, apiTeamRemoteUpdate); code != http.StatusBadRequest {
		t.Fatalf("bad timeout: %d", code)
	}
	code, out, raw = remoteCall(t, u, http.MethodPatch, base, map[string]any{"timeout_sec": 30, "max_response_bytes": 4096, "usage": "only_me"}, apiTeamRemoteUpdate)
	if code != http.StatusOK || out["timeout_sec"] != float64(30) || out["auth_set"] != true || out["usage"] != "mention" {
		t.Fatalf("patch: %d %s", code, raw)
	}
	code, out, _ = remoteCall(t, u, http.MethodPatch, base, map[string]any{"auth": map[string]any{"type": "none"}}, apiTeamRemoteUpdate)
	if code != http.StatusOK || out["auth_set"] != false {
		t.Fatalf("clear auth: %d %v", code, out)
	}

	// Another owner cannot read it.
	if code, _, _ = remoteCall(t, &entity.User{ID: "u2"}, http.MethodGet, base, nil, apiTeamRemoteGet); code != http.StatusNotFound {
		t.Fatalf("foreign read: %d", code)
	}

	// Delete drops the settings row.
	if code, _, raw = remoteCall(t, u, http.MethodDelete, "/api/team/agents/"+ids[1], nil, apiTeamAgentDelete); code != http.StatusOK {
		t.Fatalf("delete: %d %s", code, raw)
	}
	if _, ok, _ := remoteStore().Load(ids[1]); ok {
		t.Fatal("settings row kept after delete")
	}
}

func TestRemoteRefusesLoopbackWithoutAllowlist(t *testing.T) {
	withRemoteWorld(t, "")
	srv := a2aremotetest.New("x")
	defer srv.Close()
	u := &entity.User{ID: "u1"}
	for _, h := range []func(*tool.Ctx){apiTeamRemoteResolve, apiTeamRemoteCreate} {
		code, _, raw := remoteCall(t, u, http.MethodPost, "/api/team/a2a-remote", map[string]any{"url": srv.URL}, h)
		if code != http.StatusBadRequest || !strings.Contains(raw, "not allowed") {
			t.Fatalf("loopback: %d %s", code, raw)
		}
	}
	if len(srv.Received()) != 0 || len(srv.Auths()) != 0 {
		t.Fatal("the refused host was contacted")
	}
}

// TestRemoteAgentChatRunsOnRemote: the agent's chat session runs under the
// a2a-remote provider key and the factory's loader hands it the remote
// spawner, whose turn reaches the fake with the stored bearer.
func TestRemoteAgentChatRunsOnRemote(t *testing.T) {
	withRemoteWorld(t, "127.0.0.1")
	srv := a2aremotetest.New("Echo")
	srv.Bearer = "tok"
	defer srv.Close()
	u := &entity.User{ID: "u1"}
	code, out, raw := remoteCall(t, u, http.MethodPost, "/api/team/a2a-remote", map[string]any{"url": srv.URL, "auth": map[string]any{"type": "bearer", "secret": "tok"}}, apiTeamRemoteCreate)
	if code != http.StatusOK {
		t.Fatalf("create: %d %s", code, raw)
	}
	id := out["id"].(string)
	code, out, raw = remoteCall(t, u, http.MethodPost, "/api/team/agents/"+id+"/chat", map[string]any{}, apiTeamAgentChat)
	if code != http.StatusOK {
		t.Fatalf("chat: %d %s", code, raw)
	}
	sid := out["session_id"].(string)
	s, _ := globalMgr.Registry().Session(sid)
	if len(s.Agents) == 0 || s.Agents[0].Provider != a2aremote.ProviderKey {
		t.Fatalf("session provider: %+v", s.Agents)
	}
	sp, ok := RemoteSpawnerFor(sid)
	if !ok {
		t.Fatal("no remote spawner for the agent's session")
	}
	if _, ok := RemoteSpawnerFor("nope"); ok {
		t.Fatal("remote spawner for an unknown session")
	}
	p, err := sp.Spawn(t.Context(), provider.SpawnOptions{SessionDir: globalLayout.SessionDir(sid)})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p.Kill() }()
	_, _ = p.Stdin().Write([]byte(`{"type":"user","message":{"role":"user","content":"hi"}}` + "\n"))
	sc := bufio.NewScanner(p.Stdout())
	for sc.Scan() {
		if strings.Contains(sc.Text(), `"type":"result"`) {
			if !strings.Contains(sc.Text(), `"result":"echo: hi"`) {
				t.Fatalf("turn: %s", sc.Text())
			}
			break
		}
	}
	code, out, raw = remoteCall(t, u, http.MethodGet, "/api/team/agents/"+id+"/a2a-remote?session_id="+sid, nil, apiTeamRemoteGet)
	if code != http.StatusOK || out["session"].(map[string]any)["context_id"] == "" {
		t.Fatalf("session state: %d %s", code, raw)
	}
}
