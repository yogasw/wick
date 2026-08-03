package delegation

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/entity"
)

func queued(root, from, to, body, kind string) *entity.AgentMessage {
	return &entity.AgentMessage{
		RootID: root, FromHandle: from, ToHandle: to,
		Body: body, Kind: kind, Status: entity.MessageQueued,
		CreatedAt: time.Now(),
	}
}

type fakeWaker struct {
	mu   sync.Mutex
	woke []string
	err  error
}

func (f *fakeWaker) WakeChild(_ context.Context, childSessionID, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.woke = append(f.woke, childSessionID)
	return f.err
}

// messages copies the steerer's captured turns. Defined here rather than
// on the struct in phases_test.go so the mailbox tests read the slice
// without reaching into another test file's internals.
func messagesOf(s *recordingSteerer) []string {
	return append([]string(nil), s.msgs...)
}

func seedTree(t *testing.T, r *Repo) {
	t.Helper()
	rows := []entity.AgentDelegation{
		{ID: "root1", RootID: "root1", Handle: entity.LeaderHandle, Status: entity.DelegationRunning,
			ChildSessionID: "sess-main", ProfileKey: "leader"},
		{ID: "d2", RootID: "root1", Handle: "reviewer", Status: entity.DelegationRunning,
			ChildSessionID: "sess-rev", ProfileKey: "code-reviewer"},
	}
	for i := range rows {
		if err := r.db.Create(&rows[i]).Error; err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
}

func mailboxService(t *testing.T) (*Service, *Repo, *recordingSteerer) {
	t.Helper()
	r := testRepo(t)
	seedTree(t, r)
	st := &recordingSteerer{}
	return &Service{
		Repo: r, Limits: Limits{MaxHops: 10}, Steerer: st, Waker: &fakeWaker{},
	}, r, st
}

/* ── repo ─────────────────────────────────────────────────────────────── */

func TestDrainInboxIsFIFOAndBatched(t *testing.T) {
	r := testRepo(t)
	ctx := context.Background()
	for _, b := range []string{"one", "two", "three"} {
		if err := r.EnqueueMessage(ctx, queued("root1", "main", "reviewer", b, entity.MessageTell)); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
		time.Sleep(2 * time.Millisecond) // distinct created_at ordering
	}
	got, err := r.DrainInbox(ctx, "root1", "reviewer", 2)
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("batch cap ignored: got %d messages, want 2", len(got))
	}
	if got[0].Body != "one" || got[1].Body != "two" {
		t.Fatalf("not FIFO: %q, %q", got[0].Body, got[1].Body)
	}
	again, err := r.DrainInbox(ctx, "root1", "reviewer", 10)
	if err != nil {
		t.Fatalf("second drain: %v", err)
	}
	if len(again) != 1 || again[0].Body != "three" {
		t.Fatalf("redelivery: got %d messages, want only \"three\"", len(again))
	}
}

func TestCountQueuedIgnoresDelivered(t *testing.T) {
	r := testRepo(t)
	ctx := context.Background()
	for range 3 {
		if err := r.EnqueueMessage(ctx, queued("root2", "main", "worker", "x", entity.MessageTell)); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
	}
	if _, err := r.DrainInbox(ctx, "root2", "worker", 2); err != nil {
		t.Fatalf("drain: %v", err)
	}
	n, err := r.CountQueued(ctx, "root2", "worker")
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("CountQueued = %d, want 1", n)
	}
}

/* ── send ─────────────────────────────────────────────────────────────── */

