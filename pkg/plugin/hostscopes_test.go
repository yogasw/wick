package plugin

import (
	"strings"
	"testing"
)

// TestHostScopesHaveAdminLabel: the admin card lists every scope by label
// and description, so a scope without them would show up blank.
func TestHostScopesHaveAdminLabel(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range HostScopes {
		if strings.TrimSpace(s.Label) == "" || strings.TrimSpace(s.Description) == "" {
			t.Errorf("scope %q has no admin label/description", s.Name)
		}
		if seen[s.Name] {
			t.Errorf("scope %q listed twice", s.Name)
		}
		seen[s.Name] = true
	}
	for _, m := range HostMethods {
		if m.Scope == "" {
			continue // Invoke takes the scope of the method it calls
		}
		if _, ok := LookupHostScope(m.Scope); !ok {
			t.Errorf("method %s uses unknown scope %q", m.Name, m.Scope)
		}
	}
}

// TestHostDestructiveMarks pins SPEC §2: what changes data or reaches
// people is marked destructive, and db/rest.write start off.
func TestHostDestructiveMarks(t *testing.T) {
	for _, name := range []string{"SendToSession", "CreateTicket", "UpdateTicket", "AttachSession"} {
		if m, _ := LookupHostMethod(name); !m.Destructive {
			t.Errorf("%s should be destructive", name)
		}
	}
	for _, name := range []string{"GetTicket", "ListUsers", "Whoami"} {
		if m, _ := LookupHostMethod(name); m.Destructive {
			t.Errorf("%s should not be destructive", name)
		}
	}
	for _, name := range []string{ScopeHostDBRead, ScopeHostDBWrite, ScopeHostRestWrite} {
		if s, _ := LookupHostScope(name); s.DefaultAllow {
			t.Errorf("%s must be off by default", name)
		}
	}
	if s, _ := LookupHostScope(ScopeHostConnectorsExec); !s.DefaultAllow {
		t.Error("connectors.execute is allowed by default (runs as the user)")
	}
}

