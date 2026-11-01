package main

import (
	wickplugin "github.com/yogasw/wick/pkg/plugin"
	"github.com/yogasw/wick/pkg/plugin/toolplugin"
)

func main() {
	wickplugin.DeclareHostAccess([]string{wickplugin.ScopeHostEventsPublish}, nil)
	toolplugin.ServeTool(Module())
}
