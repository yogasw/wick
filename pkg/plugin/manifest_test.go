package plugin

import (
	"encoding/json"
	"os"
	"runtime"
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

func hostArch() string { return runtime.GOOS + "/" + runtime.GOARCH }

func TestVerifyManifest(t *testing.T) {
	dir := t.TempDir()
	bin := dir + "/demo"
	if err := os.WriteFile(bin, []byte("fake-binary-bytes"), 0o755); err != nil {
		t.Fatal(err)
	}
	sum, err := sha256File(bin)
	if err != nil {
		t.Fatal(err)
	}
	base := Manifest{
		SchemaVersion: 1, Version: "1", ProtoVersion: ProtoVersion,
		Entry: "demo", OSArch: []string{hostArch()}, SHA256: sum, Module: demoModule(),
	}

	t.Setenv("WICK_PLUGIN_REQUIRE_SIGNATURE", "0")
	t.Setenv("WICK_PLUGIN_PUBKEY", "")
	if err := VerifyManifest(base, bin); err != nil {
		t.Fatalf("dev unsigned should pass: %v", err)
	}

	bad := base
	bad.OSArch = []string{"plan9/foo"}
	if VerifyManifest(bad, bin) == nil {
		t.Fatal("wrong arch must fail")
	}

	bad = base
	bad.SHA256 = "00"
	if VerifyManifest(bad, bin) == nil {
		t.Fatal("sha mismatch must fail")
	}

	bad = base
	bad.ProtoVersion = ProtoVersion + 99
	if VerifyManifest(bad, bin) == nil {
		t.Fatal("proto mismatch must fail")
	}

	t.Setenv("WICK_PLUGIN_REQUIRE_SIGNATURE", "1")
	if VerifyManifest(base, bin) == nil {
		t.Fatal("require-mode unsigned must fail")
	}

	priv, pub := GenerateKeypair()
	sig, _ := signSHA256WithKey(priv, sum)
	signed := base
	signed.Signature = sig
	t.Setenv("WICK_PLUGIN_PUBKEY", pub)
	if err := VerifyManifest(signed, bin); err != nil {
		t.Fatalf("require-mode trusted-signed should pass: %v", err)
	}

	otherPriv, _ := GenerateKeypair()
	otherSig, _ := signSHA256WithKey(otherPriv, sum)
	untrusted := base
	untrusted.Signature = otherSig
	if VerifyManifest(untrusted, bin) == nil {
		t.Fatal("require-mode untrusted signature must fail")
	}
}
