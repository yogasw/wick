package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/pool"
	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/storage"
	agentstore "github.com/yogasw/wick/internal/agents/store"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/pkg/tool"
)

// replyWorld is spawnGateWorld's "s1" plus a second session "s2" of the
// same owner, each with a short history.
func replyWorld(t *testing.T) {
	t.Helper()
	spawnGateWorld(t, gateUser.ID, true)
	if _, err := session.Create(context.Background(), globalLayout, session.CreateOptions{
		ID: "s2", Origin: session.OriginUI, UserID: gateUser.ID,
	}); err != nil {
		t.Fatal(err)
	}
	if err := globalMgr.Registry().Reload(); err != nil {
		t.Fatal(err)
	}
	ts := time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)
	seedTurns(t, "s1",
		agentstore.ConversationTurn{Timestamp: ts, Role: "user", Text: "is the deploy\ndone?", Sender: &agentstore.Sender{ID: "u", Name: "Rina", Channel: "ui"}},
		agentstore.ConversationTurn{Timestamp: ts, TurnID: "111", Role: "assistant", Agent: "main", Text: "Yes. " + strings.Repeat("long ", 200)},
	)
	seedTurns(t, "s2",
		agentstore.ConversationTurn{Timestamp: ts, TurnID: "222", Role: "assistant", Agent: "main", Text: "secret from another chat"},
	)
}

func seedTurns(t *testing.T, id string, turns ...agentstore.ConversationTurn) {
	t.Helper()
	for _, turn := range turns {
		if err := storage.AppendJSONL(globalLayout.SessionConversation(id), "wick-conv-v1", id, turn); err != nil {
			t.Fatal(err)
		}
	}
}

// reply_to must name a turn of the same session; anything else is a 400
// that changes nothing, and a stranger gets the usual 404.
func TestSendReplyToValidation(t *testing.T) {
	replyWorld(t)
	path := map[string]string{"id": "s1"}
	for _, tc := range []struct {
		name string
		u    *entity.User
		body string
		want int
	}{
		{"missing id", gateUser, `{"text":"hi","reply_to":"nope"}`, http.StatusBadRequest},
		{"another session's turn", gateUser, `{"text":"hi","reply_to":"222"}`, http.StatusBadRequest},
		{"oversized id", gateUser, `{"text":"hi","reply_to":"` + strings.Repeat("9", 200) + `"}`, http.StatusBadRequest},
		{"stranger", &entity.User{ID: "dave", Role: entity.RoleUser, Approved: true}, `{"text":"hi","reply_to":"111"}`, http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, c := postCtx(t, tc.u, "/", tc.body, path)
			sendMessage(c)
			if w.Code != tc.want {
				t.Fatalf("code = %d, want %d (%s)", w.Code, tc.want, w.Body.String())
			}
			if tc.want == http.StatusBadRequest && strings.Contains(w.Body.String(), "secret") {
				t.Fatalf("refusal leaks the other chat: %s", w.Body.String())
			}
		})
	}
}

// failSpawner refuses every spawn: a send still persists its turn first.
type failSpawner struct{}

func (failSpawner) Spawn(context.Context, provider.SpawnOptions) (provider.Process, error) {
	return nil, errors.New("no spawn in tests")
}

// replyAgentWorld is replyWorld with an agent in "s1", so a send gets as
// far as storing the user turn.
func replyAgentWorld(t *testing.T) {
	t.Helper()
	replyWorld(t)
	if err := session.AddAgent(globalLayout, "s1", "main", "claude/mine"); err != nil {
		t.Fatal(err)
	}
	if err := globalMgr.RefreshSession("s1"); err != nil {
		t.Fatal(err)
	}
	globalPool = pool.New(pool.PoolConfig{Layout: globalLayout, MaxConcurrent: 2, Factory: &pool.ClaudeFactory{Layout: globalLayout, Spawner: failSpawner{}}})
	t.Cleanup(globalPool.Stop)
}

func userTurn(t *testing.T, id, text string) agentstore.ConversationTurn {
	t.Helper()
	turns, err := loadConversation(globalLayout, id)
	if err != nil {
		t.Fatal(err)
	}
	for _, turn := range turns {
		if turn.Role == "user" && turn.Text == text {
			return turn
		}
	}
	t.Fatalf("no user turn %q in %s", text, id)
	return agentstore.ConversationTurn{}
}

