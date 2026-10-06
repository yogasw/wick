package agents

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/gate"
	"github.com/yogasw/wick/internal/entity"
)

// A click on an approval_request card resolves the gate prompt exactly as
// the modal would — and only from the session the prompt belongs to.
func TestSessionApprovalDecision(t *testing.T) {
	withSessionWorld(t, []seededSession{
		{id: "s1", userID: "bob", participants: []string{"bob"}},
		{id: "s2", userID: "bob", participants: []string{"bob"}},
	})
	t.Setenv("HOME", t.TempDir())
	mgr, err := gate.NewApprovalManager(gate.ApprovalManagerOptions{
		AppName:    "wickcardtest",
		Timeout:    5 * time.Second,
		RouteByCWD: func(string) (string, bool) { return "s1", true },
		OnRequest:  RecordApprovalRequest,
		OnResolved: RecordApprovalResolved,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Stop()
	prev := globalApprovals
	globalApprovals = mgr
	defer func() { globalApprovals = prev }()

	respCh := make(chan gate.ApprovalResponse, 1)
	go func() {
		respCh <- mgr.RequestApproval(context.Background(), gate.ApprovalRequest{
			ID: "ap-1", SessionID: "s1", Tool: "agents.set_access", Cmd: "grant Notion", MatchKey: "k1",
		})
	}()
	for i := 0; i < 500; i++ {
		if _, ok := approvalCards.lookup("s1", "ap-1"); ok {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}

	call := func(u *entity.User, sid, body string) int {
		w, c := postCtx(t, u, "/api/sessions/"+sid+"/approvals/ap-1", body, map[string]string{"id": sid, "approvalID": "ap-1"})
		sessionApprovalDecision(c)
		return w.Code
	}
	bob := &entity.User{ID: "bob", Role: entity.RoleUser}
	dave := &entity.User{ID: "dave", Role: entity.RoleUser}
	if code := call(dave, "s1", `{"decision":"accept"}`); code != http.StatusNotFound {
		t.Fatalf("stranger: %d", code)
	}
	if code := call(bob, "s2", `{"decision":"accept"}`); code != http.StatusGone {
		t.Fatalf("prompt from another session: %d", code)
	}
	if code := call(bob, "s1", `{"decision":"approve_always"}`); code != http.StatusBadRequest {
		t.Fatalf("unknown decision: %d", code)
	}
	if code := call(bob, "s1", `{"decision":"accept_for_session"}`); code != http.StatusOK {
		t.Fatalf("owner accept: %d", code)
	}
	select {
	case resp := <-respCh:
		if resp.Decision != gate.DecisionApproveSession {
			t.Fatalf("decision = %q", resp.Decision)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("gate never released")
	}
	if code := call(bob, "s1", `{"decision":"accept"}`); code != http.StatusGone {
		t.Fatalf("second click: %d", code)
	}
	raw, _ := os.ReadFile(globalLayout.SessionConversation("s1"))
	body := string(raw)
	if strings.Count(body, `"kind":"approval_request"`) != 2 || !strings.Contains(body, `"state":"pending"`) ||
		!strings.Contains(body, `"state":"approve_session"`) || !strings.Contains(body, `"approval_id":"ap-1"`) {
		t.Fatalf("thread = %s", body)
	}
}

func TestGateDecision(t *testing.T) {
	for in, want := range map[string]string{
		ApprovalAccept: gate.DecisionApproveOnce, ApprovalAcceptForSession: gate.DecisionApproveSession, ApprovalDecline: gate.DecisionBlock,
	} {
		if got, ok := gateDecision(in); !ok || got != want {
			t.Errorf("%s → %s, want %s", in, got, want)
		}
	}
	if _, ok := gateDecision("approve_always"); ok {
		t.Error("a card cannot grant always-allow")
	}
}
