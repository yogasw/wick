// Package plugin is the wick connector plugin platform: the shared
// transport (hashicorp/go-plugin + gRPC) used by the host to drive a
// connector that runs in its own process, and the SDK a connector binary
// uses to serve itself. See docs/proposals/connector-plugin-platform.
package plugin

import (
	"context"

	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"

	pb "github.com/yogasw/wick/pkg/plugin/proto"
)

// PluginName is the dispense key both sides agree on.
const PluginName = "connector"

// ProtoVersion is the wire-contract version a freshly built plugin stamps into
// its manifest (the version this build of the SDK speaks natively). It bumps
// ONLY on a breaking proto change.
const ProtoVersion = 1

// MinProtoVersion is the OLDEST proto version this host still accepts. The host
// supports the inclusive range [MinProtoVersion, ProtoVersion]; a plugin built
// against any version in that range loads, anything outside is rejected at
// handshake/verify with a clear error (never a crash).
//
// This is the range-negotiation contract (§19.2): bumping ProtoVersion for a
// breaking change while leaving MinProtoVersion behind opens a transition
// window where v(old) and v(new) plugins both run; dropping support for an old
// version is a deliberate MinProtoVersion bump, not an accident.
const MinProtoVersion = 1

// SupportedProtoVersions returns every proto version this host can speak, newest
// first. Used both to advertise VersionedPlugins to go-plugin and to validate a
// manifest's declared version.
func SupportedProtoVersions() []int {
	out := make([]int, 0, ProtoVersion-MinProtoVersion+1)
	for v := ProtoVersion; v >= MinProtoVersion; v-- {
		out = append(out, v)
	}
	return out
}

// ProtoVersionSupported reports whether v is within the accepted range.
func ProtoVersionSupported(v int) bool {
	return v >= MinProtoVersion && v <= ProtoVersion
}

// Handshake is the basic mutual sanity check go-plugin runs before any
// RPC: a plugin launched without the matching magic cookie exits with a
// human-readable error instead of hanging.
var Handshake = goplugin.HandshakeConfig{
	ProtocolVersion:  ProtoVersion,
	MagicCookieKey:   "WICK_CONNECTOR_PLUGIN",
	MagicCookieValue: "b3f1c2a4-wick-connector-grpc",
}

// VersionedPlugins maps proto_version -> plugin set. The host advertises every
// version in [MinProtoVersion, ProtoVersion]; go-plugin negotiates the highest
// version common to host and plugin and rejects (clear error, no crash) a
// plugin whose version falls outside the host's range. Built from the supported
// range so adding a future v2 plugin set is the only edit needed.
var VersionedPlugins = buildVersionedPlugins()

func buildVersionedPlugins() map[int]goplugin.PluginSet {
	out := make(map[int]goplugin.PluginSet, ProtoVersion-MinProtoVersion+1)
	for _, v := range SupportedProtoVersions() {
		// All current versions share the same gRPC plugin descriptor. When a
		// breaking v2 lands, register its descriptor here keyed by 2.
		out[v] = goplugin.PluginSet{PluginName: &ConnectorGRPCPlugin{}}
	}
	return out
}

// ConnectorGRPCPlugin is the go-plugin descriptor. Impl is set only on the
// plugin side (it carries the server implementation); the host leaves it
// nil and receives a client from GRPCClient.
type ConnectorGRPCPlugin struct {
	goplugin.NetRPCUnsupportedPlugin
	Impl pb.ConnectorServer
}

func (p *ConnectorGRPCPlugin) GRPCServer(_ *goplugin.GRPCBroker, s *grpc.Server) error {
	pb.RegisterConnectorServer(s, p.Impl)
	return nil
}

func (p *ConnectorGRPCPlugin) GRPCClient(_ context.Context, _ *goplugin.GRPCBroker, c *grpc.ClientConn) (any, error) {
	return &grpcClient{inner: pb.NewConnectorClient(c)}, nil
}
