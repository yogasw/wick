package plugin

import (
	"encoding/json"
	"fmt"
	"os"

	goplugin "github.com/hashicorp/go-plugin"

	"github.com/yogasw/wick/pkg/connector"
)

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
	for _, a := range os.Args[1:] {
		if a == "--dump-manifest" {
			b, err := DumpManifest(mod)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			fmt.Println(string(b))
			return
		}
	}
	goplugin.Serve(&goplugin.ServeConfig{
		HandshakeConfig: Handshake,
		VersionedPlugins: map[int]goplugin.PluginSet{
			ProtoVersion: {PluginName: &ConnectorGRPCPlugin{Impl: NewServer(mod)}},
		},
		GRPCServer: goplugin.DefaultGRPCServer,
	})
}
