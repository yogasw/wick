package a2aserver

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2aclient/agentcard"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	agentchannels "github.com/yogasw/wick/internal/agents/channels"
	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/entity"
)

const (
	owner   = "user-owner"
	agentID = "agent-1"
)

type fakeDir struct {
	mu      sync.Mutex
	agents  map[string]Agent
	created map[string]int
}

func (d *fakeDir) Agent(_ context.Context, id string) (Agent, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	a, ok := d.agents[id]
	return a, ok
}

func (d *fakeDir) EnsureSession(_ context.Context, _ Agent, sid string) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.created[sid]++
	return d.created[sid] == 1, nil
}

type memConns struct {
	mu sync.Mutex
	m  map[string]Connection
}

func (c *memConns) Load(id string) (Connection, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.m[id]
	return v, ok, nil
}

func (c *memConns) Delete(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.m, id)
	return nil
}

type fakePAT map[string]entity.PersonalAccessToken

func (f fakePAT) AuthenticateToken(_ context.Context, plain string) (*entity.PersonalAccessToken, error) {
	if row, ok := f[plain]; ok {
		return &row, nil
	}
	return nil, errors.New("invalid")
}

// sent is one pool dispatch the fake pool saw.
type sent struct {
	sessionID, role, text, callerUserID string
	sender                              string
	senderWick                          string
}

type fixture struct {
	srv   *Server
	dir   *fakeDir
	conns *memConns
	http  *httptest.Server
	key   string

	mu    sync.Mutex
	sends []sent
	// reply scripts the agent: events emitted for every user turn.
	reply []event.AgentEvent
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	plain, hash, hint, err := NewKey()
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{
		dir: &fakeDir{agents: map[string]Agent{agentID: {
			ID: agentID, OwnerUserID: owner, ProjectID: "proj-1", Name: "Ops Bot", Description: "Answers ops questions",
			Prompts: []Prompt{{Title: "Status", Message: "What is the status?"}},
		}}, created: map[string]int{}},
		conns: &memConns{m: map[string]Connection{agentID: {AgentID: agentID, Enabled: true, KeyHash: hash, KeyHint: hint}}},
		key:   plain,
		reply: []event.AgentEvent{{Type: event.TextDelta, Text: "hello "}, {Type: event.TextDelta, Text: "world"}, {Type: event.Done}},
	}
	pat := fakePAT{
		"pat-owner":   {ID: "tok-1", UserID: owner, Name: "ci"},
		"pat-other":   {ID: "tok-2", UserID: "user-other", Name: "x"},
		"pat-allowed": {ID: "tok-3", UserID: "user-allowed", Name: "partner"},
	}
	f.http = httptest.NewServer(nil)
	f.srv = New(f.dir, f.conns, pat, func() string { return f.http.URL })
	f.http.Config.Handler = f.srv.mux
	f.srv.SetSendFunc(func(ctx context.Context, sid, _, source, role, text string) error {
		s := sent{sessionID: sid, role: role, text: text, callerUserID: agentchannels.CallerUserID(ctx)}
		if snd := agentchannels.SenderFrom(ctx); snd != nil {
			s.sender, s.senderWick = snd.ID, snd.WickUserID
		}
		f.mu.Lock()
		f.sends = append(f.sends, s)
		reply := f.reply
		f.mu.Unlock()
		if role == "user" && source == Source {
			go func() {
				for _, ev := range reply {
					f.srv.OnAgentEvent(sid, ev)
				}
			}()
		}
		return nil
	})
	t.Cleanup(f.http.Close)
	return f
}

func (f *fixture) userSends() []sent {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []sent
	for _, s := range f.sends {
		if s.role == "user" {
			out = append(out, s)
		}
	}
	return out
}

type bearerRT struct{ token string }

func (b bearerRT) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	if b.token != "" {
		r.Header.Set("Authorization", "Bearer "+b.token)
	}
	return http.DefaultTransport.RoundTrip(r)
}

func (f *fixture) client(t *testing.T, token string) *a2aclient.Client {
	t.Helper()
	hc := &http.Client{Transport: bearerRT{token}}
	card, err := (&agentcard.Resolver{Client: hc, CardParser: agentcard.DefaultCardParser}).Resolve(context.Background(), f.http.URL, agentcard.WithPath(CardPath(agentID)))
	if err != nil {
		t.Fatalf("resolve card: %v", err)
	}
	c, err := a2aclient.NewFromCard(context.Background(), card, a2aclient.WithJSONRPCTransport(hc))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Destroy() })
	return c
}

