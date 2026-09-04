package rest

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	agentchannels "github.com/yogasw/wick/internal/agents/channels"
	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/agents/gate"
	"github.com/yogasw/wick/internal/agents/provider"
)

// ── helpers ───────────────────────────────────────────────────────────

// fakeAuth is a stub Authenticator. Returns userID on the canned token,
// errs otherwise so 401 paths can be exercised.
type fakeAuth struct {
	wantToken string
	userID    string
}

func (f *fakeAuth) Authenticate(_ context.Context, plain string) (string, error) {
	if plain != f.wantToken {
		return "", errAuth("invalid token")
	}
	return f.userID, nil
}

type errAuth string

func (e errAuth) Error() string { return string(e) }

// fakeSessions stubs SessionChecker. By default reports false so the
// inject path fires once per test.
type fakeSessions struct{ exists bool }

func (f fakeSessions) SessionExists(string) bool { return f.exists }
func (f fakeSessions) AutoReplyOn(string) bool    { return false }
func (f fakeSessions) SetAutoReply(string, bool)  {}

// captured sendFn payload — one entry per call.
type sentCall struct {
	SessionID string
	AgentName string
	Source    string
	Role      string
	Text      string
}

// newTestChannel builds a Channel wired with a fake auth + a sendFn that
// records every dispatch and simulates the agent producing a fixed reply
// when role == "user". The optional onUserSend hook fires before the
// simulated reply so tests can intercept (e.g. to test session-busy).
//
// Returns (channel, captured-pointer). captured is appended to under a
// mutex so concurrent dispatches don't race.
func newTestChannel(t *testing.T, reply string, onUserSend func(sessionID string)) (*Channel, *sync.Mutex, *[]sentCall) {
	t.Helper()
	ch := New(agentconfig.RestChannelConfig{Enabled: "true", ProjectID: "main"}, &fakeAuth{wantToken: "good", userID: "user-1"})

	var mu sync.Mutex
	var captured []sentCall

	ch.SetSessionChecker(fakeSessions{})
	ch.SetSendFunc(func(_ context.Context, sessionID, agentName, source, role, text string) error {
		mu.Lock()
		captured = append(captured, sentCall{sessionID, agentName, source, role, text})
		mu.Unlock()
		if role == "user" {
			if onUserSend != nil {
				onUserSend(sessionID)
			}
			// Fire the simulated agent reply asynchronously so dispatch's
			// select { case <-tn.done } sees the close. Real pool does
			// the same via OnAgentEvent.
			go func() {
				ch.OnAgentEvent(sessionID, event.AgentEvent{Type: event.TextDelta, Text: reply})
				ch.OnAgentEvent(sessionID, event.AgentEvent{Type: event.Done})
			}()
		}
		return nil
	})
	return ch, &mu, &captured
}

// stubModels swaps modelLoader for the duration of the test and registers
// cleanup. Pass ids as "<type>" or "<type>/<name>".
func stubModels(t *testing.T, ids ...string) {
	t.Helper()
	old := modelLoader
	t.Cleanup(func() { modelLoader = old })
	modelLoader = func() ([]provider.Instance, error) {
		out := make([]provider.Instance, 0, len(ids))
		for _, id := range ids {
			typ, name := id, id
			if slash := strings.IndexByte(id, '/'); slash >= 0 {
				typ = id[:slash]
				name = id[slash+1:]
			}
			out = append(out, provider.Instance{Type: provider.Type(typ), Name: name})
		}
		return out, nil
	}
}

