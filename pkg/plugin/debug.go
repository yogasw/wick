package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	goplugin "github.com/hashicorp/go-plugin"
)

// EnvReattachOut, when set on a plugin process, switches Serve into debug
// (reattach) mode: instead of the normal go-plugin stdout handshake — which
// only works when the host is the process that spawned it — the plugin serves
// in test mode and writes its ReattachConfig as JSON to the named file. A host
// that finds a LIVE reattach file for this key attaches to this already-running
// process (see Manager.spawn) so breakpoints in the plugin bind.
//
// Run the plugin binary under a debugger (dlv) with this set, let it write the
// file, then start wick-lab; wick-lab attaches instead of spawning its own
// un-debuggable child.
const EnvReattachOut = "WICK_PLUGIN_REATTACH_OUT"

// reattachFile is the on-disk shape written by a debug-mode plugin and read by
// the host. net.Addr isn't JSON-round-trippable, so it's flattened to
// network + string and rebuilt on the host.
type reattachFile struct {
	Protocol        string `json:"protocol"`
	ProtocolVersion int    `json:"protocol_version"`
	Network         string `json:"network"`
	Addr            string `json:"addr"`
	Pid             int    `json:"pid"`
}

// serveReattach runs the plugin in go-plugin test mode and writes its
// ReattachConfig to outPath so a host can attach. Blocks until interrupted,
// mirroring the blocking contract of the normal Serve path. Removes any stale
// file up front and on exit so a host never reads a dead descriptor.
func serveReattach(cfg *goplugin.ServeConfig, outPath string) {
	_ = os.Remove(outPath)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reattachCh := make(chan *goplugin.ReattachConfig, 1)
	closeCh := make(chan struct{})
	cfg.Test = &goplugin.ServeTestConfig{
		Context:          ctx,
		ReattachConfigCh: reattachCh,
		CloseCh:          closeCh,
	}

	go goplugin.Serve(cfg) // returns immediately in test mode

	rc := <-reattachCh
	if err := writeReattachFile(outPath, rc); err != nil {
		fmt.Fprintf(os.Stderr, "wick plugin debug: write reattach file: %v\n", err)
		cancel()
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "wick plugin debug: serving on %s://%s (pid %d); reattach file %s\n",
		rc.Addr.Network(), rc.Addr.String(), rc.Pid, outPath)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	select {
	case <-sig:
	case <-closeCh:
	}
	_ = os.Remove(outPath)
	cancel()
	<-closeCh
}

func writeReattachFile(path string, rc *goplugin.ReattachConfig) error {
	f := reattachFile{
		Protocol:        string(rc.Protocol),
		ProtocolVersion: rc.ProtocolVersion,
		Network:         rc.Addr.Network(),
		Addr:            rc.Addr.String(),
		Pid:             rc.Pid,
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path) // atomic — host never sees a half-written file
}

// ReadReattachConfig loads a reattach file and rebuilds the ReattachConfig a
// host needs to attach — but ONLY when the plugin is actually reachable. It
// dials the address (short timeout) as the real liveness signal: a stale file
// from a stopped/relaunched debugger fails the dial, and the caller falls back
// to a normal spawn instead of attaching to a dead process. This is what makes
// reattach robust against dlv relaunches (new pid/port each time).
func ReadReattachConfig(path string) (*goplugin.ReattachConfig, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f reattachFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("parse reattach file %q: %w", path, err)
	}
	// Liveness: the gRPC server must be reachable. os.FindProcess is useless on
	// Windows (always succeeds), so probe the socket instead — it's both
	// cross-platform and exactly the signal that matters.
	conn, derr := net.DialTimeout(f.Network, f.Addr, 500*time.Millisecond)
	if derr != nil {
		return nil, fmt.Errorf("reattach target %s://%s not reachable: %w", f.Network, f.Addr, derr)
	}
	_ = conn.Close()

	addr, err := resolveAddr(f.Network, f.Addr)
	if err != nil {
		return nil, err
	}
	return &goplugin.ReattachConfig{
		Protocol:        goplugin.Protocol(f.Protocol),
		ProtocolVersion: f.ProtocolVersion,
		Addr:            addr,
		Pid:             f.Pid,
		// Test=true so the host's client.Kill never terminates the debugged
		// process — the debugger owns its lifecycle — and go-plugin skips the
		// stdout/stderr stdio tailing that only works for a spawned child.
		Test: true,
	}, nil
}

// ReattachPluginSet is the plugin descriptor set a host must pass as
// ClientConfig.Plugins when reattaching. Reattach bypasses version negotiation,
// so go-plugin never copies the matching VersionedPlugins entry into
// config.Plugins on its own; without this the dispense fails with "unknown
// plugin type". The host descriptor carries no Impl (it receives a client).
func ReattachPluginSet() goplugin.PluginSet {
	return goplugin.PluginSet{PluginName: &ConnectorGRPCPlugin{}}
}

func resolveAddr(network, addr string) (net.Addr, error) {
	switch network {
	case "unix", "unixgram", "unixpacket":
		return &net.UnixAddr{Name: addr, Net: network}, nil
	case "tcp", "tcp4", "tcp6":
		return net.ResolveTCPAddr(network, addr)
	default:
		return nil, fmt.Errorf("unsupported reattach network %q", network)
	}
}
