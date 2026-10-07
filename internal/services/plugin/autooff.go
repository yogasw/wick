package plugin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

// Auto-off modes the admin picks per service.
const (
	AutoOffDefault = "default" // follow the plugin's declaration
	AutoOffOn      = "on"      // force auto-off, even when the plugin says it cannot
	AutoOffOff     = "off"     // force always-on
)

// maxAutoOffIdle caps the idle limit an admin can set.
const maxAutoOffIdle = 7 * 24 * time.Hour

// idleUnit is what one idle "second" lasts (tests shorten it).
var idleUnit = time.Second

// AutoOffSetting is the admin's override of a service's auto-off. The zero
// value follows the plugin.
type AutoOffSetting struct {
	Mode string `json:"mode,omitempty"`
	// IdleSeconds overrides the plugin's idle limit (0 = plugin default).
	IdleSeconds int `json:"idle_seconds,omitempty"`
}

func (a AutoOffSetting) mode() string {
	if a.Mode == "" {
		return AutoOffDefault
	}
	return a.Mode
}

// AutoOffView is a service's auto-off as the admin page shows it: what the
// plugin declares, what the admin set, and the effective result.
type AutoOffView struct {
	Supported          bool   `json:"supported"`
	Reason             string `json:"reason,omitempty"`
	DefaultIdleSeconds int    `json:"default_idle_seconds"`
	Mode               string `json:"mode"`
	IdleSeconds        int    `json:"idle_seconds"`
	// Enabled is the effective state; Forced means the admin overrode the
	// plugin (mode on/off).
	Enabled bool `json:"enabled"`
	Forced  bool `json:"forced"`
}

// autoOffOf resolves s's effective auto-off from its manifest and the
// admin setting.
func (h *Host) autoOffOf(s *Service) AutoOffView {
	v := AutoOffView{DefaultIdleSeconds: wickplugin.DefaultAutoOffIdleSeconds, Mode: AutoOffDefault}
	if a := s.Manifest.AutoOff; a != nil {
		v.Supported, v.Reason = a.Supported, a.Reason
		if a.DefaultIdleSeconds > 0 {
			v.DefaultIdleSeconds = a.DefaultIdleSeconds
		}
	}
	var set AutoOffSetting
	if h.Tokens != nil {
		set = h.Tokens.AutoOffSetting(s.Key)
	}
	v.Mode = set.mode()
	v.IdleSeconds = v.DefaultIdleSeconds
	if set.IdleSeconds > 0 {
		v.IdleSeconds = set.IdleSeconds
	}
	switch v.Mode {
	case AutoOffOn:
		v.Enabled, v.Forced = true, true
	case AutoOffOff:
		v.Enabled, v.Forced = false, true
	default:
		v.Enabled = v.Supported
	}
	return v
}

// autoOffFunc is the supervisor's auto-off policy of service key.
func (h *Host) autoOffFunc(key string) func() (bool, time.Duration) {
	return func() (bool, time.Duration) {
		s, ok := h.Get(key)
		if !ok {
			return false, 0
		}
		v := h.autoOffOf(s)
		return v.Enabled, time.Duration(v.IdleSeconds) * idleUnit
	}
}

func (a AutoOffView) String() string {
	return fmt.Sprintf("mode=%s idle=%s", a.Mode, time.Duration(a.IdleSeconds)*time.Second)
}

// serveSetAutoOff stores the admin's auto-off setting of s. Forcing it on
// while the plugin says it cannot auto-off needs "confirm": true. Every
// change is audited.
func (h *Host) serveSetAutoOff(w http.ResponseWriter, r *http.Request, s *Service) {
	var body struct {
		Mode        string `json:"mode"`
		IdleSeconds int    `json:"idle_seconds"`
		Confirm     bool   `json:"confirm"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	if body.Mode == "" {
		body.Mode = AutoOffDefault
	}
	if body.Mode != AutoOffDefault && body.Mode != AutoOffOn && body.Mode != AutoOffOff {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "mode must be default, on or off"})
		return
	}
	if body.IdleSeconds < 0 || time.Duration(body.IdleSeconds)*time.Second > maxAutoOffIdle {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "idle_seconds must be between 0 and 604800"})
		return
	}
	if body.IdleSeconds > 0 && body.IdleSeconds < 60 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "the idle limit must be at least 60 seconds"})
		return
	}
	if h.Tokens == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "service plugin state is not available"})
		return
	}
	old := h.autoOffOf(s)
	if body.Mode == AutoOffOn && !old.Supported && !body.Confirm {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "The plugin says it cannot auto-off; confirm to force it on."})
		return
	}
	set := AutoOffSetting{IdleSeconds: body.IdleSeconds}
	if body.Mode != AutoOffDefault {
		set.Mode = body.Mode
	}
	if err := h.Tokens.SetAutoOffSetting(s.Key, set); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	cur := h.autoOffOf(s)
	if old.Mode != cur.Mode || old.IdleSeconds != cur.IdleSeconds {
		actor := ""
		if h.SessionUser != nil {
			if u := h.SessionUser(r); u != nil {
				actor = u.Email
				if actor == "" {
					actor = u.Name
				}
			}
		}
		s.Sup.Logs.Printf("auto-off changed: %s -> %s", old, cur)
		if h.Audit != nil {
			h.Audit(actor, s.Key, "service auto-off: "+old.String()+" -> "+cur.String())
		}
	}
	writeJSON(w, http.StatusOK, h.View(s, true))
}