func TestValidateHostAccess(t *testing.T) {
	cases := []struct {
		name    string
		m       Manifest
		wantErr string
	}{
		{"none", Manifest{}, ""},
		{"plain scopes", Manifest{HostScopes: []string{"users.read", "tickets.write", "datadir"}}, ""},
		{"unknown scope loads (forward compatible)", Manifest{HostScopes: []string{"notes.read"}}, ""},
		{"read_secret needs config", Manifest{HostScopes: []string{"config.read_secret"}}, "host_resources.config"},
		{"read_secret ok", Manifest{HostScopes: []string{"config.read_other", "config.read_secret"}, HostResources: &HostResources{Config: []HostConfigResource{{Owner: "job:notion-ticket-sync", Key: "token"}}}}, ""},
		{"config missing key", Manifest{HostResources: &HostResources{Config: []HostConfigResource{{Owner: "job:x"}}}}, "owner and key"},
		{"duplicate", Manifest{HostScopes: []string{"users.read", "users.read"}}, "twice"},
		{"db without tables", Manifest{HostScopes: []string{"db.read"}}, "host_resources.db"},
		{"db read ok", Manifest{HostScopes: []string{"db.read"}, HostResources: &HostResources{DB: []HostDBResource{{Table: "users", Mode: "read"}}}}, ""},
		{"db write needs a write table", Manifest{HostScopes: []string{"db.write"}, HostResources: &HostResources{DB: []HostDBResource{{Table: "users", Mode: "read"}}}}, "host_resources.db"},
		{"db bad mode", Manifest{HostResources: &HostResources{DB: []HostDBResource{{Table: "users", Mode: "all"}}}}, "mode"},
		{"rest read ok", Manifest{HostScopes: []string{"rest.read"}, HostResources: &HostResources{Rest: []HostRestResource{{Method: "GET", Path: "/api/tickets/*"}}}}, ""},
		{"rest write needs non-GET", Manifest{HostScopes: []string{"rest.write"}, HostResources: &HostResources{Rest: []HostRestResource{{Method: "GET", Path: "/api/x"}}}}, "host_resources.rest"},
		{"rest manager ok", Manifest{HostScopes: []string{"rest.write"}, HostResources: &HostResources{Rest: []HostRestResource{{Method: "post", Path: "/manager/api/jobs"}}}}, ""},
		{"rest outside api", Manifest{HostResources: &HostResources{Rest: []HostRestResource{{Method: "GET", Path: "/admin/users"}}}}, "/api/"},
		{"rest dot segment", Manifest{HostResources: &HostResources{Rest: []HostRestResource{{Method: "GET", Path: "/api/../admin"}}}}, "dot"},
		{"rest bad method", Manifest{HostResources: &HostResources{Rest: []HostRestResource{{Method: "TRACE", Path: "/api/x"}}}}, "method"},
		{"connectors needs ops", Manifest{HostScopes: []string{"connectors.execute"}}, "host_resources.connectors"},
		{"connectors empty ops", Manifest{HostResources: &HostResources{Connectors: []HostConnectorResource{{Connector: "slack"}}}}, "ops"},
		{"connectors ok", Manifest{HostScopes: []string{"connectors.execute"}, HostResources: &HostResources{Connectors: []HostConnectorResource{{Connector: "slack", Ops: []string{"lookup_user_by_email"}}}}}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.m.ValidateHostAccess()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}

func TestHostResourceItems(t *testing.T) {
	r := &HostResources{
		Rest:       []HostRestResource{{Method: "get", Path: "/api/x"}},
		DB:         []HostDBResource{{Table: "tickets", Mode: "read"}, {Table: "users", Mode: "write"}},
		Connectors: []HostConnectorResource{{Connector: "slack", Ops: []string{"a", "b"}}},
	}
	if got := strings.Join(r.Items(HostResourceDB), ","); got != "tickets:read,users:read,users:write" {
		t.Errorf("db items = %q", got)
	}
	if got := strings.Join(r.Items(HostResourceRest), ","); got != "GET /api/x" {
		t.Errorf("rest items = %q", got)
	}
	if got := strings.Join(r.Items(HostResourceConnectors), ","); got != "slack.a,slack.b" {
		t.Errorf("connector items = %q", got)
	}
	var nilRes *HostResources
	if nilRes.Items(HostResourceDB) != nil || !nilRes.Empty() {
		t.Error("nil resources should be empty")
	}
}

func TestUnknownHostScopes(t *testing.T) {
	m := Manifest{HostScopes: []string{"users.read", "notes.read", "team.write"}}
	if got := strings.Join(m.UnknownHostScopes(), ","); got != "notes.read,team.write" {
		t.Fatalf("unknown = %q", got)
	}
	if s, _ := LookupHostScope(ScopeHostConfigReadSecret); s.DefaultAllow || s.Destructive || !s.Sensitive || s.Resource != HostResourceConfig {
		t.Fatalf("config.read_secret = %+v, want off by default, not destructive, sensitive, config resource", s)
	}
	r := &HostResources{Config: []HostConfigResource{{Owner: "job:a", Key: "token"}}}
	if got := strings.Join(r.Items(HostResourceConfig), ","); got != "job:a/token" {
		t.Fatalf("config items = %q", got)
	}
}

func TestMatchRestPattern(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
	}{
		{"/api/tickets", "/api/tickets", true},
		{"/api/tickets", "/api/tickets/1", false},
		{"/api/tickets/*", "/api/tickets/1", true},
		{"/api/tickets/*", "/api/tickets/", false},
		{"/api/tickets/*", "/api/tickets/1/sessions", false},
		{"/api/tickets/*/sessions", "/api/tickets/1/sessions", true},
		{"/api/tickets/**", "/api/tickets/1/sessions", true},
		{"/api/tickets/**", "/api/tickets/1", true},
		{"/api/tickets/**", "/api/tickets", false},
		{"/api/tickets/**", "/api/tickets/", false},
		{"/api/tickets/**", "/api/ticketsx/1", false},
		{"/manager/api/*", "/manager/api/x", true},
	}
	for _, c := range cases {
		if got := MatchRestPattern(c.pattern, c.path); got != c.want {
			t.Errorf("MatchRestPattern(%q, %q) = %v, want %v", c.pattern, c.path, got, c.want)
		}
	}
	for _, bad := range []string{"/api/%2e%2e/x", "/api/x?y=1", "/api/**/x", "/api/a\\b",
		"/manager/api/plugins/x", "/manager/api/plugin-sources", "/manager/api/service-plugins/**", "/manager/api/**", "/manager/api/*/x"} {
		m := Manifest{HostScopes: []string{ScopeHostRestRead}, HostResources: &HostResources{Rest: []HostRestResource{{Method: "GET", Path: bad}}}}
		if m.ValidateHostAccess() == nil {
			t.Errorf("path %q should not validate", bad)
		}
	}
}
