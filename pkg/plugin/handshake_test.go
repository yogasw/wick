package plugin

import "testing"

func TestVersionedPluginsHasV1(t *testing.T) {
	if _, ok := VersionedPlugins[1]; !ok {
		t.Fatalf("expected proto_version 1 in VersionedPlugins, got %v", VersionedPlugins)
	}
	if _, ok := VersionedPlugins[1][PluginName]; !ok {
		t.Fatalf("expected plugin %q registered under version 1", PluginName)
	}
}

func TestHandshakeMagicCookieSet(t *testing.T) {
	if Handshake.MagicCookieKey == "" || Handshake.MagicCookieValue == "" {
		t.Fatal("handshake magic cookie must be non-empty")
	}
}