func postJSON(t *testing.T, h http.Handler, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", path, bytes.NewReader(buf))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// ── modelLoader stub plumbing ─────────────────────────────────────────

func TestIsModelAllowed(t *testing.T) {
	stubModels(t, "claude", "codex/work")
	tests := []struct {
		in   string
		want bool
	}{
		{"", true},          // empty → server picks
		{"claude", true},    // bare type
		{"codex/work", true},// named instance
		{"gemini", false},   // not configured
		{"gpt-4o", false},   // openai id wick doesn't advertise
		{"claude/work", false}, // wrong combination
	}
	for _, tc := range tests {
		if got := IsModelAllowed(tc.in); got != tc.want {
			t.Errorf("IsModelAllowed(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestAvailableModelsShape(t *testing.T) {
	stubModels(t, "claude", "codex/work", "gemini")
	got := availableModels()
	if len(got) != 3 {
		t.Fatalf("want 3 models, got %d", len(got))
	}
	ids := map[string]bool{}
	for _, m := range got {
		ids[m.ID] = true
		if m.Object != "model" {
			t.Errorf("model %s: object=%q want \"model\"", m.ID, m.Object)
		}
		if m.OwnedBy == "" {
			t.Errorf("model %s: owned_by empty", m.ID)
		}
	}
	for _, want := range []string{"claude", "codex/work", "gemini"} {
		if !ids[want] {
			t.Errorf("missing model id %q in %v", want, ids)
		}
	}
}

func TestAvailableModelsSkipsDisabled(t *testing.T) {
	old := modelLoader
	t.Cleanup(func() { modelLoader = old })
	modelLoader = func() ([]provider.Instance, error) {
		return []provider.Instance{
			{Type: provider.TypeClaude, Name: "claude"},
			{Type: provider.TypeCodex, Name: "codex", Disabled: true},
		}, nil
	}
	got := availableModels()
	if len(got) != 1 || got[0].ID != "claude" {
		t.Fatalf("disabled instance leaked: %+v", got)
	}
}

// ── /v1/models handler ────────────────────────────────────────────────

func TestHandleModelsAuthRequired(t *testing.T) {
	stubModels(t, "claude")
	ch, _, _ := newTestChannel(t, "ok", nil)
	mux := http.NewServeMux()
	for path, h := range ch.HTTPHandlers() {
		mux.Handle(path, h)
	}
	req := httptest.NewRequest("GET", "/integrations/rest/api/v1/openai/models", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("no bearer → got %d want 401", rec.Code)
	}
}

func TestHandleModelsHappyPath(t *testing.T) {
	stubModels(t, "claude", "codex/work")
	ch, _, _ := newTestChannel(t, "ok", nil)
	mux := http.NewServeMux()
	for path, h := range ch.HTTPHandlers() {
		mux.Handle(path, h)
	}
	req := httptest.NewRequest("GET", "/integrations/rest/api/v1/openai/models", nil)
	req.Header.Set("Authorization", "Bearer good")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body modelsListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Object != "list" || len(body.Data) != 2 {
		t.Errorf("got %+v", body)
	}
}

// ── /v1/chat/completions auth + validation ────────────────────────────

func TestChatCompletions_MissingBearer(t *testing.T) {
	stubModels(t, "claude")
	ch, _, _ := newTestChannel(t, "ok", nil)
	rec := postJSON(t, http.HandlerFunc(ch.handleChatCompletions), "/", "", map[string]any{
		"model":    "claude",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("got %d want 401", rec.Code)
	}
}

func TestChatCompletions_InvalidBearer(t *testing.T) {
	stubModels(t, "claude")
	ch, _, _ := newTestChannel(t, "ok", nil)
	rec := postJSON(t, http.HandlerFunc(ch.handleChatCompletions), "/", "wrong", map[string]any{
		"model":    "claude",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("got %d want 401", rec.Code)
	}
}

func TestChatCompletions_Disabled(t *testing.T) {
	stubModels(t, "claude")
	// Build channel with Enabled=false; IsConfigured → false → 503.
	ch := New(agentconfig.RestChannelConfig{Enabled: "false"}, &fakeAuth{wantToken: "good", userID: "u"})
	rec := postJSON(t, http.HandlerFunc(ch.handleChatCompletions), "/", "good", map[string]any{})
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("disabled → got %d want 503", rec.Code)
	}
}

func TestChatCompletions_StreamingRejected(t *testing.T) {
	stubModels(t, "claude")
	ch, _, _ := newTestChannel(t, "ok", nil)
	rec := postJSON(t, http.HandlerFunc(ch.handleChatCompletions), "/", "good", map[string]any{
		"model":    "claude",
		"stream":   true,
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("stream=true → got %d want 400", rec.Code)
	}
}

func TestChatCompletions_EmptyMessages(t *testing.T) {
	stubModels(t, "claude")
	ch, _, _ := newTestChannel(t, "ok", nil)
	rec := postJSON(t, http.HandlerFunc(ch.handleChatCompletions), "/", "good", map[string]any{
		"model":    "claude",
		"messages": []any{},
	})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("got %d want 400", rec.Code)
	}
}

func TestChatCompletions_ModelNotFound(t *testing.T) {
	stubModels(t, "claude")
	ch, _, _ := newTestChannel(t, "ok", nil)
	rec := postJSON(t, http.HandlerFunc(ch.handleChatCompletions), "/", "good", map[string]any{
		"model":    "gpt-4o",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("got %d want 404; body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	errObj, ok := body["error"].(map[string]any)
	if !ok || errObj["code"] != "model_not_found" {
		t.Errorf("error shape wrong: %v", body)
	}
}

func TestChatCompletions_ModelEmptyAllowed(t *testing.T) {
	stubModels(t, "claude")
	ch, _, _ := newTestChannel(t, "hello", nil)
	rec := postJSON(t, http.HandlerFunc(ch.handleChatCompletions), "/", "good", map[string]any{
		// model omitted → server picks
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	if rec.Code != 200 {
		t.Fatalf("empty model → got %d body=%s", rec.Code, rec.Body.String())
	}
}

// ── chat happy path + dispatch wiring ─────────────────────────────────

func TestChatCompletions_HappyPath(t *testing.T) {
	stubModels(t, "claude")
	ch, mu, captured := newTestChannel(t, "hello world", nil)
	rec := postJSON(t, http.HandlerFunc(ch.handleChatCompletions), "/", "good", map[string]any{
		"model": "claude",
		"messages": []map[string]string{
			{"role": "user", "content": "say hi"},
		},
	})
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body chatResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Object != "chat.completion" {
		t.Errorf("object=%q", body.Object)
	}
	if len(body.Choices) != 1 || body.Choices[0].Message.Content != "hello world" {
		t.Errorf("choices wrong: %+v", body.Choices)
	}
	if body.Model != "claude" {
		t.Errorf("echo model: %q", body.Model)
	}

	// Dispatch must go through sendFn — not a direct spawn. Expect at
	// least one user-role call carrying the prompt text. The system
	// inject ("rest request context") may precede it.
	mu.Lock()
	defer mu.Unlock()
	if len(*captured) == 0 {
		t.Fatal("sendFn never called — handler must dispatch through pool, never spawn directly")
	}
	var userCall *sentCall
	for i := range *captured {
		c := &(*captured)[i]
		if c.Role == "user" {
			userCall = c
			break
		}
	}
	if userCall == nil {
		t.Fatal("no user-role dispatch captured")
	}
	if userCall.Source != "rest" {
		t.Errorf("source=%q want \"rest\"", userCall.Source)
	}
	if !strings.Contains(userCall.Text, "say hi") {
		t.Errorf("prompt missing: %q", userCall.Text)
	}
	if !strings.HasPrefix(userCall.SessionID, "rest-") {
		t.Errorf("sessionID prefix: %q", userCall.SessionID)
	}
}

func TestChatCompletions_StatefulSessionReuse(t *testing.T) {
	stubModels(t, "claude")
	ch, mu, captured := newTestChannel(t, "ok", nil)

	// Two requests with same session_id → same sessionID, second call
	// should NOT re-inject system context.
	body := map[string]any{
		"model":      "claude",
		"conversation": "abc",
		"messages":   []map[string]string{{"role": "user", "content": "first"}},
	}
	rec := postJSON(t, http.HandlerFunc(ch.handleChatCompletions), "/", "good", body)
	if rec.Code != 200 {
		t.Fatalf("first: %d %s", rec.Code, rec.Body.String())
	}
	// Flip fakeSessions to "exists" so second call sees the existing session.
	ch.SetSessionChecker(fakeSessions{exists: true})
	body["messages"] = []map[string]string{{"role": "user", "content": "second"}}
	rec = postJSON(t, http.HandlerFunc(ch.handleChatCompletions), "/", "good", body)
	if rec.Code != 200 {
		t.Fatalf("second: %d %s", rec.Code, rec.Body.String())
	}

	mu.Lock()
	defer mu.Unlock()
	wantSession := restSessionID("user-1", "abc")
	var systemCalls, userCalls int
	for _, c := range *captured {
		if c.SessionID != wantSession {
			t.Errorf("sessionID drift: %q want %q", c.SessionID, wantSession)
		}
		switch c.Role {
		case "system":
			systemCalls++
		case "user":
			userCalls++
		}
	}
	if systemCalls != 1 {
		t.Errorf("system inject fired %d times, want 1 (first turn only)", systemCalls)
	}
	if userCalls != 2 {
		t.Errorf("user dispatches=%d, want 2", userCalls)
	}
}

// ── concurrency safety ────────────────────────────────────────────────

// TestChatCompletions_ConcurrentSameSessionQueues verifies a second sync
// request on a busy session no longer 409s — it queues behind the
// in-flight turn (like a chat message) and gets its own reply.
func TestChatCompletions_ConcurrentSameSessionQueues(t *testing.T) {
	stubModels(t, "claude")

	// Hold the first send inside sendFn so the second request arrives
	// while the first turn is still in flight.
	release := make(chan struct{})
	ch, _, _ := newTestChannel(t, "ok", func(sessionID string) {
		<-release // closed channel → later sends pass straight through
	})

	body := map[string]any{
		"model":        "claude",
		"conversation": "busy",
		"messages":     []map[string]string{{"role": "user", "content": "long-running"}},
	}

	var codes [2]int32
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rec := postJSON(t, http.HandlerFunc(ch.handleChatCompletions), "/", "good", body)
			atomic.StoreInt32(&codes[i], int32(rec.Code))
		}(i)
		if i == 0 {
			// Wait until the first turn is queued before firing the second.
			sid := restSessionID("user-1", "busy")
			deadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) {
				ch.mu.Lock()
				queued := len(ch.turns[sid]) > 0
				ch.mu.Unlock()
				if queued {
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
		}
	}

	close(release)
	wg.Wait()
	for i := range codes {
		if got := atomic.LoadInt32(&codes[i]); got != 200 {
			t.Errorf("request %d: got %d want 200 (same-session requests must queue, not 409)", i, got)
		}
	}
}

// TestChatCompletions_AbandonedRequestDoesNotShiftReplies reproduces the
// Postman stop-button scenario: request 1 is cancelled mid-flight, the
// agent still finishes message 1, and request 2 must receive message 2's
// reply — not message 1's leftover.
func TestChatCompletions_AbandonedRequestDoesNotShiftReplies(t *testing.T) {
	stubModels(t, "claude")
	ch := New(agentconfig.RestChannelConfig{Enabled: "true", ProjectID: "main"}, &fakeAuth{wantToken: "good", userID: "u"})
	ch.SetSessionChecker(fakeSessions{exists: true})
	ch.SetSendFunc(func(_ context.Context, _, _, _, _, _ string) error { return nil }) // events fired manually
	sid := restSessionID("u", "pm")

	post := func(ctx context.Context, content string) (*httptest.ResponseRecorder, chan struct{}) {
		buf, _ := json.Marshal(map[string]any{
			"model":        "claude",
			"conversation": "pm",
			"messages":     []map[string]string{{"role": "user", "content": content}},
		})
		req := httptest.NewRequest("POST", "/", bytes.NewReader(buf)).WithContext(ctx)
		req.Header.Set("Authorization", "Bearer good")
		rec := httptest.NewRecorder()
		done := make(chan struct{})
		go func() {
			ch.handleChatCompletions(rec, req)
			close(done)
		}()
		return rec, done
	}
	waitQueue := func(n int) {
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			ch.mu.Lock()
			l := len(ch.turns[sid])
			ch.mu.Unlock()
			if l == n {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("queue never reached %d turns", n)
	}

	// Request 1 — user clicks stop before the agent finishes.
	ctx1, cancel := context.WithCancel(context.Background())
	rec1, done1 := post(ctx1, "message one")
	waitQueue(1)
	cancel()
	<-done1
	if rec1.Code != 499 {
		t.Fatalf("cancelled request: code=%d want 499", rec1.Code)
	}

	// Request 2 arrives while message 1 is still being processed.
	rec2, done2 := post(context.Background(), "message two")
	waitQueue(2)

	// Agent finishes message 1 → must pop the abandoned turn, silently.
	ch.OnAgentEvent(sid, event.AgentEvent{Type: event.TextDelta, Text: "reply-ONE"})
	ch.OnAgentEvent(sid, event.AgentEvent{Type: event.Done})
	// Agent finishes message 2 → this is request 2's reply.
	ch.OnAgentEvent(sid, event.AgentEvent{Type: event.TextDelta, Text: "reply-TWO"})
	ch.OnAgentEvent(sid, event.AgentEvent{Type: event.Done})

	<-done2
	if rec2.Code != 200 {
		t.Fatalf("request 2: code=%d body=%s", rec2.Code, rec2.Body.String())
	}
	var body chatResponse
	_ = json.Unmarshal(rec2.Body.Bytes(), &body)
	if got := body.Choices[0].Message.Content; got != "reply-TWO" {
		t.Errorf("request 2 got %q want \"reply-TWO\" — replies shifted after abandoned request", got)
	}
}

// ── background mode ───────────────────────────────────────────────────

// TestChatCompletions_BackgroundReturnsImmediately verifies background:true
// returns 200 with status "queued" without waiting for Done — the agent
// never replies in this test, so a blocking handler would hang.
func TestChatCompletions_BackgroundReturnsImmediately(t *testing.T) {
	stubModels(t, "claude")
	ch := New(agentconfig.RestChannelConfig{Enabled: "true", ProjectID: "main"}, &fakeAuth{wantToken: "good", userID: "u"})
	ch.SetSessionChecker(fakeSessions{exists: true})

	var mu sync.Mutex
	var captured []sentCall
	ch.SetSendFunc(func(_ context.Context, sessionID, agentName, source, role, text string) error {
		mu.Lock()
		captured = append(captured, sentCall{sessionID, agentName, source, role, text})
		mu.Unlock()
		return nil // no Done ever fires
	})

	respCh := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		respCh <- postJSON(t, http.HandlerFunc(ch.handleChatCompletions), "/", "good", map[string]any{
			"model":        "claude",
			"conversation": "bg",
			"background":   true,
			"messages":     []map[string]string{{"role": "user", "content": "long job"}},
		})
	}()

	var rec *httptest.ResponseRecorder
	select {
	case rec = <-respCh:
	case <-time.After(2 * time.Second):
		t.Fatal("background request blocked waiting for Done")
	}
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body chatResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Status != "queued" {
		t.Errorf("status=%q want \"queued\"", body.Status)
	}
	mu.Lock()
	defer mu.Unlock()
	var userCalls int
	for _, c := range captured {
		if c.Role == "user" {
			userCalls++
			if !strings.Contains(c.Text, "long job") {
				t.Errorf("prompt missing: %q", c.Text)
			}
		}
	}
	if userCalls != 1 {
		t.Errorf("user dispatches=%d want 1", userCalls)
	}
}

// TestChatCompletions_BackgroundQueuesMultiple verifies several background
// sends on one conversation all dispatch (no 409) — they queue in the
// pool, exactly like chat channel messages.
func TestChatCompletions_BackgroundQueuesMultiple(t *testing.T) {
	stubModels(t, "claude")
	ch := New(agentconfig.RestChannelConfig{Enabled: "true", ProjectID: "main"}, &fakeAuth{wantToken: "good", userID: "u"})
	ch.SetSessionChecker(fakeSessions{exists: true})

	var mu sync.Mutex
	var userCalls int
	ch.SetSendFunc(func(_ context.Context, _, _, _, role, _ string) error {
		if role == "user" {
			mu.Lock()
			userCalls++
			mu.Unlock()
		}
		return nil // agent still busy — no Done
	})

	for i := 0; i < 3; i++ {
		rec := postJSON(t, http.HandlerFunc(ch.handleChatCompletions), "/", "good", map[string]any{
			"model":        "claude",
			"conversation": "bg-queue",
			"background":   true,
			"messages":     []map[string]string{{"role": "user", "content": "msg"}},
		})
		if rec.Code != 200 {
			t.Fatalf("send %d: status=%d body=%s", i, rec.Code, rec.Body.String())
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if userCalls != 3 {
		t.Errorf("user dispatches=%d want 3", userCalls)
	}
}

// TestBackground_MetadataFlag verifies metadata.background="true" works for
// SDKs that only expose the standard OpenAI fields.
func TestBackground_MetadataFlag(t *testing.T) {
	stubModels(t, "claude")
	ch := New(agentconfig.RestChannelConfig{Enabled: "true", ProjectID: "main"}, &fakeAuth{wantToken: "good", userID: "u"})
	ch.SetSessionChecker(fakeSessions{exists: true})
	ch.SetSendFunc(func(_ context.Context, _, _, _, _, _ string) error { return nil })

	rec := postJSON(t, http.HandlerFunc(ch.handleChatCompletions), "/", "good", map[string]any{
		"model":    "claude",
		"metadata": map[string]string{"background": "true", "conversation": "bg-meta"},
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body chatResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Status != "queued" {
		t.Errorf("status=%q want \"queued\"", body.Status)
	}
}

// TestBackground_AutoBlocksApprovalWhilePending verifies gate approvals
// arriving during a background turn are auto-blocked (no waiter exists,
// but bgPending keeps the session marked as REST-owned), and that the
// accounting clears once the turn's Done arrives.
func TestBackground_AutoBlocksApprovalWhilePending(t *testing.T) {
	stubModels(t, "claude")
	ch := New(agentconfig.RestChannelConfig{Enabled: "true", ProjectID: "main"}, &fakeAuth{wantToken: "good", userID: "u"})
	ch.SetSessionChecker(fakeSessions{exists: true})
	ch.SetSendFunc(func(_ context.Context, _, _, _, _, _ string) error { return nil })

	var mu sync.Mutex
	var blocks int
	ch.SetApproveFn(func(sid, rid, decision, matchKey string) error {
		mu.Lock()
		if decision == gate.DecisionBlock {
			blocks++
		}
		mu.Unlock()
		return nil
	})

	rec := postJSON(t, http.HandlerFunc(ch.handleChatCompletions), "/", "good", map[string]any{
		"model":        "claude",
		"conversation": "bg-appr",
		"background":   true,
		"messages":     []map[string]string{{"role": "user", "content": "rm -rf"}},
	})
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	sid := restSessionID("u", "bg-appr")

	// Approval during the background turn → auto-block.
	ch.OnApprovalRequest(sid, gate.ApprovalRequest{ID: "r1"})
	mu.Lock()
	if blocks != 1 {
		t.Errorf("blocks=%d want 1 (approval during background turn must auto-block)", blocks)
	}
	mu.Unlock()

	// Turn finishes → accounting cleared → later approvals (e.g. the
	// session continued interactively in the web UI) are left alone.
	ch.OnAgentEvent(sid, event.AgentEvent{Type: event.Done})
	ch.OnApprovalRequest(sid, gate.ApprovalRequest{ID: "r2"})
	mu.Lock()
	if blocks != 1 {
		t.Errorf("blocks=%d want 1 (approval after Done must NOT auto-block)", blocks)
	}
	mu.Unlock()

	ch.mu.Lock()
	if len(ch.turns) != 0 {
		t.Errorf("turn queue not cleaned after Done: %v", ch.turns)
	}
	ch.mu.Unlock()
}

// TestResponses_Background verifies the Responses API variant returns
// status "queued" with a chainable resp_ id.
func TestResponses_Background(t *testing.T) {
	stubModels(t, "claude")
	ch := New(agentconfig.RestChannelConfig{Enabled: "true", ProjectID: "main"}, &fakeAuth{wantToken: "good", userID: "user-1"})
	ch.SetSessionChecker(fakeSessions{exists: true})
	ch.SetSendFunc(func(_ context.Context, _, _, _, _, _ string) error { return nil })

	rec := postJSON(t, http.HandlerFunc(ch.handleResponses), "/", "good", map[string]any{
		"model":        "claude",
		"conversation": "bg-resp",
		"background":   true,
		"input":        "long job",
	})
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body responsesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Status != "queued" {
		t.Errorf("status=%q want \"queued\"", body.Status)
	}
	wantID := responsesIDPrefix + strings.TrimPrefix(restSessionID("user-1", "bg-resp"), "rest-")
	if body.ID != wantID {
		t.Errorf("id=%q want %q (must stay chainable)", body.ID, wantID)
	}
	if body.OutputText != "" || len(body.Output) != 0 {
		t.Errorf("queued response must carry no output: %+v", body)
	}
}

func TestResolveBackground(t *testing.T) {
	tests := []struct {
		name string
		bg   bool
		meta map[string]string
		want bool
	}{
		{"off", false, nil, false},
		{"explicit field", true, nil, true},
		{"metadata true", false, map[string]string{"background": "true"}, true},
		{"metadata 1", false, map[string]string{"background": "1"}, true},
		{"metadata yes", false, map[string]string{"background": "YES"}, true},
		{"metadata false", false, map[string]string{"background": "false"}, false},
		{"unrelated metadata", false, map[string]string{"foo": "bar"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveBackground(tc.bg, tc.meta); got != tc.want {
				t.Errorf("got %v want %v", got, tc.want)
			}
		})
	}
}

// ── session key resolution ────────────────────────────────────────────

func TestResolveConversation(t *testing.T) {
	tests := []struct {
		name string
		conv string
		meta map[string]string
		want string
	}{
		{"empty", "", nil, ""},
		{"explicit field", "conv-1", map[string]string{"conversation": "should-lose"}, "conv-1"},
		{"metadata fallback", "", map[string]string{"conversation": "m1"}, "m1"},
		{"trims whitespace", "  conv  ", nil, "conv"},
		{"trims metadata too", "", map[string]string{"conversation": "  m2  "}, "m2"},
		{"unrelated metadata ignored", "", map[string]string{"foo": "bar"}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveConversation(tc.conv, tc.meta); got != tc.want {
				t.Errorf("got %q want %q", got, tc.want)
			}
		})
	}
}

// TestChatCompletions_ConversationFieldKeysSession verifies the OpenAI
// "conversation" field is honoured as a session key when session_id is
// absent — sample payload: { "conversation": "<uuid>" }.
func TestChatCompletions_ConversationFieldKeysSession(t *testing.T) {
	stubModels(t, "claude")
	ch, mu, captured := newTestChannel(t, "ok", nil)
	rec := postJSON(t, http.HandlerFunc(ch.handleChatCompletions), "/", "good", map[string]any{
		"model":        "claude",
		"conversation": "e54e13b7e6774a89b64341963335c2a7",
		"messages":     []map[string]string{{"role": "user", "content": "hi"}},
	})
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	mu.Lock()
	defer mu.Unlock()
	var userCall *sentCall
	for i := range *captured {
		if (*captured)[i].Role == "user" {
			userCall = &(*captured)[i]
			break
		}
	}
	if userCall == nil {
		t.Fatal("no user-role dispatch captured")
	}
	want := restSessionID("user-1", "e54e13b7e6774a89b64341963335c2a7")
	if userCall.SessionID != want {
		t.Errorf("sessionID=%q want %q (conversation field not routed)", userCall.SessionID, want)
	}
}

// TestResponses_ConversationFieldKeysSession mirrors the chat test but
// exercises the Responses API.
func TestResponses_ConversationFieldKeysSession(t *testing.T) {
	stubModels(t, "claude")
	ch, mu, captured := newTestChannel(t, "hi", nil)
	rec := postJSON(t, http.HandlerFunc(ch.handleResponses), "/", "good", map[string]any{
		"model":        "claude",
		"conversation": "e54e13b7e6774a89b64341963335c2a7",
		"input":        "hello",
	})
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	mu.Lock()
	defer mu.Unlock()
	var userCall *sentCall
	for i := range *captured {
		if (*captured)[i].Role == "user" {
			userCall = &(*captured)[i]
			break
		}
	}
	if userCall == nil {
		t.Fatal("no user-role dispatch captured")
	}
	want := restSessionID("user-1", "e54e13b7e6774a89b64341963335c2a7")
	if userCall.SessionID != want {
		t.Errorf("sessionID=%q want %q", userCall.SessionID, want)
	}
	// Response id should echo the namespaced base (scope-key) so the same
	// authenticated caller can chain via previous_response_id or keep
	// sending the same conversation. The scope is opaque to the client.
	var body responsesResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	wantID := responsesIDPrefix + strings.TrimPrefix(want, "rest-")
	if body.ID != wantID {
		t.Errorf("response id=%q want %q", body.ID, wantID)
	}
}

// ── pool dispatch wiring ──────────────────────────────────────────────
//
// The pool's queue itself (FIFO, preempt-idle, slot-full backpressure) is
// covered by pool_test.go — TestQueueWhenPoolFull and friends. These
// tests prove the REST layer correctly delegates to that queue: every
// request goes through sendFn, the handler blocks until the agent
// finishes, dispatch errors propagate as 500, and parallel requests on
// distinct sessions don't cross-contaminate state.

// TestChatCompletions_PoolDispatchError verifies the handler surfaces a
// pool-level send error (slot rejected, pool closed, …) as a 500 with
// the "pool dispatch failed" prefix rather than hanging or fabricating
// a fake reply.
func TestChatCompletions_PoolDispatchError(t *testing.T) {
	stubModels(t, "claude")
	ch := New(agentconfig.RestChannelConfig{Enabled: "true", ProjectID: "main"}, &fakeAuth{wantToken: "good", userID: "u"})
	ch.SetSessionChecker(fakeSessions{exists: true}) // skip inject so error comes from the user-role send
	ch.SetSendFunc(func(_ context.Context, _, _, _, _, _ string) error {
		return errAuth("pool closed")
	})
	rec := postJSON(t, http.HandlerFunc(ch.handleChatCompletions), "/", "good", map[string]any{
		"model":      "claude",
		"conversation": "abc",
		"messages":   []map[string]string{{"role": "user", "content": "hi"}},
	})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("got %d want 500 body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "pool dispatch failed") {
		t.Errorf("error message missing prefix: %s", rec.Body.String())
	}
}

// TestChatCompletions_WaitsForDone verifies dispatch blocks until the
// agent fires event.Done — proving REST honours the queue's async
// completion model instead of returning early on send-accept.
func TestChatCompletions_WaitsForDone(t *testing.T) {
	stubModels(t, "claude")
	ch := New(agentconfig.RestChannelConfig{Enabled: "true", ProjectID: "main"}, &fakeAuth{wantToken: "good", userID: "u"})
	ch.SetSessionChecker(fakeSessions{exists: true})

	doneReleased := make(chan struct{})
	ch.SetSendFunc(func(_ context.Context, sessionID, _, _, role, _ string) error {
		if role != "user" {
			return nil
		}
		go func() {
			ch.OnAgentEvent(sessionID, event.AgentEvent{Type: event.TextDelta, Text: "ok"})
			// Hold off on Done — handler must NOT return until this fires.
			<-doneReleased
			ch.OnAgentEvent(sessionID, event.AgentEvent{Type: event.Done})
		}()
		return nil
	})

	respCh := make(chan int, 1)
	go func() {
		rec := postJSON(t, http.HandlerFunc(ch.handleChatCompletions), "/", "good", map[string]any{
			"model":      "claude",
			"conversation": "wait",
			"messages":   []map[string]string{{"role": "user", "content": "hi"}},
		})
		respCh <- rec.Code
	}()

	select {
	case code := <-respCh:
		t.Fatalf("handler returned %d before Done fired — REST is not honouring async queue", code)
	case <-time.After(100 * time.Millisecond):
		// Expected — handler is parked on tn.done.
	}

	close(doneReleased)
	select {
	case code := <-respCh:
		if code != 200 {
			t.Errorf("after Done: got %d want 200", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not return within 2s after Done fired")
	}
}

// TestChatCompletions_ParallelDistinctSessions verifies multiple REST
// requests with different session_ids all dispatch in parallel through
// sendFn (one user-role call per request) and each receives its own
// reply without state crossing between sessions.
func TestChatCompletions_ParallelDistinctSessions(t *testing.T) {
	stubModels(t, "claude")
	ch := New(agentconfig.RestChannelConfig{Enabled: "true", ProjectID: "main"}, &fakeAuth{wantToken: "good", userID: "u"})
	ch.SetSessionChecker(fakeSessions{exists: true})

	var mu sync.Mutex
	userCalls := map[string]int{}
	ch.SetSendFunc(func(_ context.Context, sessionID, _, _, role, _ string) error {
		if role != "user" {
			return nil
		}
		mu.Lock()
		userCalls[sessionID]++
		mu.Unlock()
		// Echo back the sessionID so we can confirm no cross-talk.
		go func() {
			ch.OnAgentEvent(sessionID, event.AgentEvent{Type: event.TextDelta, Text: "reply-for-" + sessionID})
			ch.OnAgentEvent(sessionID, event.AgentEvent{Type: event.Done})
		}()
		return nil
	})

	const N = 8
	results := make([]string, N)
	codes := make([]int, N)
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sid := "s-" + string(rune('a'+i))
			rec := postJSON(t, http.HandlerFunc(ch.handleChatCompletions), "/", "good", map[string]any{
				"model":      "claude",
				"conversation": sid,
				"messages":   []map[string]string{{"role": "user", "content": "hi"}},
			})
			codes[i] = rec.Code
			var body chatResponse
			_ = json.Unmarshal(rec.Body.Bytes(), &body)
			if len(body.Choices) > 0 {
				results[i] = body.Choices[0].Message.Content
			}
		}(i)
	}
	wg.Wait()

	for i, code := range codes {
		if code != 200 {
			t.Errorf("request %d: code=%d", i, code)
		}
		want := "reply-for-" + restSessionID("u", "s-"+string(rune('a'+i)))
		if results[i] != want {
			t.Errorf("request %d: reply=%q want %q (cross-session contamination?)", i, results[i], want)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(userCalls) != N {
		t.Errorf("expected %d distinct sessions hitting sendFn, got %d (%v)", N, len(userCalls), userCalls)
	}
	for sid, n := range userCalls {
		if n != 1 {
			t.Errorf("session %s dispatched %d times, want 1", sid, n)
		}
	}
}

// ── /v1/responses ─────────────────────────────────────────────────────

func TestResponses_HappyPathStringInput(t *testing.T) {
	stubModels(t, "claude")
	ch, _, _ := newTestChannel(t, "hi there", nil)
	rec := postJSON(t, http.HandlerFunc(ch.handleResponses), "/", "good", map[string]any{
		"model": "claude",
		"input": "hello",
	})
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body responsesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Object != "response" {
		t.Errorf("object=%q", body.Object)
	}
	if body.OutputText != "hi there" {
		t.Errorf("output_text=%q want \"hi there\"", body.OutputText)
	}
	if !strings.HasPrefix(body.ID, "resp_") {
		t.Errorf("id prefix: %q", body.ID)
	}
	if len(body.Output) != 1 || len(body.Output[0].Content) != 1 ||
		body.Output[0].Content[0].Type != "output_text" ||
		body.Output[0].Content[0].Text != "hi there" {
		t.Errorf("output shape: %+v", body.Output)
	}
}

func TestResponses_ModelNotFound(t *testing.T) {
	stubModels(t, "claude")
	ch, _, _ := newTestChannel(t, "ok", nil)
	rec := postJSON(t, http.HandlerFunc(ch.handleResponses), "/", "good", map[string]any{
		"model": "gpt-5",
		"input": "hello",
	})
	if rec.Code != http.StatusNotFound {
		t.Errorf("got %d want 404 (body=%s)", rec.Code, rec.Body.String())
	}
}

func TestResponses_StreamRejected(t *testing.T) {
	stubModels(t, "claude")
	ch, _, _ := newTestChannel(t, "ok", nil)
	rec := postJSON(t, http.HandlerFunc(ch.handleResponses), "/", "good", map[string]any{
		"model":  "claude",
		"input":  "hi",
		"stream": true,
	})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("got %d want 400", rec.Code)
	}
}

func TestResponses_EmptyInput(t *testing.T) {
	stubModels(t, "claude")
	ch, _, _ := newTestChannel(t, "ok", nil)
	rec := postJSON(t, http.HandlerFunc(ch.handleResponses), "/", "good", map[string]any{
		"model": "claude",
		"input": "",
	})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("got %d want 400", rec.Code)
	}
}

func TestResponses_PreviousResponseIDChainsSession(t *testing.T) {
	stubModels(t, "claude")
	ch, mu, captured := newTestChannel(t, "ok", nil)

	// Turn 1.
	rec := postJSON(t, http.HandlerFunc(ch.handleResponses), "/", "good", map[string]any{
		"model": "claude",
		"input": "hi",
	})
	if rec.Code != 200 {
		t.Fatalf("turn 1: %d %s", rec.Code, rec.Body.String())
	}
	var first responsesResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &first)
	if !strings.HasPrefix(first.ID, "resp_") {
		t.Fatalf("turn 1 id: %q", first.ID)
	}

	// Flip session checker so the chained turn skips re-inject.
	ch.SetSessionChecker(fakeSessions{exists: true})

	// Turn 2 — pass previous_response_id.
	rec = postJSON(t, http.HandlerFunc(ch.handleResponses), "/", "good", map[string]any{
		"model":                "claude",
		"input":                "again",
		"previous_response_id": first.ID,
	})
	if rec.Code != 200 {
		t.Fatalf("turn 2: %d %s", rec.Code, rec.Body.String())
	}
	var second responsesResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &second)

	// Both turns should hit the same wick session id.
	turn1Base := strings.TrimPrefix(first.ID, "resp_")
	turn2Base := strings.TrimPrefix(second.ID, "resp_")
	if turn1Base != turn2Base {
		t.Errorf("session base diverged: %q vs %q", turn1Base, turn2Base)
	}

	mu.Lock()
	defer mu.Unlock()
	sessions := map[string]int{}
	for _, c := range *captured {
		if c.Role == "user" {
			sessions[c.SessionID]++
		}
	}
	if len(sessions) != 1 {
		t.Errorf("user dispatches landed in multiple sessions: %v", sessions)
	}
}

// ── prompt-shaping helpers ────────────────────────────────────────────

func TestDecodeInput(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"string", `"hello"`, "hello"},
		{"empty string", `""`, ""},
		{"null", `null`, ""},
		{"array of strings", `[{"role":"user","content":"hi"}]`, "hi"},
		{"array of parts", `[{"role":"user","content":[{"type":"input_text","text":"part1"},{"type":"input_text","text":"part2"}]}]`, "part1\npart2"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := decodeInput(json.RawMessage(tc.raw))
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestComposeResponsesPrompt(t *testing.T) {
	got := composeResponsesPrompt("be terse", "hello", false)
	if !strings.HasPrefix(got, "[system] be terse") || !strings.Contains(got, "hello") {
		t.Errorf("stateless prompt missing system block: %q", got)
	}
	got = composeResponsesPrompt("be terse", "hello", true)
	if got != "hello" {
		t.Errorf("reused turn leaked instructions: %q", got)
	}
}

// ── compile-time interface assertions ─────────────────────────────────

// These ensure Channel keeps satisfying the interfaces the registry
// type-asserts at wire-up time. A regression here would silently break
// dispatch routing — surface it at compile time.
var (
	_ agentchannels.Channel                 = (*Channel)(nil)
	_ agentchannels.SendFuncSetter          = (*Channel)(nil)
	_ agentchannels.SessionCheckerSetter    = (*Channel)(nil)
	_ agentchannels.SessionStartHookSetter  = (*Channel)(nil)
	_ agentchannels.ApproveFnSetter         = (*Channel)(nil)
	_ agentchannels.AgentEventReceiver      = (*Channel)(nil)
	_ agentchannels.ApprovalReceiver        = (*Channel)(nil)
	_ agentchannels.MultiHTTPHandlerProvider = (*Channel)(nil)
)
