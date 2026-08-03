package delegation

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/entity"
)

// The mention router.
//
// `delegate` works and stays. This is a second entry point with a very
// different cost: writing "@log-investigator check the 401s" costs one
// line, while the equivalent tool call costs a round-trip and a model
// that remembered to make it. For a fan-out — four investigators on one
// problem — that difference decides whether the pattern gets used at all.
//
// Routing is best-effort by construction. It never fails the turn that
// carried the mention, because the mention is a side channel: the text
// itself still has to reach whoever it was addressed to.

// Dispatch records one mention that was acted on.
type Dispatch struct {
	// Token is the @name as written, without the @.
	Token string
	Kind  TargetKind
	// DelegationID is set when a role was spawned.
	DelegationID string
	Queued       bool
	QueuePos     int
	// Err carries a governor refusal, phrased for the model.
	Err string
}

// RouteInput is one piece of text to scan.
type RouteInput struct {
	// SessionID is whose text this is — a leader's session or a child's.
	SessionID string
	ProjectID string
	// FromHandle is the author's address inside the tree, resolved from
	// the SESSION rather than from model input.
	FromHandle string
	// Human marks text a person wrote. A human's mentions never consume
	// the agent-to-agent hop budget, because that budget exists to stop
	// agents talking to each other unattended.
	Human       bool
	Text        string
	TriggeredBy string
}

// Route acts on the mentions in one piece of text.
func (s *Service) Route(ctx context.Context, in RouteInput) []Dispatch {
	if s == nil || s.Repo == nil || !s.limits().MentionRouter {
		return nil
	}
	if strings.TrimSpace(in.Text) == "" {
		return nil
	}

	rootID := s.RootForSession(ctx, in.SessionID)
	res, err := s.NewResolver(ctx, rootID, in.ProjectID)
	if err != nil {
		log.Warn().Err(err).Str("session", in.SessionID).
			Msg("delegation: mention routing skipped; roster unavailable")
		return nil
	}

	mentions := ParseMentions(in.Text, res.AllNames())
	if len(mentions) == 0 {
		return nil
	}

	// Two or more mentions in one message is a fan-out, and a fan-out that
	// blocks is not a fan-out: the first synchronous call would hold the
	// author while the rest waited behind it. Q still runs them one at a
	// time; async only decides that nobody is blocked meanwhile.
	fanOut := len(mentions) > 1

	out := make([]Dispatch, 0, len(mentions))
	for _, m := range mentions {
		switch t := res.Resolve(m.Handle); t.Kind {
		case TargetAgent:
			out = append(out, s.routeToAgent(ctx, in, rootID, m, t))
		case TargetRole:
			out = append(out, s.routeToRole(ctx, in, rootID, m, t, fanOut))
		}
		// TargetUnknown is deliberately silent. Most @ tokens in agent
		// output are not mentions, and a "could not resolve @ts-ignore"
		// line teaches the model to avoid syntax it was never misusing.
	}
	return out
}

// routeToAgent delivers a mention addressed to a live instance.
//
// Always `tell`, never `ask`: an ask blocks its sender, and the sender
// here is a turn that has already ended.
func (s *Service) routeToAgent(ctx context.Context, in RouteInput, rootID string, m Mention, t Target) Dispatch {
	d := Dispatch{Token: m.Handle, Kind: TargetAgent}
	if in.FromHandle == t.Handle {
		d.Err = "an agent cannot message itself"
		return d
	}
	_, err := s.SendMessage(ctx, SendInput{
		RootID: rootID, FromHandle: in.FromHandle,
		ToHandle: t.Handle, Body: m.Body, Kind: entity.MessageTell,
	})
	if err != nil {
		d.Err = refusalText(err, "message failed")
	}
	return d
}

// routeToRole spawns the role a mention names.
func (s *Service) routeToRole(ctx context.Context, in RouteInput, rootID string, m Mention, t Target, fanOut bool) Dispatch {
	d := Dispatch{Token: m.Handle, Kind: TargetRole}

	req := Request{
		ProfileKey:      t.Role,
		Task:            m.Body,
		ParentSessionID: in.SessionID,
		ProjectID:       in.ProjectID,
		TriggeredBy:     in.TriggeredBy,
		RootID:          rootID,
	}
	// A mentioning sub-agent inherits its own place in the tree, which is
	// what keeps the depth limit and the cycle guard meaningful.
	if parent, perr := s.Repo.FindByChildSession(ctx, in.SessionID); perr == nil && parent != nil {
		req.RootID = parent.RootID
		req.Depth = parent.Depth + 1
		req.AncestorKeys = Ancestors(parent)
		if parent.TriggeredBy != "" {
			req.TriggeredBy = parent.TriggeredBy
		}
	}
	if fanOut {
		req.Mode, req.DeliverySink = ModeAsync, SinkSession
	}

	res, err := s.Run(ctx, req)
	if err != nil {
		d.Err = refusalText(err, "dispatch failed")
		return d
	}
	d.DelegationID = res.DelegationID
	d.Queued = res.Status == entity.DelegationQueued
	d.QueuePos = res.QueuePosition
	return d
}

