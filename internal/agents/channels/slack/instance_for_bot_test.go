package slack

import (
	"testing"

	agentchannels "github.com/yogasw/wick/internal/agents/channels"
	agentconfig "github.com/yogasw/wick/internal/agents/config"
)

// Every Slack instance reports Name() == "slack", so Registry.ChannelByName
// hands back whichever was registered first. Anything that needs a
// SPECIFIC bot — a workflow action replying as the bot that was talked to,
// the connector's "Sent using @bot" footer — has to match on bot user id.
func TestInstanceForBot(t *testing.T) {
	reg := agentchannels.NewRegistry()
	owner := NewWithOwner(agentconfig.SlackChannelConfig{}, "")
	botA := NewWithOwner(agentconfig.SlackChannelConfig{}, "user-a")
	botB := NewWithOwner(agentconfig.SlackChannelConfig{}, "user-b")
	unresolved := NewWithOwner(agentconfig.SlackChannelConfig{}, "user-c")
	owner.botUserID = "U0OWNER"
	botA.botUserID = "U0AAA"
	botB.botUserID = "U0BBB"
	// unresolved keeps botUserID == "" — auth.test hasn't landed yet.
	reg.AddKeyed("slack:__owner__", owner, nil)
	reg.AddKeyed("slack:user-a", botA, nil)
	reg.AddKeyed("slack:user-b", botB, nil)
	reg.AddKeyed("slack:user-c", unresolved, nil)

	cases := []struct {
		name  string
		botID string
		want  *Channel
	}{
		{"first registered", "U0OWNER", owner},
		{"middle", "U0AAA", botA},
		{"last", "U0BBB", botB},
		{"unknown id", "U0NOPE", nil},
		{"empty id never matches an unresolved instance", "", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := InstanceForBot(reg, tc.botID); got != tc.want {
				t.Errorf("InstanceForBot(%q) = %p, want %p", tc.botID, got, tc.want)
			}
		})
	}

	if got := InstanceForBot(nil, "U0AAA"); got != nil {
		t.Error("nil registry should return nil, not panic")
	}
}
