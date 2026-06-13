package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/askuser"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/sessionconfig"
	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/enc"
	"github.com/yogasw/wick/internal/entity"
)

const sessionConfigToolName = "wick_session_config"

// sessionConfigField is one config entry in the `get` response.
// Secret values are always wick_enc_ tokens — plaintext never leaves
// the server through this tool.
type sessionConfigField struct {
	Key        string `json:"key"`
	Value      string `json:"value"`
	Secret     bool   `json:"secret"`
	Required   bool   `json:"required"`
	Overridden bool   `json:"overridden"`
	Help       string `json:"help,omitempty"`
}

// WickSessionConfig handles the wick_session_config tool — a
// per-session override layer on top of a connector row's configs.
// Overrides live in the session dir and die with the session; the
// connector row in the DB is never touched.
//
// Actions:
//   - get:   effective config (row + overrides merged), secrets as tokens
//   - set:   apply overrides directly (no UI) — secret values must
//     already be wick_enc_ tokens
//   - ask:   open a form modal in the session UI; the user types
//     values (secrets included) directly into the browser, the
//     server encrypts them, and the agent only ever sees tokens
//   - clear: drop overrides (all keys or a subset)
func WickSessionConfig(
	w http.ResponseWriter,
	r *http.Request,
	req RPCRequest,
	rsp Responder,
	svc *connectors.Service,
	layout agentconfig.Layout,
	asks askuser.Asker,
	askAllowed func(sessionID string) (bool, string),
	args map[string]any,
	user *entity.User,
	tagIDs []string,
) {
	action, _ := args["action"].(string)
	action = strings.TrimSpace(action)
	sessionID, _ := args["session_id"].(string)
	sessionID = strings.TrimSpace(sessionID)
	connectorID, _ := args["connector_id"].(string)
	connectorID = strings.TrimSpace(connectorID)

	if sessionID == "" {
		rsp.ToolError(w, req.ID, "session_id is required", sessionConfigToolName)
		return
	}
	if connectorID == "" {
		rsp.ToolError(w, req.ID, "connector_id is required", sessionConfigToolName)
		return
	}
	// session.Load both validates the id refers to a real session and
	// rejects path-mangling ids (no meta.json → error).
	if _, err := session.Load(layout, sessionID); err != nil {
		rsp.ToolError(w, req.ID, "load session: "+err.Error(), sessionConfigToolName)
		return
	}
	allowed, err := svc.IsVisibleTo(r.Context(), connectorID, tagIDs, user.IsAdmin())
	if err != nil || !allowed {
		rsp.ToolError(w, req.ID, "connector not found or not accessible", sessionConfigToolName)
		return
	}
	row, err := svc.Get(r.Context(), connectorID)
	if err != nil {
		rsp.ToolError(w, req.ID, "get connector: "+err.Error(), sessionConfigToolName)
		return
	}
	specs := svc.RowConfigs(*row)
	if specs == nil {
		rsp.ToolError(w, req.ID, "connector module not registered", sessionConfigToolName)
		return
	}

	switch action {
	case "get":
		sessionConfigGet(w, req, rsp, svc, layout, specs, sessionID, connectorID, user.ID)
	case "set":
		sessionConfigSet(w, req, rsp, svc, layout, specs, sessionID, connectorID, args)
	case "ask":
		sessionConfigAsk(w, r, req, rsp, svc, layout, asks, askAllowed, specs, sessionID, connectorID, row.Label, args, user.ID)
	case "clear":
		sessionConfigClear(w, req, rsp, layout, sessionID, connectorID, args)
	default:
		rsp.ToolError(w, req.ID, "action must be one of: get, set, ask, clear", sessionConfigToolName)
	}
}

// effectiveFields merges row values with session overrides and
// tokenizes secrets. A secret stored as plaintext at rest is
// encrypted under the caller's key on the way out; if encryption is
// unavailable the value is replaced with a placeholder rather than
// leaked.
func effectiveFields(svc *connectors.Service, specs []entity.Config, overrides map[string]string, userID string) []sessionConfigField {
	out := make([]sessionConfigField, 0, len(specs))
	for _, spec := range specs {
		val := spec.Value
		_, overridden := overrides[spec.Key]
		if overridden {
			val = overrides[spec.Key]
		}
		if spec.IsSecret && val != "" && !enc.IsToken(val) {
			if e := svc.Enc(); e != nil && !e.Disabled() {
				if tok, err := e.EncryptValue(val, userID); err == nil {
					val = tok
				} else {
					val = "(set, hidden)"
				}
			} else {
				val = "(set, hidden)"
			}
		}
		out = append(out, sessionConfigField{
			Key:        spec.Key,
			Value:      val,
			Secret:     spec.IsSecret,
			Required:   spec.Required,
			Overridden: overridden,
			Help:       spec.Description,
		})
	}
	return out
}

