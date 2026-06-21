package plugin

import (
	"encoding/json"
	"testing"

	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/entity"
	"github.com/yogasw/wick/pkg/wickdocs"
)

func demoModule() connector.Module {
	return connector.Module{
		Meta:    connector.Meta{Key: "demo", Name: "Demo"},
		Configs: entity.StructToConfigs(struct{}{}),
		Operations: []connector.Category{
			connector.Cat("Main", "",
				connector.Op("say", "Say", "echo", struct {
					Text string `wick:"text"`
				}{}, func(c *connector.Ctx) (any, error) { return nil, nil }, wickdocs.Docs{})),
		},
	}
}

func TestManifestEnvelopeRoundTrip(t *testing.T) {
	env := Manifest{
		SchemaVersion: 1,
		Version:       "1.2.3",
		ProtoVersion:  ProtoVersion,
		Entry:         "demo",
		OSArch:        []string{"linux/amd64"},
		SHA256:        "deadbeef",
		Signature:     "",
		Module:        demoModule(),
	}
	b, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	var got Manifest
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.Version != "1.2.3" || got.Entry != "demo" || got.Module.Meta.Key != "demo" {
		t.Fatalf("envelope did not round-trip: %+v", got)
	}
	if len(got.Module.AllOps()) != 1 || got.Module.AllOps()[0].Key != "say" {
		t.Fatalf("module ops lost: %+v", got.Module.AllOps())
	}
}
