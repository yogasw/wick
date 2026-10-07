package slack

import (
	"context"
	"errors"
	"net"
	"regexp"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	slackgo "github.com/slack-go/slack"

	"github.com/yogasw/wick/internal/agents/store"
)

// DeliveryFunc records how posting a turn's reply to Slack went. turnID ""
// means "the session's newest assistant turn"; the function returns the turn
// ID it recorded against, so the follow-up report for the same reply lands
// on the same turn even if another one has finished in between.
type DeliveryFunc func(sessionID, turnID string, d store.Delivery) string

// SetDeliveryFn wires where reply delivery outcomes are recorded.
func (s *Channel) SetDeliveryFn(fn DeliveryFunc) { s.deliveryFn = fn }

// errSlackNotConnected is the delivery error when the channel has no client.
var errSlackNotConnected = errors.New("not_connected")

// reportDelivery hands d to the recorder, stamped as a Slack delivery.
// Returns the turn ID it was recorded against ("" when nothing recorded).
func (s *Channel) reportDelivery(sessionKey, turnID string, d store.Delivery) string {
	fn := s.deliveryFn
	if fn == nil {
		return ""
	}
	d.Channel = "slack"
	if d.At.IsZero() {
		d.At = time.Now().UTC()
	}
	return fn(sessionKey, turnID, d)
}

// finishDelivery records the settled outcome of a reply: sent with a link to
// its first message, or failed with Slack's short reason. A reply that went
// out partly (first message posted, a continuation refused) is failed — the
// reader must know something is missing — but keeps its link.
func (s *Channel) finishDelivery(sessionKey, turnID, channelID, firstTS string, failed error) {
	if s.deliveryFn == nil {
		return
	}
	d := store.Delivery{Status: store.DeliverySent}
	if firstTS != "" {
		d.Permalink = s.messagePermalink(channelID, firstTS)
	}
	switch {
	case failed != nil:
		d.Status = store.DeliveryFailed
		d.Error = deliveryErrorCode(failed)
	case firstTS == "":
		d.Status = store.DeliveryFailed
		d.Error = "not_posted"
	}
	s.reportDelivery(sessionKey, turnID, d)
}

// messagePermalink is Slack's permalink for the message at ts, "" when
// Slack will not give one. Never an error: a missing link only means the
// web UI shows no "Jump to thread".
func (s *Channel) messagePermalink(channelID, ts string) string {
	if channelID == "" || ts == "" {
		return ""
	}
	s.cfgMu.Lock()
	api := s.api
	s.cfgMu.Unlock()
	if api == nil {
		return ""
	}
	pl, err := api.GetPermalink(&slackgo.PermalinkParameters{Channel: channelID, Ts: ts})
	if err != nil {
		log.Debug().Str("channel", "slack").Str("slack_channel", channelID).Err(err).Msg("chat.getPermalink failed; no jump link")
		return ""
	}
	return pl
}

// slackErrorCode matches a bare Slack API error ("channel_not_found"),
// which is exactly what slack-go's error string is for an `ok:false` reply.
var slackErrorCode = regexp.MustCompile(`^[a-z0-9_]{2,64}$`)

// deliveryErrorCode turns a posting error into the short reason shown in the
// web UI. Only known shapes pass through — a Slack error code, rate limiting,
// a timeout, an HTTP status — anything else is "request_failed", so a
// request dump never reaches the page.
func deliveryErrorCode(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.TrimSpace(err.Error())
	if isRateLimit(err) || strings.Contains(msg, "rate limit") {
		return "rate_limited"
	}
	var ne net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()) {
		return "timeout"
	}
	if slackErrorCode.MatchString(msg) {
		return msg
	}
	// slack-go: "slack server error: 503 Service Unavailable".
	if rest, ok := strings.CutPrefix(msg, "slack server error: "); ok {
		if code, _, _ := strings.Cut(rest, " "); code != "" && len(code) == 3 {
			return "http_" + code
		}
	}
	return "request_failed"
}
