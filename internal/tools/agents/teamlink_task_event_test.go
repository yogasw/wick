package agents

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/remote"
	"github.com/yogasw/wick/internal/agents/teamlink"
)

// A change of a team task reaches the SENDING session's viewers as a
// team_task event carrying the task as the team-tasks list shows it.
func TestTeamHubPublishesTaskChangeToSender(t *testing.T) {
	prevB, prevL, prevF := globalBcast, globalLayout, remote.OnFollowUp
	t.Cleanup(func() { globalBcast, globalLayout, remote.OnFollowUp = prevB, prevL, prevF })
	globalBcast, globalLayout = NewBroadcaster(), config.Layout{}
	sender, unsub := globalBcast.Subscribe("S-sender")
	defer unsub()
	other, unsubOther := globalBcast.Subscribe("S-other")
	defer unsubOther()

	hub := NewTeamLinkHub(nil, nil)
	if hub.OnTaskChange == nil {
		t.Fatal("hub has no OnTaskChange")
	}
	hub.OnTaskChange("S-sender", teamlink.TaskView{TaskID: "t1", ToHandle: "anton", State: "input_required", NeedsYou: true})

	select {
	case ev := <-sender:
		var got teamlink.TaskView
		if ev.Type != evTeamTask || json.Unmarshal([]byte(ev.Data), &got) != nil {
			t.Fatalf("event = %+v", ev)
		}
		if got.TaskID != "t1" || got.State != "input_required" || !got.NeedsYou || got.ToHandle != "anton" {
			t.Fatalf("task = %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("no team_task event")
	}
	select {
	case ev := <-other:
		t.Fatalf("another session got %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestPublishTeamTaskNoBroadcasterOrSession(t *testing.T) {
	publishTeamTask(nil, "S1", teamlink.TaskView{TaskID: "t1"})
	publishTeamTask(NewBroadcaster(), "", teamlink.TaskView{TaskID: "t1"})
}
