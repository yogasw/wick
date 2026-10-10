package teamlink

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

// askingHub is a hub whose teammate asks back until it is told "prod".
func askingHub(t *testing.T) (*Hub, *fakeNotify, *Result) {
	t.Helper()
	h, _, note := newTestHub(func(_ Peer, text string) string {
		if strings.HasSuffix(text, "prod") {
			return "deployed to prod"
		}
		return InputRequiredToken + " which environment?"
	})
	q, err := h.Send(context.Background(), SendInput{CallerSession: "sess-cap", CallerAgentID: "a-cap", To: "anton", Text: "deploy it"})
	if err != nil || q.State != "input_required" {
		t.Fatalf("question = %+v, %v", q, err)
	}
	return h, note, q
}

// waitTaskState waits until the session's task reads state.
func waitTaskState(t *testing.T, h *Hub, session, id, state string) TaskView {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		for _, v := range h.SentFrom(session) {
			if v.TaskID == id && v.State == state {
				return v
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("task %s never reached %s: %+v", id, state, h.SentFrom(session))
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A question no turn of the sending chat handles needs the user; while
// that chat runs a turn, its agent is answering.
func TestSentFromNeedsYou(t *testing.T) {
	h, _, q := askingHub(t)
	busy := true
	h.CallerBusy = func(string) bool { return busy }
	if v := waitTaskState(t, h, "sess-cap", q.TaskID, "input_required"); v.NeedsYou || v.Reply != "which environment?" {
		t.Fatalf("busy caller: %+v", v)
	}
	busy = false
	if v := waitTaskState(t, h, "sess-cap", q.TaskID, "input_required"); !v.NeedsYou {
		t.Fatalf("idle caller: %+v", v)
	}
}

// The user's answer goes to the same task, the agent is told, and a
// second answer — the user's or the agent's — is refused.
func TestAnswerFromUserFirstAnswerWins(t *testing.T) {
	h, note, q := askingHub(t)
	ctx := context.Background()
	res, err := h.AnswerFromUser(ctx, "sess-cap", q.TaskID, "prod")
	if err != nil || res.TaskID != q.TaskID {
		t.Fatalf("answer = %+v, %v", res, err)
	}
	if _, err := h.AnswerFromUser(ctx, "sess-cap", q.TaskID, "staging"); !errors.Is(err, ErrAlreadyAnswered) && !errors.Is(err, ErrTaskNotWaiting) {
		t.Fatalf("second user answer: %v", err)
	}
	waitTaskState(t, h, "sess-cap", q.TaskID, "completed")
	got := waitDelivered(t, note, 1)
	joined := strings.Join(got, "\n---\n")
	if !strings.Contains(joined, "The user answered Anton (@anton)'s question [task "+q.TaskID+"]") || !strings.Contains(joined, "deployed to prod") {
		t.Fatalf("delivered = %v", got)
	}
	if _, err := h.Send(ctx, SendInput{CallerSession: "sess-cap", CallerAgentID: "a-cap", TaskID: q.TaskID, Text: "again"}); !errors.Is(err, ErrTaskNotWaiting) {
		t.Fatalf("agent answer after the user's: %v", err)
	}
}

// While the user's answer holds the claim, the agent's answer is refused.
func TestAnswerClaimRefusesAgent(t *testing.T) {
	h, _, q := askingHub(t)
	h.mu.Lock()
	h.tasks[a2a.TaskID(q.TaskID)].userAnswer = true
	h.mu.Unlock()
	if _, err := h.Send(context.Background(), SendInput{CallerSession: "sess-cap", CallerAgentID: "a-cap", TaskID: q.TaskID, Text: "prod"}); !errors.Is(err, ErrAlreadyAnswered) {
		t.Fatalf("agent answer under the user's claim: %v", err)
	}
	if _, err := h.AnswerFromUser(context.Background(), "sess-cap", q.TaskID, "prod"); !errors.Is(err, ErrAlreadyAnswered) {
		t.Fatalf("second user answer under the claim: %v", err)
	}
	for _, v := range h.SentFrom("sess-cap") {
		if v.TaskID == q.TaskID && v.NeedsYou {
			t.Fatalf("claimed task still needs the user: %+v", v)
		}
	}
}

// Only the conversation that sent a task may answer or cancel it.
func TestUserTaskActionsScopedToSession(t *testing.T) {
	h, _, q := askingHub(t)
	ctx := context.Background()
	if _, err := h.AnswerFromUser(ctx, "sess-other", q.TaskID, "prod"); !errors.Is(err, ErrUnknownTask) {
		t.Fatalf("answer from another session: %v", err)
	}
	if _, err := h.CancelFromUser(ctx, "sess-other", q.TaskID); !errors.Is(err, ErrUnknownTask) {
		t.Fatalf("cancel from another session: %v", err)
	}
	if _, err := h.AnswerFromUser(ctx, "sess-cap", q.TaskID, "  "); err == nil {
		t.Fatal("empty answer accepted")
	}
}

// The user cancels a waiting task; a settled one cannot be canceled.
func TestCancelFromUser(t *testing.T) {
	h, _, q := askingHub(t)
	ctx := context.Background()
	res, err := h.CancelFromUser(ctx, "sess-cap", q.TaskID)
	if err != nil || res.State != "canceled" {
		t.Fatalf("cancel = %+v, %v", res, err)
	}
	if _, err := h.CancelFromUser(ctx, "sess-cap", q.TaskID); !errors.Is(err, ErrTaskSettled) {
		t.Fatalf("second cancel: %v", err)
	}
	if _, err := h.AnswerFromUser(ctx, "sess-cap", q.TaskID, "prod"); !errors.Is(err, ErrTaskNotWaiting) {
		t.Fatalf("answer to a canceled task: %v", err)
	}
}

// The user's answer and the agent's race for the same question: exactly
// one gets it and reaches the teammate, the other gets ErrAlreadyAnswered.
func TestAnswerRaceExactlyOneWins(t *testing.T) {
	for i := 0; i < 50; i++ {
		var mu sync.Mutex
		var answers []string
		hold := make(chan struct{})
		h, _, _ := newTestHub(func(_ Peer, text string) string {
			if strings.HasSuffix(text, "deploy it") {
				return InputRequiredToken + " which environment?"
			}
			mu.Lock()
			answers = append(answers, text)
			mu.Unlock()
			<-hold // the winner's turn runs until both answers came back
			return "deployed"
		})
		ctx := context.Background()
		q, err := h.Send(ctx, SendInput{CallerSession: "sess-cap", CallerAgentID: "a-cap", To: "anton", Text: "deploy it"})
		if err != nil || q.State != "input_required" {
			t.Fatalf("question = %+v, %v", q, err)
		}
		h.SetQuickWait(10 * time.Millisecond)
		start := make(chan struct{})
		errs := make([]error, 2)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, errs[0] = h.AnswerFromUser(ctx, "sess-cap", q.TaskID, "prod")
		}()
		go func() {
			defer wg.Done()
			<-start
			_, errs[1] = h.Send(ctx, SendInput{CallerSession: "sess-cap", CallerAgentID: "a-cap", TaskID: q.TaskID, Text: "staging"})
		}()
		close(start)
		wg.Wait()
		close(hold)
		won := 0
		for _, err := range errs {
			switch {
			case err == nil:
				won++
			case !errors.Is(err, ErrAlreadyAnswered):
				t.Fatalf("run %d: loser got %v, want ErrAlreadyAnswered", i, err)
			}
		}
		if won != 1 {
			t.Fatalf("run %d: %d answers won (%v)", i, won, errs)
		}
		waitTaskState(t, h, "sess-cap", q.TaskID, "completed")
		mu.Lock()
		got := len(answers)
		mu.Unlock()
		if got != 1 {
			t.Fatalf("run %d: teammate got %d answers: %q", i, got, answers)
		}
	}
}

// An answer that never reaches the teammate gives the question back, and
// a cancel that lands meanwhile is kept.
func TestUnclaimKeepsConcurrentCancel(t *testing.T) {
	h, _, q := askingHub(t)
	id := a2a.TaskID(q.TaskID)
	_, _, claim, err := h.answering("a-cap", SendInput{TaskID: q.TaskID})
	if err != nil {
		t.Fatal(err)
	}
	h.unclaim(id, claim)
	if v := waitTaskState(t, h, "sess-cap", q.TaskID, "input_required"); v.Summary != "which environment?" {
		t.Fatalf("question not given back: %+v", v)
	}
	if _, _, claim, err = h.answering("a-cap", SendInput{TaskID: q.TaskID}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.CancelFromUser(context.Background(), "sess-cap", q.TaskID); err != nil {
		t.Fatal(err)
	}
	h.unclaim(id, claim)
	waitTaskState(t, h, "sess-cap", q.TaskID, "canceled")
}

// Listing reads old tasks back from disk without charging their turns
// again: a live exchange is not pushed into its turn cap by refetches.
func TestSentFromDoesNotRechargeTurns(t *testing.T) {
	h, _, _ := newTestHub(func(Peer, string) string { return "ok" })
	if err := h.Persist(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	clk := &fakeClock{t: time.Unix(1_000_000, 0)}
	h.now = clk.now
	ctx := context.Background()
	first, err := h.Send(ctx, SendInput{CallerSession: "sess-cap", CallerAgentID: "a-cap", To: "anton", Text: "one"})
	if err != nil {
		t.Fatal(err)
	}
	clk.add(TaskTTL - time.Minute)
	if _, err := h.Send(ctx, SendInput{CallerSession: "sess-cap", CallerAgentID: "a-cap", To: "anton", Text: "two", ContextID: first.ContextID}); err != nil {
		t.Fatal(err)
	}
	turns := func() int {
		h.mu.Lock()
		defer h.mu.Unlock()
		return h.contexts[first.ContextID].turns
	}
	want := turns()
	for i := 0; i < 2*MaxContextTurns; i++ {
		clk.add(pruneEvery + time.Second) // the first task ages out of memory, and is read back
		if len(h.SentFrom("sess-cap")) != 2 {
			t.Fatalf("list = %+v", h.SentFrom("sess-cap"))
		}
		if got := turns(); got != want {
			t.Fatalf("refetch %d: turns %d, want %d", i, got, want)
		}
	}
}

// A user's cancel tells the sending agent.
func TestCancelFromUserTellsAgent(t *testing.T) {
	h, note, q := askingHub(t)
	if _, err := h.CancelFromUser(context.Background(), "sess-cap", q.TaskID); err != nil {
		t.Fatal(err)
	}
	got := waitDelivered(t, note, 1)
	if !strings.Contains(strings.Join(got, "\n"), "The user canceled the task to Anton (@anton) [task "+q.TaskID+"]") {
		t.Fatalf("delivered = %v", got)
	}
}
