// Command googleworkspace runs the wick Google Workspace connector as an
// out-of-process plugin. OAuth identity resolution is served over gRPC
// (ResolveIdentity); at execute time the host injects the access_token via creds.
package main

import (
	"github.com/yogasw/wick/internal/connectors/googleworkspace"
	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/entity"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

func main() {
	mod := connector.Module{
		Meta:          googleworkspace.Meta(),
		Configs:       entity.StructToConfigs(googleworkspace.Configs{}),
		Operations:    googleworkspace.Operations(),
		HealthCheck:   googleworkspace.HealthCheck,
		OAuth:         googleworkspace.OAuthMeta(),
		DefaultAccess: connector.AccessDefaults{EnableSSO: true, AllowOthersConnectSSO: true},
	}
	wickplugin.Serve(mod)
}
