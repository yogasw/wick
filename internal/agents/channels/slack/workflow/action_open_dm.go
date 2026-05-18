package workflow

import (
	"context"
	"fmt"

	slackgo "github.com/slack-go/slack"

	"github.com/yogasw/wick/internal/agents/channels/slack"
	"github.com/yogasw/wick/internal/agents/workflow/integration"
)

// OpenDMInput is the schema for the slack.open_dm action.
type OpenDMInput struct {
	UserID string `json:"user_id" wick:"required;key=user_id;desc=Slack user ID (U...) to open DM with"`
}

// OpenDMOutput is the response containing the DM channel ID.
type OpenDMOutput struct {
	ChannelID string `json:"channel_id"` // D... format DM channel ID
	UserID    string `json:"user_id"`    // echoed back for reference
}

func registerActionOpenDM(reg *integration.Registry, ch *slack.Channel) {
	reg.RegisterAction(integration.ActionDescriptor{
		Channel:     Channel,
		Action:      "open_dm",
		Name:        "Slack: Open DM channel",
		Description: "Open or retrieve an existing DM channel with a user by their Slack user ID (U...). Returns the channel_id (D...) to use in send_message.",
		InputType:   OpenDMInput{},
		OutputType:  OpenDMOutput{},
		Destructive: false,
		Execute: func(ctx context.Context, args map[string]any) (any, error) {
			api := ch.API()
			if api == nil {
				return nil, fmt.Errorf("slack channel not configured")
			}
			userID, err := argString(args, "user_id")
			if err != nil {
				return nil, err
			}
			if userID == "" {
				return nil, fmt.Errorf("user_id is required")
			}
			dmCh, _, _, err := api.OpenConversationContext(ctx, &slackgo.OpenConversationParameters{
				Users: []string{userID},
			})
			if err != nil {
				return nil, fmt.Errorf("open DM with %s: %w", userID, err)
			}
			return OpenDMOutput{ChannelID: dmCh.ID, UserID: userID}, nil
		},
	})
}
