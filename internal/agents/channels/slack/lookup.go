// Package slack — picker lookup sources.
//
// Implements channels.LookupProvider so the admin UI's picker widget can
// search the Slack workspace in real time (users, user groups, channels).
// Results are cached briefly per (source,query), and the workspace listings
// they are filtered from (users.list, the bot's channels) per bot, so typing
// filters one listing instead of paging Slack on every key.

package slack

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	slackgo "github.com/slack-go/slack"
	"golang.org/x/sync/singleflight"

	agentchannels "github.com/yogasw/wick/internal/agents/channels"
)

const (
	lookupMaxResults = 20
	lookupCacheTTL   = 60 * time.Second
	// listingTTL is how long one workspace listing is filtered locally
	// before it is paged from Slack again.
	listingTTL = 5 * time.Minute
)

type listingEntry struct {
	at   time.Time
	data any
}

var (
	listingMu    sync.Mutex
	listingCache = map[string]listingEntry{}
	listingGroup singleflight.Group
)

// cachedListing returns the listing under key, paging it through fetch at
// most once per listingTTL. Concurrent callers share one fetch, and a failed
// refresh (rate limited, usually) keeps serving the expired listing.
func cachedListing[T any](key string, fetch func() ([]T, error)) ([]T, error) {
	listingMu.Lock()
	e, ok := listingCache[key]
	listingMu.Unlock()
	if ok && time.Since(e.at) < listingTTL {
		return e.data.([]T), nil
	}
	v, err, _ := listingGroup.Do(key, func() (any, error) {
		list, err := fetch()
		if err != nil {
			return nil, err
		}
		listingMu.Lock()
		listingCache[key] = listingEntry{at: time.Now(), data: list}
		listingMu.Unlock()
		return list, nil
	})
	if err != nil {
		if ok {
			log.Debug().Str("channel", "slack").Str("listing", key).Err(err).Msg("listing refresh failed, serving stale")
			return e.data.([]T), nil
		}
		return nil, err
	}
	return v.([]T), nil
}

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
//   - "slack.bots"       → bot / app users (users.list is_bot, not deleted)
//   - "slack.channels"   → public + private channels this bot is a MEMBER of
func (s *Channel) Lookup(source, query string) ([]agentchannels.LookupItem, error) {
	s.cfgMu.Lock()
	api := s.api
	instance := s.ownerUserID
	s.cfgMu.Unlock()
	if api == nil {
		return nil, fmt.Errorf("slack not configured")
	}

	q := strings.ToLower(strings.TrimSpace(query))
	// The cache is package-level but the results are per-bot (each
	// instance has its own token and its own channel memberships), so the
	// owning instance has to be part of the key — otherwise the first bot
	// to answer a query serves its channel list to every other bot.
	cacheKey := instance + "|" + source + "|" + q
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
			items, err = lookupSlackUsers(api, instance, q)
		}
	case "slack.usergroups":
		items, err = lookupSlackUserGroups(api, q)
	case "slack.bots":
		items, err = lookupSlackBots(api, instance, q)
	case "slack.channels":
		// No assistant.search.context here: it searches the whole
		// workspace, which is exactly what this source must NOT offer.
		items, err = lookupSlackChannels(api, instance, q)
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

// workspaceUsers is users.list for the bot, shared by the users and bots
// sources.
func workspaceUsers(api *slackgo.Client, instance string) ([]slackgo.User, error) {
	return cachedListing(instance+"|users.list", func() ([]slackgo.User, error) { return api.GetUsers() })
}

func lookupSlackUsers(api *slackgo.Client, instance, q string) ([]agentchannels.LookupItem, error) {
	users, err := workspaceUsers(api, instance)
	if err != nil {
		return nil, err
	}
	return userItems(users, q), nil
}

func userItems(users []slackgo.User, q string) []agentchannels.LookupItem {
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
	return out
}

// lookupSlackBots lists the workspace's bot users for the BotsMode allow
// list. The item id is the bot's user id (U...), which is what a bot's
// message event carries in its user field. Slackbot is skipped: it is a
// bot user in users.list but never a meaningful trigger.
func lookupSlackBots(api *slackgo.Client, instance, q string) ([]agentchannels.LookupItem, error) {
	users, err := workspaceUsers(api, instance)
	if err != nil {
		return nil, err
	}
	return botItems(users, q), nil
}

