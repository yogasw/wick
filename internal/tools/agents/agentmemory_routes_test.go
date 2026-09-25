package agents

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/pkg/tool"
)

// routeRecorder is a tool.Router that records paths instead of serving them,
// so a test can assert a feature is actually mounted. Worth having: every
// handler in this feature is reachable only through Register, and an unwired
// one is dead code that still compiles and still passes its own unit tests.
type routeRecorder struct {
	get  []string
	post []string
}

func (r *routeRecorder) GET(p string, _ tool.HandlerFunc)                       { r.get = append(r.get, p) }
func (r *routeRecorder) POST(p string, _ tool.HandlerFunc)                      { r.post = append(r.post, p) }
func (r *routeRecorder) PUT(string, tool.HandlerFunc)                           {}
func (r *routeRecorder) DELETE(string, tool.HandlerFunc)                        {}
func (r *routeRecorder) PATCH(string, tool.HandlerFunc)                         {}
func (r *routeRecorder) Use(string, tool.Middleware)                            {}
func (r *routeRecorder) Static(string, fs.FS)                                   {}
func (r *routeRecorder) HandleRaw(string, func(tool.ConfigReader) http.Handler) {}
func (r *routeRecorder) WebhookGroup(string) tool.WebhookRouter                 { return nil }
func (r *routeRecorder) Meta() tool.Tool                                        { return tool.Tool{Key: "agents", Path: "/tools/agents"} }

