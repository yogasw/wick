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
	ch := New(agentconfig.RestChannelConfig{Enabled: "true", Workspace: "main"}, &fakeAuth{wantToken: "good", userID: "user-1"})

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
	var systemCalls, userCalls int
	for _, c := range *captured {
		if c.SessionID != "rest-abc" {
			t.Errorf("sessionID drift: %q", c.SessionID)
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

func TestChatCompletions_SessionBusyReturns409(t *testing.T) {
	stubModels(t, "claude")

	// Block the first request inside sendFn until we fire the second
	// request, so the second sees an in-flight turn → 409.
	release := make(chan struct{})
	ch, _, _ := newTestChannel(t, "ok", func(sessionID string) {
		// Hold off on the agent reply for the first call so the turn
		// stays in-flight. Channel state mutex isn't held here.
		<-release
	})

	body := map[string]any{
		"model":      "claude",
		"conversation": "busy",
		"messages":   []map[string]string{{"role": "user", "content": "long-running"}},
	}

	var firstStatus int32
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		rec := postJSON(t, http.HandlerFunc(ch.handleChatCompletions), "/", "good", body)
		atomic.StoreInt32(&firstStatus, int32(rec.Code))
	}()

	// Give first request time to register the turn.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		ch.mu.Lock()
		busy := ch.turns["rest-busy"] != nil
		ch.mu.Unlock()
		if busy {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	rec := postJSON(t, http.HandlerFunc(ch.handleChatCompletions), "/", "good", body)
	if rec.Code != http.StatusConflict {
		t.Errorf("concurrent same-session → got %d want 409 (body=%s)", rec.Code, rec.Body.String())
	}

	// Release the held first request and confirm it succeeds.
	close(release)
	wg.Wait()
	if got := atomic.LoadInt32(&firstStatus); got != 200 {
		t.Errorf("first request after release: %d want 200", got)
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
	want := "rest-e54e13b7e6774a89b64341963335c2a7"
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
	want := "rest-e54e13b7e6774a89b64341963335c2a7"
	if userCall.SessionID != want {
		t.Errorf("sessionID=%q want %q", userCall.SessionID, want)
	}
	// Response id should echo the same base so callers can chain via
	// previous_response_id or just keep sending the same conversation.
	var body responsesResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.ID != "resp_e54e13b7e6774a89b64341963335c2a7" {
		t.Errorf("response id=%q want resp_<conversation>", body.ID)
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
	ch := New(agentconfig.RestChannelConfig{Enabled: "true", Workspace: "main"}, &fakeAuth{wantToken: "good", userID: "u"})
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
	ch := New(agentconfig.RestChannelConfig{Enabled: "true", Workspace: "main"}, &fakeAuth{wantToken: "good", userID: "u"})
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
	ch := New(agentconfig.RestChannelConfig{Enabled: "true", Workspace: "main"}, &fakeAuth{wantToken: "good", userID: "u"})
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
		want := "reply-for-rest-s-" + string(rune('a'+i))
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
