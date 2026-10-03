// Package slack — instant.go: Instant-mode Team agents on a shared bot.
//
// An Instant agent has no Slack app of its own. It rides one of wick's
// Slack instances and is told apart per message: replies go out through
// chat.postMessage with the agent's username and icon_url (scope
// chat:write.customize). Which agent answers a message is decided here:
//
//  1. a thread that already has a session keeps the agent that answered it
//     first (sticky — read back from the session, so it survives restarts);
//  2. a channel bound to an agent goes to that agent;
//  3. "@bot handle: …" or "@bot @handle …" picks an agent whose prefix
//     routing is on;
//  4. anything else is wick's usual answer, as before.
//
// The agent list and the sticky lookup live with the Team store; the
// channel reaches them through the PersonaRouter set at boot.

package slack

import (
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/rs/zerolog/log"
	slackgo "github.com/slack-go/slack"
)

// Persona is how an Instant agent appears on a shared bot: the project its
// turns run in and the name and icon its replies carry.
type Persona struct {
	AgentID   string
	ProjectID string
	Username  string
	IconURL   string
}

// InstantAgent is one Instant agent on a shared bot, as the router sees it.
type InstantAgent struct {
	Persona
	Handle        string
	Channels      []string
	PrefixEnabled bool
}

// PersonaRouter resolves Instant agents for a shared bot. Implemented by
// the Team layer; nil = no Instant agents anywhere.
type PersonaRouter interface {
	// Agents lists the Instant agents riding ch.
	Agents(ch *Channel) []InstantAgent
	// SessionAgent is the agent id a session answers as, "" for none.
	SessionAgent(sessionID string) string
	// AvatarPNG serves the avatar behind a public token: the PNG and its
	// ETag, ok=false for an unknown token.
	AvatarPNG(token string) (png []byte, etag string, ok bool)
}

var (
	routerMu      sync.RWMutex
	personaRouter PersonaRouter
)

// SetPersonaRouter wires the Instant-agent lookup every Slack instance uses.
func SetPersonaRouter(r PersonaRouter) {
	routerMu.Lock()
	personaRouter = r
	routerMu.Unlock()
}

func currentRouter() PersonaRouter {
	routerMu.RLock()
	defer routerMu.RUnlock()
	return personaRouter
}

// ServesInstant reports whether any Instant agent rides this instance.
func (s *Channel) ServesInstant() bool {
	r := currentRouter()
	return r != nil && len(r.Agents(s)) > 0
}

// prefixRe matches "handle: rest" / "handle, rest" or "@handle rest" at the
// start of a mention's text. A bare word never matches, so "hello there"
// cannot pick an agent called "hello".
var prefixRe = regexp.MustCompile(`^(?:@([a-z0-9][a-z0-9_-]*)\b[:,]?|([a-z0-9][a-z0-9_-]*)[:,])(?:\s+|$)`)

// PickInstant is the routing decision for a message that opens a session:
// the agent bound to channelID, else the agent named by text's prefix,
// else none. text comes back without the prefix when one picked the agent.
func PickInstant(agents []InstantAgent, channelID, text string) (InstantAgent, string, bool) {
	for _, a := range agents {
		if channelID != "" && slices.Contains(a.Channels, channelID) {
			return a, text, true
		}
	}
	trimmed := strings.TrimSpace(text)
	m := prefixRe.FindStringSubmatch(strings.ToLower(trimmed))
	if m == nil {
		return InstantAgent{}, text, false
	}
	handle := m[1] + m[2]
	for _, a := range agents {
		if a.PrefixEnabled && strings.EqualFold(a.Handle, handle) {
			rest := strings.TrimSpace(trimmed[len(m[0]):])
			if rest == "" {
				rest = trimmed
			}
			return a, rest, true
		}
	}
	return InstantAgent{}, text, false
}

// routePersona picks the persona a message is answered as. Existing
// sessions are sticky; dmMain sessions belong to a Custom bot and never
// route. text is the message with any routing prefix removed.
func (s *Channel) routePersona(channelID, sessionID, text string, existing bool) (Persona, string) {
	r := currentRouter()
	if r == nil {
		return Persona{}, text
	}
	agents := r.Agents(s)
	if len(agents) == 0 {
		return Persona{}, text
	}
	var p Persona
	if existing {
		if id := r.SessionAgent(sessionID); id != "" {
			for _, a := range agents {
				if a.AgentID == id {
					p = a.Persona
				}
			}
		}
	} else if a, rest, ok := PickInstant(agents, channelID, text); ok {
		p, text = a.Persona, rest
	}
	if p.AgentID != "" {
		s.personas.Store(sessionID, p)
	}
	return p, text
}

