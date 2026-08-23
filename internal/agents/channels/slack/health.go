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
	agentconfig "github.com/yogasw/wick/internal/agents/config"
)

// HealthCheck satisfies channels.HealthChecker. Each entry corresponds
// to one upstream call the channel makes during normal operation.
// Calls run sequentially — Slack rate limits per-method, not per-app —
// and each is given a short timeout so a single hung call doesn't block
// the whole probe.
//
// Transport mode is probed separately at the end: for socket mode the
// current subscription state is reported and an async Reconnect is
// kicked off when disconnected/errored (anti-duplicate guard inside
// Reconnect). For http mode the public webhook URL is verified.
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
		probeUserEmail,
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

	// Transport mode probe: always emitted so the operator sees
	// subscribe status / webhook URL even when other probes pass.
	out = append(out, s.probeTransport(cfg))
	return out
}

// probeTransport reports the runtime status of whichever transport the
// channel is configured for. Socket mode: current subscription state
// plus an async reconnect when disconnected. HTTP mode: public URL +
// signing secret presence.
func (s *Channel) probeTransport(cfg agentconfig.SlackChannelConfig) agentchannels.HealthCheck {
	if cfg.Mode == "http" {
		if cfg.SigningSecret == "" {
			return agentchannels.HealthCheck{
				Name:   "http.webhook",
				OK:     false,
				Detail: "signing_secret required for http mode",
			}
		}
		s.cfgMu.Lock()
		pubURL := s.pubURL
		s.cfgMu.Unlock()
		if pubURL == "" {
			return agentchannels.HealthCheck{
				Name:   "http.webhook",
				OK:     false,
				Detail: "public URL not configured — Slack can't reach POST /integrations/slack/events",
			}
		}
		return agentchannels.HealthCheck{
			Name:   "http.webhook",
			OK:     true,
			Detail: pubURL + "/integrations/slack/events",
		}
	}

	// Socket mode (default).
	if cfg.AppToken == "" {
		return agentchannels.HealthCheck{
			Name:   "socket.subscribe",
			OK:     false,
			Detail: "app_token (xapp-...) required for socket mode",
		}
	}
	state, at := s.SocketState()
	switch state {
	case "connected":
		age := time.Since(at).Round(time.Second)
		return agentchannels.HealthCheck{
			Name:   "socket.subscribe",
			OK:     true,
			Detail: fmt.Sprintf("subscribed (connected %s ago)", age),
		}
	case "connecting":
		return agentchannels.HealthCheck{
			Name:   "socket.subscribe",
			OK:     false,
			Detail: "still connecting — try again in a few seconds",
		}
	case "error", "disconnected":
		s.Reconnect(context.Background())
		return agentchannels.HealthCheck{
			Name:   "socket.subscribe",
			OK:     false,
			Error:  "not subscribed (state=" + state + ")",
			Detail: "kicked off async reconnect — re-run the test to verify",
		}
	default:
		// Empty state: never started, or applyConfig wiped it.
		s.Reconnect(context.Background())
		return agentchannels.HealthCheck{
			Name:   "socket.subscribe",
			OK:     false,
			Detail: "no connection yet — kicked off async connect, re-run the test to verify",
		}
	}
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

// probeUserEmail checks the one scope identity resolution depends on:
// users:read.email.
//
// This needs its own probe because its failure is SILENT. Without the scope,
// users.info still succeeds — it just returns a blank email — so users.list
// passing tells you nothing about it. Every sender would then be refused with
// "email is required" and the operator would have no idea which scope to add.
//
// Reads real members rather than a synthetic id: the email field is only
// populated for actual humans, so probing a made-up user would look identical
// to a missing scope.
func probeUserEmail(api *slackgo.Client) agentchannels.HealthCheck {
	const name = "users.info (email)"
	const need = "needs scope: users:read.email — without it wick cannot match a Slack sender to a wick account"

	ctx, cancel := withTimeout(6 * time.Second)
	defer cancel()
	users, err := api.GetUsersContext(ctx)
	if err != nil {
		// users.list already reports its own failure; don't double-report the
		// same cause as an email problem.
		return agentchannels.HealthCheck{
			Name:   name,
			OK:     true,
			Detail: "skipped: users.list unavailable",
		}
	}

	humans, withEmail := countMemberEmails(users)
	return emailScopeVerdict(name, need, humans, withEmail)
}

// countMemberEmails tallies real people and how many of them carry an email.
//
// Bots, apps and deleted accounts legitimately have no email, so counting them
// would make a healthy workspace look like a missing scope.
func countMemberEmails(users []slackgo.User) (humans, withEmail int) {
	for _, u := range users {
		if u.IsBot || u.Deleted || u.ID == "USLACKBOT" {
			continue
		}
		humans++
		if strings.TrimSpace(u.Profile.Email) != "" {
			withEmail++
		}
	}
	return humans, withEmail
}

// emailScopeVerdict turns the tally into a health result.
//
// The distinction that matters: NOBODY having an email means the scope is
// missing, while SOME members lacking one is normal — real workspaces have
// members with no address on file. Reporting the second as a failure would
// train operators to ignore this check.
func emailScopeVerdict(name, need string, humans, withEmail int) agentchannels.HealthCheck {
	switch {
	case humans == 0:
		// Nothing to judge from. Report OK rather than inventing a failure the
		// operator cannot act on.
		return agentchannels.HealthCheck{
			Name:   name,
			OK:     true,
			Detail: "no human members visible to check",
		}
	case withEmail == 0:
		return agentchannels.HealthCheck{
			Name:   name,
			OK:     false,
			Error:  "no member emails returned",
			Detail: need,
		}
	case withEmail < humans:
		return agentchannels.HealthCheck{
			Name: name,
			OK:   true,
			Detail: fmt.Sprintf("%d of %d members have an email; the rest cannot be matched to a wick account",
				withEmail, humans),
		}
	default:
		return agentchannels.HealthCheck{
			Name:   name,
			OK:     true,
			Detail: fmt.Sprintf("%d members have an email", withEmail),
		}
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
