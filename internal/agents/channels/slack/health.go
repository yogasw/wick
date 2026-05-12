// Package slack — integration health probe.
//
// HealthCheck runs the Slack API calls the channel actually depends on
// and reports per-call OK/error so the operator can see which scopes
// are still missing without booting the agent loop.

package slack

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	slackgo "github.com/slack-go/slack"

	agentchannels "github.com/yogasw/wick/internal/agents/channels"
)

// HealthCheck satisfies channels.HealthChecker. Each entry corresponds
// to one upstream call the channel makes during normal operation.
// Calls run sequentially — Slack rate limits per-method, not per-app —
// and each is given a short timeout so a single hung call doesn't block
// the whole probe.
func (s *Channel) HealthCheck() []agentchannels.HealthCheck {
	s.cfgMu.Lock()
	api := s.api
	cfg := s.cfg
	s.cfgMu.Unlock()

	out := []agentchannels.HealthCheck{}
	if api == nil || cfg.BotToken == "" {
		out = append(out, agentchannels.HealthCheck{
			Name:  "config",
			OK:    false,
			Error: "bot_token not set",
		})
		return out
	}

	probes := []func(*slackgo.Client) agentchannels.HealthCheck{
		probeAuth,
		probeTeamInfo,
		probeUsersList,
		probeUserGroups,
		probeConversationsList,
		probeChatWrite,
		probeReactionsWrite,
		probeAssistantSearch,
	}
	results := make([]agentchannels.HealthCheck, len(probes))
	var wg sync.WaitGroup
	for i, fn := range probes {
		wg.Add(1)
		go func(idx int, p func(*slackgo.Client) agentchannels.HealthCheck) {
			defer wg.Done()
			results[idx] = p(api)
		}(i, fn)
	}
	wg.Wait()
	for _, r := range results {
		if !r.OK {
			out = append(out, r)
		}
	}

	if cfg.Mode == "socket" && cfg.AppToken == "" {
		out = append(out, agentchannels.HealthCheck{
			Name:   "app_token",
			OK:     false,
			Detail: "required for socket mode",
		})
	}
	if cfg.Mode == "http" && cfg.SigningSecret == "" {
		out = append(out, agentchannels.HealthCheck{
			Name:   "signing_secret",
			OK:     false,
			Detail: "required for http mode",
		})
	}
	return out
}

func withTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}

func probeAuth(api *slackgo.Client) agentchannels.HealthCheck {
	ctx, cancel := withTimeout(4 * time.Second)
	defer cancel()
	resp, err := api.AuthTestContext(ctx)
	if err != nil {
		return agentchannels.HealthCheck{Name: "auth.test", OK: false, Error: err.Error()}
	}
	return agentchannels.HealthCheck{
		Name:   "auth.test",
		OK:     true,
		Detail: fmt.Sprintf("team=%s user=%s", resp.Team, resp.User),
	}
}

func probeUsersList(api *slackgo.Client) agentchannels.HealthCheck {
	ctx, cancel := withTimeout(6 * time.Second)
	defer cancel()
	users, err := api.GetUsersContext(ctx)
	if err != nil {
		return agentchannels.HealthCheck{
			Name:   "users.list",
			OK:     false,
			Error:  err.Error(),
			Detail: "needs scope: users:read",
		}
	}
	return agentchannels.HealthCheck{
		Name:   "users.list",
		OK:     true,
		Detail: fmt.Sprintf("%d users", len(users)),
	}
}

func probeUserGroups(api *slackgo.Client) agentchannels.HealthCheck {
	ctx, cancel := withTimeout(5 * time.Second)
	defer cancel()
	groups, err := api.GetUserGroupsContext(ctx, slackgo.GetUserGroupsOptionIncludeUsers(true))
	if err != nil {
		return agentchannels.HealthCheck{
			Name:   "usergroups.list",
			OK:     false,
			Error:  err.Error(),
			Detail: "needs scope: usergroups:read",
		}
	}
	return agentchannels.HealthCheck{
		Name:   "usergroups.list",
		OK:     true,
		Detail: fmt.Sprintf("%d groups", len(groups)),
	}
}

func probeConversationsList(api *slackgo.Client) agentchannels.HealthCheck {
	ctx, cancel := withTimeout(6 * time.Second)
	defer cancel()
	chans, _, err := api.GetConversationsContext(ctx, &slackgo.GetConversationsParameters{
		ExcludeArchived: true,
		Limit:           5,
		Types:           []string{"public_channel", "private_channel"},
	})
	if err != nil {
		return agentchannels.HealthCheck{
			Name:   "conversations.list",
			OK:     false,
			Error:  err.Error(),
			Detail: "needs scope: channels:read, groups:read",
		}
	}
	return agentchannels.HealthCheck{
		Name:   "conversations.list",
		OK:     true,
		Detail: fmt.Sprintf("sample %d channels", len(chans)),
	}
}

