package teamlink

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/teamlink"
	"github.com/yogasw/wick/pkg/connector"
)

type dir struct{}

var peers = []teamlink.Peer{
	{ID: "a-cap", OwnerID: "u1", Handle: "captain", Name: "Yoga Bot", IsCaptain: true},
	{ID: "a-anton", OwnerID: "u1", Handle: "anton", Name: "Anton"},
}

func (dir) Peers(_ context.Context, owner string) ([]teamlink.Peer, error) { return peers, nil }
func (dir) Get(_ context.Context, id string) (teamlink.Peer, error) {
	for _, p := range peers {
		if p.ID == id {
			return p, nil
		}
	}
	return teamlink.Peer{}, errors.New("nope")
}

type turns struct{ gate chan struct{} }

func (t turns) Run(_ context.Context, a teamlink.Peer, text string) (string, string, error) {
	if t.gate != nil {
		<-t.gate
	}
	return "sess-" + a.ID, "anton: got it", nil
}

type notify struct {
	mu  sync.Mutex
	got []string
}

func (n *notify) Deliver(_ context.Context, s, text string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.got = append(n.got, s+"|"+text)
	return nil
}
func (n *notify) Audit(context.Context, string, teamlink.Handoff) {}

func deps(hub *teamlink.Hub) Deps {
	return Deps{
		Hub: func() *teamlink.Hub { return hub },
		AgentOf: func(_ context.Context, s string) string {
			if s == "sess-cap" {
				return "a-cap"
			}
			return ""
		},
	}
}

func call(t *testing.T, d Deps, fn func(Deps, *connector.Ctx) (any, error), session string, in map[string]string) (any, error) {
	t.Helper()
	c := connector.NewCtx(context.Background(), "row", nil, in, nil, nil, nil)
	c.SetSessionID(session)
	return fn(d, c)
}

func TestMessageSyncAndGetTask(t *testing.T) {
	d := deps(teamlink.NewHub(dir{}, turns{}, &notify{}))
	out, err := call(t, d, Deps.message, "sess-cap", map[string]string{"to": "@anton", "message": "check the 401s"})
	if err != nil {
		t.Fatal(err)
	}
	res := out.(*teamlink.Result)
	if res.State != "completed" || res.ReplyText != "anton: got it" {
		t.Fatalf("result = %+v", res)
	}
	got, err := call(t, d, Deps.getTask, "sess-cap", map[string]string{"task_id": res.TaskID})
	if err != nil || got.(*teamlink.Result).State != "completed" {
		t.Fatalf("get_task = %+v, %v", got, err)
	}
}

func TestMessageAsyncDelivers(t *testing.T) {
	gate := make(chan struct{})
	n := &notify{}
	hub := teamlink.NewHub(dir{}, turns{gate: gate}, n)
	hub.SetQuickWait(20 * time.Millisecond)
	d := deps(hub)
	// wait_seconds is gone: an old prompt still sending it is not refused,
	// and it does not stretch the wait.
	start := time.Now()
	out, err := call(t, d, Deps.message, "sess-cap", map[string]string{"to": "anton", "message": "slow", "wait_seconds": "150"})
	if err != nil || out.(*teamlink.Result).State != "working" || time.Since(start) > 5*time.Second {
		t.Fatalf("result = %+v, %v after %s", out, err, time.Since(start))
	}
	listed, err := call(t, d, Deps.listTasks, "sess-cap", nil)
	if tasks := listed.(map[string]any)["tasks"].([]teamlink.TaskView); err != nil || len(tasks) != 1 || tasks[0].State != "working" {
		t.Fatalf("list_tasks = %+v, %v", listed, err)
	}
	close(gate)
	for i := 0; i < 200; i++ {
		n.mu.Lock()
		k := len(n.got)
		n.mu.Unlock()
		if k == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("late reply never delivered to the caller session")
}

func TestMessageRefusesNonTeamSession(t *testing.T) {
	d := deps(teamlink.NewHub(dir{}, turns{}, &notify{}))
	if _, err := call(t, d, Deps.message, "plain-session", map[string]string{"to": "anton", "message": "hi"}); !errors.Is(err, teamlink.ErrNotTeamSession) {
		t.Fatalf("err = %v", err)
	}
}

// new_chat reaches the hub: turns here cannot open a chat, so it is refused.
func TestMessagePassesNewChat(t *testing.T) {
	d := deps(teamlink.NewHub(dir{}, turns{}, &notify{}))
	if _, err := call(t, d, Deps.message, "sess-cap", map[string]string{"to": "anton", "message": "hi", "new_chat": "true"}); !errors.Is(err, teamlink.ErrNewChatUnsupported) {
		t.Fatalf("err = %v", err)
	}
}
