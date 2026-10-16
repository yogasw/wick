package plugin

import (
	"net/http"
	"net/http/httptest"
	"testing"

	wickplugin "github.com/yogasw/wick/pkg/plugin"
	wickhost "github.com/yogasw/wick/pkg/plugin/host"
)

type fakeDoor struct {
	calls  []string
	served string
}

func (f *fakeDoor) URL() string { return "http://127.0.0.1:1" }
func (f *fakeDoor) BeginCallFrom(key, kind, userID, source, _ string) (string, func()) {
	f.calls = append(f.calls, key+"/"+kind+"/"+userID+"/"+source)
	return "cid", func() {}
}
func (f *fakeDoor) ServeAs(w http.ResponseWriter, _ *http.Request, key, kind string, _ []string) {
	f.served = key + "/" + kind
	w.WriteHeader(http.StatusTeapot)
}

func TestServiceTokenRouteRunsAsTokenOwner(t *testing.T) {
	tokens := NewTokens("")
	_, plain, err := tokens.GenerateBy("svc", "ci", "admin-1")
	if err != nil {
		t.Fatal(err)
	}
	if owner, ok := tokens.VerifyOwner("svc", plain); !ok || owner != "admin-1" {
		t.Fatalf("VerifyOwner = %q, %v", owner, ok)
	}
	door := &fakeDoor{}
	h := NewHost(tokens, t.TempDir())
	h.HostAPI = door
	if env := h.processEnv("svc", nil); envOf(env, wickhost.EnvHostAPIURL) != door.URL() || envOf(env, wickplugin.EnvPluginToken) == "" {
		t.Fatalf("service env = %v", env)
	}
	// The original public whoami goes to the Host API handler.
	key, scopes, _ := tokens.LookupCallback(envOf(h.processEnv("svc", []string{"x"}), wickplugin.EnvPluginToken))
	if key != "svc" || len(scopes) != 1 {
		t.Fatalf("callback token = %q %v", key, scopes)
	}
	req := httptest.NewRequest(http.MethodGet, "/x/-/api/whoami", nil)
	req.Header.Set("Authorization", "Bearer "+envOf(h.processEnv("svc", nil), wickplugin.EnvPluginToken))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusTeapot || door.served != "svc/service" {
		t.Fatalf("whoami went to %q (%d)", door.served, rec.Code)
	}
}

func envOf(env []string, key string) string {
	for _, kv := range env {
		if len(kv) > len(key) && kv[:len(key)+1] == key+"=" {
			return kv[len(key)+1:]
		}
	}
	return ""
}