// A valid reply is stored on the person's turn with the quote the server
// built (a client-sent excerpt is ignored), under the id /send returns.
func TestSendReplyToPersistsServerQuote(t *testing.T) {
	replyAgentWorld(t)
	w, c := postCtx(t, gateUser, "/", `{"text":"roll it back","reply_to":"111","excerpt":"FAKE","author":"FAKE"}`, map[string]string{"id": "s1"})
	sendMessage(c)
	got := userTurn(t, "s1", "roll it back")
	if got.ReplyTo == nil || got.ReplyTo.TurnID != "111" || got.ReplyTo.Author != "main" || !strings.HasPrefix(got.ReplyTo.Excerpt, "Yes. long") {
		t.Fatalf("reply_to = %+v (status %d %s)", got.ReplyTo, w.Code, w.Body.String())
	}
	if got.TurnID == "" || strings.HasPrefix(got.TurnID, "turn-") {
		t.Fatalf("user turn id = %q, want a real id", got.TurnID)
	}
	if w.Code == http.StatusOK {
		var res map[string]string
		_ = json.Unmarshal(w.Body.Bytes(), &res)
		if res["turn_id"] != got.TurnID {
			t.Fatalf("/send turn_id = %q, stored %q", res["turn_id"], got.TurnID)
		}
	}
	// A bare slash command is sent without the reply.
	_, c = postCtx(t, gateUser, "/", `{"text":"/compact","reply_to":"111"}`, map[string]string{"id": "s1"})
	sendMessage(c)
	for _, turn := range mustTurns(t, "s1") {
		if turn.Text == "/compact" && turn.ReplyTo != nil {
			t.Fatalf("slash command stored a reply: %+v", turn.ReplyTo)
		}
	}
}

func mustTurns(t *testing.T, id string) []agentstore.ConversationTurn {
	t.Helper()
	turns, err := loadConversation(globalLayout, id)
	if err != nil {
		t.Fatal(err)
	}
	return turns
}

// Multipart: a refused reply_to answers 400 before any upload is written.
func TestSendReplyToMultipartRefusedBeforeUpload(t *testing.T) {
	replyWorld(t)
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("text", "see file")
	_ = mw.WriteField("reply_to", "222")
	fw, _ := mw.CreateFormFile("files", "a.txt")
	_, _ = fw.Write([]byte("hello"))
	_ = mw.Close()
	r := httptest.NewRequest(http.MethodPost, "/", &buf)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	r = r.WithContext(login.WithUser(r.Context(), gateUser, nil))
	r.SetPathValue("id", "s1")
	w := httptest.NewRecorder()
	sendMessage(tool.NewCtx(w, r, nil, tool.Tool{Key: "agents", Path: "/tools/agents"}, nil, nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400 (%s)", w.Code, w.Body.String())
	}
	if entries, _ := os.ReadDir(filepath.Join(globalLayout.SessionDir("s1"), uploadsDirName)); len(entries) > 0 {
		t.Fatalf("refused send still wrote %d upload(s)", len(entries))
	}
}

// A group chat takes no reply.
func TestSendReplyToGroupChatRefused(t *testing.T) {
	replyWorld(t)
	sess, err := session.Load(globalLayout, "s1")
	if err != nil {
		t.Fatal(err)
	}
	sess.Meta.AgentGroup = &session.AgentGroup{}
	if err := session.SaveMeta(globalLayout, "s1", sess.Meta); err != nil {
		t.Fatal(err)
	}
	if err := globalMgr.RefreshSession("s1"); err != nil {
		t.Fatal(err)
	}
	w, c := postCtx(t, gateUser, "/", `{"text":"hi","reply_to":"111"}`, map[string]string{"id": "s1"})
	sendMessage(c)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "group") {
		t.Fatalf("code = %d (%s), want 400 group", w.Code, w.Body.String())
	}
}

// A read-only viewer (a shared agent's owner chat) is refused by the
// session middleware before reply_to is even looked at.
func TestSendReplyToReadOnlyViewerForbidden(t *testing.T) {
	withTeamWorld(t)
	owner, bob := &entity.User{ID: "u1"}, &entity.User{ID: "bob"}
	seedTeamProject(t, "p1", owner.ID)
	p := seedTeamAgent(t, owner.ID, "helper", "p1")
	ownChat := openChat(t, owner, p.ID, false)
	if code := shareWith(t, owner, p.ID, bob.ID); code != http.StatusOK {
		t.Fatalf("share: %d", code)
	}
	w, c := teamReq(t, bob, http.MethodPost, "/", map[string]any{"text": "hi", "reply_to": "turn-0"}, map[string]string{"id": ownChat})
	sessionAccessMW(sharedChatReadOnlyMW(sendMessage))(c)
	if w.Code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403 (%s)", w.Code, w.Body.String())
	}
}

// The quote is built from the stored turn: author from server fields, the
// excerpt one line and bounded. Older turns are found by the position id
// the conversation API gives them.
func TestResolveReplyToBuildsExcerptServerSide(t *testing.T) {
	replyWorld(t)
	r, err := resolveReplyTo("s1", "turn-0", nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.TurnID != "turn-0" || r.Role != "user" || r.Author != "Rina" || r.Excerpt != "is the deploy done?" {
		t.Fatalf("reply = %+v", r)
	}
	r, err = resolveReplyTo("s1", "111", &agentstore.Speaker{AgentID: "a1", Handle: "ops"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Author != "@ops" || len([]rune(r.Excerpt)) > agentstore.ReplyExcerptMax || !strings.HasPrefix(r.Excerpt, "Yes. long") {
		t.Fatalf("reply = %+v", r)
	}
	if _, err := resolveReplyTo("s1", "222", nil); err == nil {
		t.Fatal("resolved a turn of another session")
	}
}
