package delegation

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/entity"
)

// Serial execution per room.
//
// A conversation runs one sub-agent at a time by default. Work that
// arrives while the room is busy is written as a `queued` row and started
// when a slot frees, rather than refused — a four-way mention fan-out that
// produced one run and three rejections would make the pattern unusable.
//
// Two rules make this safe:
//
//   - A slot is held by an agent DOING work, not one waiting. A parent
//     blocked on a synchronous child releases its slot (entity.Blocked),
//     otherwise a serial room deadlocks the instant a sub-agent delegates.
//   - Admission is re-checked when an item finally starts, not only when
//     it was enqueued. A tree that burned its budget while an item waited
//     must fail that item visibly rather than run it.

// slotPokeInterval is how often a waiter re-checks its own eligibility
// even without a poke. Pokes are the real mechanism; this only has to
// catch one lost to a crash.
const slotPokeInterval = 3 * time.Second

// hasSlot reports whether the tree may start one more sub-agent now.
//
// Fails CLOSED: an unreadable count must not be treated as "room
// available", or a database blip turns a serial room into an unbounded
// fan-out.
func (s *Service) hasSlot(ctx context.Context, rootID string) bool {
	n, err := s.Repo.CountActiveByRoot(ctx, rootID)
	if err != nil {
		log.Warn().Err(err).Str("root", rootID).Msg("delegation: slot count failed; treating the room as full")
		return false
	}
	return int(n) < s.limits().MaxParallel
}

// subscribeSlot registers a waiter for "a slot in this tree may have
// freed" notifications.
func (s *Service) subscribeSlot(rootID string) chan struct{} {
	ch := make(chan struct{}, 1)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.slotWaiters == nil {
		s.slotWaiters = map[string][]chan struct{}{}
	}
	s.slotWaiters[rootID] = append(s.slotWaiters[rootID], ch)
	return ch
}

func (s *Service) unsubscribeSlot(rootID string, ch chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	waiters := s.slotWaiters[rootID]
	for i, w := range waiters {
		if w == ch {
			s.slotWaiters[rootID] = append(waiters[:i], waiters[i+1:]...)
			break
		}
	}
	if len(s.slotWaiters[rootID]) == 0 {
		delete(s.slotWaiters, rootID)
	}
}

// pokeSlot wakes every parked waiter and starts the next queued item.
// Called wherever a delegation reaches a terminal status.
//
// Every waiter, not only this tree's: the slot belongs to the whole
// conversation, which spans several trees, and the waiter list on a host
// is a handful of channels.
func (s *Service) pokeSlot(rootID string) {
	if rootID == "" {
		return
	}
	s.mu.Lock()
	var waiters []chan struct{}
	for _, ws := range s.slotWaiters {
		waiters = append(waiters, ws...)
	}
	s.mu.Unlock()
	for _, ch := range waiters {
		// Non-blocking: the channel is buffered by one, and a waiter that
		// has not drained its previous poke will re-check anyway.
		select {
		case ch <- struct{}{}:
		default:
		}
	}
	go s.startNextQueued(context.Background(), rootID)
}

// claimSlot atomically takes the conversation's next slot for row id:
// the row must be the head of the queue AND a slot must be free, and the
// flip to running happens under the same lock as the check.
//
// Without the lock, four delegations fired in the same instant each saw
// zero running siblings and each claimed — the cap was a race, not a
// limit. errNotQueued means another dispatcher already started the row.
func (s *Service) claimSlot(ctx context.Context, rootID, id string) (bool, error) {
	s.slotMu.Lock()
	defer s.slotMu.Unlock()
	head, err := s.Repo.OldestQueued(ctx, rootID)
	if err != nil {
		return false, nil
	}
	if head == nil {
		// Not queued at all any more: someone else moved it.
		return false, errNotQueued
	}
	if head.ID != id || !s.hasSlot(ctx, rootID) {
		return false, nil
	}
	if err := s.Repo.MarkRunning(ctx, id); err != nil {
		if errors.Is(err, errNotQueued) {
			return false, err
		}
		log.Warn().Err(err).Str("delegation", id).Msg("delegation: mark running failed")
		return false, nil
	}
	return true, nil
}

// waitForSlot blocks until this row has claimed a slot (see claimSlot).
//
// A synchronous caller waits here rather than getting a timeout knob of
// its own: it inherits the MCP call's context, so if the caller goes away
// the wait ends with it. A knob nobody can sensibly set is worse than the
// cancellation that already exists. errNotQueued means a dispatcher took
// the row first.
func (s *Service) waitForSlot(ctx context.Context, rootID, id string) error {
	ch := s.subscribeSlot(rootID)
	defer s.unsubscribeSlot(rootID, ch)
	tick := time.NewTicker(slotPokeInterval)
	defer tick.Stop()
	for {
		ok, err := s.claimSlot(ctx, rootID, id)
		if ok {
			return nil
		}
		if err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ch:
		case <-tick.C:
		}
	}
}

