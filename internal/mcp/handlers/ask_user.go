package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/yogasw/wick/internal/agents/askuser"
)

// AskUser handles the ask_user tool.
// asker is nil when ask_user is disabled; it's the in-process Manager
// on the HTTP server and a SocketAsker (forwarding to the server's
// askuser socket) in stdio mode.
// allowed is the gate-policy check; nil means always allowed.
func AskUser(
	w http.ResponseWriter,
	r *http.Request,
	req RPCRequest,
	rsp Responder,
	asker askuser.Asker,
	allowed func(sessionID string) (bool, string),
	args map[string]any,
) {
	if asker == nil {
		rsp.WriteError(w, req.ID, -32603, "ask_user disabled — wick is not running with the agents UI", nil)
		return
	}

	type optionIn struct {
		Label       string `json:"label"`
		Value       string `json:"value"`
		Description string `json:"description"`
	}
	type questionIn struct {
		Key           string     `json:"key"`
		Question      string     `json:"question"`
		Type          string     `json:"type"`
		Options       []optionIn `json:"options"`
		AllowFreeform bool       `json:"allow_freeform"`
		Required      bool       `json:"required"`
		Placeholder   string     `json:"placeholder"`
		Help          string     `json:"help"`
	}
	type input struct {
		SessionID     string       `json:"session_id"`
		AgentName     string       `json:"agent_name"`
		Question      string       `json:"question"`
		Options       []optionIn   `json:"options"`
		AllowFreeform bool         `json:"allow_freeform"`
		Questions     []questionIn `json:"questions"`
	}
	raw, _ := json.Marshal(args)
	var in input
	if err := json.Unmarshal(raw, &in); err != nil {
		rsp.ToolError(w, req.ID, "invalid arguments: "+err.Error(), "ask_user")
		return
	}
	if strings.TrimSpace(in.SessionID) == "" {
		rsp.ToolError(w, req.ID, "session_id is required", "ask_user")
		return
	}
	multi := len(in.Questions) > 0
	if !multi && strings.TrimSpace(in.Question) == "" {
		rsp.ToolError(w, req.ID, "question is required (or pass questions[] for a multi-question form)", "ask_user")
		return
	}
	// Policy is resolved per session origin (set by the wiring in
	// internal/pkg/api). Done after parsing so we have session_id.
	if allowed != nil {
		if ok, reason := allowed(in.SessionID); !ok {
			msg := "ask_user blocked by policy"
			if strings.TrimSpace(reason) != "" {
				msg += " (" + reason + ")"
			}
			msg += " — pick a sensible default and proceed without prompting the user."
			rsp.ToolError(w, req.ID, msg, "ask_user")
			return
		}
	}
	opts := make([]askuser.Option, 0, len(in.Options))
	for _, o := range in.Options {
		opts = append(opts, askuser.Option{Label: o.Label, Value: o.Value})
	}

	// Multi-question form: each questions[] entry becomes a Field. A
	// missing key falls back to its index so the answer map is still
	// addressable; a missing type defaults to "choice" when options are
	// present, else "text".
	var fields []askuser.Field
	for i, qq := range in.Questions {
		key := strings.TrimSpace(qq.Key)
		if key == "" {
			key = "q" + strconv.Itoa(i+1)
		}
		fopts := make([]askuser.Option, 0, len(qq.Options))
		for _, o := range qq.Options {
			fopts = append(fopts, askuser.Option{Label: o.Label, Value: o.Value, Description: o.Description})
		}
		ftype := strings.TrimSpace(qq.Type)
		if ftype == "" {
			if len(fopts) > 0 {
				ftype = "choice"
			} else {
				ftype = "text"
			}
		}
		fields = append(fields, askuser.Field{
			Key:           key,
			Label:         qq.Question,
			Type:          ftype,
			Options:       fopts,
			AllowFreeform: qq.AllowFreeform,
			Required:      qq.Required,
			Placeholder:   qq.Placeholder,
			Help:          qq.Help,
		})
	}

	q := askuser.Question{
		SessionID:     in.SessionID,
		AgentName:     in.AgentName,
		Question:      in.Question,
		Options:       opts,
		AllowFreeform: in.AllowFreeform,
		Fields:        fields,
		Timeout:       4 * time.Minute,
	}

	done := make(chan struct{})
	if r != nil {
		ctx := r.Context()
		go func() {
			<-ctx.Done()
			close(done)
		}()
	}

	ans, err := asker.Ask(q, done)
	if err != nil {
		rsp.ToolError(w, req.ID, err.Error(), "ask_user")
		return
	}
	var out any
	if multi {
		// Multi-question form: answer is the per-key map. multi-select
		// fields arrive JSON-array-encoded in the string value.
		vals := ans.Values
		if vals == nil {
			vals = map[string]string{}
		}
		out = map[string]any{"values": vals}
	} else {
		out = map[string]string{"value": ans.Value, "text": ans.Text}
	}
	b, _ := json.Marshal(out)
	rsp.WriteResult(w, req.ID, ToolCallResult{
		Content: []ToolContent{{Type: "text", Text: string(b)}},
	})
}
