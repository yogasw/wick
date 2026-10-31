package slack

import (
	"fmt"
	"net/http"
	"os"
	"strings"

	slackgo "github.com/slack-go/slack"
	"regexp"

	"github.com/slack-go/slack/slackevents"
)

const dmOnlyUserEnv = "WICK_SLACK_DM_ONLY_USER"
const dmOnlyChannelEnv = "WICK_SLACK_DM_ONLY_CHANNEL"

var dmOnlyUserID = regexp.MustCompile(`^U[A-Z0-9]{8,}$`)
var dmOnlyChannelID = regexp.MustCompile(`^D[A-Z0-9]{8,}$`)

// dmOnlyRestricted is opt-in by presence, including an empty or malformed value.
// This guard limits inbound dispatch, not all Slack outbound API calls.
func dmOnlyRestricted() bool {
	_, userSet := os.LookupEnv(dmOnlyUserEnv)
	_, channelSet := os.LookupEnv(dmOnlyChannelEnv)
	return userSet || channelSet
}

func dmOnlyMessageAllowed(ev *slackevents.MessageEvent) bool {
	if !dmOnlyRestricted() {
		return true
	}
	user, channel := os.Getenv(dmOnlyUserEnv), os.Getenv(dmOnlyChannelEnv)
	if !dmOnlyUserID.MatchString(user) || !dmOnlyChannelID.MatchString(channel) || ev == nil {
		return false
	}
	return ev.User == user && ev.Channel == channel && ev.ChannelType == "im" && ev.BotID == "" && ev.SubType == ""
}

const connectOnlyEnv = "WICK_SLACK_CONNECT_ONLY"

func connectOnlyMode() bool { return os.Getenv(connectOnlyEnv) == "true" }

// Preserve the SDK's default client settings, wrapping only its transport.
func restrictedSlackHTTPClient() *http.Client {
	if !connectOnlyMode() {
		return http.DefaultClient
	}
	client := *http.DefaultClient
	next := client.Transport
	if next == nil {
		next = http.DefaultTransport
	}
	client.Transport = restrictedSlackTransport{next: next}
	return &client
}

type restrictedSlackTransport struct{ next http.RoundTripper }

func (p restrictedSlackTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Default deny includes conversations.open, message writes, reactions,
	// uploads, assistant status, views and unknown/new Slack methods.
	method := strings.TrimPrefix(req.URL.Path, "/api/")
	switch method {
	case "auth.test", "apps.connections.open", "users.info", "users.list", "team.info",
		"conversations.info", "conversations.list", "users.conversations":
		return p.next.RoundTrip(req)
	default:
		return nil, fmt.Errorf("Slack connection-only mode blocks API method %q", method)
	}
}

func dmOnlyLookupQueryAllowed(q string) bool {
	return dmOnlyRestricted() && dmOnlyUserID.MatchString(os.Getenv(dmOnlyUserEnv)) &&
		dmOnlyChannelID.MatchString(os.Getenv(dmOnlyChannelEnv)) && strings.EqualFold(q, os.Getenv(dmOnlyChannelEnv))
}

func dmOnlyConversationAllowed(ch *slackgo.Channel) bool {
	return ch != nil && dmOnlyLookupQueryAllowed(ch.ID) && ch.IsIM && ch.User == os.Getenv(dmOnlyUserEnv)
}
