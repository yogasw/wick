package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/actioncard"
	"github.com/yogasw/wick/internal/agents/store"
	"github.com/yogasw/wick/internal/agents/teamlink"
	"github.com/yogasw/wick/pkg/tool"
)

// ActionCard and ActionButton are the fence schema (package actioncard,
// shared with the chat channels that render cards without the web UI).
type (
	ActionCard   = actioncard.Card
	ActionButton = actioncard.Button
)

// CardState is what the thread says about one card id, computed from the
// whole conversation so the UI does not have to: which turn holds its
// current version, and whether its buttons still take a click.
type CardState struct {
	// TurnID is the assistant turn with the newest version. A fence with
	// this id in any other turn is superseded.
	TurnID string `json:"turn_id"`
	// Locked: clicked since the newest version was written, or Final.
	Locked bool `json:"locked"`
	// Postback is the click that locked it, and PostbackTurnID its turn.
	Postback       *store.Postback `json:"postback,omitempty"`
	PostbackTurnID string          `json:"postback_turn_id,omitempty"`

	card ActionCard
}

// parseActionCards returns every well-formed actioncard fence in text, in
// order. A fence whose JSON does not parse, or that has no id, is not a
// card — the UI shows it as an ordinary code block.
func parseActionCards(text string) []ActionCard { return actioncard.Parse(text) }

// actionCardStates walks the thread once. Only assistant turns can hold a
// card and only a postback-marked user turn (set by the postback
// endpoint, never parsed from text) can lock one.
func actionCardStates(turns []store.ConversationTurn) map[string]*CardState {
	out := map[string]*CardState{}
	for _, t := range turns {
		switch {
		case t.Role == "assistant":
			for _, c := range parseActionCards(t.Text) {
				// A new version re-opens the card unless it says it is final.
				out[c.ID] = &CardState{TurnID: t.TurnID, Locked: c.Final, card: c}
			}
		case t.Role == "user" && t.Postback != nil:
			if st := out[t.Postback.CardID]; st != nil {
				st.Locked = true
				pb := *t.Postback
				st.Postback, st.PostbackTurnID = &pb, t.TurnID
			}
		}
	}
	return out
}

// postbackReq is the body for POST /api/sessions/{id}/postback.
type postbackReq struct {
	CardID string `json:"card_id"`
	Value  string `json:"value"`
}

// postbackMu serialises check-then-send so two quick clicks on one card
// cannot both get through before the first is in the thread.
var postbackMu sync.Mutex

// errPostback is a refused click: the HTTP status and why.
type errPostback struct {
	status int
	msg    string
}

func (e *errPostback) Error() string { return e.msg }

// checkPostback validates a click against the thread: the card must exist
// in an agent turn, value must be one of its current buttons, and the
// card must not be locked. Returns the postback to send.
func checkPostback(turns []store.ConversationTurn, req postbackReq) (*store.Postback, error) {
	st := actionCardStates(turns)[req.CardID]
	if st == nil {
		return nil, &errPostback{http.StatusNotFound, "no actioncard with that id in this conversation"}
	}
	if st.Locked {
		return nil, &errPostback{http.StatusConflict, "this card already has a decision"}
	}
	for _, a := range st.card.Actions {
		if a.Value == req.Value {
			return &store.Postback{CardID: req.CardID, Value: a.Value, Label: a.Label}, nil
		}
	}
	return nil, &errPostback{http.StatusBadRequest, "value is not a button of this card"}
}

// sessionPostback handles POST /api/sessions/{id}/postback: an actioncard
// button was clicked. It becomes a user turn marked postback {card_id,
// value, label} and goes to the agent as a new turn reading
// "[postback card=<id> value=<value>] <label>".
//
// A postback is a UI signal, not a permission: anything risky the agent
// does next still goes through the gate / ask_user.
func sessionPostback(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	id := c.PathValue("id")
	sess, ok := globalMgr.Registry().Session(id)
	if !ok || !ownsSession(c, sess) {
		c.JSON(http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	var req postbackReq
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	req.CardID, req.Value = strings.TrimSpace(req.CardID), strings.TrimSpace(req.Value)
	if req.CardID == "" || req.Value == "" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "card_id and value required"})
		return
	}
	agentName := sess.Meta.ActiveAgent
	if agentName == "" && len(sess.Agents) > 0 {
		agentName = sess.Agents[0].Name
	}
	if agentName == "" {
		c.JSON(http.StatusUnprocessableEntity, map[string]string{"error": "no agent in session"})
		return
	}

	bgCtx := withComposerSender(c, log.Ctx(c.Context()).WithContext(context.Background()), id)
	pb, text, err := sendPostback(bgCtx, id, agentName, "ui", req)
	if err != nil {
		if e, ok := err.(*errPostback); ok {
			c.JSON(e.status, map[string]string{"error": e.msg})
			return
		}
		log.Ctx(c.Context()).Error().Msgf("postback send %s: %s", id, err.Error())
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]any{"status": "sent", "postback": pb, "text": text})
}

