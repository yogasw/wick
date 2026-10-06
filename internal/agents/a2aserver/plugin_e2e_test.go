package a2aserver

import (
	"strings"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"

	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/remote"
	"github.com/yogasw/wick/internal/agents/remote/pluginremote"
	"github.com/yogasw/wick/internal/services/plugin/plugintest"
)

// A Team agent sourced from a service plugin is exposed back through its
// A2A Connection: A2A caller → wick A2A server → pool → pluginremote → the
// real example_a2a_repeater process, and the plugin's answer streams out.
func TestPluginAgentOverA2A(t *testing.T) {
	h := plugintest.StartRepeater(t)
	f := newFixture(t)
	wirePool(t, f, func(string) provider.Spawner {
		return remote.Spawner{Source: pluginremote.NewSource(plugintest.RepeaterKey, plugintest.Transport(h))}
	})
	text, state, msg := stream(t, f, "ping")
	if state != a2a.TaskStateCompleted || !strings.HasPrefix(text, "echo: ping (turn 1 of conv-") || msg != text {
		t.Fatalf("text=%q state=%v msg=%q", text, state, msg)
	}
}
