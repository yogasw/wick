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

// ProtoVersion is the wire-contract version negotiated at handshake. It
// bumps ONLY on a breaking proto change.
const ProtoVersion = 1

// Handshake is the basic mutual sanity check go-plugin runs before any
// RPC: a plugin launched without the matching magic cookie exits with a
// human-readable error instead of hanging.
var Handshake = goplugin.HandshakeConfig{
	ProtocolVersion:  ProtoVersion,
	MagicCookieKey:   "WICK_CONNECTOR_PLUGIN",
	MagicCookieValue: "b3f1c2a4-wick-connector-grpc",
}

// VersionedPlugins maps proto_version -> plugin set. The host advertises
// every version it can speak; go-plugin negotiates the highest common one
// and rejects a plugin whose version is unsupported (clear error, no
// crash).
var VersionedPlugins = map[int]goplugin.PluginSet{
	ProtoVersion: {PluginName: &ConnectorGRPCPlugin{}},
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
