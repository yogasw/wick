// Package slack — send.go: local agent-proxy send handler + reply helpers.
//
// Purpose: HTTP handler for POST /integrations/slack/send (localhost-only),
//
//	Block-Kit reply helpers (postReply, postReplyWithFooter, signedContextBlock),
//	and the ConnectorTokenFn type alias.
//
// Caller:   Channel.HTTPHandlers() mounts sendHandler(); postChunked calls
//
//	postReply / postReplyWithFooter.
//
// Dependencies: slackgo, appname, zerolog.
// Main Functions:
//   - sendHandler()           — HTTP proxy endpoint, bot or user-token post
//   - postReply()             — plain-text thread reply with backoff
//   - postReplyWithFooter()   — Block Kit reply with muted footer
//   - signedContextBlock()    — builds "Sent using <@BOT>" context block
//
// Side Effects: none (mutates no global state; userTokenCache is per-Channel).
package slack

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/rs/zerolog/log"
	slackgo "github.com/slack-go/slack"

	"github.com/yogasw/wick/internal/appname"
)

// ConnectorTokenFn resolves an xoxp user token from the connectors service
// for the given Slack user ID. Called by sendHandler when sender_user_id is
// set. Return found=false when no connector row holds a token for that user.
type ConnectorTokenFn func(ctx context.Context, slackUserID string) (token string, found bool)

// SetConnectorTokenFn wires an optional user-token lookup function so
// sendHandler can post messages appearing to come from a specific user.
// Safe to call after New; nil = no user-token DM support (default).
func (s *Channel) SetConnectorTokenFn(fn ConnectorTokenFn) {
	s.cfgMu.Lock()
	s.connectorToken = fn
	s.cfgMu.Unlock()
}

