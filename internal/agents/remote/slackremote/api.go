package slackremote

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/yogasw/wick/internal/agents/remote"
)

// Message is a Slack message as the adapter reads it, from an event or a
// conversations.replies page.
type Message struct {
	Channel  string
	TS       string
	ThreadTS string
	User     string
	BotID    string
	Text     string
	// Edited is set for a message_changed event.
	Edited bool
	// Deleted is set for a message_deleted event: TS is the deleted one.
	Deleted bool
	// Reactions are emoji names on the message.
	Reactions []string
	// Status is metadata event_type agent_status's payload status, if any.
	Status string
}

// API is the slice of the Slack Web API the adapter needs.
type API interface {
	// AuthTest returns who the token posts as.
	AuthTest(ctx context.Context) (userID, botID string, err error)
	// OpenDM opens (or finds) the DM with user.
	OpenDM(ctx context.Context, user string) (string, error)
	// Post sends text; threadTS empty = a new message.
	Post(ctx context.Context, channel, text, threadTS string) (string, error)
	// Replies lists thread's messages newer than oldest.
	Replies(ctx context.Context, channel, threadTS, oldest string) ([]Message, error)
	// History lists channel's top-level messages newer than oldest.
	History(ctx context.Context, channel, oldest string) ([]Message, error)
}

// BaseURL is the Slack Web API root; tests point it at a fake.
var BaseURL = "https://slack.com/api"

// HTTPAPI is API over Slack's Web API with one token. The token is never
// logged or returned.
type HTTPAPI struct {
	Token  string
	Client *http.Client
}

func (a HTTPAPI) client() *http.Client {
	if a.Client != nil {
		return a.Client
	}
	return &http.Client{Timeout: 20 * time.Second}
}

// call POSTs form to method and decodes the reply into out. A 429 comes
// back as a remote.RetryAfterError.
func (a HTTPAPI) call(ctx context.Context, method string, form url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, BaseURL+"/"+method, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Bearer "+a.Token)
	resp, err := a.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		sec, _ := strconv.Atoi(resp.Header.Get("Retry-After"))
		if sec <= 0 {
			sec = 1
		}
		return &remote.RetryAfterError{After: time.Duration(sec) * time.Second, Err: fmt.Errorf("slack %s: rate limited", method)}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	var head struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &head); err != nil {
		return fmt.Errorf("slack %s: %s", method, resp.Status)
	}
	if !head.OK {
		return fmt.Errorf("slack %s: %s", method, head.Error)
	}
	if out != nil {
		return json.Unmarshal(body, out)
	}
	return nil
}

func (a HTTPAPI) AuthTest(ctx context.Context) (string, string, error) {
	var r struct {
		UserID string `json:"user_id"`
		BotID  string `json:"bot_id"`
	}
	err := a.call(ctx, "auth.test", url.Values{}, &r)
	return r.UserID, r.BotID, err
}

func (a HTTPAPI) OpenDM(ctx context.Context, user string) (string, error) {
	var r struct {
		Channel struct {
			ID string `json:"id"`
		} `json:"channel"`
	}
	if err := a.call(ctx, "conversations.open", url.Values{"users": {user}}, &r); err != nil {
		return "", err
	}
	if r.Channel.ID == "" {
		return "", errors.New("slack conversations.open: no channel")
	}
	return r.Channel.ID, nil
}

func (a HTTPAPI) Post(ctx context.Context, channel, text, threadTS string) (string, error) {
	form := url.Values{"channel": {channel}, "text": {text}, "unfurl_links": {"false"}}
	if threadTS != "" {
		form.Set("thread_ts", threadTS)
	}
	var r struct {
		TS string `json:"ts"`
	}
	if err := a.call(ctx, "chat.postMessage", form, &r); err != nil {
		return "", err
	}
	return r.TS, nil
}

// repliesLimit is the page size of a thread or history read.
const repliesLimit = 100

// wireMessage is a message as the Web API returns it.
type wireMessage struct {
	TS        string `json:"ts"`
	ThreadTS  string `json:"thread_ts"`
	User      string `json:"user"`
	BotID     string `json:"bot_id"`
	Text      string `json:"text"`
	Reactions []struct {
		Name string `json:"name"`
	} `json:"reactions"`
	Metadata struct {
		EventType    string         `json:"event_type"`
		EventPayload map[string]any `json:"event_payload"`
	} `json:"metadata"`
}

func (w wireMessage) message(channel string) Message {
	m := Message{Channel: channel, TS: w.TS, ThreadTS: w.ThreadTS, User: w.User, BotID: w.BotID, Text: w.Text}
	for _, r := range w.Reactions {
		m.Reactions = append(m.Reactions, r.Name)
	}
	if w.Metadata.EventType == "agent_status" {
		m.Status, _ = w.Metadata.EventPayload["status"].(string)
	}
	return m
}

func (a HTTPAPI) list(ctx context.Context, method string, form url.Values, channel string) ([]Message, error) {
	var r struct {
		Messages []wireMessage `json:"messages"`
	}
	form.Set("include_all_metadata", "true")
	form.Set("limit", strconv.Itoa(repliesLimit))
	if err := a.call(ctx, method, form, &r); err != nil {
		return nil, err
	}
	out := make([]Message, 0, len(r.Messages))
	for _, w := range r.Messages {
		out = append(out, w.message(channel))
	}
	return out, nil
}

func (a HTTPAPI) Replies(ctx context.Context, channel, threadTS, oldest string) ([]Message, error) {
	form := url.Values{"channel": {channel}, "ts": {threadTS}}
	if oldest != "" {
		form.Set("oldest", oldest)
	}
	return a.list(ctx, "conversations.replies", form, channel)
}

func (a HTTPAPI) History(ctx context.Context, channel, oldest string) ([]Message, error) {
	form := url.Values{"channel": {channel}}
	if oldest != "" {
		form.Set("oldest", oldest)
	}
	return a.list(ctx, "conversations.history", form, channel)
}
