package workflow

import (
	"context"
	"fmt"

	slackgo "github.com/slack-go/slack"

	"github.com/yogasw/wick/internal/agents/channels/slack"
	"github.com/yogasw/wick/internal/agents/workflow/integration"
	"github.com/yogasw/wick/pkg/wickdocs"
)

// PublishHomeInput renders the bot's Home tab for a specific user.
// Typically called from an app_home_opened trigger so each user sees
// content tailored to them.
type PublishHomeInput struct {
	User string `json:"user"           wick:"required;desc=Target user ID"`
	Hash string `json:"hash,omitempty" wick:"desc=Concurrency token (optional, from previous publish)"`
	View string `json:"view"           wick:"required;textarea;desc=Block Kit Home tab JSON (type: home)"`
}

type PublishHomeOutput struct {
	ViewID   string `json:"view_id"`
	ViewHash string `json:"view_hash"`
}

func registerActionPublishHome(reg *integration.Registry, ch *slack.Channel) {
	reg.RegisterAction(integration.ActionDescriptor{
		Channel:     Channel,
		Action:      "publish_home",
		Name:        "Slack: Publish Home tab",
		Description: "Render the bot's Home tab for a user. Top-level view JSON must have type: \"home\". Pair with the app_home_opened event for per-user dynamic content.",
		InputType:   PublishHomeInput{},
		OutputType:  PublishHomeOutput{},
		Docs: wickdocs.Docs{
			OutputShape: map[string]string{
				"view_id":   "Published Home tab view ID.",
				"view_hash": "Concurrency hash. Pass back as input.hash on the next publish_home for safe optimistic concurrency.",
			},
			TemplateableFields: []string{"user", "hash", "view"},
			Quirks: []string{
				"Top-level view JSON MUST have type: \"home\" — not \"modal\".",
				"hash is optional but recommended when multiple workflows can publish to the same user concurrently; without it the later publish always wins.",
			},
			PairWith: []string{"channel:slack.app_home_opened"},
			OutputSample: `{"view_id":"VH0123ABCDE","view_hash":"1700001234.abcdef00"}`,
			Examples: []wickdocs.Example{
				{
					Name: "render_home_on_open",
					YAML: `- id: render_home
  type: channel
  channel: slack
  action: publish_home
  arg_modes:
    user: expression
    view: expression
  args:
    user: '{{.Node.trigger.payload.user}}'
    view: '{{toJSON .Node.build_view.home}}'`,
				},
			},
		},
		Execute: func(ctx context.Context, args map[string]any) (any, error) {
			api := ch.API()
			if api == nil {
				return nil, fmt.Errorf("slack channel not configured")
			}
			userID, err := argString(args, "user")
			if err != nil {
				return nil, err
			}
			hash := argStringOpt(args, "hash")
			var view slackgo.HomeTabViewRequest
			if err := argJSON(args, "view", &view); err != nil {
				return nil, err
			}
			req := slackgo.PublishViewContextRequest{UserID: userID, View: view}
			if hash != "" {
				req.Hash = &hash
			}
			resp, err := api.PublishViewContext(ctx, req)
			if err != nil {
				return nil, err
			}
			return PublishHomeOutput{ViewID: resp.View.ID, ViewHash: resp.View.Hash}, nil
		},
	})
}
