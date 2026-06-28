// Command echo is a minimal connector plugin used by the plugin platform
// integration test. It is NOT a shipping connector.
package main

import (
	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/entity"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
	"github.com/yogasw/wick/pkg/wickdocs"
)

func main() {
	say := func(c *connector.Ctx) (any, error) {
		return map[string]string{"said": c.Input("text"), "token": c.Cfg("token")}, nil
	}
	mod := connector.Module{
		Meta: connector.Meta{Key: "echo", Name: "Echo"},
		Configs: entity.StructToConfigs(struct {
			Token string `wick:"token,secret"`
		}{}),
		Operations: []connector.Category{
			connector.Cat("Main", "",
				connector.Op("say", "Say", "echoes text + token",
					struct {
						Text string `wick:"text"`
					}{}, say, wickdocs.Docs{})),
		},
	}
	wickplugin.Serve(mod)
}
