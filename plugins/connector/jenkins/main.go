// Command jenkins is the Jenkins connector shipped as an external wick plugin.
// Same key as the in-tree connector it replaced, so existing instances,
// accounts and access rows keep matching.
package main

import (
	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/entity"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
	"github.com/yogasw/wick/plugins/tags"
)

func main() {
	m := Meta()
	m.DefaultTags = []entity.DefaultTag{tags.Connector, tags.Development}
	wickplugin.Serve(connector.Module{
		Meta: m,
		Configs: entity.StructToConfigs(Configs{
			DefaultDepth: 3,
			MaxDepth:     6,
		}),
		Operations: Operations(),
	})
}
