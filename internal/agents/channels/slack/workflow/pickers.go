// Slack picker resolvers — feed workflow_picker_resolve so AI authors
// editing a trigger match (channel_id whitelist, user whitelist) can
// get real IDs instead of guessing C123/U456.
//
// One PickerFunc per source name. Pulled live from the Slack API on
// each call — small workspaces tolerate that fine; heavy-traffic
// callers can wrap with their own caching layer at setup if needed.
//
// Every source fans out over EVERY registered Slack instance: one
// process hosts one bot per owning user, each with its own token and
// its own set of channels. Binding a picker to a single instance would
// offer the author channels the bot that actually runs the workflow has
// never joined.
package workflow

import (
	"context"
	"fmt"

	slackgo "github.com/slack-go/slack"

	agentchannels "github.com/yogasw/wick/internal/agents/channels"
	"github.com/yogasw/wick/internal/agents/channels/slack"
	wfmcp "github.com/yogasw/wick/internal/agents/workflow/mcp"
)

// RegisterPickers wires this channel's picker sources into the
// workflow MCP picker registry. Setup composers call this after
// constructing the channel registry.
//
// Sources registered:
//
//	slack.channels    — channels each bot is a MEMBER of
//	slack.users       — workspace members (non-bot, non-deleted)
//	slack.usergroups  — workspace user groups (subteams)
func RegisterPickers(pr *wfmcp.PickerRegistry, reg *agentchannels.Registry) {
	if pr == nil || reg == nil {
		return
	}
	pr.Register("slack.channels", channelsPicker(reg))
	pr.Register("slack.users", usersPicker(reg))
	pr.Register("slack.usergroups", usergroupsPicker(reg))
}

// slackInstances returns every Slack channel instance in the registry,
// in registration order.
func slackInstances(reg *agentchannels.Registry) []*slack.Channel {
	out := []*slack.Channel{}
	for _, ch := range reg.Channels() {
		if sc, ok := ch.(*slack.Channel); ok {
			out = append(out, sc)
		}
	}
	return out
}

// perInstance runs fn against each configured Slack instance and merges
// the results, de-duped by item ID. When more than one bot is connected,
// each name is suffixed with the bot that can reach it so the author can
// tell two same-named entries apart. An instance whose call fails is
// skipped rather than failing the whole lookup — one bot with a missing
// scope shouldn't blank the dropdown for the others.
func perInstance(reg *agentchannels.Registry, fn func(context.Context, *slackgo.Client) ([]wfmcp.PickerItem, error)) wfmcp.PickerFunc {
	return func(ctx context.Context, _ string) ([]wfmcp.PickerItem, error) {
		insts := slackInstances(reg)
		if len(insts) == 0 {
			return nil, fmt.Errorf("slack channel not configured")
		}
		label := len(insts) > 1
		seen := map[string]bool{}
		out := []wfmcp.PickerItem{}
		var lastErr error
		for _, inst := range insts {
			api := inst.API()
			if api == nil {
				continue
			}
			items, err := fn(ctx, api)
			if err != nil {
				lastErr = err
				continue
			}
			bot := inst.BotUserName()
			for _, it := range items {
				if seen[it.ID] {
					continue
				}
				seen[it.ID] = true
				if label && bot != "" {
					it.Name = it.Name + " — @" + bot
				}
				out = append(out, it)
			}
		}
		if len(out) == 0 && lastErr != nil {
			return nil, lastErr
		}
		return out, nil
	}
}

// channelsPicker lists the channels each bot is a MEMBER of.
//
// users.conversations (GetConversationsForUser with an empty User =
// the token's own bot), NOT conversations.list: the latter returns every
// non-archived channel in the workspace, including thousands the bot was
// never invited to — picking one of those yields a trigger that can
// never fire and a send that fails with not_in_channel.
func channelsPicker(reg *agentchannels.Registry) wfmcp.PickerFunc {
	return perInstance(reg, func(ctx context.Context, api *slackgo.Client) ([]wfmcp.PickerItem, error) {
		params := &slackgo.GetConversationsForUserParameters{
			ExcludeArchived: true,
			Types:           []string{"public_channel", "private_channel"},
			Limit:           1000,
		}
		out := []wfmcp.PickerItem{}
		for {
			chans, cursor, err := api.GetConversationsForUserContext(ctx, params)
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
	})
}

// usersPicker lists workspace members. Bots and deleted users are
// filtered out — they're rarely valid match targets.
func usersPicker(reg *agentchannels.Registry) wfmcp.PickerFunc {
	return perInstance(reg, func(ctx context.Context, api *slackgo.Client) ([]wfmcp.PickerItem, error) {
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
	})
}

// usergroupsPicker lists workspace user groups. Slack labels them as
// "subteams" in the API.
func usergroupsPicker(reg *agentchannels.Registry) wfmcp.PickerFunc {
	return perInstance(reg, func(ctx context.Context, api *slackgo.Client) ([]wfmcp.PickerItem, error) {
		groups, err := api.GetUserGroupsContext(ctx)
		if err != nil {
			return nil, err
		}
		out := make([]wfmcp.PickerItem, 0, len(groups))
		for _, g := range groups {
			out = append(out, wfmcp.PickerItem{ID: g.ID, Name: "@" + g.Handle})
		}
		return out, nil
	})
}
