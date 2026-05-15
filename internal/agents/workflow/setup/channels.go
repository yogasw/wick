package setup

import (
	agentchannels "github.com/yogasw/wick/internal/agents/channels"
	"github.com/yogasw/wick/internal/agents/workflow/channel"
)

// RegisterLiveChannels rewires the workflow channel registry to use the
// live agentchannels.Registry as its backing source. Channels become
// workflow-visible automatically once they implement
// agentchannels.WorkflowTriggerProvider or WorkflowActionProvider —
// channels without either (UI, API, REST one-shot) stay invisible to
// the workflow editor.
//
// Call after the base channel registry has its channels added (after
// channels/setup constructs them in server.go) and before
// wfMgr.Start(ctx).
func RegisterLiveChannels(wfReg *channel.Registry, base *agentchannels.Registry) {
	if wfReg == nil || base == nil {
		return
	}
	wfReg.SetBase(base)
}
