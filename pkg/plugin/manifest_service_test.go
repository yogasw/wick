package plugin

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/yogasw/wick/pkg/entity"
)

func demoServiceManifest() Manifest {
	sm := ServiceModule{
		Meta:    ToolMeta{Key: "svc", Name: "Svc", Description: "demo service"},
		Routes:  []ServiceRoute{{Prefix: "/", Auth: AuthSession}},
		Configs: []entity.Config{{Key: "base_url", Required: true}},
	}
	return Manifest{
		SchemaVersion: ManifestSchemaVersion,
		Kind:          KindService,
		Version:       "0.1.0",
		ProtoVersion:  ProtoVersion,
		Entry:         "svc",
		OSArch:        []string{"linux/amd64"},
		SHA256:        "deadbeef",
		Module:        demoModule(), // ignored on write for kind=service
		Service:       &sm,
	}
}

// A service plugin.json carries no legacy "module" block; reading it back
// still mirrors Module.Meta from the service meta.
func TestServiceManifestOmitsModule(t *testing.T) {
	b, err := json.Marshal(demoServiceManifest())
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["module"]; ok {
		t.Fatalf("service manifest still has a module block: %s", b)
	}
	if _, ok := raw["service"]; !ok {
		t.Fatalf("service block missing: %s", b)
	}

	var got Manifest
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.Module.Meta.Key != "svc" || got.Module.Meta.Name != "Svc" {
		t.Fatalf("Module.Meta not mirrored from service meta: %+v", got.Module.Meta)
	}
	if got.Service == nil || len(got.Service.Configs) != 1 || got.Service.Configs[0].Key != "base_url" {
		t.Fatalf("service configs lost: %+v", got.Service)
	}
}

// Installed service plugins built before the change still have the module
// block (with null configs); they must keep loading.
func TestServiceManifestLegacyModuleStillLoads(t *testing.T) {
	legacy := `{"schema_version":1,"kind":"service","version":"0.0.9","proto_version":1,
		"entry":"svc","os_arch":["linux/amd64"],"sha256":"deadbeef","signature":"",
		"module":{"meta":{"key":"svc","name":"Svc Legacy"},"configs":null},
		"service":{"meta":{"key":"svc","name":"Svc"},"routes":[{"prefix":"/","auth":"wick-session"}]}}`
	var got Manifest
	if err := json.Unmarshal([]byte(legacy), &got); err != nil {
		t.Fatal(err)
	}
	if got.Module.Meta.Key != "svc" || got.Module.Meta.Name != "Svc Legacy" {
		t.Fatalf("legacy module block not read: %+v", got.Module.Meta)
	}
	if got.Service == nil || got.Service.Meta.Key != "svc" {
		t.Fatalf("service block not read: %+v", got.Service)
	}
}

// Non-service kinds keep the module block.
func TestConnectorManifestKeepsModule(t *testing.T) {
	m := demoServiceManifest()
	m.Kind, m.Service = KindConnector, nil
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"module":`) {
		t.Fatalf("connector manifest lost its module block: %s", b)
	}
}
