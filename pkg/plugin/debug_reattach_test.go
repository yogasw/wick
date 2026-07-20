package plugin

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	goplugin "github.com/hashicorp/go-plugin"

	"github.com/yogasw/wick/pkg/connector"
)

// TestReattachRoundTrip proves the reattach path end-to-end WITHOUT dlv: a
// plugin serves in test mode + writes a reattach file, then a fresh go-plugin
// client reads that file and dispenses "connector". If this passes, the
// dispense-fail seen under dlv is dlv-specific (it intercepts/pauses the
// process), not a bug in our reattach wiring.
func TestReattachRoundTrip(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "reattach.json")

	// Minimal module — enough to dispense a connector.
	mod := connector.Module{
		Meta: connector.Meta{Key: "probe", Name: "Probe"},
	}
	cfg := &goplugin.ServeConfig{
		HandshakeConfig: Handshake,
		VersionedPlugins: map[int]goplugin.PluginSet{
			ProtoVersion: {PluginName: &ConnectorGRPCPlugin{Impl: NewServer(mod)}},
		},
		GRPCServer: goplugin.DefaultGRPCServer,
	}

	// Serve in test mode in-process; capture the ReattachConfig and write the
	// file exactly as serveReattach does.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rcCh := make(chan *goplugin.ReattachConfig, 1)
	closeCh := make(chan struct{})
	cfg.Test = &goplugin.ServeTestConfig{Context: ctx, ReattachConfigCh: rcCh, CloseCh: closeCh}
	go goplugin.Serve(cfg)

	var rc *goplugin.ReattachConfig
	select {
	case rc = <-rcCh:
	case <-time.After(5 * time.Second):
		t.Fatal("plugin did not publish reattach config")
	}
	if err := writeReattachFile(out, rc); err != nil {
		t.Fatalf("write reattach file: %v", err)
	}

	// Host side: read the file (with the dial liveness check) and attach.
	got, err := ReadReattachConfig(out)
	if err != nil {
		t.Fatalf("ReadReattachConfig: %v", err)
	}
	client := goplugin.NewClient(&goplugin.ClientConfig{
		HandshakeConfig: Handshake,
		// Reattach skips version negotiation, so go-plugin never copies the
		// matching VersionedPlugins set into config.Plugins — set it directly.
		Plugins:          ReattachPluginSet(),
		Reattach:         got,
		AllowedProtocols: []goplugin.Protocol{goplugin.ProtocolGRPC},
	})
	defer client.Kill()

	rpc, err := client.Client()
	if err != nil {
		t.Fatalf("client.Client(): %v", err)
	}
	raw, err := rpc.Dispense(PluginName)
	if err != nil {
		t.Fatalf("dispense %q: %v", PluginName, err)
	}
	if _, ok := raw.(GRPCConn); !ok {
		t.Fatalf("dispensed unexpected type %T", raw)
	}

	cancel()
	select {
	case <-closeCh:
	case <-time.After(3 * time.Second):
	}
	_ = os.Remove(out)
}