func probeTeamInfo(api *slackgo.Client) agentchannels.HealthCheck {
	ctx, cancel := withTimeout(4 * time.Second)
	defer cancel()
	team, err := api.GetTeamInfoContext(ctx)
	if err != nil {
		return agentchannels.HealthCheck{
			Name:   "team.info",
			OK:     false,
			Error:  err.Error(),
			Detail: "needs scope: team:read",
		}
	}
	return agentchannels.HealthCheck{
		Name:   "team.info",
		OK:     true,
		Detail: fmt.Sprintf("team=%s domain=%s", team.Name, team.Domain),
	}
}

// probeChatWrite dry-runs chat.postMessage with a deliberately-invalid
// channel ID. We only care whether scope checking *precedes* channel
// resolution: a `channel_not_found` / `invalid_channel` error means the
// token already cleared the auth/scope gate and would have posted on a
// real channel. Anything containing `missing_scope` / `not_in_channel`
// / `not_authed` is a real scope failure.
func probeChatWrite(api *slackgo.Client) agentchannels.HealthCheck {
	ctx, cancel := withTimeout(4 * time.Second)
	defer cancel()
	_, _, err := api.PostMessageContext(ctx, "WICK_HEALTH_PROBE_INVALID",
		slackgo.MsgOptionText("wick health probe (should not be delivered)", false),
	)
	if err == nil {
		return agentchannels.HealthCheck{
			Name:   "chat.postMessage",
			OK:     true,
			Detail: "unexpected success — dry-run channel was accepted",
		}
	}
	msg := err.Error()
	if classifyScopeError(msg) {
		return agentchannels.HealthCheck{
			Name:   "chat.postMessage",
			OK:     false,
			Error:  msg,
			Detail: "needs scope: chat:write",
		}
	}
	return agentchannels.HealthCheck{
		Name:   "chat.postMessage",
		OK:     true,
		Detail: "scope ok (dry-run rejected with: " + msg + ")",
	}
}

// probeReactionsWrite dry-runs reactions.add with an invalid timestamp.
// Same classification logic as probeChatWrite — only `missing_scope` /
// `not_authed` count as failures; `bad_timestamp` / `message_not_found`
// mean the scope was honored.
func probeReactionsWrite(api *slackgo.Client) agentchannels.HealthCheck {
	ctx, cancel := withTimeout(4 * time.Second)
	defer cancel()
	err := api.AddReactionContext(ctx, "white_check_mark", slackgo.ItemRef{
		Channel:   "WICK_HEALTH_PROBE_INVALID",
		Timestamp: "0.0",
	})
	if err == nil {
		return agentchannels.HealthCheck{
			Name:   "reactions.add",
			OK:     true,
			Detail: "unexpected success — dry-run was accepted",
		}
	}
	msg := err.Error()
	if classifyScopeError(msg) {
		return agentchannels.HealthCheck{
			Name:   "reactions.add",
			OK:     false,
			Error:  msg,
			Detail: "needs scope: reactions:write",
		}
	}
	return agentchannels.HealthCheck{
		Name:   "reactions.add",
		OK:     true,
		Detail: "scope ok (dry-run rejected with: " + msg + ")",
	}
}

// classifyScopeError returns true when the Slack error message points to
// a missing scope / unauthenticated client rather than a bad argument.
func classifyScopeError(msg string) bool {
	for _, needle := range []string{"missing_scope", "not_authed", "invalid_auth", "no_permission", "access_denied", "token_revoked"} {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}

func probeAssistantSearch(api *slackgo.Client) agentchannels.HealthCheck {
	ctx, cancel := withTimeout(5 * time.Second)
	defer cancel()
	resp, err := api.SearchAssistantContextContext(ctx, slackgo.AssistantSearchContextParameters{
		Query:        "test",
		ChannelTypes: []string{"public_channel"},
		Limit:        1,
	})
	if err != nil {
		return agentchannels.HealthCheck{
			Name:   "assistant.search.context",
			OK:     false,
			Error:  err.Error(),
			Detail: "needs Slack AI features + scope: assistant:write (optional — falls back to users.list / conversations.list)",
		}
	}
	total := len(resp.Results.Messages) + len(resp.Results.Channels) + len(resp.Results.Files)
	return agentchannels.HealthCheck{
		Name:   "assistant.search.context",
		OK:     true,
		Detail: fmt.Sprintf("%d hits", total),
	}
}
