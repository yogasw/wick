package channel

import (
	"github.com/yogasw/wick/internal/agents/workflow"
)

// InjectImplicitReply scans the workflow and, if the trigger is a
// channel event without an explicit reply node back to the same
// channel+thread, appends a synthetic reply node at every terminal
// edge. ReplySource=false on the trigger opts out.
//
// Behavior:
//   - skip if trigger.reply_source explicitly false
//   - skip if any existing `type: channel` action node uses op
//     reply_thread/reply/send_message
func InjectImplicitReply(w *workflow.Workflow) {
	chTrig := firstChannelTrigger(w)
	if chTrig == nil {
		return
	}
	if chTrig.ReplySource != nil && !*chTrig.ReplySource {
		return
	}
	if hasExplicitReply(w) {
		return
	}
	const syntheticID = "__implicit_reply__"
	for _, n := range w.Graph.Nodes {
		if n.ID == syntheticID {
			return
		}
	}
	reply := workflow.Node{
		ID:          syntheticID,
		Type:        workflow.NodeChannel,
		ChannelName: chTrig.ChannelName,
		Op:          "reply_thread",
		Args: map[string]any{
			"channel": "{{.Event.Payload.channel_id}}",
			"thread":  "{{.Event.Payload.thread}}",
			"text":    "{{.Run.ID}}",
		},
	}
	w.Graph.Nodes = append(w.Graph.Nodes, reply)
	hasOut := map[string]bool{}
	for _, e := range w.Graph.Edges {
		hasOut[e.From] = true
	}
	for _, n := range w.Graph.Nodes {
		if n.ID == syntheticID {
			continue
		}
		if n.Type == workflow.NodeEnd {
			continue
		}
		if !hasOut[n.ID] {
			w.Graph.Edges = append(w.Graph.Edges, workflow.Edge{From: n.ID, To: syntheticID})
		}
	}
}

func firstChannelTrigger(w *workflow.Workflow) *workflow.Trigger {
	for i, tr := range w.Triggers {
		if tr.Type == workflow.TriggerChannel {
			return &w.Triggers[i]
		}
	}
	return nil
}

func hasExplicitReply(w *workflow.Workflow) bool {
	for _, n := range w.Graph.Nodes {
		if n.Type != workflow.NodeChannel {
			continue
		}
		switch n.Op {
		case "reply_thread", "reply", "send_message":
			return true
		}
	}
	return false
}
