package slack

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"

	slackgo "github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
)

const dmOnlyUserEnv = "WICK_SLACK_DM_ONLY_USER"
const dmOnlyChannelEnv = "WICK_SLACK_DM_ONLY_CHANNEL"

var dmOnlyUserID = regexp.MustCompile(`^U[A-Z0-9]{8,}$`)
var dmOnlyChannelID = regexp.MustCompile(`^D[A-Z0-9]{8,}$`)

// dmOnlyRestricted is opt-in by presence, including an empty or malformed value.
// It gates inbound dispatch and, once the connection-only stage is over, also
// restricts outbound replies to the configured DM.
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
// The wrapper stays installed for the whole DM restriction: connection-only first,
// then the reply allowlist once WICK_SLACK_CONNECT_ONLY is cleared.
func restrictedSlackHTTPClient() *http.Client {
	if !connectOnlyMode() && !dmOnlyRestricted() {
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

// restrictedReadMethods stay available in both restriction stages: they only read
// workspace/identity data and never post to a conversation.
var restrictedReadMethods = map[string]bool{
	"auth.test":             true,
	"apps.connections.open": true,
	"users.info":            true,
	"users.list":            true,
	"team.info":             true,
	"conversations.info":    true,
	"conversations.list":    true,
	"users.conversations":   true,
}

// restrictedReplyMethods are the only writes the DM reply stage may perform.
// assistant.threads.setStatus drives the "Thinking…/Working…" banner; it is
// scoped to the same DM as the replies.
var restrictedReplyMethods = map[string]bool{
	"chat.postMessage":            true,
	"chat.update":                 true,
	"assistant.threads.setStatus": true,
}

type restrictedSlackTransport struct{ next http.RoundTripper }

func (p restrictedSlackTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	method := strings.TrimPrefix(req.URL.Path, "/api/")
	if restrictedReadMethods[method] {
		return p.next.RoundTrip(req)
	}
	if connectOnlyMode() {
		// Default deny includes conversations.open, message writes, reactions,
		// uploads, assistant status, views and unknown/new Slack methods.
		return nil, fmt.Errorf("Slack connection-only mode blocks API method %q", method)
	}
	if !restrictedReplyMethods[method] {
		return nil, fmt.Errorf("Slack DM reply mode blocks API method %q", method)
	}
	channel, err := replyRequestBodyChannel(req)
	if err != nil {
		return nil, err
	}
	if !dmOnlyReplyTargetAllowed(channel) {
		return nil, fmt.Errorf("Slack DM reply mode blocks channel %q", channel)
	}
	return p.next.RoundTrip(req)
}

// replyRequestBodyChannel reads the form body to inspect the target channel and
// restores it so the wrapped transport still sends the original request.
func replyRequestBodyChannel(req *http.Request) (string, error) {
	if req.Body == nil {
		return "", fmt.Errorf("Slack DM reply mode blocks request without a body")
	}
	body, err := io.ReadAll(io.LimitReader(req.Body, 8<<20))
	_ = req.Body.Close()
	if err != nil {
		return "", fmt.Errorf("Slack DM reply mode cannot read request body: %w", err)
	}
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }
	values, err := url.ParseQuery(string(body))
	if err != nil {
		return "", fmt.Errorf("Slack DM reply mode cannot parse request body: %w", err)
	}
	// chat.postMessage/chat.update carry "channel"; assistant.threads.setStatus
	// carries "channel_id".
	if ch := values.Get("channel"); ch != "" {
		return ch, nil
	}
	return values.Get("channel_id"), nil
}

// dmOnlyReplyTargetAllowed accepts exactly the configured owner DM and fails
// closed when either variable is missing or malformed.
func dmOnlyReplyTargetAllowed(channel string) bool {
	if !dmOnlyRestricted() || channel == "" {
		return false
	}
	return dmOnlyChannelID.MatchString(os.Getenv(dmOnlyChannelEnv)) && channel == os.Getenv(dmOnlyChannelEnv)
}

func dmOnlyLookupQueryAllowed(q string) bool {
	return dmOnlyRestricted() && dmOnlyUserID.MatchString(os.Getenv(dmOnlyUserEnv)) &&
		dmOnlyChannelID.MatchString(os.Getenv(dmOnlyChannelEnv)) && strings.EqualFold(q, os.Getenv(dmOnlyChannelEnv))
}

func dmOnlyConversationAllowed(ch *slackgo.Channel) bool {
	return ch != nil && dmOnlyLookupQueryAllowed(ch.ID) && ch.IsIM && ch.User == os.Getenv(dmOnlyUserEnv)
}
