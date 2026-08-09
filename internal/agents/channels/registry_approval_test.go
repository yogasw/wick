package channels

import (
	"context"
	"testing"
)

// answeringChannel stands in for Slack/Telegram: it can answer approvals
// for the sessions it is currently driving, and no others.
type answeringChannel struct{ owns map[string]bool }

func (c *answeringChannel) Name() string                     { return "answering" }
func (c *answeringChannel) Start(context.Context) error      { return nil }
func (c *answeringChannel) Stop()                            {}
func (c *answeringChannel) IsConfigured() bool               { return true }
func (c *answeringChannel) CanAnswerApproval(s string) bool  { return c.owns[s] }

// silentChannel is a channel that never renders an answerable approval —
// REST, which auto-blocks. It must not implement ApprovalResponder.
type silentChannel struct{}

func (c *silentChannel) Name() string                { return "silent" }
func (c *silentChannel) Start(context.Context) error { return nil }
func (c *silentChannel) Stop()                       {}
func (c *silentChannel) IsConfigured() bool          { return true }

// A session driven from Slack has a human able to press the buttons even
// with no browser tab open. If the registry reported otherwise, the gate
// would judge the session unattended and revoke the prompt underneath them.
func TestRegistry_CanAnswerApproval(t *testing.T) {
	reg := NewRegistry()
	reg.Add(&silentChannel{}, nil)
	reg.Add(&answeringChannel{owns: map[string]bool{"S1": true}}, nil)

	if !reg.CanAnswerApproval("S1") {
		t.Error("a channel driving S1 should be able to answer for it")
	}
	if reg.CanAnswerApproval("S2") {
		t.Error("no channel drives S2 — nobody can answer")
	}
}

func TestRegistry_CanAnswerApproval_NoResponders(t *testing.T) {
	reg := NewRegistry()
	reg.Add(&silentChannel{}, nil)

	// A channel that cannot render an answerable approval must not keep a
	// prompt alive — that is what makes headless runs block promptly.
	if reg.CanAnswerApproval("S1") {
		t.Error("a non-responder channel must not count as able to answer")
	}
}

func TestRegistry_CanAnswerApproval_Empty(t *testing.T) {
	if NewRegistry().CanAnswerApproval("S1") {
		t.Error("an empty registry cannot answer anything")
	}
}