func sessionConfigGet(w http.ResponseWriter, req RPCRequest, rsp Responder, svc *connectors.Service, layout agentconfig.Layout, specs []entity.Config, sessionID, connectorID, userID string) {
	overrides, err := sessionconfig.For(layout, sessionID, connectorID)
	if err != nil {
		rsp.ToolError(w, req.ID, err.Error(), sessionConfigToolName)
		return
	}
	writeSessionConfigResult(w, req, rsp, map[string]any{
		"connector_id": connectorID,
		"session_id":   sessionID,
		"configs":      effectiveFields(svc, specs, overrides, userID),
	})
}

func sessionConfigSet(w http.ResponseWriter, req RPCRequest, rsp Responder, svc *connectors.Service, layout agentconfig.Layout, specs []entity.Config, sessionID, connectorID string, args map[string]any) {
	rawValues, _ := args["values"].(map[string]any)
	values := StringifyArgs(rawValues)
	if len(values) == 0 {
		rsp.ToolError(w, req.ID, "values is required for action=set — or use action=ask to collect them from the user", sessionConfigToolName)
		return
	}
	specByKey := make(map[string]entity.Config, len(specs))
	for _, sp := range specs {
		specByKey[sp.Key] = sp
	}
	for k, v := range values {
		sp, ok := specByKey[k]
		if !ok {
			rsp.ToolError(w, req.ID, fmt.Sprintf("unknown config key %q for this connector", k), sessionConfigToolName)
			return
		}
		if sp.IsSecret && v != "" && !enc.IsToken(v) {
			rsp.ToolError(w, req.ID, fmt.Sprintf("config %q is secret — pass a wick_enc_ token (have the user mint one via wick_encrypt) or use action=ask so they can type it in the UI", k), sessionConfigToolName)
			return
		}
	}
	if err := sessionconfig.Set(layout, sessionID, connectorID, values); err != nil {
		rsp.ToolError(w, req.ID, err.Error(), sessionConfigToolName)
		return
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	writeSessionConfigResult(w, req, rsp, map[string]any{
		"connector_id": connectorID,
		"session_id":   sessionID,
		"applied":      keys,
	})
}

func sessionConfigAsk(
	w http.ResponseWriter,
	r *http.Request,
	req RPCRequest,
	rsp Responder,
	svc *connectors.Service,
	layout agentconfig.Layout,
	asks askuser.Asker,
	askAllowed func(sessionID string) (bool, string),
	specs []entity.Config,
	sessionID, connectorID, label string,
	args map[string]any,
	userID string,
) {
	if asks == nil {
		rsp.ToolError(w, req.ID, "action=ask unavailable — wick is not running with the agents UI; use action=set instead", sessionConfigToolName)
		return
	}
	if askAllowed != nil {
		if ok, reason := askAllowed(sessionID); !ok {
			msg := "action=ask blocked by policy"
			if strings.TrimSpace(reason) != "" {
				msg += " (" + reason + ")"
			}
			msg += " — use action=set with known values instead of prompting the user."
			rsp.ToolError(w, req.ID, msg, sessionConfigToolName)
			return
		}
	}

	// Optional subset of keys to ask for; default = every config field.
	wanted := map[string]bool{}
	if rawKeys, ok := args["keys"].([]any); ok {
		for _, k := range rawKeys {
			if s, ok := k.(string); ok && strings.TrimSpace(s) != "" {
				wanted[strings.TrimSpace(s)] = true
			}
		}
	}

	overrides, err := sessionconfig.For(layout, sessionID, connectorID)
	if err != nil {
		rsp.ToolError(w, req.ID, err.Error(), sessionConfigToolName)
		return
	}

	specByKey := make(map[string]entity.Config, len(specs))
	fields := make([]askuser.Field, 0, len(specs))
	for _, sp := range specs {
		if sp.Hidden {
			continue
		}
		if len(wanted) > 0 && !wanted[sp.Key] {
			continue
		}
		specByKey[sp.Key] = sp
		f := askuser.Field{
			Key:      sp.Key,
			Label:    sp.Key,
			Help:     sp.Description,
			Required: false, // empty = keep current, so nothing is hard-required
		}
		switch {
		case sp.IsSecret:
			f.Type = "secret"
			f.Placeholder = "Leave empty to keep current"
		case sp.Type == "dropdown" && sp.Options != "":
			f.Type = "dropdown"
			for _, o := range strings.Split(sp.Options, "|") {
				f.Options = append(f.Options, askuser.Option{Label: o, Value: o})
			}
			f.Value = overrideOr(overrides, sp.Key, sp.Value)
		default:
			f.Type = "text"
			f.Value = overrideOr(overrides, sp.Key, sp.Value)
		}
		fields = append(fields, f)
	}
	if len(fields) == 0 {
		rsp.ToolError(w, req.ID, "no matching config fields to ask for", sessionConfigToolName)
		return
	}

	question, _ := args["reason"].(string)
	question = strings.TrimSpace(question)
	if question == "" {
		question = "Override config for this session"
	}
	question = label + " — " + question

	done := make(chan struct{})
	if r != nil {
		ctx := r.Context()
		go func() {
			<-ctx.Done()
			close(done)
		}()
	}
	ans, err := asks.Ask(askuser.Question{
		SessionID: sessionID,
		Question:  question,
		Fields:    fields,
		Timeout:   4 * time.Minute,
	}, done)
	if err != nil {
		rsp.ToolError(w, req.ID, err.Error(), sessionConfigToolName)
		return
	}
	if len(ans.Values) == 0 {
		writeSessionConfigResult(w, req, rsp, map[string]any{
			"connector_id": connectorID,
			"session_id":   sessionID,
			"applied":      []string{},
			"note":         "user submitted no changes — current config kept",
		})
		return
	}

	// Tokenize user-typed secrets before anything is persisted or
	// returned. Encryption unavailable + secret submitted = hard stop;
	// silently storing plaintext would betray the tool's contract.
	toStore := make(map[string]string, len(ans.Values))
	for k, v := range ans.Values {
		sp, ok := specByKey[k]
		if !ok || v == "" {
			continue
		}
		if sp.IsSecret && !enc.IsToken(v) {
			e := svc.Enc()
			if e == nil || e.Disabled() {
				rsp.ToolError(w, req.ID, fmt.Sprintf("config %q is secret but encryption is disabled on this server — cannot store it safely", k), sessionConfigToolName)
				return
			}
			tok, err := e.EncryptValue(v, userID)
			if err != nil {
				rsp.ToolError(w, req.ID, "encrypt secret: "+err.Error(), sessionConfigToolName)
				return
			}
			v = tok
		}
		toStore[k] = v
	}
	if len(toStore) == 0 {
		writeSessionConfigResult(w, req, rsp, map[string]any{
			"connector_id": connectorID,
			"session_id":   sessionID,
			"applied":      []string{},
			"note":         "user submitted no changes — current config kept",
		})
		return
	}
	if err := sessionconfig.Set(layout, sessionID, connectorID, toStore); err != nil {
		rsp.ToolError(w, req.ID, err.Error(), sessionConfigToolName)
		return
	}
	keys := make([]string, 0, len(toStore))
	for k := range toStore {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	writeSessionConfigResult(w, req, rsp, map[string]any{
		"connector_id": connectorID,
		"session_id":   sessionID,
		"applied":      keys,
	})
}

func sessionConfigClear(w http.ResponseWriter, req RPCRequest, rsp Responder, layout agentconfig.Layout, sessionID, connectorID string, args map[string]any) {
	var keys []string
	if rawKeys, ok := args["keys"].([]any); ok {
		for _, k := range rawKeys {
			if s, ok := k.(string); ok && strings.TrimSpace(s) != "" {
				keys = append(keys, strings.TrimSpace(s))
			}
		}
	}
	removed, err := sessionconfig.Clear(layout, sessionID, connectorID, keys)
	if err != nil {
		rsp.ToolError(w, req.ID, err.Error(), sessionConfigToolName)
		return
	}
	sort.Strings(removed)
	if removed == nil {
		removed = []string{}
	}
	writeSessionConfigResult(w, req, rsp, map[string]any{
		"connector_id": connectorID,
		"session_id":   sessionID,
		"cleared":      removed,
	})
}

func overrideOr(overrides map[string]string, key, fallback string) string {
	if v, ok := overrides[key]; ok {
		return v
	}
	return fallback
}

func writeSessionConfigResult(w http.ResponseWriter, req RPCRequest, rsp Responder, out map[string]any) {
	b, _ := json.Marshal(out)
	rsp.WriteResult(w, req.ID, ToolCallResult{
		Content: []ToolContent{{Type: "text", Text: string(b)}},
	})
}
