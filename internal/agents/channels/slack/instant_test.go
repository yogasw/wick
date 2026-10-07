package slack

import (
	"bytes"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

type fakeRouter struct {
	agents   []InstantAgent
	sessions map[string]string
	avatars  map[string][]byte
}

func (f *fakeRouter) Agents(*Channel) []InstantAgent { return f.agents }
func (f *fakeRouter) SessionAgent(id string) string  { return f.sessions[id] }
func (f *fakeRouter) AvatarPNG(tok string) ([]byte, string, bool) {
	b, ok := f.avatars[tok]
	return b, "etag1", ok
}

func useRouter(t *testing.T, r PersonaRouter) {
	t.Helper()
	SetPersonaRouter(r)
	t.Cleanup(func() { SetPersonaRouter(nil) })
}

var (
	nanda = InstantAgent{Persona: Persona{AgentID: "a1", ProjectID: "p1", Username: "Nanda", IconURL: "https://w/x/t1.png"},
		Handle: "nanda", Channels: []string{"CBOUND"}, PrefixEnabled: true}
	critic = InstantAgent{Persona: Persona{AgentID: "a2", ProjectID: "p2", Username: "Critic"},
		Handle: "critic", PrefixEnabled: false}
)

func TestPickInstant(t *testing.T) {
	agents := []InstantAgent{nanda, critic}
	cases := []struct {
		name, ch, text, wantID, wantText string
	}{
		{"bound channel wins", "CBOUND", "critic: hi", "a1", "critic: hi"},
		{"colon prefix", "COTHER", "nanda: check logs", "a1", "check logs"},
		{"at prefix", "COTHER", "@Nanda check logs", "a1", "check logs"},
		{"comma prefix", "D1", "nanda, hi", "a1", "hi"},
		{"bare word never routes", "COTHER", "nanda check logs", "", "nanda check logs"},
		{"prefix off", "COTHER", "critic: review", "", "critic: review"},
		{"unknown handle", "COTHER", "@ghost hi", "", "@ghost hi"},
		{"prefix alone keeps text", "COTHER", "nanda:", "a1", "nanda:"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, text, ok := PickInstant(agents, tc.ch, tc.text)
			if ok != (tc.wantID != "") || a.AgentID != tc.wantID {
				t.Fatalf("agent = %q (ok %v), want %q", a.AgentID, ok, tc.wantID)
			}
			if text != tc.wantText {
				t.Errorf("text = %q, want %q", text, tc.wantText)
			}
		})
	}
}

func TestRoutePersonaStickyAndFallback(t *testing.T) {
	r := &fakeRouter{agents: []InstantAgent{nanda, critic}, sessions: map[string]string{"slack-T2": "a2"}}
	useRouter(t, r)
	c := &Channel{sessionPrefix: "slack-"}

	// New thread in a bound channel: the bound agent.
	if p, _ := c.routePersona("CBOUND", "slack-T1", "hello", false); p.AgentID != "a1" {
		t.Fatalf("bound = %q, want a1", p.AgentID)
	}
	// Existing thread: whoever answered first, even in a bound channel and
	// even when the text names another agent.
	if p, text := c.routePersona("CBOUND", "slack-T2", "nanda: hi", true); p.AgentID != "a2" || text != "nanda: hi" {
		t.Fatalf("sticky = %q %q, want a2 with text untouched", p.AgentID, text)
	}
	// Existing thread wick answered: stays wick.
	if p, _ := c.routePersona("CBOUND", "slack-T3", "nanda: hi", true); p.AgentID != "" {
		t.Fatalf("wick thread rerouted to %q", p.AgentID)
	}
	// Unbound channel, no prefix: wick as before.
	if p, _ := c.routePersona("COTHER", "slack-T4", "hello", false); p.AgentID != "" {
		t.Fatalf("fallback = %q, want wick", p.AgentID)
	}
	// No router: nothing changes.
	SetPersonaRouter(nil)
	if p, text := c.routePersona("CBOUND", "slack-T5", "hi", false); p.AgentID != "" || text != "hi" {
		t.Fatalf("no router routed to %q", p.AgentID)
	}
}

// The reply carries the agent's name and icon; a thread of another agent
// or of wick does not.
func TestPostThreadUsesPersona(t *testing.T) {
	useRouter(t, &fakeRouter{agents: []InstantAgent{nanda, critic}, sessions: map[string]string{"slack-T1": "a1", "slack-T2": "a2"}})
	f := newFakeSlack(t)
	c := &Channel{api: f.client(), sessionPrefix: "slack-"}

	c.postReply("C1", "T1", "hi")
	c.postReply("C1", "T2", "hi")
	c.postReply("C1", "T9", "hi")
	posts := f.posts()
	if len(posts) != 3 {
		t.Fatalf("posts = %d, want 3", len(posts))
	}
	if posts[0].Get("username") != "Nanda" || posts[0].Get("icon_url") != "https://w/x/t1.png" {
		t.Errorf("nanda post = %v", posts[0])
	}
	if posts[1].Get("username") != "Critic" || posts[1].Get("icon_emoji") == "" {
		t.Errorf("critic post without icon_url should fall back to an emoji: %v", posts[1])
	}
	if posts[2].Get("username") != "" || posts[2].Get("icon_url") != "" {
		t.Errorf("wick thread got a persona: %v", posts[2])
	}
}