func botItems(users []slackgo.User, q string) []agentchannels.LookupItem {
	// Apps first, Workflow Builder bots after: every published workflow gets
	// its own bot user named after the workflow, so one app can sit among
	// several same-named workflow bots and the app is the one usually meant.
	apps := make([]agentchannels.LookupItem, 0, lookupMaxResults)
	var workflows []agentchannels.LookupItem
	for _, u := range users {
		if u.Deleted || !u.IsBot || u.ID == "USLACKBOT" {
			continue
		}
		name := u.RealName
		if name == "" {
			name = u.Profile.DisplayName
		}
		if name == "" {
			name = u.Name
		}
		if q != "" && !containsFold(name, q) && !containsFold(u.Name, q) && !containsFold(u.ID, q) && !containsFold(u.Profile.BotID, q) {
			continue
		}
		if isWorkflowBot(u.Name) {
			workflows = append(workflows, agentchannels.LookupItem{ID: u.ID, Name: name + " (workflow)"})
			continue
		}
		apps = append(apps, agentchannels.LookupItem{ID: u.ID, Name: name})
	}
	out := append(apps, workflows...)
	if len(out) > lookupMaxResults {
		out = out[:lookupMaxResults]
	}
	return out
}

// isWorkflowBot reports a Workflow Builder bot user: Slack names them
// "wf_bot_<workflow id>", and their display name is the workflow's own.
func isWorkflowBot(username string) bool {
	return strings.HasPrefix(username, "wf_bot_")
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

// lookupSlackChannels lists the channels THIS bot is a member of.
//
// users.conversations, not conversations.list: the latter returns every
// non-archived channel in the workspace, so the picker offered hundreds
// of channels the bot was never invited to — a trigger scoped to one of
// them can never fire, and a send to it fails with not_in_channel.
func lookupSlackChannels(api *slackgo.Client, instance, q string) ([]agentchannels.LookupItem, error) {
	chans, err := cachedListing(instance+"|users.conversations", func() ([]slackgo.Channel, error) {
		params := &slackgo.GetConversationsForUserParameters{
			ExcludeArchived: true,
			Limit:           200,
			Types:           []string{"public_channel", "private_channel"},
		}
		var all []slackgo.Channel
		for {
			page, cursor, err := api.GetConversationsForUser(params)
			if err != nil {
				return nil, err
			}
			all = append(all, page...)
			if cursor == "" {
				return all, nil
			}
			params.Cursor = cursor
		}
	})
	if err != nil {
		return nil, err
	}
	return channelItems(chans, q), nil
}

func channelItems(chans []slackgo.Channel, q string) []agentchannels.LookupItem {
	out := make([]agentchannels.LookupItem, 0, lookupMaxResults)
	for _, ch := range chans {
		if q != "" && !containsFold(ch.Name, q) && !containsFold(ch.ID, q) {
			continue
		}
		out = append(out, agentchannels.LookupItem{ID: ch.ID, Name: "#" + ch.Name})
		if len(out) >= lookupMaxResults {
			break
		}
	}
	return out
}

func containsFold(s, sub string) bool {
	if sub == "" {
		return true
	}
	return strings.Contains(strings.ToLower(s), sub)
}

// InstanceForBot returns the registered Slack instance whose resolved
// bot user id matches botID, or nil when none does.
//
// One process hosts one Slack instance per owning user, all reporting
// Name() == "slack", so callers that need a SPECIFIC bot cannot use
// Registry.ChannelByName — it returns whichever was registered first.
// An empty botID never matches (an instance whose auth.test hasn't
// landed yet also reports "", and "the bot with no id" is not a thing
// anyone means to select).
func InstanceForBot(reg *agentchannels.Registry, botID string) *Channel {
	if reg == nil || botID == "" {
		return nil
	}
	for _, ch := range reg.Channels() {
		sc, ok := ch.(*Channel)
		if !ok {
			continue
		}
		if sc.BotUserID() == botID {
			return sc
		}
	}
	return nil
}
