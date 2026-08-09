package delegation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/yogasw/wick/internal/entity"
)

// Agent-to-agent messaging.
//
// This package's older comment says a leader may not inject turns into
// another agent (see takeover.go). Messaging deliberately opens that
// door, and pays for it three ways: the sender is written by the server,
// a message never sets UserSteered (that flag stays a human's), and every
// exchange is counted against a hop cap only a human can reset.

const (
	// defaultAskTimeout bounds a blocking ask. Timing out stops the WAIT,
	// never the question: the message stays queued, so the recipient still
	// answers it — just not into a caller that is still standing there.
	defaultAskTimeout = 10 * time.Minute
	// defaultInboxCap is the backpressure limit per handle.
	defaultInboxCap = 20
	// askPollInterval is how often a waiting ask looks for its reply. A
	// poll rather than a subscription because the reply is written by the
	// recipient's own turn, possibly in another goroutine entirely, and
	// the wait is minutes-scale.
	askPollInterval = 200 * time.Millisecond
	// inboxBatchMax bounds one delivery. Each turn is a full model round,
	// so five queued messages must not become five turns; the remainder
	// waits for the next boundary.
	inboxBatchMax = 10
	// messageMaxRunes bounds one message, matching the delegate task cap.
	messageMaxRunes = 8000
)

// Waker starts or resumes a sub-agent so it can read its inbox.
// Implemented by the pool wiring; an interface keeps this package free of
// process handling.
type Waker interface {
	// WakeChild resumes an exited child from its recorded CLI session, or
	// no-ops when it is already running. It returns ErrContextLost when
	// the child had to be respawned without its previous transcript.
	WakeChild(ctx context.Context, childSessionID, agentName string) error
}

// ErrContextLost means a child was respawned without its earlier
// transcript. The recipient must be told: without the warning it answers
// as though it remembers a conversation it has actually lost, and the
// sender has no way to tell that from a real answer.
var ErrContextLost = errors.New("previous context unavailable")

// ErrUnknownHandle means the address does not exist in this tree.
var ErrUnknownHandle = errors.New("unknown handle")

// SendInput is one agent-to-agent message.
type SendInput struct {
	RootID string
	// FromHandle is resolved from the calling SESSION by the caller of
	// this method, never from model input.
	FromHandle string
	ToHandle   string
	Body       string
	Kind       string // ask | tell
}

// SendResult is what the sender gets back.
type SendResult struct {
	MessageID string `json:"message_id"`
	Status    string `json:"status"` // sent | answered | timeout
	Reply     string `json:"reply,omitempty"`
	Note      string `json:"note,omitempty"`
}