// startNextQueued admits and starts the head of a conversation's queue,
// if any.
//
// Only ASYNC rows are started here. A sync row's caller is parked inside
// waitForSlot and starts itself — starting it from the dispatcher would
// run the work twice while its caller still waits.
func (s *Service) startNextQueued(ctx context.Context, rootID string) {
	head, profile, refusal := s.claimQueuedHead(ctx, rootID)
	if refusal != nil {
		s.finish(ctx, refusal.row, entity.DelegationQueued, refusal.status, refusal.result, refusal.errMsg, 0)
		return
	}
	if head == nil {
		return
	}

	userTags := []string(nil)
	if s.Tags != nil && head.TriggeredBy != "" {
		userTags = s.Tags.GetUserFilterTagIDs(ctx, head.TriggeredBy)
	}
	if _, err := s.execute(context.WithoutCancel(ctx), head, profile, EffectiveTags(userTags, profile)); err != nil {
		log.Error().Err(err).Str("delegation", head.ID).Msg("delegation: queued start failed")
	}
}

// queuedRefusal is a queued head that must end instead of start. finish
// runs after slotMu is released: it pokes the queue again.
type queuedRefusal struct {
	row                    *entity.AgentDelegation
	status, result, errMsg string
}

// claimQueuedHead is startNextQueued's check-admit-claim, all under
// slotMu so a concurrent Run cannot take the same slot in between.
func (s *Service) claimQueuedHead(ctx context.Context, rootID string) (*entity.AgentDelegation, *entity.AgentProfile, *queuedRefusal) {
	s.slotMu.Lock()
	defer s.slotMu.Unlock()
	head, err := s.Repo.OldestQueued(ctx, rootID)
	if err != nil || head == nil {
		return nil, nil, nil
	}
	if !s.hasSlot(ctx, rootID) {
		return nil, nil, nil
	}
	if head.Mode != ModeAsync {
		// The sync waiter has already been poked; it will take the slot.
		return nil, nil, nil
	}

	profile, err := s.Repo.GetProfileScoped(ctx, head.ProjectID, head.ProfileKey)
	if err != nil {
		return nil, nil, &queuedRefusal{row: head, status: entity.DelegationFailed,
			errMsg: "the role disappeared while this was queued: " + err.Error()}
	}

	// Re-admit at START time, not only at enqueue time. An item that sat
	// in the queue while the tree spent its budget must fail visibly with
	// the refusal as its result, not run as though nothing had changed.
	//
	// The stored chain ends with the row's OWN role (Run appends it), so
	// it is trimmed here: re-admitting against it read every queued item
	// as delegating to itself and failed it as a loop.
	if err := s.Repo.Admit(ctx, s.limits(), profile, head.RootID, head.Depth, parentChain(head.AncestorKeys)); err != nil {
		var refusal *Refusal
		if errors.As(err, &refusal) {
			return nil, nil, &queuedRefusal{row: head, status: refusal.Status(), result: refusal.Message}
		}
		// A transient storage error leaves the row queued; the next poke
		// or sweep retries it.
		log.Warn().Err(err).Str("delegation", head.ID).Msg("delegation: queued re-admission failed")
		return nil, nil, nil
	}

	if err := s.Repo.MarkRunning(ctx, head.ID); err != nil {
		if !errors.Is(err, errNotQueued) { // errNotQueued: another dispatcher claimed it first
			log.Warn().Err(err).Str("delegation", head.ID).Msg("delegation: queued mark-running failed")
		}
		return nil, nil, nil
	}
	head.Status = entity.DelegationRunning
	return head, profile, nil
}

// queuedResult is what a background caller gets when the room is busy.
func queuedResult(row *entity.AgentDelegation, pos int) *Result {
	note := "Queued — nothing else is ahead of it; it starts as soon as the current sub-agent finishes."
	if pos > 1 {
		note = fmt.Sprintf(
			"Queued behind %d other sub-agent(s) in this conversation. Carry on with other work; the result is delivered when it finishes.",
			pos-1)
	}
	return &Result{
		DelegationID:  row.ID,
		Profile:       row.ProfileKey,
		Status:        entity.DelegationQueued,
		Mode:          ModeBackground,
		QueuePosition: pos,
		Note:          note,
		WorkspaceNote: row.WorkspaceNote,
	}
}

// parentChain is a row's stored ancestor chain without the row's own
// role — the chain Run admitted it against.
func parentChain(encoded string) []string {
	keys := decodeStringSlice(encoded)
	if len(keys) == 0 {
		return keys
	}
	return keys[:len(keys)-1]
}
