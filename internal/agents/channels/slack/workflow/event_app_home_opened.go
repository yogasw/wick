package workflow

import (
	"github.com/yogasw/wick/internal/agents/workflow/integration"
	"github.com/yogasw/wick/pkg/wickdocs"
)

// AppHomeOpenedEvent fires when a user opens the bot's Home tab. Use
// it to push a fresh Home view via the publish_home action.
type AppHomeOpenedEvent struct {
	User string `json:"user"`
	Tab  string `json:"tab"` // "home" | "messages"
}

func registerEventAppHomeOpened(reg *integration.Registry) {
	reg.RegisterEvent(integration.EventDescriptor{
		Channel:     Channel,
		Event:       "app_home_opened",
		Name:        "Slack: App Home opened",
		Description: "Fires when a user opens the bot's Home tab. Pair with publish_home to render dynamic content.",
		PayloadType: AppHomeOpenedEvent{},
		Docs: wickdocs.Docs{
			OutputShape: map[string]string{
				"payload.user": "User who opened the tab. Use to render per-user Home content.",
				"payload.tab":  "\"home\" for the bot's Home tab, \"messages\" for the DM tab.",
			},
			Quirks: []string{
				"Fires every time the user navigates to the Home tab, not just the first time. Throttle in your workflow if the build cost is non-trivial.",
				"You usually only care about tab == \"home\" — filter or branch on it.",
			},
			PairWith: []string{
				"channel:slack.publish_home",
			},
			OutputSample: `{"type":"channel","channel":"slack","event":"app_home_opened","payload":{"user":"U02ABCDEF","tab":"home"}}`,
			Examples: []wickdocs.Example{
				{
					Name: "render_home",
					YAML: `- type: channel
  channel: slack
  event: app_home_opened
  entry_node: build_home_view`,
				},
			},
		},
	})
}
