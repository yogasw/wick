// Package slack — picker lookup sources.
//
// Implements channels.LookupProvider so the admin UI's picker widget can
// search the Slack workspace in real time (users, user groups, channels).
// Results are cached briefly per (source,query) to avoid hammering Slack's
// rate limits when the operator types.

package slack

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	slackgo "github.com/slack-go/slack"

	agentchannels "github.com/yogasw/wick/internal/agents/channels"
)

const (
	lookupMaxResults = 20
	lookupCacheTTL   = 60 * time.Second
)

type lookupCacheEntry struct {
	at    time.Time
	items []agentchannels.LookupItem
}

var (
	lookupCacheMu sync.Mutex
	lookupCache   = map[string]lookupCacheEntry{}
)

// Lookup satisfies channels.LookupProvider. Supported sources:
//   - "slack.users"      → workspace users (skips bots / deleted)
//   - "slack.usergroups" → user groups (matches name + handle)
//   - "slack.channels"   → public + private channels the bot can see
func (s *Channel) Lookup(source, query string) ([]agentchannels.LookupItem, error) {
	s.cfgMu.Lock()
	api := s.api
	s.cfgMu.Unlock()
	if api == nil {
		return nil, fmt.Errorf("slack not configured")
	}

	q := strings.ToLower(strings.TrimSpace(query))
	cacheKey := source + "|" + q
	lookupCacheMu.Lock()
	if e, ok := lookupCache[cacheKey]; ok && time.Since(e.at) < lookupCacheTTL {
		lookupCacheMu.Unlock()
		return e.items, nil
	}
	lookupCacheMu.Unlock()

	var items []agentchannels.LookupItem
	var err error
	switch source {
	case "slack.users":
		items, err = lookupSlackUsersAssistant(api, q)
		if err != nil || len(items) == 0 {
			if err != nil {
				log.Debug().Str("channel", "slack").Err(err).Msg("assistant.search.context users failed, falling back to users.list")
			}
			items, err = lookupSlackUsers(api, q)
		}
	case "slack.usergroups":
		items, err = lookupSlackUserGroups(api, q)
	case "slack.channels":
		items, err = lookupSlackChannelsAssistant(api, q)
		if err != nil || len(items) == 0 {
			if err != nil {
				log.Debug().Str("channel", "slack").Err(err).Msg("assistant.search.context channels failed, falling back to conversations.list")
			}
			items, err = lookupSlackChannels(api, q)
		}
	default:
		return nil, fmt.Errorf("unknown source %q", source)
	}
	if err != nil {
		return nil, err
	}

	lookupCacheMu.Lock()
	lookupCache[cacheKey] = lookupCacheEntry{at: time.Now(), items: items}
	lookupCacheMu.Unlock()
	return items, nil
}

