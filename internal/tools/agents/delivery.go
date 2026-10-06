package agents

import (
	"encoding/json"

	"github.com/rs/zerolog/log"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/store"
)

// evDelivery tells open viewers an assistant turn's channel delivery changed
// (sending → sent / failed), so its bubble updates without a reload.
const evDelivery = "delivery"

// RecordChannelDelivery is the channels' delivery recorder (see
// slack.DeliveryFunc): it saves d against turnID — the session's newest
// assistant turn when turnID is "" — and pushes it to open viewers.
// Returns the turn ID recorded against, "" when nothing was.
func RecordChannelDelivery(sessionID, turnID string, d store.Delivery) string {
	return recordDelivery(globalLayout, globalBcast, sessionID, turnID, d)
}

func recordDelivery(layout agentconfig.Layout, b *Broadcaster, sessionID, turnID string, d store.Delivery) string {
	if layout.BaseDir == "" || sessionID == "" {
		return ""
	}
	if turnID == "" {
		turnID = store.LastAssistantTurnID(layout, sessionID)
		if turnID == "" {
			return ""
		}
	}
	if err := store.SaveDelivery(layout, sessionID, turnID, d); err != nil {
		log.Warn().Err(err).Str("session", sessionID).Str("turn", turnID).Msg("agents: delivery record failed")
		return turnID
	}
	if b != nil {
		body, err := json.Marshal(map[string]any{"turn_id": turnID, "delivery": d})
		if err == nil {
			b.PublishRaw(sessionID, "", evDelivery, string(body))
		}
	}
	return turnID
}
