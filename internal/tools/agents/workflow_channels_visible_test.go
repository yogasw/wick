package agents

import (
	"testing"

	wfchannel "github.com/yogasw/wick/internal/agents/workflow/channel"
)

// A person's legacy bot and their Team agents' bots are all listed; other
// people's bots of the same type stay hidden behind the caller's own.
func TestVisibleChannelInstancesListsEveryOwnBot(t *testing.T) {
	rows := []wfchannel.Info{
		{Name: "slack", InstanceKey: "slack:other", OwnerUserID: "other"},
		{Name: "slack", InstanceKey: "slack:me", OwnerUserID: "me"},
		{Name: "slack", InstanceKey: "slack-agent:yelan", OwnerUserID: "me"},
		{Name: "telegram", InstanceKey: "telegram:a", OwnerUserID: "a"},
		{Name: "telegram", InstanceKey: "telegram:b", OwnerUserID: "b"},
	}

	got := visibleChannelInstances(rows, "me")

	want := []string{"slack:me", "slack-agent:yelan", "telegram:a"}
	if len(got) != len(want) {
		t.Fatalf("got %d rows %v, want %v", len(got), got, want)
	}
	for i, k := range want {
		if got[i].InstanceKey != k {
			t.Errorf("row %d = %q, want %q", i, got[i].InstanceKey, k)
		}
	}
}

// Two instances of one type need two drills, or the second overwrites the
// first in the palette.
func TestChannelDrillKeyIsPerInstance(t *testing.T) {
	a := channelDrillKey("channel-ops:", wfchannel.Info{Name: "slack", InstanceKey: "slack:me"})
	b := channelDrillKey("channel-ops:", wfchannel.Info{Name: "slack", InstanceKey: "slack-agent:yelan"})
	if a == b {
		t.Fatalf("both instances got drill key %q", a)
	}
	if k := channelDrillKey("channel-ops:", wfchannel.Info{Name: "cron"}); k != "channel-ops:cron" {
		t.Errorf("instance-less key = %q, want channel-ops:cron", k)
	}
}
