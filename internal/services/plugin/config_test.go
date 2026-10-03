package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/yogasw/wick/pkg/entity"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

// memStore is an in-memory ConfigStore.
type memStore struct {
	mu   sync.Mutex
	rows map[string][]entity.Config
}

func (m *memStore) EnsureOwned(_ context.Context, owner string, rows ...entity.Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.rows == nil {
		m.rows = map[string][]entity.Config{}
	}
	if _, ok := m.rows[owner]; !ok {
		m.rows[owner] = append([]entity.Config(nil), rows...)
	}
	return nil
}

func (m *memStore) ListOwned(owner string) []entity.Config {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]entity.Config(nil), m.rows[owner]...)
}

func (m *memStore) SetOwned(_ context.Context, owner, key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, r := range m.rows[owner] {
		if r.Key == key {
			m.rows[owner][i].Value = value
			return nil
		}
	}
	return fmt.Errorf("unknown config %s/%s", owner, key)
}

// cfgConn records every Configure; reject makes pushes after the first fail.
type cfgConn struct {
	mu     sync.Mutex
	pushes []map[string]string
	reject bool
}

func (c *cfgConn) Configure(_ context.Context, cfg map[string]string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pushes = append(c.pushes, cfg)
	if c.reject && len(c.pushes) > 1 {
		return errors.New("unimplemented")
	}
	return nil
}
func (*cfgConn) Health(context.Context) error { return nil }

func (c *cfgConn) last() map[string]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pushes[len(c.pushes)-1]
}

func configHost(t *testing.T, reject bool) (*Host, *Service, *fakeProcs, *[]*cfgConn, *httptest.Server) {
	t.Helper()
	f := &fakeProcs{handler: http.NotFoundHandler()}
	var mu sync.Mutex
	conns := []*cfgConn{}
	h := NewHost(nil, t.TempDir())
	h.Configs = &memStore{}
	h.spawn = func(bin, socket string, env []string, stderr io.Writer) (func(), wickplugin.ToolConn, <-chan struct{}, error) {
		kill, _, exited, err := f.spawn(bin, socket, env, stderr)
		c := &cfgConn{reject: reject}
		mu.Lock()
		conns = append(conns, c)
		mu.Unlock()
		return kill, c, exited, err
	}
	sm := wickplugin.ServiceModule{
		Meta:   wickplugin.ToolMeta{Key: "cfgsvc"},
		Routes: []wickplugin.ServiceRoute{{Prefix: "/", Auth: wickplugin.AuthPublic}},
		Configs: []entity.Config{
			{Key: "prefix", Value: "echo: ", Description: "Reply prefix"},
			{Key: "api_key", IsSecret: true, Required: true},
		},
	}
	s := h.Add("cfgsvc", "1.0.0", sm, "bin")
	h.Start()
	t.Cleanup(h.Shutdown)
	waitFor(t, "running", func() bool { return s.Sup.Status().State == StateRunning })
	mux := http.NewServeMux()
	h.RegisterAdmin(mux, func(next http.Handler) http.Handler { return next })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return h, s, f, &conns, srv
}

func putConfig(t *testing.T, srv *httptest.Server, values map[string]string) (int, ServiceView) {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"values": values})
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/manager/api/service-plugins/cfgsvc/config", strings.NewReader(string(b)))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var v ServiceView
	_ = json.NewDecoder(resp.Body).Decode(&v)
	return resp.StatusCode, v
}

func fieldOf(v ServiceView, key string) ConfigField {
	for _, c := range v.Configs {
		if c.Key == key {
			return c
		}
	}
	return ConfigField{}
}

func TestServiceConfigSeedMaskAndPush(t *testing.T) {
	_, s, f, conns, srv := configHost(t, false)
	// Seeded defaults reach the first spawn.
	if got := (*conns)[0].last(); got["prefix"] != "echo: " {
		t.Fatalf("first spawn config = %v", got)
	}
	code, v := putConfig(t, srv, map[string]string{"prefix": "hi: ", "api_key": "s3cret"})
	if code != http.StatusOK {
		t.Fatalf("save config = %d", code)
	}
	if k := fieldOf(v, "api_key"); k.Value != "" || !k.HasValue || fieldOf(v, "prefix").Value != "hi: " {
		t.Fatalf("view configs = %+v", v.Configs)
	}
	// The secret never comes back in plaintext, from save or GET.
	resp, err := http.Get(srv.URL + "/manager/api/service-plugins/cfgsvc")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(raw), "s3cret") {
		t.Fatal("GET returned the plaintext secret")
	}
	// Pushed live to the running process, no restart.
	if got := (*conns)[0].last(); got["prefix"] != "hi: " || got["api_key"] != "s3cret" {
		t.Fatalf("pushed config = %v", got)
	}
	if f.count() != 1 {
		t.Fatalf("push must not restart, spawns = %d", f.count())
	}
	// Masked / empty secret keeps the stored one.
	putConfig(t, srv, map[string]string{"prefix": "yo: ", "api_key": SecretMask})
	if got := (*conns)[0].last(); got["api_key"] != "s3cret" || got["prefix"] != "yo: " {
		t.Fatalf("masked secret must keep value, got %v", got)
	}
	if code, _ := putConfig(t, srv, map[string]string{"nope": "x"}); code != http.StatusBadRequest {
		t.Fatalf("unknown key = %d, want 400", code)
	}
	_ = s
}

func TestServiceConfigRestartWhenPushUnsupported(t *testing.T) {
	_, s, f, conns, srv := configHost(t, true)
	putConfig(t, srv, map[string]string{"prefix": "new: "})
	waitFor(t, "respawn", func() bool { return f.count() == 2 && s.Sup.Status().State == StateRunning })
	waitFor(t, "new process configured", func() bool { return len(*conns) == 2 })
	if got := (*conns)[1].last(); got["prefix"] != "new: " {
		t.Fatalf("restarted process config = %v", got)
	}
}