func TestSendTellDoesNotBlockAndCountsAHop(t *testing.T) {
	svc, r, _ := mailboxService(t)

	res, err := svc.SendMessage(context.Background(), SendInput{
		RootID: "root1", FromHandle: entity.LeaderHandle, ToHandle: "reviewer",
		Body: "how is it going?", Kind: entity.MessageTell,
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if res.Status != "sent" {
		t.Fatalf("tell must not wait, status = %q", res.Status)
	}
	var root entity.AgentDelegation
	if err := r.db.First(&root, "id = ?", "root1").Error; err != nil {
		t.Fatalf("reload root: %v", err)
	}
	if root.HopCount != 1 {
		t.Fatalf("hop not counted: %d", root.HopCount)
	}
}

func TestSendToUnknownHandleIsRefused(t *testing.T) {
	svc, _, _ := mailboxService(t)
	_, err := svc.SendMessage(context.Background(), SendInput{
		RootID: "root1", FromHandle: entity.LeaderHandle, ToHandle: "ghost",
		Body: "hello", Kind: entity.MessageTell,
	})
	if err == nil {
		t.Fatal("unknown handle must be refused")
	}
}

// Addressing must not cross trees: a handle borrowed from someone else's
// conversation would let a leader reach a session it does not own.
func TestSendCannotCrossTrees(t *testing.T) {
	svc, r, _ := mailboxService(t)
	other := entity.AgentDelegation{
		ID: "root9", RootID: "root9", Handle: "reviewer",
		Status: entity.DelegationRunning, ChildSessionID: "sess-other", ProfileKey: "code-reviewer",
	}
	if err := r.db.Create(&other).Error; err != nil {
		t.Fatalf("seed other tree: %v", err)
	}
	res, err := svc.SendMessage(context.Background(), SendInput{
		RootID: "root1", FromHandle: entity.LeaderHandle, ToHandle: "reviewer",
		Body: "hi", Kind: entity.MessageTell,
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	var got entity.AgentMessage
	if err := r.db.First(&got, "id = ?", res.MessageID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.RootID != "root1" {
		t.Fatalf("message escaped its tree: root_id = %q", got.RootID)
	}
}

func TestSendRefusedWhenInboxFull(t *testing.T) {
	svc, _, _ := mailboxService(t)
	svc.InboxCap = 2
	svc.Limits = Limits{MaxHops: 100}
	ctx := context.Background()
	for i := range 2 {
		if _, err := svc.SendMessage(ctx, SendInput{
			RootID: "root1", FromHandle: entity.LeaderHandle, ToHandle: "reviewer",
			Body: "x", Kind: entity.MessageTell,
		}); err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
	}
	if _, err := svc.SendMessage(ctx, SendInput{
		RootID: "root1", FromHandle: entity.LeaderHandle, ToHandle: "reviewer",
		Body: "one too many", Kind: entity.MessageTell,
	}); err == nil {
		t.Fatal("inbox cap must refuse the third message")
	}
}

func TestSendRefusedWhenHopsExhausted(t *testing.T) {
	svc, _, _ := mailboxService(t)
	svc.Limits = Limits{MaxHops: 1}
	ctx := context.Background()
	if _, err := svc.SendMessage(ctx, SendInput{
		RootID: "root1", FromHandle: entity.LeaderHandle, ToHandle: "reviewer",
		Body: "first", Kind: entity.MessageTell,
	}); err != nil {
		t.Fatalf("first send: %v", err)
	}
	_, err := svc.SendMessage(ctx, SendInput{
		RootID: "root1", FromHandle: entity.LeaderHandle, ToHandle: "reviewer",
		Body: "second", Kind: entity.MessageTell,
	})
	if err == nil {
		t.Fatal("second message must be refused at the hop cap")
	}
	if !strings.Contains(err.Error(), "report back to the user") {
		t.Fatalf("refusal gives no next step: %v", err)
	}
}

func TestSendWarnsWhenTheTargetLostItsContext(t *testing.T) {
	svc, _, _ := mailboxService(t)
	svc.Waker = &fakeWaker{err: ErrContextLost}

	res, err := svc.SendMessage(context.Background(), SendInput{
		RootID: "root1", FromHandle: entity.LeaderHandle, ToHandle: "reviewer",
		Body: "carry on", Kind: entity.MessageTell,
	})
	if err != nil {
		t.Fatalf("a lost context must not fail the send: %v", err)
	}
	if !strings.Contains(res.Note, "lost the context") {
		t.Fatalf("sender not warned about the amnesia: %q", res.Note)
	}
}

/* ── ask ──────────────────────────────────────────────────────────────── */

func TestAskReturnsTheReply(t *testing.T) {
	svc, r, _ := mailboxService(t)
	svc.AskTimeout = 3 * time.Second
	ctx := context.Background()

	done := make(chan *SendResult, 1)
	go func() {
		res, err := svc.SendMessage(ctx, SendInput{
			RootID: "root1", FromHandle: entity.LeaderHandle, ToHandle: "reviewer",
			Body: "ready?", Kind: entity.MessageAsk,
		})
		if err != nil {
			t.Errorf("ask: %v", err)
			done <- nil
			return
		}
		done <- res
	}()

	var ask entity.AgentMessage
	for range 200 {
		if err := r.db.Where("kind = ?", entity.MessageAsk).First(&ask).Error; err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if ask.ID == "" {
		t.Fatal("ask row never appeared")
	}
	if err := svc.Reply(ctx, ask.ID, "reviewer", "yes, ready"); err != nil {
		t.Fatalf("reply: %v", err)
	}

	select {
	case res := <-done:
		if res == nil || res.Reply != "yes, ready" {
			t.Fatalf("ask did not receive the reply: %+v", res)
		}
		if res.Status != "answered" {
			t.Fatalf("status = %q, want answered", res.Status)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ask never returned")
	}
}

func TestAskTimesOutButKeepsTheQuestion(t *testing.T) {
	svc, _, _ := mailboxService(t)
	svc.AskTimeout = 150 * time.Millisecond

	res, err := svc.SendMessage(context.Background(), SendInput{
		RootID: "root1", FromHandle: entity.LeaderHandle, ToHandle: "reviewer",
		Body: "still there?", Kind: entity.MessageAsk,
	})
	if err != nil {
		t.Fatalf("ask: %v", err)
	}
	if res.Status != "timeout" {
		t.Fatalf("status = %q, want timeout", res.Status)
	}
	n, err := svc.Repo.CountQueued(context.Background(), "root1", "reviewer")
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("timeout dropped the question: %d queued", n)
	}
}

// A recipient that forgets to call reply must not hang the sender until
// the timeout — that is the commonest and most expensive failure here.
func TestUnansweredAskIsClosedByTheFinalTurnText(t *testing.T) {
	svc, r, _ := mailboxService(t)
	ctx := context.Background()

	// Enqueued directly: SendMessage would block on the ask, and what is
	// under test is the recipient side, not the wait.
	if err := r.EnqueueMessage(ctx, queued("root1", entity.LeaderHandle, "reviewer",
		"what did you find?", entity.MessageAsk)); err != nil {
		t.Fatalf("enqueue ask: %v", err)
	}

	// Deliver it, then close the turn without an explicit reply.
	if _, err := svc.DeliverInbox(ctx, "root1", "reviewer"); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if err := svc.CloseUnansweredAsk(ctx, "root1", "reviewer", "found a nil deref in auth.go"); err != nil {
		t.Fatalf("close: %v", err)
	}

	var reply entity.AgentMessage
	if err := svc.Repo.db.Where("kind = ?", entity.MessageReply).First(&reply).Error; err != nil {
		t.Fatalf("no auto-reply written: %v", err)
	}
	if !reply.AutoReply {
		t.Fatal("auto-reply must be marked, or the asker cannot tell it from a real answer")
	}
	if reply.Body != "found a nil deref in auth.go" {
		t.Fatalf("auto-reply body = %q", reply.Body)
	}
}

/* ── delivery ─────────────────────────────────────────────────────────── */

func TestDeliverInboxSendsOneBatchedTurn(t *testing.T) {
	svc, _, st := mailboxService(t)
	svc.Limits = Limits{MaxHops: 100}
	ctx := context.Background()

	for _, b := range []string{"first", "second"} {
		if _, err := svc.SendMessage(ctx, SendInput{
			RootID: "root1", FromHandle: entity.LeaderHandle, ToHandle: "reviewer",
			Body: b, Kind: entity.MessageTell,
		}); err != nil {
			t.Fatalf("send: %v", err)
		}
	}
	if _, err := svc.DeliverInbox(ctx, "root1", "reviewer"); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	sent := messagesOf(st)
	if len(sent) != 1 {
		t.Fatalf("want ONE batched turn, got %d", len(sent))
	}
	if !strings.Contains(sent[0], "first") || !strings.Contains(sent[0], "second") {
		t.Fatalf("batch lost a message: %q", sent[0])
	}
	if !strings.Contains(sent[0], "hops left") {
		t.Fatalf("delivery carries no budget footer: %q", sent[0])
	}
}

func TestDeliverInboxOnEmptyInboxSendsNothing(t *testing.T) {
	svc, _, st := mailboxService(t)
	if _, err := svc.DeliverInbox(context.Background(), "root1", "reviewer"); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if len(messagesOf(st)) != 0 {
		t.Fatalf("woke an agent for nothing: %v", messagesOf(st))
	}
}

func TestResetHopsClearsTheCounter(t *testing.T) {
	svc, r, _ := mailboxService(t)
	svc.Limits = Limits{MaxHops: 100}
	ctx := context.Background()
	for range 3 {
		if _, err := svc.SendMessage(ctx, SendInput{
			RootID: "root1", FromHandle: entity.LeaderHandle, ToHandle: "reviewer",
			Body: "x", Kind: entity.MessageTell,
		}); err != nil {
			t.Fatalf("send: %v", err)
		}
	}
	if err := r.ResetHops(ctx, "root1"); err != nil {
		t.Fatalf("reset: %v", err)
	}
	var root entity.AgentDelegation
	if err := r.db.First(&root, "id = ?", "root1").Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if root.HopCount != 0 {
		t.Fatalf("hop count after reset = %d, want 0", root.HopCount)
	}
}