// personaFor is the persona a session's replies carry, resolved once and
// cached; a turn restored after a restart finds it through the router.
func (s *Channel) personaFor(sessionID string) (Persona, bool) {
	if v, ok := s.personas.Load(sessionID); ok {
		p := v.(Persona)
		return p, p.AgentID != ""
	}
	r := currentRouter()
	if r == nil || sessionID == "" {
		return Persona{}, false
	}
	agents := r.Agents(s)
	if len(agents) == 0 {
		return Persona{}, false
	}
	var p Persona
	if id := r.SessionAgent(sessionID); id != "" {
		for _, a := range agents {
			if a.AgentID == id {
				p = a.Persona
			}
		}
	}
	// Remembered either way: a thread of the shared bot that no agent
	// answers is asked about once, not on every post.
	s.personas.Store(sessionID, p)
	return p, p.AgentID != ""
}

// personaOpts are the username/icon overrides of a thread's replies; none
// when the thread has no Instant agent or the token lacks
// chat:write.customize.
func (s *Channel) personaOpts(threadTS string) []slackgo.MsgOption {
	if threadTS == "" || s.customizeDenied.Load() {
		return nil
	}
	p, ok := s.personaFor(s.sessionKey(threadTS))
	if !ok {
		return nil
	}
	opts := []slackgo.MsgOption{slackgo.MsgOptionUsername(p.Username)}
	if p.IconURL != "" {
		opts = append(opts, slackgo.MsgOptionIconURL(p.IconURL))
	} else {
		opts = append(opts, slackgo.MsgOptionIconEmoji(":robot_face:"))
	}
	return opts
}

// isCustomizeDenied reports a post refused for its username/icon override.
func isCustomizeDenied(err error) bool {
	if err == nil {
		return false
	}
	e := err.Error()
	return strings.Contains(e, "missing_scope") || strings.Contains(e, "not_allowed_token_type")
}

// postThread posts into threadTS as the thread's Instant agent. Without
// chat:write.customize the post is retried as the bot and the instance
// stops trying the override, so a reply is never lost to the scope.
func (s *Channel) postThread(api *slackgo.Client, channelID, threadTS string, opts ...slackgo.MsgOption) (string, error) {
	opts = append(opts, slackgo.MsgOptionTS(threadTS))
	extra := s.personaOpts(threadTS)
	_, ts, err := api.PostMessage(channelID, append(opts, extra...)...)
	if err != nil && len(extra) > 0 && isCustomizeDenied(err) {
		s.customizeDenied.Store(true)
		log.Warn().Str("channel", "slack").Err(err).
			Msg("instant agent: chat:write.customize missing, replying as the bot")
		_, ts, err = api.PostMessage(channelID, opts...)
	}
	return ts, err
}

// avatarPath is the public route of an Instant agent's avatar. Slack
// fetches icon_url without a session, so the path itself is the secret.
const avatarPath = "/integrations/slack/avatar/"

// AvatarURL is the icon_url of an avatar token under base (wick's public
// URL); version busts Slack's cache when the avatar changes.
func AvatarURL(base, token, version string) string {
	if base == "" || token == "" {
		return ""
	}
	u := strings.TrimRight(base, "/") + avatarPath + token + ".png"
	if version != "" {
		u += "?v=" + version
	}
	return u
}

// avatarHandler serves GET /integrations/slack/avatar/{file}. Unknown
// tokens get the same 404 as a wrong path, so a probe learns nothing.
func avatarHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := strings.CutSuffix(r.PathValue("file"), ".png")
		rt := currentRouter()
		if !ok || token == "" || rt == nil {
			http.NotFound(w, r)
			return
		}
		png, etag, found := rt.AvatarPNG(token)
		if !found {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.Header().Set("ETag", `"`+etag+`"`)
		if r.Header.Get("If-None-Match") == `"`+etag+`"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(png)
	})
}

// BotTokenScopes are the bot token's granted scopes, read from auth.test.
func (s *Channel) BotTokenScopes() ([]string, error) {
	cfg := s.snapshot()
	if cfg.BotToken == "" {
		return nil, nil
	}
	return TokenScopes(cfg.BotToken, s.apiURLOverride())
}
