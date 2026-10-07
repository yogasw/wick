package view

import (
	"context"
	"strings"
	"testing"

	"github.com/yogasw/wick/pkg/entity"
)

// The Channels config page has many setting groups (Connection, Access
// Control, Agent Behaviour, Reaction Auto-Reply, Approval Gates, Routing).
// Every group card starts closed so the page reads as a list of headings,
// opened one at a time.
func TestChannelConfigGroupsStartCollapsed(t *testing.T) {
	rows := []entity.Config{
		{Key: "users_mode", Type: "dropdown", Options: "all|whitelist", Group: "Access Control|Who may trigger agents."},
		{Key: "ask_user_enabled", Type: "bool", Group: "Agent Behaviour"},
	}
	var b strings.Builder
	vm := ChannelConfigVM{Base: "/tools/agents", ChannelName: "Slack", ChannelSlug: "slack", Rows: rows, ActionBase: "/x"}
	if err := ChannelConfigPage(vm).Render(context.Background(), &b); err != nil {
		t.Fatalf("render: %v", err)
	}
	html := b.String()
	if n := strings.Count(html, "data-cfg-group"); n < 2 {
		t.Fatalf("want 2 group cards, got %d", n)
	}
	for _, chunk := range strings.Split(html, "<details")[1:] {
		head := chunk[:strings.Index(chunk, ">")]
		if strings.Contains(head, " open") {
			t.Errorf("group card renders open: <details%s>", head)
		}
	}
}