// lookupSlackUsersAssistant queries assistant.search.context for messages
// matching q across all surfaces, then de-dupes by AuthorUserID to surface
// users who recently posted relevant content. Requires Slack AI features
// + chat:write (or assistant:write) scope.
func lookupSlackUsersAssistant(api *slackgo.Client, q string) ([]agentchannels.LookupItem, error) {
	if q == "" {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, err := api.SearchAssistantContextContext(ctx, slackgo.AssistantSearchContextParameters{
		Query:        q,
		ChannelTypes: []string{"public_channel", "private_channel", "im", "mpim"},
		ContentTypes: []string{"messages"},
		IncludeBots:  false,
		Limit:        50,
	})
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	out := make([]agentchannels.LookupItem, 0, lookupMaxResults)
	for _, m := range resp.Results.Messages {
		if m.AuthorUserID == "" || seen[m.AuthorUserID] {
			continue
		}
		seen[m.AuthorUserID] = true
		name := m.AuthorName
		if name == "" {
			name = m.AuthorUserID
		}
		out = append(out, agentchannels.LookupItem{ID: m.AuthorUserID, Name: name})
		if len(out) >= lookupMaxResults {
			break
		}
	}
	return out, nil
}

// lookupSlackChannelsAssistant queries assistant.search.context for channel
// entities matching q. The Slack response carries no channel ID, only a
// permalink (…/archives/<channel_id>) — parse it back out.
func lookupSlackChannelsAssistant(api *slackgo.Client, q string) ([]agentchannels.LookupItem, error) {
	if q == "" {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, err := api.SearchAssistantContextContext(ctx, slackgo.AssistantSearchContextParameters{
		Query:        q,
		ChannelTypes: []string{"public_channel", "private_channel"},
		ContentTypes: []string{"channels"},
		Limit:        50,
	})
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	out := make([]agentchannels.LookupItem, 0, lookupMaxResults)
	for _, ch := range resp.Results.Channels {
		id := channelIDFromPermalink(ch.Permalink)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, agentchannels.LookupItem{ID: id, Name: "#" + ch.Name})
		if len(out) >= lookupMaxResults {
			break
		}
	}
	return out, nil
}

// channelIDFromPermalink extracts the channel ID from a Slack archive
// permalink like https://team.slack.com/archives/C0123ABC or
// .../archives/C0123ABC/p1234567890. Returns empty when unparseable.
func channelIDFromPermalink(permalink string) string {
	const marker = "/archives/"
	i := strings.Index(permalink, marker)
	if i < 0 {
		return ""
	}
	rest := permalink[i+len(marker):]
	if j := strings.IndexByte(rest, '/'); j >= 0 {
		rest = rest[:j]
	}
	if q := strings.IndexByte(rest, '?'); q >= 0 {
		rest = rest[:q]
	}
	return rest
}

func lookupSlackUsers(api *slackgo.Client, q string) ([]agentchannels.LookupItem, error) {
	users, err := api.GetUsers()
	if err != nil {
		return nil, err
	}
	out := make([]agentchannels.LookupItem, 0, lookupMaxResults)
	for _, u := range users {
		if u.Deleted || u.IsBot {
			continue
		}
		name := u.RealName
		if name == "" {
			name = u.Profile.DisplayName
		}
		if name == "" {
			name = u.Name
		}
		if q != "" && !containsFold(name, q) && !containsFold(u.Name, q) && !containsFold(u.ID, q) {
			continue
		}
		out = append(out, agentchannels.LookupItem{ID: u.ID, Name: name})
		if len(out) >= lookupMaxResults {
			break
		}
	}
	return out, nil
}

func lookupSlackUserGroups(api *slackgo.Client, q string) ([]agentchannels.LookupItem, error) {
	groups, err := api.GetUserGroups()
	if err != nil {
		return nil, err
	}
	out := make([]agentchannels.LookupItem, 0, lookupMaxResults)
	for _, g := range groups {
		if q != "" && !containsFold(g.Name, q) && !containsFold(g.Handle, q) && !containsFold(g.ID, q) {
			continue
		}
		label := g.Name
		if g.Handle != "" {
			label = g.Name + " (@" + g.Handle + ")"
		}
		out = append(out, agentchannels.LookupItem{ID: g.ID, Name: label})
		if len(out) >= lookupMaxResults {
			break
		}
	}
	return out, nil
}

func lookupSlackChannels(api *slackgo.Client, q string) ([]agentchannels.LookupItem, error) {
	params := &slackgo.GetConversationsParameters{
		ExcludeArchived: true,
		Limit:           200,
		Types:           []string{"public_channel", "private_channel"},
	}
	out := make([]agentchannels.LookupItem, 0, lookupMaxResults)
	for {
		chans, cursor, err := api.GetConversations(params)
		if err != nil {
			return nil, err
		}
		for _, ch := range chans {
			if q != "" && !containsFold(ch.Name, q) && !containsFold(ch.ID, q) {
				continue
			}
			out = append(out, agentchannels.LookupItem{ID: ch.ID, Name: "#" + ch.Name})
			if len(out) >= lookupMaxResults {
				return out, nil
			}
		}
		if cursor == "" {
			break
		}
		params.Cursor = cursor
	}
	return out, nil
}

func containsFold(s, sub string) bool {
	if sub == "" {
		return true
	}
	return strings.Contains(strings.ToLower(s), sub)
}