// SendMessage queues a message and, for an ask, waits for its reply.
func (s *Service) SendMessage(ctx context.Context, in SendInput) (*SendResult, error) {
	in.Body = strings.TrimSpace(in.Body)
	if in.Body == "" {
		return nil, errors.New("message body is required")
	}
	if len([]rune(in.Body)) > messageMaxRunes {
		return nil, fmt.Errorf("message too long (max %d characters)", messageMaxRunes)
	}
	if in.Kind == "" {
		in.Kind = entity.MessageTell
	}
	if in.Kind != entity.MessageTell && in.Kind != entity.MessageAsk {
		return nil, fmt.Errorf("kind must be %q or %q, got %q", entity.MessageTell, entity.MessageAsk, in.Kind)
	}
	if in.ToHandle == in.FromHandle {
		return nil, errors.New("an agent cannot message itself")
	}

	target, err := s.Repo.FindByHandle(ctx, in.RootID, in.ToHandle)
	if err != nil {
		return nil, err
	}
	if target == nil {
		return nil, fmt.Errorf("%w: @%s is not part of this conversation", ErrUnknownHandle, in.ToHandle)
	}
	// A finished agent is NOT refused. It used to be: a message to a dead
	// recipient would queue forever, indistinguishable from one being
	// considered. That reasoning stopped holding once the waker respawns a
	// child in its own session — the reader does arrive, and it arrives
	// with its transcript, so "@developer keep going" reaches the agent
	// that did the work rather than nobody.
	//
	// Refusing it was also what made a stopped sub-agent unreachable in
	// the one state where reaching it matters most: stopped_max_turns
	// means the work was cut short, not that it was wrong.
	//
	// What IS still refused is a row with no session to wake at all — a
	// message there really would go nowhere.
	if target.ChildSessionID == "" {
		return nil, fmt.Errorf("@%s has no session to deliver to", in.ToHandle)
	}

	capacity := s.InboxCap
	if capacity <= 0 {
		capacity = defaultInboxCap
	}
	queued, err := s.Repo.CountQueued(ctx, in.RootID, in.ToHandle)
	if err != nil {
		return nil, err
	}
	if queued >= int64(capacity) {
		return nil, fmt.Errorf("@%s already has %d unread messages — wait for it to catch up", in.ToHandle, queued)
	}

	hop, err := s.Repo.BumpHop(ctx, in.RootID)
	if err != nil {
		return nil, err
	}
	if err := AdmitMessage(s.limits(), hop); err != nil {
		return nil, err
	}

	m := &entity.AgentMessage{
		ID:         uuid.NewString(),
		RootID:     in.RootID,
		FromHandle: in.FromHandle,
		ToHandle:   in.ToHandle,
		Body:       in.Body,
		Kind:       in.Kind,
		Hop:        hop,
		Status:     entity.MessageQueued,
		CreatedAt:  time.Now(),
	}
	if err := s.Repo.EnqueueMessage(ctx, m); err != nil {
		return nil, err
	}

	note := ""
	if s.Waker != nil {
		if err := s.Waker.WakeChild(ctx, target.ChildSessionID, target.ChildAgent); err != nil {
			if !errors.Is(err, ErrContextLost) {
				return nil, fmt.Errorf("wake @%s: %w", in.ToHandle, err)
			}
			note = fmt.Sprintf("@%s had to be restarted and lost the context of its earlier work — it is answering fresh.", in.ToHandle)
		}
	}

	// Hand it over now only when the recipient is NOT mid-turn. A running
	// child is between no reads, so writing into it would interleave with
	// the turn it is already thinking through; that one waits for its turn
	// boundary, which is also what lets several messages arrive as one
	// batch instead of one turn each.
	if target.Status != entity.DelegationRunning {
		if _, derr := s.DeliverInbox(ctx, in.RootID, in.ToHandle); derr != nil {
			return nil, fmt.Errorf("deliver to @%s: %w", in.ToHandle, derr)
		}
	}

	if in.Kind != entity.MessageAsk {
		return &SendResult{MessageID: m.ID, Status: "sent", Note: note}, nil
	}
	res, err := s.waitForReply(ctx, m)
	if err != nil {
		return nil, err
	}
	if note != "" {
		res.Note = strings.TrimSpace(note + " " + res.Note)
	}
	return res, nil
}

// waitForReply blocks until the ask is answered or the timeout expires.
func (s *Service) waitForReply(ctx context.Context, ask *entity.AgentMessage) (*SendResult, error) {
	timeout := s.AskTimeout
	if timeout <= 0 {
		timeout = defaultAskTimeout
	}
	deadline := time.After(timeout)
	tick := time.NewTicker(askPollInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline:
			return &SendResult{
				MessageID: ask.ID,
				Status:    "timeout",
				Note: fmt.Sprintf(
					"@%s has not answered yet. The question is still in its inbox — carry on with other work and check back.",
					ask.ToHandle),
			}, nil
		case <-tick.C:
			reply, err := s.Repo.FindReply(ctx, ask.ID)
			if err != nil {
				return nil, err
			}
			if reply == nil {
				continue
			}
			res := &SendResult{MessageID: ask.ID, Status: "answered", Reply: reply.Body}
			if reply.AutoReply {
				res.Note = "This is @" + reply.FromHandle +
					"'s closing message for that turn, not a direct reply — it may not answer what you asked."
			}
			return res, nil
		}
	}
}

// Reply answers an ask.
func (s *Service) Reply(ctx context.Context, askID, fromHandle, body string) error {
	return s.reply(ctx, askID, fromHandle, body, false)
}

// AutoReply closes an unanswered ask with the recipient's final text.
//
// Without it, a recipient that simply forgot to call reply hangs the
// sender until the timeout — the commonest failure mode with a model in
// the loop, and the most expensive one, since the sender is blocked the
// whole time.
func (s *Service) AutoReply(ctx context.Context, askID, fromHandle, body string) error {
	return s.reply(ctx, askID, fromHandle, body, true)
}

