package store

import (
	"encoding/json"
	"errors"
	"os"
	"sync"
	"time"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/storage"
)

// Delivery is the outcome of posting an assistant turn's reply to the
// channel thread it answers. It exists so the person reading the web UI can
// tell the reply really went out — or see why it did not — without opening
// Slack to check.
//
// It is only a record: the channel posts, retries and gives up exactly as it
// always did, and reports what happened here afterwards.
type Delivery struct {
	Channel string `json:"channel"` // "slack"
	Status  string `json:"status"`  // DeliverySending | DeliverySent | DeliveryFailed
	// Permalink opens the posted reply (its first message when it was split).
	// Empty while sending, and when the platform would not give one.
	Permalink string `json:"permalink,omitempty"`
	// Error is the platform's short error code ("channel_not_found",
	// "rate_limited", "timeout"). Never a token or a request dump.
	Error string    `json:"error,omitempty"`
	At    time.Time `json:"at"`
}

// Delivery.Status values.
const (
	DeliverySending = "sending"
	DeliverySent    = "sent"
	DeliveryFailed  = "failed"
)

// deliveriesMu serialises the read-modify-write of deliveries.json. One
// lock for every session is plenty: a write happens twice per posted reply.
var deliveriesMu sync.Mutex

// LoadDeliveries returns sessionID's recorded deliveries keyed by turn ID.
// A missing or unreadable file is an empty map: delivery status is a nice
// to have, never a reason to fail loading a conversation.
func LoadDeliveries(layout config.Layout, sessionID string) map[string]Delivery {
	out := map[string]Delivery{}
	if err := storage.ReadJSON(layout.SessionDeliveries(sessionID), &out); err != nil || out == nil {
		return map[string]Delivery{}
	}
	return out
}

// SaveDelivery records d for turnID, replacing what was there.
func SaveDelivery(layout config.Layout, sessionID, turnID string, d Delivery) error {
	if turnID == "" {
		return errors.New("save delivery: empty turn id")
	}
	deliveriesMu.Lock()
	defer deliveriesMu.Unlock()
	all := LoadDeliveries(layout, sessionID)
	all[turnID] = d
	return storage.WriteJSON(layout.SessionDeliveries(sessionID), all)
}

// LastAssistantTurnID is the ID of the newest assistant turn in
// sessionID's conversation, "" when there is none. The provider writes a
// finished turn to disk before the Done event fans out to channels, so a
// channel settling its reply at Done finds that very turn here.
func LastAssistantTurnID(layout config.Layout, sessionID string) string {
	id := ""
	err := storage.TailJSONL(layout.SessionConversation(sessionID), 32, func(line []byte) {
		var t ConversationTurn
		if json.Unmarshal(line, &t) == nil && t.Role == "assistant" && t.TurnID != "" {
			id = t.TurnID
		}
	})
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return ""
	}
	return id
}

// StampDeliveries puts each recorded delivery on its assistant turn.
func StampDeliveries(turns []ConversationTurn, deliveries map[string]Delivery) {
	if len(deliveries) == 0 {
		return
	}
	for i := range turns {
		if turns[i].Role != "assistant" {
			continue
		}
		if d, ok := deliveries[turns[i].TurnID]; ok {
			d := d
			turns[i].Delivery = &d
		}
	}
}
