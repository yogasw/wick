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
			TargetUserID string `json:"target_user_id,omitempty"`
			Text         string `json:"text"`
			SenderUserID string `json:"sender_user_id,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Text == "" {
			http.Error(w, `{"error":"text is required"}`, http.StatusBadRequest)
			return
		}
		if body.ChannelID == "" && body.TargetUserID == "" {
			http.Error(w, `{"error":"channel_id or target_user_id is required"}`, http.StatusBadRequest)
			return
		}

		// Auto-promote: if channel_id looks like a Slack user ID (U... or W...),
		// treat it as target_user_id so conversations.open opens the right DM.
		if body.TargetUserID == "" &&
			len(body.ChannelID) > 1 &&
			(body.ChannelID[0] == 'U' || body.ChannelID[0] == 'W') {
			body.TargetUserID = body.ChannelID
			body.ChannelID = ""
		}

		s.cfgMu.Lock()
		api := s.api
		connTokFn := s.connectorToken
		s.cfgMu.Unlock()

		// Auto-inject sender_user_id from the registered token map.
		// If the caller didn't specify a sender but there's exactly one
		// registered user token that matches — use it automatically.
		if body.SenderUserID == "" && connTokFn != nil && body.TargetUserID != "" {
			// The session context always includes the mentioning user's ID
			// via X-Wick-Session-User header; fall back to scanning all tokens.
			if sessionUser := r.Header.Get("X-Wick-Session-User"); sessionUser != "" {
				if tok := s.resolveUserToken(r.Context(), sessionUser, connTokFn); tok != "" {
					body.SenderUserID = sessionUser
				}
			}
		}

		if api == nil {
			http.Error(w, `{"error":"slack not configured"}`, http.StatusServiceUnavailable)
			return
		}

		// Build Block Kit message with muted context-block footer.
		blocks := []slackgo.Block{
			slackgo.NewSectionBlock(
				slackgo.NewTextBlockObject(slackgo.MarkdownType, body.Text, false, false),
				nil, nil,
			),
			s.signedContextBlock(),
		}
		opts := []slackgo.MsgOption{slackgo.MsgOptionBlocks(blocks...)}

		// Resolve the xoxp token for the sender when provided.
		var xoxpClient *slackgo.Client
		if body.SenderUserID != "" {
			if token := s.resolveUserToken(r.Context(), body.SenderUserID, connTokFn); token != "" {
				xoxpClient = slackgo.New(token)
			}
		}

		// Resolve channel: target_user_id opens a real DM using sender's token.
		// conversations.open with xoxp token creates a DM in both users' inboxes.
		channelID := body.ChannelID
		if body.TargetUserID != "" {
			client := xoxpClient
			if client == nil {
				client = api // fallback: bot token (creates bot↔target DM)
			}
			ch, _, _, err := client.OpenConversationContext(r.Context(), &slackgo.OpenConversationParameters{
				Users:    []string{body.TargetUserID},
				ReturnIM: true,
			})
			if err != nil {
				// Return structured error so Claude can decide to fallback
				// (e.g. post to original channel thread instead).
				// missing_scope means the token needs im:write user scope.
				errMsg := err.Error()
				hint := ""
				if strings.Contains(errMsg, "missing_scope") {
					hint = "; the user token is missing im:write scope — add it in Slack app OAuth & Permissions → User Token Scopes, then reinstall"
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				fmt.Fprintf(w, `{"error":"open_dm_failed","detail":%q,"hint":%q}`, errMsg, hint)
				return
			}
			channelID = ch.ID
		}

		// Send with xoxp client (real sender identity) when available.
		if xoxpClient != nil {
			if u, err := xoxpClient.GetUserInfoContext(r.Context(), body.SenderUserID); err == nil {
				name := u.Profile.DisplayName
				if name == "" {
					name = u.Profile.RealName
				}
				opts = append(opts,
					slackgo.MsgOptionUsername(name),
					slackgo.MsgOptionIconURL(u.Profile.Image72),
				)
			}
			_, ts, err := xoxpClient.PostMessageContext(r.Context(), channelID, opts...)
			if err == nil {
				writeOK(w, ts)
				return
			}
			log.Warn().Str("channel", "slack").Str("sender", body.SenderUserID).Err(err).
				Msg("sendHandler: xoxp post failed, falling back to bot token")
		}

		// Cosmetic fallback via bot token.
		if body.SenderUserID != "" {
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

		_, ts, err := api.PostMessageContext(r.Context(), channelID, opts...)
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
//
// On a slow-path miss where fn also returns no token, a background
// RefreshTokenMap is triggered so the next request benefits from an
// updated connector row (e.g. when an admin just added a user token).
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
		// Trigger async map refresh so the next request benefits from any
		// connector rows added since startup. Debounced inside RefreshTokenMap.
		s.cfgMu.Lock()
		refreshFn := s.tokenRefreshFn
		s.cfgMu.Unlock()
		if refreshFn != nil {
			go s.RefreshTokenMap(context.Background())
		}
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
