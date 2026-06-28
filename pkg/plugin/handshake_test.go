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

func TestSupportedProtoVersions(t *testing.T) {
	got := SupportedProtoVersions()
	if len(got) == 0 {
		t.Fatal("supported versions must be non-empty")
	}
	// Newest first, and every advertised version has a plugin set.
	if got[0] != ProtoVersion {
		t.Errorf("first supported = %d, want newest %d", got[0], ProtoVersion)
	}
	for _, v := range got {
		if _, ok := VersionedPlugins[v]; !ok {
			t.Errorf("supported version %d missing from VersionedPlugins", v)
		}
	}
}

func TestProtoVersionSupported(t *testing.T) {
	if !ProtoVersionSupported(ProtoVersion) {
		t.Errorf("current ProtoVersion %d must be supported", ProtoVersion)
	}
	if !ProtoVersionSupported(MinProtoVersion) {
		t.Errorf("MinProtoVersion %d must be supported", MinProtoVersion)
	}
	if ProtoVersionSupported(ProtoVersion + 1) {
		t.Errorf("v%d (newer than host) must be rejected", ProtoVersion+1)
	}
	if ProtoVersionSupported(MinProtoVersion - 1) {
		t.Errorf("v%d (older than host floor) must be rejected", MinProtoVersion-1)
	}
}

func TestHandshakeMagicCookieSet(t *testing.T) {
	if Handshake.MagicCookieKey == "" || Handshake.MagicCookieValue == "" {
		t.Fatal("handshake magic cookie must be non-empty")
	}
}