// sendHandler returns an http.Handler for the local agent send proxy.
// It accepts JSON {"channel_id","text","sender_user_id"?} from localhost
// and posts to Slack using wick's own authenticated client — no bot token
// is exposed to callers.
//
// If sender_user_id is set:
//  1. s.userTokenCache is checked first (read-lock).
//  2. On cache miss: s.connectorToken is called; result is stored.
//  3. If a token is found: post with display name + avatar override via
//     a temporary xoxp client (chat:write.customize scope required).
//  4. If no token: fall back to bot api with cosmetic username override.
func (s *Channel) sendHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		// Localhost-only guard — agents run on the same host as wick.
		host := r.RemoteAddr
		if !strings.HasPrefix(host, "127.") && !strings.HasPrefix(host, "[::1]") && host != "localhost" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		var body struct {
			ChannelID    string `json:"channel_id"`
			Text         string `json:"text"`
			SenderUserID string `json:"sender_user_id,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ChannelID == "" || body.Text == "" {
			http.Error(w, `{"error":"channel_id and text are required"}`, http.StatusBadRequest)
			return
		}

		s.cfgMu.Lock()
		api := s.api
		connTokFn := s.connectorToken
		s.cfgMu.Unlock()

		if api == nil {
			http.Error(w, `{"error":"slack not configured"}`, http.StatusServiceUnavailable)
			return
		}

		// Build Block Kit message with muted context-block footer.
		textBlock := slackgo.NewSectionBlock(
			slackgo.NewTextBlockObject(slackgo.MarkdownType, body.Text, false, false),
			nil, nil,
		)
		blocks := []slackgo.Block{textBlock, s.signedContextBlock()}
		opts := []slackgo.MsgOption{slackgo.MsgOptionBlocks(blocks...)}

		if body.SenderUserID != "" {
			// Attempt user-token impersonation so the message renders
			// with the sender's display name and avatar.
			token := s.resolveUserToken(r.Context(), body.SenderUserID, connTokFn)
			if token != "" {
				// Use a temporary xoxp client; do NOT cache the client — only
				// the token is cached. Token creation is cheap; client objects
				// hold goroutines, so we create one per-request from cache.
				xoxpClient := slackgo.New(token)
				if u, err := xoxpClient.GetUserInfoContext(r.Context(), body.SenderUserID); err == nil {
					name := u.Profile.DisplayName
					if name == "" {
						name = u.Profile.RealName
					}
					opts = append(opts,
						slackgo.MsgOptionUsername(name),
						slackgo.MsgOptionIconURL(u.Profile.Image72),
					)
					_, ts, err := xoxpClient.PostMessageContext(r.Context(), body.ChannelID, opts...)
					if err != nil {
						log.Warn().Str("channel", "slack").Str("sender", body.SenderUserID).Err(err).
							Msg("sendHandler: xoxp post failed, falling back to bot")
					} else {
						writeOK(w, ts)
						return
					}
				} else {
					log.Warn().Str("channel", "slack").Str("sender", body.SenderUserID).Err(err).
						Msg("sendHandler: GetUserInfo via xoxp failed, using cosmetic fallback")
				}
			}

			// Cosmetic fallback via bot api: resolve display name with bot token.
			if u, err := api.GetUserInfoContext(r.Context(), body.SenderUserID); err == nil {
				name := u.Profile.DisplayName
				if name == "" {
					name = u.Profile.RealName
				}
				opts = append(opts,
					slackgo.MsgOptionUsername(name),
					slackgo.MsgOptionIconURL(u.Profile.Image72),
				)
			}
		}

		_, ts, err := api.PostMessageContext(r.Context(), body.ChannelID, opts...)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			fmt.Fprintf(w, `{"error":%q}`, err.Error())
			return
		}
		writeOK(w, ts)
	})
}

// resolveUserToken returns the xoxp token for senderUserID, checking the
// in-process cache first and invoking fn on a miss. Returns "" when no
// token is found or fn is nil.
func (s *Channel) resolveUserToken(ctx context.Context, senderUserID string, fn ConnectorTokenFn) string {
	if fn == nil {
		return ""
	}
	// Fast path: read lock.
	s.userTokenMu.RLock()
	if t, ok := s.userTokenCache[senderUserID]; ok {
		s.userTokenMu.RUnlock()
		return t
	}
	s.userTokenMu.RUnlock()

	// Slow path: call connector lookup, then write to cache.
	token, found := fn(ctx, senderUserID)
	if !found {
		token = "" // normalise
	}
	s.userTokenMu.Lock()
	if s.userTokenCache == nil {
		s.userTokenCache = make(map[string]string)
	}
	s.userTokenCache[senderUserID] = token
	s.userTokenMu.Unlock()
	return token
}

// signedContextBlock returns a muted Block Kit context block with the bot
// mention footer — "Sent using @BotName" matching Slack MCP Claude's style.
// Uses <@botUserID> so Slack renders the bot's display name as a mention.
func (s *Channel) signedContextBlock() slackgo.Block {
	s.cfgMu.Lock()
	botID := s.botUserID
	s.cfgMu.Unlock()
	var footerText string
	if botID != "" {
		footerText = "Sent using <@" + botID + ">"
	} else {
		footerText = "Sent using *" + appname.Resolve() + "*"
	}
	return slackgo.NewContextBlock("",
		slackgo.NewTextBlockObject(slackgo.MarkdownType, footerText, false, false),
	)
}

// postReply posts a plain-text reply in a Slack thread, with retry backoff
// on rate-limit errors.
func (s *Channel) postReply(channelID, threadTS, text string) {
	s.cfgMu.Lock()
	api := s.api
	s.cfgMu.Unlock()
	s.withBackoff(func() error {
		_, _, err := api.PostMessage(
			channelID,
			slackgo.MsgOptionText(text, false),
			slackgo.MsgOptionTS(threadTS),
		)
		return err
	})
}

// postReplyWithFooter posts the message body as a section block and appends
// a muted context block footer — Block Kit context elements render smaller
// than regular text, matching the "Sent using @Claude" style in Slack.
func (s *Channel) postReplyWithFooter(channelID, threadTS, text string) {
	s.cfgMu.Lock()
	api := s.api
	s.cfgMu.Unlock()
	blocks := []slackgo.Block{
		slackgo.NewSectionBlock(
			slackgo.NewTextBlockObject(slackgo.MarkdownType, text, false, false),
			nil, nil,
		),
		s.signedContextBlock(),
	}
	s.withBackoff(func() error {
		_, _, err := api.PostMessage(
			channelID,
			slackgo.MsgOptionBlocks(blocks...),
			slackgo.MsgOptionTS(threadTS),
		)
		return err
	})
}

func writeOK(w http.ResponseWriter, ts string) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"ok":true,"ts":%q}`, ts)
}

// AuthTestWithToken calls auth.test for the given xoxp token and returns
// the Slack UserID of the token owner. Used by the server's connector-token
// lookup to match a user_token connector row to a Slack user ID.
func AuthTestWithToken(ctx context.Context, token string) (userID string, err error) {
	client := slackgo.New(token)
	resp, err := client.AuthTestContext(ctx)
	if err != nil {
		return "", err
	}
	return resp.UserID, nil
}
