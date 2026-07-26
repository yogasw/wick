package wick

import (
	"testing"

	"github.com/yogasw/wick/internal/agents/provider"
)

// TestModelCallSnapshotAndCancel covers the live-phase registry that backs the
// in-flight model-call features (viewer/curl, retry badge, per-call cancel).
func TestModelCallSnapshotAndCancel(t *testing.T) {
	const sid = "sess-live-1"
	markModelCallDone(sid) // ensure clean

	if st := ModelCallSnapshot(sid); st.InFlight {
		t.Fatal("expected idle before start")
	}

	cancelled := false
	cancel := func() { cancelled = true }
	req := &modelCallRequest{
		Kind:    "generate",
		Model:   "gpt-x",
		ModelID: "m1",
		System:  "sys",
		Tools:   []string{"a", "b"},
		Request: []interactionMsg{{Role: "user", Text: "hi"}},
	}
	markModelCallStart(sid, 1, req, cancel)

	st := ModelCallSnapshot(sid)
	if !st.InFlight {
		t.Fatal("expected in-flight after start")
	}
	if st.Attempt != 1 || st.Model != "gpt-x" || st.ModelID != "m1" {
		t.Fatalf("snapshot fields wrong: %+v", st)
	}
	if len(st.Tools) != 2 || len(st.Messages) != 1 || st.Messages[0].Text != "hi" {
		t.Fatalf("snapshot request not carried: %+v", st)
	}

	// A retry bumps the attempt + reason and resets the clock.
	markModelCallRetry(sid, 2, "rate limited", req, cancel)
	st = ModelCallSnapshot(sid)
	if st.Attempt != 2 || st.RetryReason != "rate limited" {
		t.Fatalf("retry not reflected: attempt=%d reason=%q", st.Attempt, st.RetryReason)
	}

	// Cancel fires the stored cancel func.
	if !CancelModelCall(sid) {
		t.Fatal("CancelModelCall returned false for an in-flight call")
	}
	if !cancelled {
		t.Fatal("cancel func was not invoked")
	}

	// Done clears it; cancel on a cleared session is a no-op.
	markModelCallDone(sid)
	if ModelCallSnapshot(sid).InFlight {
		t.Fatal("expected idle after done")
	}
	if CancelModelCall(sid) {
		t.Fatal("CancelModelCall should be false when nothing is in flight")
	}
}

// TestBuildRequestFromLive proves the live snapshot reconstructs a curl the same
// way a finished record does (reuses BuildRequest under the hood).
func TestBuildRequestFromLive(t *testing.T) {
	st := ModelCallState{
		InFlight: true,
		Kind:     "generate",
		Model:    "gpt-x",
		ModelID:  "m1",
		System:   "you are helpful",
		Messages: []interactionMsg{{Role: "user", Text: "hello"}},
	}
	m := provider.WickModel{ID: "m1", Kind: "openai", Model: "gpt-4o", APIKey: "x"}
	req, err := BuildRequestFromLive(st, m)
	if err != nil {
		t.Fatalf("BuildRequestFromLive: %v", err)
	}
	if req.URL == "" || req.Method == "" {
		t.Fatalf("reconstructed request incomplete: %+v", req)
	}
	if line := RenderSingleLine(req, "$KEY"); line == "" {
		t.Fatal("empty curl render")
	}
}
