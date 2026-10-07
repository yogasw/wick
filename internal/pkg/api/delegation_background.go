package api

import (
	"context"

	agentchannels "github.com/yogasw/wick/internal/agents/channels"
	"github.com/yogasw/wick/internal/agents/delegation"
)

// channelSurvivor maps a delegation survivor onto the channel layer's copy of
// the same two names.
func channelSurvivor(sv delegation.Survivor) agentchannels.DetachedSurvivor {
	return agentchannels.DetachedSurvivor{Handle: sv.Handle, ProfileKey: sv.ProfileKey}
}

func channelSurvivors(svs []delegation.Survivor) []agentchannels.DetachedSurvivor {
	out := make([]agentchannels.DetachedSurvivor, 0, len(svs))
	for _, sv := range svs {
		out = append(out, channelSurvivor(sv))
	}
	return out
}

// backgroundRecheck adapts the delegation service's re-check to the channel
// layer's probe. A read error answers "could not tell" so the banner keeps
// its current set rather than clearing on a hiccup.
func backgroundRecheck(svc *delegation.Service) agentchannels.BackgroundRecheckFn {
	return func(ctx context.Context, sessionID string) ([]agentchannels.DetachedSurvivor, bool) {
		active, err := svc.RecheckBackground(ctx, sessionID)
		if err != nil {
			return nil, false
		}
		return channelSurvivors(active), true
	}
}
