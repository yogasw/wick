package api

import (
	"strings"

	agentchannels "github.com/yogasw/wick/internal/agents/channels"
	agentconfig "github.com/yogasw/wick/internal/agents/config"
	agentsession "github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/configs"

	"gorm.io/gorm"
)

// askUserPolicy resolves whether the ask_user tool (and
// wick_session_config action=ask) may run for one session.
//
// The decision is per-session ORIGIN, not the master command gate —
// ask_user rides wick's own socket/SSE channel, so the PreToolUse
// gate being off must not disable it (see GateConfig docs).
//
// Resolution:
//   - slack / telegram / rest → that channel's own ask_user_enabled
//     flag (default false: those transports can't render the modal
//     yet, so enabling it would just hang until timeout). Operators
//     flip it on per channel once the ask is surfaced there.
//   - ui / external MCP clients / stdio / unknown → the global
//     agents.ask_user_mode (default on: a human is at the web UI or
//     an interactive dev tool).
//
// db / layout may be zero in degraded modes; a failed session load
// falls through to the global path, which is the safe interactive
// default.
func askUserPolicy(db *gorm.DB, configsSvc *configs.Service, layout agentconfig.Layout, sessionID string) (bool, string) {
	origin := ""
	if strings.TrimSpace(sessionID) != "" {
		if s, err := agentsession.Load(layout, sessionID); err == nil {
			origin = string(s.Meta.Origin)
		}
	}

	switch origin {
	case string(agentsession.OriginSlack),
		string(agentsession.OriginTelegram),
		string(agentsession.OriginREST):
		enabled := false // channel default — no modal rendering on these transports yet
		if db != nil {
			if m, err := agentchannels.GetChannelConfigMap(db, origin); err == nil {
				enabled = strings.EqualFold(strings.TrimSpace(m["ask_user_enabled"]), "true")
			}
		}
		if !enabled {
			return false, "ask_user disabled for channel " + origin
		}
		return true, ""
	default:
		mode := ""
		if configsSvc != nil {
			mode = configsSvc.GetOwned("agents", "ask_user_mode")
		}
		if mode == "" {
			mode = "on"
		}
		if mode != "on" {
			return false, "ask_user_mode=" + mode
		}
		return true, ""
	}
}
