// Command slack runs the wick Slack connector as an out-of-process plugin.
// Phase 0: bot-token path only (OAuth ResolveIdentity is added in a later
// task). Reuses internal/connectors/slack verbatim.
package main

import (
	"github.com/yogasw/wick/internal/connectors/slack"
	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/entity"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

func main() {
	mod := connector.Module{
		Meta:        slack.Meta(),
		Configs:     entity.StructToConfigs(slack.Configs{}),
		Operations:  slack.Operations(),
		HealthCheck: slack.HealthCheck,
	}
	wickplugin.Serve(mod)
}
