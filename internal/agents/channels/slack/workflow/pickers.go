// Slack picker resolvers — feed workflow_picker_resolve so AI authors
// editing a trigger match (channel_id whitelist, user whitelist) can
// get real IDs instead of guessing C123/U456.
//
// One PickerFunc per source name. Pulled live from the Slack API on
// each call — small workspaces tolerate that fine; heavy-traffic
// callers can wrap with their own caching layer at setup if needed.
package workflow

import (
	"context"
	"fmt"

	slackgo "github.com/slack-go/slack"

	"github.com/yogasw/wick/internal/agents/channels/slack"
	wfmcp "github.com/yogasw/wick/internal/agents/workflow/mcp"
)

// RegisterPickers wires this channel's picker sources into the
// workflow MCP picker registry. Setup composers call this after
// constructing the slack.Channel and the workflow mcp.Ops.
//
// Sources registered:
//
//	slack.channels    — public + private channels visible to the bot
//	slack.users       — workspace members (non-bot, non-deleted)
//	slack.usergroups  — workspace user groups (subteams)
func RegisterPickers(pr *wfmcp.PickerRegistry, ch *slack.Channel) {
	if pr == nil || ch == nil {
		return
	}
	pr.Register("slack.channels", channelsPicker(ch))
	pr.Register("slack.users", usersPicker(ch))
	pr.Register("slack.usergroups", usergroupsPicker(ch))
}

// channelsPicker returns a PickerFunc that lists channels the bot can
// see. Returns {id, name} pairs with the # prefix on names so the UI
// can render them as-is.
func channelsPicker(ch *slack.Channel) wfmcp.PickerFunc {
	return func(ctx context.Context, _ string) ([]wfmcp.PickerItem, error) {
		api := ch.API()
		if api == nil {
			return nil, fmt.Errorf("slack channel not configured")
		}
		params := &slackgo.GetConversationsParameters{
			ExcludeArchived: true,
			Types:           []string{"public_channel", "private_channel"},
			Limit:           1000,
		}
		out := []wfmcp.PickerItem{}
		for {
			chans, cursor, err := api.GetConversationsContext(ctx, params)
			if err != nil {
				return nil, err
			}
			for _, c := range chans {
				out = append(out, wfmcp.PickerItem{ID: c.ID, Name: "#" + c.Name})
			}
			if cursor == "" {
				break
			}
			params.Cursor = cursor
		}
		return out, nil
	}
}

// usersPicker returns a PickerFunc that lists workspace members. Bots
// and deleted users are filtered out — they're rarely valid match
// targets.
func usersPicker(ch *slack.Channel) wfmcp.PickerFunc {
	return func(ctx context.Context, _ string) ([]wfmcp.PickerItem, error) {
		api := ch.API()
		if api == nil {
			return nil, fmt.Errorf("slack channel not configured")
		}
		users, err := api.GetUsersContext(ctx)
		if err != nil {
			return nil, err
		}
		out := make([]wfmcp.PickerItem, 0, len(users))
		for _, u := range users {
			if u.IsBot || u.Deleted {
				continue
			}
			name := u.Profile.DisplayName
			if name == "" {
				name = u.RealName
			}
			if name == "" {
				name = u.Name
			}
			out = append(out, wfmcp.PickerItem{ID: u.ID, Name: name})
		}
		return out, nil
	}
}

// usergroupsPicker returns a PickerFunc that lists workspace
// user groups. Slack labels them as "subteams" in the API.
func usergroupsPicker(ch *slack.Channel) wfmcp.PickerFunc {
	return func(ctx context.Context, _ string) ([]wfmcp.PickerItem, error) {
		api := ch.API()
		if api == nil {
			return nil, fmt.Errorf("slack channel not configured")
		}
		groups, err := api.GetUserGroupsContext(ctx)
		if err != nil {
			return nil, err
		}
		out := make([]wfmcp.PickerItem, 0, len(groups))
		for _, g := range groups {
			out = append(out, wfmcp.PickerItem{ID: g.ID, Name: "@" + g.Handle})
		}
		return out, nil
	}
}