func (f *fixture) post(t *testing.T, token string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, f.http.URL+EndpointPath(agentID), strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"GetTask","params":{"id":"x"}}`))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func send(t *testing.T, c *a2aclient.Client, contextID, text string) *a2a.Task {
	t.Helper()
	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart(text))
	msg.ContextID = contextID
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := c.SendMessage(ctx, &a2a.SendMessageRequest{Message: msg})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	task, ok := out.(*a2a.Task)
	if !ok {
		t.Fatalf("result %T, want *a2a.Task", out)
	}
	return task
}

func TestBuildCardFromPersona(t *testing.T) {
	a := Agent{ID: "a1", Name: "Ops Bot", Description: "Answers ops", Prompts: []Prompt{{Title: "Status", Message: "What is the status?"}, {}}}
	card := BuildCard(a, "https://wick.example/")
	if card.Name != "Ops Bot" || card.Description != "Answers ops" || !card.Capabilities.Streaming {
		t.Fatalf("card = %+v", card)
	}
	if got := card.SupportedInterfaces[0].URL; got != "https://wick.example/integrations/a2a/a1" {
		t.Fatalf("url = %q", got)
	}
	if len(card.Skills) != 1 || card.Skills[0].Name != "Status" || card.Skills[0].Examples[0] != "What is the status?" {
		t.Fatalf("skills = %+v", card.Skills)
	}
	if empty := BuildCard(Agent{ID: "a2"}, ""); len(empty.Skills) != 1 || empty.Skills[0].ID != "chat" || empty.Name != "a2" {
		t.Fatalf("fallback card = %+v", empty)
	}
}

func TestAuth(t *testing.T) {
	f := newFixture(t)
	cases := []struct {
		name, token string
		want        int
	}{
		{"no key", "", http.StatusUnauthorized},
		{"wrong key", "wa2a_nope", http.StatusUnauthorized},
		{"right key", f.key, http.StatusOK},
		{"owner PAT", "pat-owner", http.StatusOK},
		{"stranger PAT", "pat-other", http.StatusUnauthorized},
		{"unlisted caller PAT", "pat-allowed", http.StatusUnauthorized},
	}
	for _, tc := range cases {
		if got := f.post(t, tc.token); got != tc.want {
			t.Errorf("%s: status %d, want %d", tc.name, got, tc.want)
		}
	}

	c := f.conns.m[agentID]
	c.AllowedCallers = []string{"user-allowed"}
	f.conns.m[agentID] = c
	if got := f.post(t, "pat-allowed"); got != http.StatusOK {
		t.Errorf("allowed caller PAT: status %d", got)
	}

	// Revoke: the key stops working at once; the owner's PAT still does.
	c.KeyHash, c.KeyHint = "", ""
	f.conns.m[agentID] = c
	if got := f.post(t, f.key); got != http.StatusUnauthorized {
		t.Errorf("revoked key: status %d", got)
	}
	if got := f.post(t, "pat-owner"); got != http.StatusOK {
		t.Errorf("owner PAT after revoke: status %d", got)
	}
}

func TestCardNeedsAuthUnlessPublic(t *testing.T) {
	f := newFixture(t)
	get := func(token string) int {
		req, _ := http.NewRequest(http.MethodGet, f.http.URL+CardPath(agentID), nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if got := get(""); got != http.StatusUnauthorized {
		t.Fatalf("private card without key: %d", got)
	}
	if got := get(f.key); got != http.StatusOK {
		t.Fatalf("private card with key: %d", got)
	}
	c := f.conns.m[agentID]
	c.PublicCard = true
	f.conns.m[agentID] = c
	if got := get(""); got != http.StatusOK {
		t.Fatalf("public card: %d", got)
	}
	if got := f.post(t, ""); got != http.StatusUnauthorized {
		t.Fatalf("public card must not open the endpoint: %d", got)
	}
}

func TestContextIDMapsToSession(t *testing.T) {
	f := newFixture(t)
	c := f.client(t, f.key)
	t1 := send(t, c, "ctx-a", "first")
	send(t, c, "ctx-a", "second")
	send(t, c, "ctx-b", "third")
	if t1.Status.State != a2a.TaskStateCompleted {
		t.Fatalf("state = %s", t1.Status.State)
	}
	if got := messageText(t1.Status.Message); got != "hello world" {
		t.Fatalf("reply = %q", got)
	}
	us := f.userSends()
	if len(us) != 3 {
		t.Fatalf("user sends = %d", len(us))
	}
	if us[0].sessionID != us[1].sessionID || us[0].sessionID != SessionID(agentID, "ctx-a") {
		t.Fatalf("same context, sessions %q / %q", us[0].sessionID, us[1].sessionID)
	}
	if us[2].sessionID == us[0].sessionID {
		t.Fatal("different context reused the session")
	}
	if f.dir.created[us[0].sessionID] != 2 || f.dir.created[us[2].sessionID] != 1 {
		t.Fatalf("ensure calls = %v", f.dir.created)
	}
	// One origin-context note per new session, none for the reuse.
	f.mu.Lock()
	notes := 0
	for _, s := range f.sends {
		if s.role == "system" {
			notes++
		}
	}
	f.mu.Unlock()
	if notes != 2 {
		t.Fatalf("context notes = %d, want 2", notes)
	}
}

func TestTurnRunsAsOwner(t *testing.T) {
	f := newFixture(t)
	c := f.conns.m[agentID]
	c.AllowedCallers = []string{"user-allowed"}
	f.conns.m[agentID] = c
	send(t, f.client(t, "pat-allowed"), "ctx", "hi")
	us := f.userSends()
	if len(us) != 1 || us[0].callerUserID != owner {
		t.Fatalf("caller user = %+v, want owner", us)
	}
	if us[0].sender != "user-allowed" || us[0].senderWick != "user-allowed" {
		t.Fatalf("sender = %+v", us[0])
	}
	send(t, f.client(t, f.key), "ctx2", "hi")
	if us := f.userSends(); us[1].senderWick != "" || us[1].callerUserID != owner {
		t.Fatalf("key turn = %+v", us[1])
	}
}

func TestStreamTranslatesEvents(t *testing.T) {
	f := newFixture(t)
	c := f.client(t, f.key)
	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("go"))
	var arts []*a2a.TaskArtifactUpdateEvent
	var states []a2a.TaskState
	for ev, err := range c.SendStreamingMessage(context.Background(), &a2a.SendMessageRequest{Message: msg}) {
		if err != nil {
			t.Fatal(err)
		}
		switch e := ev.(type) {
		case *a2a.TaskArtifactUpdateEvent:
			arts = append(arts, e)
		case *a2a.TaskStatusUpdateEvent:
			states = append(states, e.Status.State)
		}
	}
	if len(arts) != 2 || arts[0].Append || !arts[1].Append || arts[0].Artifact.ID != arts[1].Artifact.ID {
		t.Fatalf("artifacts = %+v", arts)
	}
	if len(states) == 0 || states[len(states)-1] != a2a.TaskStateCompleted {
		t.Fatalf("states = %v", states)
	}

	f.mu.Lock()
	f.reply = []event.AgentEvent{{Type: event.Error, ErrorMsg: "provider crashed"}}
	f.mu.Unlock()
	task := send(t, c, "ctx-err", "boom")
	if task.Status.State != a2a.TaskStateFailed || messageText(task.Status.Message) != "provider crashed" {
		t.Fatalf("failed task = %+v", task.Status)
	}
}

func TestUnavailableAgent(t *testing.T) {
	f := newFixture(t)
	a := f.dir.agents[agentID]
	a.Disabled = true
	f.dir.agents[agentID] = a
	if got := f.post(t, f.key); got != http.StatusGone {
		t.Fatalf("disabled agent: %d", got)
	}
	a.Disabled = false
	f.dir.agents[agentID] = a
	c := f.conns.m[agentID]
	c.Enabled = false
	f.conns.m[agentID] = c
	if got := f.post(t, f.key); got != http.StatusNotFound {
		t.Fatalf("disabled connection: %d", got)
	}
	// Deleted agent: 404, and its leftover connection goes with it.
	delete(f.dir.agents, agentID)
	if got := f.post(t, f.key); got != http.StatusNotFound {
		t.Fatalf("deleted agent: %d", got)
	}
	if _, ok := f.conns.m[agentID]; ok {
		t.Fatal("connection of a deleted agent survived")
	}
}

func TestProbe(t *testing.T) {
	f := newFixture(t)
	res := f.srv.Probe(context.Background(), agentID)
	if !res.OK || res.Reply != "pong" {
		t.Fatalf("probe = %+v", res)
	}
	if n := len(f.userSends()); n != 0 {
		t.Fatalf("probe woke the agent: %d sends", n)
	}
	delete(f.dir.agents, agentID)
	if res := f.srv.Probe(context.Background(), agentID); res.OK || res.Status != "card_failed" {
		t.Fatalf("probe of a deleted agent = %+v", res)
	}
}

func TestStoreKeepsOnlyHash(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&entity.AgentChannel{}); err != nil {
		t.Fatal(err)
	}
	st := NewStore(db)
	plain, hash, hint, err := NewKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Save(Connection{AgentID: "a1", OwnerUserID: owner, Enabled: true, KeyHash: hash, KeyHint: hint, AllowedCallers: []string{"u2"}}); err != nil {
		t.Fatal(err)
	}
	var row entity.AgentChannel
	if err := db.First(&row, "id = ?", RowID("a1")).Error; err != nil {
		t.Fatal(err)
	}
	if strings.Contains(row.Config, plain) || row.Type != RowType {
		t.Fatalf("row = %+v", row)
	}
	got, ok, err := st.Load("a1")
	if err != nil || !ok || !got.Enabled || got.OwnerUserID != owner || got.AllowedCallers[0] != "u2" || !VerifyKey(plain, got.KeyHash) {
		t.Fatalf("load = %+v %v %v", got, ok, err)
	}
	got.PublicCard, got.Enabled = true, false
	if err := st.Save(got); err != nil {
		t.Fatal(err)
	}
	if again, _, _ := st.Load("a1"); !again.PublicCard || again.Enabled {
		t.Fatalf("update lost: %+v", again)
	}
	if VerifyKey("wa2a_other", hash) || VerifyKey(hash, hash) {
		t.Fatal("VerifyKey accepted a wrong key")
	}
	if err := st.Delete("a1"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := st.Load("a1"); ok {
		t.Fatal("delete kept the row")
	}
}
