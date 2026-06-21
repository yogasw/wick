package plugin

import (
	"testing"

	"github.com/yogasw/wick/pkg/connector"
)

func TestScanFindsManifests(t *testing.T) {
	found, err := Scan("testdata/connectors")
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 {
		t.Fatalf("expected 1 manifest, got %d", len(found))
	}
	if found[0].Key != "demo" {
		t.Fatalf("key not parsed: %s", found[0].Key)
	}
	if found[0].BinaryPath == "" {
		t.Fatal("binary path must be set (sibling of plugin.json)")
	}
}

func TestLoadRegistersModules(t *testing.T) {
	var registered []connector.Module
	register := func(m connector.Module) { registered = append(registered, m) }

	n, err := loadWith("testdata/connectors", register, nil)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || len(registered) != 1 || registered[0].Meta.Key != "demo" {
		t.Fatalf("expected demo registered, got %d / %+v", n, registered)
	}
}
