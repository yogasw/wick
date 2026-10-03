package agents

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/teamlink"
)

func TestAppendHandoffWritesSystemTurn(t *testing.T) {
	layout := config.NewLayout(t.TempDir())
	if err := os.MkdirAll(layout.SessionDir("S1"), 0o755); err != nil {
		t.Fatal(err)
	}
	h := teamlink.Handoff{From: "captain", To: "anton", ToID: "ag-2", TaskID: "t1", ContextID: "c1", State: a2a.TaskStateCompleted}
	if err := appendHandoff(layout, "S1", handoffTurn(h, time.Unix(100, 0))); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(layout.SessionConversation("S1"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var turn struct {
		Role, Kind, Text string
		Extras           map[string]string
	}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if !strings.Contains(line, "mention_handoff") {
			continue
		}
		if err := json.Unmarshal([]byte(line), &turn); err != nil {
			t.Fatal(err)
		}
	}
	if turn.Role != "system" || turn.Kind != "mention_handoff" {
		t.Fatalf("turn = %+v, want a mention_handoff system turn", turn)
	}
	if turn.Extras["to_agent_id"] != "ag-2" || turn.Extras["to"] != "anton" || turn.Extras["state"] != string(a2a.TaskStateCompleted) {
		t.Fatalf("extras = %v", turn.Extras)
	}
}

func TestAppendHandoffNoLayoutIsNoop(t *testing.T) {
	if err := appendHandoff(config.Layout{}, "S1", handoffTurn(teamlink.Handoff{}, time.Now())); err != nil {
		t.Fatal(err)
	}
}

func TestPublishHandoffPushesLiveEvent(t *testing.T) {
	b := NewBroadcaster()
	ch, unsub := b.Subscribe("S1")
	defer unsub()
	h := teamlink.Handoff{From: "captain", To: "anton", ToID: "ag-2", TaskID: "t1", State: a2a.TaskStateWorking}
	publishHandoff(b, "S1", handoffTurn(h, time.Unix(100, 0)))
	select {
	case ev := <-ch:
		if ev.Type != "mention_handoff" || !strings.Contains(ev.Data, `"task_id":"t1"`) || !strings.Contains(ev.Data, `"kind":"mention_handoff"`) {
			t.Fatalf("event = %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("no live mention_handoff event")
	}
	publishHandoff(nil, "S1", handoffTurn(h, time.Now())) // no broadcaster: no panic
}
