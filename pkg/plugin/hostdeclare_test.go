package plugin

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/yogasw/wick/pkg/connector"
)

// TestDeclareHostAccessInManifest: what main declares lands in the
// self-built manifest, survives the JSON round trip `wick plugin build`
// does, and a bad declaration fails the dump.
func TestDeclareHostAccessInManifest(t *testing.T) {
	t.Cleanup(func() { DeclareHostAccess(nil, nil) })
	mod := connector.Module{Meta: connector.Meta{Key: "demo"}}

	DeclareHostAccess([]string{ScopeHostUsersRead, ScopeHostConnectorsExec}, &HostResources{
		Connectors: []HostConnectorResource{{Connector: "slack", Ops: []string{"lookup_user_by_email"}}},
	})
	m, err := BuildSelfManifest(mod, "")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var back Manifest
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if strings.Join(back.HostScopes, ",") != "users.read,connectors.execute" {
		t.Fatalf("host_scopes = %v", back.HostScopes)
	}
	if got := back.HostResources.Items(HostResourceConnectors); len(got) != 1 || got[0] != "slack.lookup_user_by_email" {
		t.Fatalf("connector items = %v", got)
	}

	DeclareHostAccess([]string{ScopeHostDBRead}, nil) // db.read without host_resources.db
	if _, err := BuildSelfManifest(mod, ""); err == nil || !strings.Contains(err.Error(), "db.read") {
		t.Fatalf("undeclared db.read should fail the dump, got %v", err)
	}

	DeclareHostAccess(nil, nil)
	m, err = BuildSelfManifest(mod, "")
	if err != nil || len(m.HostScopes) != 0 || m.HostResources != nil {
		t.Fatalf("no declaration: %v %v %v", err, m.HostScopes, m.HostResources)
	}
}