// refusalText surfaces a governor refusal verbatim — those messages are
// already written as guidance to a model — and hides anything else behind
// a short phrase, so an internal error never reaches an agent's context.
func refusalText(err error, fallback string) string {
	var refusal *Refusal
	if errors.As(err, &refusal) {
		return refusal.Message
	}
	log.Warn().Err(err).Msg("delegation: mention dispatch failed")
	return fallback
}

// FormatDispatches renders the line an author is told after its mentions
// were acted on.
//
// The author's own text is never rewritten or stripped — an agent that
// sees its words edited loses the thread — so this rides alongside as a
// short report of what actually started.
func FormatDispatches(ds []Dispatch) string {
	if len(ds) == 0 {
		return ""
	}
	parts := make([]string, 0, len(ds))
	for _, d := range ds {
		switch {
		case d.Err != "":
			parts = append(parts, fmt.Sprintf("@%s (refused: %s)", d.Token, d.Err))
		case d.Kind == TargetAgent:
			parts = append(parts, fmt.Sprintf("@%s (messaged)", d.Token))
		case d.Queued:
			// The position is the point: an author fanning out four
			// investigators must see that three have not started yet
			// rather than assume all four are already reading.
			parts = append(parts, fmt.Sprintf("@%s (queued #%d, %s)", d.Token, d.QueuePos, d.DelegationID))
		default:
			parts = append(parts, fmt.Sprintf("@%s (running, %s)", d.Token, d.DelegationID))
		}
	}
	return "dispatched: " + strings.Join(parts, " · ")
}

// RouteAgentTurn scans one finished agent turn and reports back what it
// dispatched.
//
// The author's identity is resolved from its SESSION, never from the text
// — a model that could name its own sender could impersonate a peer. The
// report is enqueued as an ordinary message rather than injected, so it
// arrives at the author's next turn boundary alongside anything else
// waiting, instead of interrupting the turn that has just ended.
func (s *Service) RouteAgentTurn(ctx context.Context, sessionID, agentName, text string) {
	if s == nil || s.Repo == nil {
		return
	}
	in := RouteInput{SessionID: sessionID, FromHandle: entity.LeaderHandle, Text: text}
	if row, err := s.Repo.FindByChildSession(ctx, sessionID); err == nil && row != nil {
		in.FromHandle = row.Handle
		in.ProjectID = row.ProjectID
		in.TriggeredBy = row.TriggeredBy
	} else if root := s.RootForSession(ctx, sessionID); root != "" {
		if r, gerr := s.Repo.Get(ctx, root); gerr == nil && r != nil {
			in.ProjectID = r.ProjectID
			in.TriggeredBy = r.TriggeredBy
		}
	}

	ds := s.Route(ctx, in)
	line := FormatDispatches(ds)
	if line == "" {
		return
	}
	// The leader is not told. It has the rail panel — every dispatch, its
	// queue position and its result are already in front of the person
	// reading, and waking the leader for a line it can see would spend one
	// of the tree's turns to say nothing new. A sub-agent has no panel, so
	// for it the line is the only report there is.
	if in.FromHandle == entity.LeaderHandle {
		return
	}
	rootID := s.RootForSession(ctx, sessionID)
	if rootID == "" || in.FromHandle == "" {
		return
	}
	// Best-effort: the work is already running, and failing to tell the
	// author about it must not undo that.
	if _, err := s.SendMessage(ctx, SendInput{
		RootID: rootID, FromHandle: entity.LeaderHandle,
		ToHandle: in.FromHandle, Body: line, Kind: entity.MessageTell,
	}); err != nil {
		log.Debug().Err(err).Str("session", sessionID).
			Msg("delegation: dispatch report not delivered")
	}
}

// RootForSession resolves the delegation tree a session belongs to, or ""
// when it has no tree yet. A sub-agent's own row names its root; a leader
// owns the trees it started.
func (s *Service) RootForSession(ctx context.Context, sessionID string) string {
	if s == nil || s.Repo == nil || sessionID == "" {
		return ""
	}
	if row, err := s.Repo.FindByChildSession(ctx, sessionID); err == nil && row != nil {
		return row.RootID
	}
	rows, err := s.Repo.ListByParent(ctx, sessionID)
	if err != nil {
		return ""
	}
	for _, r := range rows {
		if r.RootID != "" {
			return r.RootID
		}
	}
	return ""
}
