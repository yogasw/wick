package agents

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/store"
	"github.com/yogasw/wick/pkg/tool"
)

// ActionCard is the JSON inside an ```actioncard fence an agent writes.
// A later card with the same ID replaces the earlier one in the thread.
type ActionCard struct {
	ID       string         `json:"id"`
	Icon     string         `json:"icon,omitempty"`
	Title    string         `json:"title"`
	Subtitle string         `json:"subtitle,omitempty"`
	Status   string         `json:"status,omitempty"`
	Rows     [][]string     `json:"rows,omitempty"`
	Actions  []ActionButton `json:"actions,omitempty"`
	// Final locks the card's buttons: the decision it asked for is made.
	Final bool `json:"final,omitempty"`
}

// ActionButton is one button of an actioncard. Clicking it sends Value
// back to the agent as a postback.
type ActionButton struct {
	Label string `json:"label"`
	Value string `json:"value"`
	Style string `json:"style,omitempty"`
}

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
func parseActionCards(text string) []ActionCard {
	var out []ActionCard
	lines := strings.Split(text, "\n")
	for i := 0; i < len(lines); i++ {
		open := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(open, "```actioncard") || strings.TrimSpace(strings.TrimPrefix(open, "```actioncard")) != "" {
			continue
		}
		var body []string
		j := i + 1
		for ; j < len(lines) && strings.TrimSpace(lines[j]) != "```"; j++ {
			body = append(body, lines[j])
		}
		if j == len(lines) {
			break // unterminated: not a card
		}
		i = j
		var c ActionCard
		if json.Unmarshal([]byte(strings.Join(body, "\n")), &c) != nil || strings.TrimSpace(c.ID) == "" {
			continue
		}
		out = append(out, c)
	}
	return out
}

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

	postbackMu.Lock()
	defer postbackMu.Unlock()
	turns, err := loadConversation(globalLayout, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": "load conversation: " + err.Error()})
		return
	}
	pb, err := checkPostback(turns, req)
	if err != nil {
		e := err.(*errPostback)
		c.JSON(e.status, map[string]string{"error": e.msg})
		return
	}
	text := pb.PostbackText()
	bgCtx := store.WithPostback(withComposerSender(c, log.Ctx(c.Context()).WithContext(context.Background()), id), pb)
	if err := globalPool.Send(bgCtx, id, agentName, "ui", "user", text); err != nil {
		log.Ctx(c.Context()).Error().Msgf("postback send %s: %s", id, err.Error())
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	publishPostback(globalBcast, id, agentName, pb, text)
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