// Without chat:write.customize the reply still goes out, as the bot, and
// later replies stop asking for the override.
func TestPostThreadFallsBackWithoutCustomizeScope(t *testing.T) {
	useRouter(t, &fakeRouter{agents: []InstantAgent{nanda}, sessions: map[string]string{"slack-T1": "a1"}})
	var bodies []string
	mux := http.NewServeMux()
	mux.HandleFunc("/chat.postMessage", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		bodies = append(bodies, r.Form.Get("username"))
		w.Header().Set("Content-Type", "application/json")
		if r.Form.Get("username") != "" {
			_, _ = w.Write([]byte(`{"ok":false,"error":"missing_scope","needed":"chat:write.customize"}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"channel":"C1","ts":"1.2"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	f := &fakeSlack{srv: srv}
	c := &Channel{api: f.client(), sessionPrefix: "slack-"}

	c.postReply("C1", "T1", "first")
	c.postReply("C1", "T1", "second")
	if len(bodies) != 3 || bodies[0] != "Nanda" || bodies[1] != "" || bodies[2] != "" {
		t.Fatalf("usernames sent = %q, want [Nanda, \"\", \"\"]", bodies)
	}
	if !c.customizeDenied.Load() {
		t.Error("customizeDenied not set")
	}
}

func TestAvatarHandler(t *testing.T) {
	img := RenderAvatarPNG("squircle", "#ff8800")
	useRouter(t, &fakeRouter{avatars: map[string][]byte{"goodtoken": img}})
	mux := http.NewServeMux()
	mux.Handle("GET "+avatarPath+"{file}", avatarHandler())

	get := func(path, inm string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if inm != "" {
			req.Header.Set("If-None-Match", inm)
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	ok := get(avatarPath+"goodtoken.png", "")
	if ok.Code != http.StatusOK || ok.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("good token: %d %s", ok.Code, ok.Header().Get("Content-Type"))
	}
	if ok.Header().Get("Cache-Control") == "" || ok.Header().Get("ETag") == "" {
		t.Error("avatar is not cache-friendly")
	}
	if _, err := png.Decode(bytes.NewReader(ok.Body.Bytes())); err != nil {
		t.Errorf("body is not a PNG: %v", err)
	}
	if rec := get(avatarPath+"goodtoken.png", ok.Header().Get("ETag")); rec.Code != http.StatusNotModified {
		t.Errorf("revalidation = %d, want 304", rec.Code)
	}
	for _, p := range []string{avatarPath + "wrong.png", avatarPath + "goodtoken", avatarPath + ".png"} {
		if rec := get(p, ""); rec.Code != http.StatusNotFound {
			t.Errorf("%s = %d, want 404", p, rec.Code)
		}
	}
}

func TestRenderAvatarShapes(t *testing.T) {
	for _, s := range []string{"circle", "squircle", "triangle", "diamond", "cloud", ""} {
		img, err := png.Decode(bytes.NewReader(RenderAvatarPNG(s, "#123")))
		if err != nil || img.Bounds().Dx() != avatarSize {
			t.Fatalf("%s: %v", s, err)
		}
	}
	if got := parseHexColor("nope"); got.B != 0x92 {
		t.Errorf("bad colour should fall back to the default, got %v", got)
	}
}

// handleMessage needs a live Slack API, so the wiring is pinned the same
// way as TestOwnerStampPrecedesSend: route first, then every send carries
// the agent's project and the prefix-stripped text.
func TestHandleMessageRoutesBeforeSend(t *testing.T) {
	src, err := os.ReadFile("slack.go")
	if err != nil {
		t.Fatal(err)
	}
	body := handlerBody(t, string(src))
	route := strings.Index(body, "s.routePersona(")
	send := strings.Index(body, "s.sendFn(")
	if route < 0 || send < 0 || route > send {
		t.Fatalf("routePersona at %d, first sendFn at %d: the persona must be known before a spawn", route, send)
	}
	if !strings.Contains(body, "WithProjectOverride(s.sendCtxAs(context.Background(), callerUserID), persona.ProjectID)") {
		t.Error("sends no longer carry the Instant agent's project")
	}
	if strings.Count(body, "s.sendFn(baseCtx") != 1 || !strings.Contains(body, "WithSender(baseCtx, sender)") {
		t.Error("a send bypasses baseCtx and would run in the shared bot's project")
	}
	if !strings.Contains(body, "normalizeUserText(routedText)") {
		t.Error("the routing prefix is no longer stripped from the agent's input")
	}
}
