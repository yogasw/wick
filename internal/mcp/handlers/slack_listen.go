package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"

	agentchannels "github.com/yogasw/wick/internal/agents/channels"
	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/login"
)

// ThreadListenFn is the channel registry's ThreadListen.
type ThreadListenFn func(sessionID string, req agentchannels.ListenRequest) (agentchannels.ListenResult, error)

const slackListenDescription = "Listen mode for the Slack channel thread THIS conversation is bound to. " +
	"action=on when someone asks you to listen / watch / keep answering in this thread: replies then reach you " +
	"without an @mention, held for your delay first, each marked [listen mode] with your listen instructions; " +
	"you answer only what is meant for you and reply exactly [silent] otherwise. Pass instructions for anything " +
	"the person said about this thread ('only WABA questions') and delay_sec when they named a wait ('wait 2 minutes' = 120). " +
	"action=off when they ask you to stop in any wording (a bare 'stop' reply and removing the 🤖 are already handled " +
	"by wick); whole_channel=true also stops a channel-wide listen. action=status reports the current state. " +
	"Defaults come from LISTEN.md: the wick-wide one at the agents root (a built-in default while that file is absent) and yours in your project folder, " +
	"frontmatter `delay: 60s`, `scope: thread|channel|all`, `on_team_reply: review|skip`, `replace_default: true|false`, " +
	"then the instructions as plain text. When a person corrects what you should or should not answer while listening, " +
	"add that rule to your own LISTEN.md. No session_id: the thread is resolved from the call."

// SlackListenDescriptor is the wick_slack_listen tool.
func SlackListenDescriptor() ToolDescriptor {
	return ToolDescriptor{
		Name:        "wick_slack_listen",
		Description: slackListenDescription,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type": "string", "enum": []string{"on", "off", "status"},
					"description": "on = start listening, off = stop, status = read the state.",
				},
				"instructions": map[string]any{
					"type":        "string",
					"description": "on: extra instructions for this thread only, added after LISTEN.md.",
				},
				"delay_sec": map[string]any{
					"type":        "integer",
					"description": "on: seconds to hold each reply before you see it (0 = at once). Omit to keep LISTEN.md's delay.",
				},
				"whole_channel": map[string]any{
					"type":        "boolean",
					"description": "off: also stop a channel-wide listen (scope: channel).",
				},
			},
			"required": []string{"action"},
		},
		Annotations: &ToolAnnotation{
			Title:           "Slack thread listen mode",
			ReadOnlyHint:    PtrBool(false),
			DestructiveHint: PtrBool(false),
		},
	}
}

// WickSlackListen handles wick_slack_listen for the call's own session.
func WickSlackListen(w http.ResponseWriter, r *http.Request, req RPCRequest, rsp Responder, layout agentconfig.Layout, listen ThreadListenFn, args map[string]any) {
	const tool = "wick_slack_listen"
	if listen == nil {
		rsp.ToolError(w, req.ID, "Slack listen mode is not available in this process", tool)
		return
	}
	sessionID := ResolveSessionPreferArg(SessionOf(r), "")
	if sessionID == "" {
		rsp.ToolError(w, req.ID, "no session on the call", tool)
		return
	}
	sess, err := session.Load(layout, sessionID)
	if err != nil {
		rsp.ToolError(w, req.ID, "load session: "+err.Error(), tool)
		return
	}
	if !canManageSession(login.GetUser(r.Context()), sess.Meta.UserID) {
		rsp.ToolError(w, req.ID, fmt.Sprintf("session not found: %s", sessionID), tool)
		return
	}
	lr := agentchannels.ListenRequest{}
	lr.Action, _ = args["action"].(string)
	lr.Instructions, _ = args["instructions"].(string)
	lr.WholeChannel, _ = args["whole_channel"].(bool)
	if v, ok := args["delay_sec"].(float64); ok {
		n := int(v)
		lr.DelaySec = &n
	}
	out, err := listen(sessionID, lr)
	if err != nil {
		rsp.ToolError(w, req.ID, err.Error(), tool)
		return
	}
	b, _ := json.Marshal(out)
	rsp.WriteResult(w, req.ID, ToolCallResult{Content: []ToolContent{{Type: "text", Text: string(b)}}})
}
