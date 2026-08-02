package delegation

import (
	"context"
	"errors"
	"fmt"

	"github.com/yogasw/wick/internal/entity"
)

// Take-over: a human sending a message straight into a running
// sub-agent.
//
// Off by default, per role. The reason is not safety but honesty: once a
// person steers a sub-agent, the answer is no longer that role's own
// work, and handing it to the leader unlabelled would misrepresent where
// it came from. So the delegation is flagged UserSteered and the leader
// is told when it collects the result.
//
// Stopping a sub-agent is a different act and stays always-allowed —
// interrupting is not steering.

// ErrTakeOverNotAllowed means the profile has not opted into take-over.
var ErrTakeOverNotAllowed = errors.New("this sub-agent role does not allow take-over")

// ErrNotSteerable means the delegation is not in a state that can accept
// a message (already finished, or never started).
var ErrNotSteerable = errors.New("sub-agent is not running")

// TakeOver sends a human message into a running sub-agent.
//
// Authorization matches interrupt: the human who triggered the tree, or
// an admin. A leader agent cannot call this — steering is a human act,
// and letting one agent inject turns into another would blur the
// delegation boundary the whole design rests on.
func (s *Service) TakeOver(ctx context.Context, delegationID, actorID, message string, isAdmin bool) error {
	if message == "" {
		return fmt.Errorf("message is required")
	}
	d, err := s.Repo.Get(ctx, delegationID)
	if err != nil {
		return err
	}
	if !CanInterrupt(d, actorID, isAdmin) {
		return ErrForbidden
	}
	if d.Status != entity.DelegationRunning {
		return ErrNotSteerable
	}

	profile, err := s.Repo.GetProfileScoped(ctx, d.ProjectID, d.ProfileKey)
	if err != nil {
		return err
	}
	if !profile.AllowTakeOver {
		return ErrTakeOverNotAllowed
	}

	// Flag BEFORE delivering. If the sub-agent finishes between the write
	// and the send, the result is still correctly labelled as steered —
	// the reverse order could hand back a steered answer marked as
	// unaided work.
	if err := s.Repo.MarkSteered(ctx, delegationID); err != nil {
		return err
	}
	if s.Steerer == nil {
		return fmt.Errorf("take-over is not wired on this transport")
	}
	return s.Steerer.SendToChild(ctx, d.ChildSessionID, d.ChildAgent, message)
}

// Steerer delivers a human message into a running sub-agent session.
type Steerer interface {
	SendToChild(ctx context.Context, childSessionID, agentName, message string) error
}
