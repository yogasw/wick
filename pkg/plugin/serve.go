package plugin

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"

	"github.com/yogasw/wick/pkg/connector"
)

// MaxGRPCMessageBytes is the plugin transport's max gRPC message size (both
// directions, both ends). gRPC defaults to 4 MiB recv, which is too small for
// ops that carry file payloads — e.g. extension_install sends a browser
// extension as base64 (a 4 MB .crx becomes ~6 MB on the wire). 64 MiB matches
// the connector-side upload cap. Server + client must agree, so both read this.
const MaxGRPCMessageBytes = 64 << 20

// grpcServerWithLimits mirrors goplugin.DefaultGRPCServer but raises the message
// size limits so large op payloads (file uploads) aren't rejected with
// ResourceExhausted.
func grpcServerWithLimits(opts []grpc.ServerOption) *grpc.Server {
	opts = append(opts,
		grpc.MaxRecvMsgSize(MaxGRPCMessageBytes),
		grpc.MaxSendMsgSize(MaxGRPCMessageBytes),
	)
	return grpc.NewServer(opts...)
}

// DumpManifest returns json.Marshal(mod) — the manifest is the module
// itself, so the plugin.json on disk can never drift from the binary.
func DumpManifest(mod connector.Module) ([]byte, error) {
	return json.Marshal(mod)
}

// Serve is the entire main() of a connector plugin binary. Call it with the
// module the binary wraps. When invoked with --dump-manifest it prints the
// manifest JSON and exits (used by `make plugins` / CI); otherwise it serves
// the gRPC plugin and blocks until the host disconnects.
func Serve(mod connector.Module) {
	args := os.Args[1:]
	dump := false
	signKey := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--dump-manifest":
			dump = true
		case "--sign-key":
			if i+1 < len(args) {
				signKey = args[i+1]
				i++
			}
		default:
			if v, ok := strings.CutPrefix(args[i], "--sign-key="); ok {
				signKey = v
			}
		}
	}
	if dump {
		m, err := BuildSelfManifest(mod, signKey)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		b, err := json.Marshal(m)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println(string(b))
		return
	}
	applyRlimits()
	cfg := &goplugin.ServeConfig{
		HandshakeConfig: Handshake,
		VersionedPlugins: map[int]goplugin.PluginSet{
			ProtoVersion: {PluginName: &ConnectorGRPCPlugin{Impl: NewServer(mod)}},
		},
		GRPCServer: grpcServerWithLimits,
	}
	// Debug: when WICK_PLUGIN_REATTACH_OUT is set the binary runs standalone
	// (typically under dlv) so a developer can breakpoint it — serve in test
	// mode and publish a reattach file instead of the parent-only stdout
	// handshake. See debug.go.
	if out := strings.TrimSpace(os.Getenv(EnvReattachOut)); out != "" {
		serveReattach(cfg, out)
		return
	}
	goplugin.Serve(cfg)
}