// publishPostback tells every open tab a card was clicked, so it locks
// the buttons and draws the "✓ label" chip without a reload. The composer
// that clicked renders its own copy; source "ui" is not echoed as a
// user_message.
func publishPostback(b *Broadcaster, sessionID, agentName string, pb *store.Postback, text string) {
	if b == nil {
		return
	}
	body, err := json.Marshal(map[string]any{"postback": pb, "text": text})
	if err != nil {
		return
	}
	b.PublishRaw(sessionID, agentName, "postback", string(body))
}

// sendPostback checks req against the thread and sends it to the agent as
// a postback turn from source, then tells open tabs. Shared by the web
// endpoint and the chat channels (Slack buttons, numbered replies).
func sendPostback(ctx context.Context, sessionID, agentName, source string, req postbackReq) (*store.Postback, string, error) {
	postbackMu.Lock()
	defer postbackMu.Unlock()
	turns, err := loadConversation(globalLayout, sessionID)
	if err != nil {
		return nil, "", fmt.Errorf("load conversation: %w", err)
	}
	pb, err := checkPostback(turns, req)
	if err != nil {
		return nil, "", err
	}
	text := pb.PostbackText()
	if err := globalPool.Send(teamlink.WithPersonMessage(store.WithPostback(ctx, pb)), sessionID, agentName, source, "user", text); err != nil {
		return nil, "", err
	}
	publishPostback(globalBcast, sessionID, agentName, pb, text)
	return pb, text, nil
}

// sessionAgentName is the agent a channel postback goes to.
func sessionAgentName(sessionID string) (string, bool) {
	if globalMgr == nil {
		return "", false
	}
	sess, ok := globalMgr.Registry().Session(sessionID)
	if !ok {
		return "", false
	}
	name := sess.Meta.ActiveAgent
	if name == "" && len(sess.Agents) > 0 {
		name = sess.Agents[0].Name
	}
	return name, name != ""
}

// ChannelPostback is a card button clicked in a chat channel (Slack): the
// same check and send as the web endpoint. Returns the button's label.
func ChannelPostback(ctx context.Context, sessionID, source, cardID, value string) (string, error) {
	agentName, ok := sessionAgentName(sessionID)
	if !ok {
		return "", fmt.Errorf("session %s has no agent", sessionID)
	}
	pb, _, err := sendPostback(ctx, sessionID, agentName, source, postbackReq{CardID: cardID, Value: value})
	if err != nil {
		return "", err
	}
	return pb.Label, nil
}

// numberedCard is the card a bare-number reply answers: the last card,
// in thread order, whose buttons still take a click.
func numberedCard(turns []store.ConversationTurn) *CardState {
	var last *CardState
	for _, st := range actionCardStates(turns) {
		if st.Locked || len(st.card.Actions) == 0 {
			continue
		}
		if last == nil || st.TurnID > last.TurnID {
			last = st
		}
	}
	return last
}

// ParseNumberReply reads text as a bare 1-based choice ("2", " 2. ").
func ParseNumberReply(text string) (int, bool) {
	t := strings.TrimSuffix(strings.TrimSpace(text), ".")
	if t == "" || len(t) > 2 {
		return 0, false
	}
	n := 0
	for _, r := range t {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int(r-'0')
	}
	return n, n > 0
}

// ChannelNumberPostback maps a bare-number reply on a channel without
// buttons (Telegram, plain text) to a click on the open card's n-th
// button. ok=false when text is not a number or no card is open, so the
// caller sends it as an ordinary message.
func ChannelNumberPostback(ctx context.Context, sessionID, source, text string) (label string, ok bool, err error) {
	n, isNum := ParseNumberReply(text)
	if !isNum {
		return "", false, nil
	}
	turns, err := loadConversation(globalLayout, sessionID)
	if err != nil {
		return "", false, nil
	}
	st := numberedCard(turns)
	if st == nil || n > len(st.card.Actions) {
		return "", false, nil
	}
	label, err = ChannelPostback(ctx, sessionID, source, st.card.ID, st.card.Actions[n-1].Value)
	return label, true, err
}