func (s *Service) reply(ctx context.Context, askID, fromHandle, body string, auto bool) error {
	body = strings.TrimSpace(body)
	if body == "" {
		return errors.New("reply body is required")
	}
	ask, err := s.Repo.GetMessage(ctx, askID)
	if err != nil {
		return err
	}
	if ask == nil {
		return fmt.Errorf("no such question: %s", askID)
	}
	// Idempotent: an explicit reply that beat the turn-end fallback wins,
	// and a double call must not enqueue two answers.
	if ask.Status == entity.MessageAnswered {
		return nil
	}
	r := &entity.AgentMessage{
		ID:         uuid.NewString(),
		RootID:     ask.RootID,
		FromHandle: fromHandle,
		ToHandle:   ask.FromHandle,
		Body:       body,
		Kind:       entity.MessageReply,
		ReplyTo:    askID,
		AutoReply:  auto,
		Hop:        ask.Hop,
		// Delivered, not queued: the waiting sender reads it directly, so
		// queueing it would also push it into the sender's next turn.
		Status:    entity.MessageDelivered,
		CreatedAt: time.Now(),
	}
	if err := s.Repo.EnqueueMessage(ctx, r); err != nil {
		return err
	}
	return s.Repo.MarkAnswered(ctx, askID, r.ID)
}

// DeliverInbox hands a handle's queued messages to it as ONE turn and
// reports how many were delivered.
//
// Called at a turn boundary, never mid-turn: writing into a child that is
// mid-thought puts two writers on one stdin. The count matters to the
// caller — a delegation that just received work must keep running instead
// of closing on the turn it was about to finish with.
func (s *Service) DeliverInbox(ctx context.Context, rootID, handle string) (int, error) {
	if handle == "" {
		return 0, nil
	}
	msgs, err := s.Repo.DrainInbox(ctx, rootID, handle, inboxBatchMax)
	if err != nil || len(msgs) == 0 {
		return 0, err
	}
	target, err := s.Repo.FindByHandle(ctx, rootID, handle)
	if err != nil {
		return 0, err
	}
	if target == nil {
		return 0, fmt.Errorf("%w: %s", ErrUnknownHandle, handle)
	}
	roster, budget, err := s.rosterAndBudget(ctx, rootID)
	if err != nil {
		return 0, err
	}
	if s.Steerer == nil {
		return 0, errors.New("no transport for agent messages")
	}
	if err := s.Steerer.SendToChild(ctx, target.ChildSessionID, target.ChildAgent,
		FormatInbound(msgs, roster, budget)); err != nil {
		return 0, err
	}
	return len(msgs), nil
}

// CloseUnansweredAsk promotes a recipient's final text to a reply when it
// finished its turn without answering. Call at turn end, before delivery.
func (s *Service) CloseUnansweredAsk(ctx context.Context, rootID, handle, finalText string) error {
	if handle == "" || strings.TrimSpace(finalText) == "" {
		return nil
	}
	ask, err := s.Repo.OldestUnansweredAsk(ctx, rootID, handle)
	if err != nil || ask == nil {
		return err
	}
	return s.AutoReply(ctx, ask.ID, handle, finalText)
}

// rosterAndBudget builds the live address list and remaining allowances
// that ride along with every delivery.
func (s *Service) rosterAndBudget(ctx context.Context, rootID string) ([]RosterEntry, BudgetLine, error) {
	rows, err := s.Repo.ListByRoot(ctx, rootID)
	if err != nil {
		return nil, BudgetLine{}, err
	}
	lim := s.limits().normalize()
	b := BudgetLine{TurnsMax: lim.RootBudget, TokensMax: lim.RootTokenBudget, HopMax: lim.MaxHops}
	roster := make([]RosterEntry, 0, len(rows))
	for _, d := range rows {
		if d.Handle == "" {
			continue
		}
		state := "idle"
		switch {
		case d.Status == entity.DelegationRunning:
			state = "working"
		case entity.IsTerminalDelegationStatus(d.Status):
			state = "done"
		}
		roster = append(roster, RosterEntry{Handle: d.Handle, Role: d.ProfileKey, State: state})
		b.TurnsUsed += d.TurnsUsed
		b.TokensUsed += d.TokensUsed
		if d.ID == rootID {
			b.Hop = d.HopCount
		}
	}
	return roster, b, nil
}

// HopsLeft reports how many agent-to-agent messages the tree may still
// send before a human has to speak. Read-only; drives the UI's warning
// and its "allow more" button.
func (s *Service) HopsLeft(ctx context.Context, rootID string) int {
	lim := s.limits().normalize()
	root, err := s.Repo.Get(ctx, rootID)
	if err != nil || root == nil {
		return lim.MaxHops
	}
	return max(lim.MaxHops-root.HopCount, 0)
}