// TestRegisterAgentMemoryMountsItsRoutes pins the control + data surface of
// the built-in backend onto the agents router.
func TestRegisterAgentMemoryMountsItsRoutes(t *testing.T) {
	rec := &routeRecorder{}
	RegisterAgentMemory(rec)

	wantGET := []string{
		"/agentmemory/backends",
		"/agentmemory/ai-memory/status",
		"/agentmemory/ai-memory/settings",
		"/agentmemory/ai-memory/projects",
		"/agentmemory/ai-memory/health",
		"/agentmemory/ai-memory/handoffs",
		"/agentmemory/ai-memory/search",
		"/agentmemory/ai-memory/logs",
	}
	wantPOST := []string{
		"/agentmemory/ai-memory/start",
		"/agentmemory/ai-memory/stop",
		"/agentmemory/ai-memory/restart",
		"/agentmemory/ai-memory/install",
		"/agentmemory/ai-memory/test",
		"/agentmemory/ai-memory/settings",
		"/agentmemory/ai-memory/backfill/preview",
		"/agentmemory/ai-memory/backfill/run",
	}
	for _, p := range wantGET {
		if !contains(rec.get, p) {
			t.Errorf("GET %s not registered (have %v)", p, rec.get)
		}
	}
	for _, p := range wantPOST {
		if !contains(rec.post, p) {
			t.Errorf("POST %s not registered (have %v)", p, rec.post)
		}
	}
	// The feature is named "Agent Memory" end to end; "crossmemory" was an
	// early working name and must not survive anywhere in the surface.
	for _, p := range append(append([]string{}, rec.get...), rec.post...) {
		if strings.Contains(p, "crossmemory") {
			t.Errorf("route %s uses the abandoned name", p)
		}
	}
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// TestApplyAgentMemoryForm is the per-instance toggle save. The table covers
// what the form has to be able to express, including the states an
// only-write-non-empty handler would make unreachable.
func TestApplyAgentMemoryForm(t *testing.T) {
	cases := []struct {
		name  string
		start provider.Instance
		form  url.Values
		check func(*testing.T, provider.Instance)
	}{
		{
			name: "checkbox on enables with the default backend",
			form: url.Values{"use_agent_memory": {"on"}},
			check: func(t *testing.T, ins provider.Instance) {
				if !ins.UseAgentMemory {
					t.Fatal("toggle not set")
				}
				// Empty provider = the default backend, resolved at spawn.
				// Storing a name here would freeze today's default into
				// every instance created before a second backend existed.
				if ins.AgentMemoryProvider != "" {
					t.Fatalf("provider %q should stay empty", ins.AgentMemoryProvider)
				}
			},
		},
		{
			name:  "absent toggle switches it off",
			start: provider.Instance{UseAgentMemory: true},
			form:  url.Values{"agent_memory_capture": {"on"}},
			check: func(t *testing.T, ins provider.Instance) {
				if ins.UseAgentMemory {
					t.Fatal("an unchecked box must switch the toggle off")
				}
			},
		},
		{
			name: "every field round-trips",
			form: url.Values{
				"use_agent_memory":        {"true"},
				"agent_memory_provider":   {"ai-memory"},
				"agent_memory_server_url": {"http://10.0.0.5:49374"},
				"agent_memory_capture":    {"true"},
			},
			check: func(t *testing.T, ins provider.Instance) {
				if !ins.UseAgentMemory || !ins.AgentMemoryCapture {
					t.Fatalf("toggles: %+v", ins)
				}
				if ins.AgentMemoryProvider != "ai-memory" || ins.AgentMemoryServerURL != "http://10.0.0.5:49374" {
					t.Fatalf("fields: %+v", ins)
				}
			},
		},
		{
			name:  "clearing the server url returns to the managed daemon",
			start: provider.Instance{AgentMemoryServerURL: "http://10.0.0.5:49374"},
			form:  url.Values{"use_agent_memory": {"on"}, "agent_memory_server_url": {""}},
			check: func(t *testing.T, ins provider.Instance) {
				if ins.AgentMemoryServerURL != "" {
					t.Fatalf("server url not cleared: %q", ins.AgentMemoryServerURL)
				}
			},
		},
		{
			name:  "an empty token leaves the stored one alone",
			start: provider.Instance{AgentMemoryAuthKey: "wick_cenc_stored"},
			form:  url.Values{"use_agent_memory": {"on"}, "agent_memory_auth_key": {""}},
			check: func(t *testing.T, ins provider.Instance) {
				if ins.AgentMemoryAuthKey != "wick_cenc_stored" {
					t.Fatalf("token lost: %q", ins.AgentMemoryAuthKey)
				}
			},
		},
		{
			name:  "the masked placeholder is not a new token",
			start: provider.Instance{AgentMemoryAuthKey: "wick_cenc_stored"},
			form:  url.Values{"use_agent_memory": {"on"}, "agent_memory_auth_key": {"••••••••"}},
			check: func(t *testing.T, ins provider.Instance) {
				if ins.AgentMemoryAuthKey != "wick_cenc_stored" {
					t.Fatalf("a masked value overwrote the stored token: %q", ins.AgentMemoryAuthKey)
				}
			},
		},
		{
			name: "a real token is taken",
			form: url.Values{"use_agent_memory": {"on"}, "agent_memory_auth_key": {"deadbeef"}},
			check: func(t *testing.T, ins provider.Instance) {
				// Encryption is a no-op with no configs service wired, which
				// is the point being checked here: the value arrives.
				if ins.AgentMemoryAuthKey != "deadbeef" {
					t.Fatalf("token: %q", ins.AgentMemoryAuthKey)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(tc.form.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			c := tool.NewCtx(httptest.NewRecorder(), r, nil, tool.Tool{Key: "agents", Path: "/tools/agents"}, nil, nil)
			ins := tc.start
			applyAgentMemoryForm(&ins, c)
			tc.check(t, ins)
		})
	}
}

// TestAgentMemoryWebAPIDefaultsOn pins the decision that a wick-managed
// daemon runs with its HTTP API mounted unless the operator switched it off
// (Yoga, 2026-09-25): the panel's project list and search are reads against
// that API, so a default-off daemon would ship an empty Projects tab. Only an
// explicit stored "false" turns it off.
func TestAgentMemoryWebAPIDefaultsOn(t *testing.T) {
	for _, tc := range []struct {
		name   string
		stored string
		want   bool
	}{
		{"never configured", "", true},
		{"switched off", "false", false},
		{"switched on", "true", true},
		{"unparsable", "yes", true},
	} {
		if got := parseCfgBool(tc.stored, true); got != tc.want {
			t.Errorf("%s: enable_web = %v, want %v", tc.name, got, tc.want)
		}
	}
	// The autostart key keeps the opposite default — absent means off, and
	// only the derived lock turns it on.
	if parseCfgBool("", false) {
		t.Error("autostart must stay off until it is stored on or locked")
	}
}
