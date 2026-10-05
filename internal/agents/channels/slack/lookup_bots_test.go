package slack

import (
	"testing"

	slackgo "github.com/slack-go/slack"
)

// Workflow Builder bots share their workflow's name with the app they mimic,
// so the picker must tell them apart and offer the app first.
func TestBotItemsMarksWorkflowBotsAndListsAppsFirst(t *testing.T) {
	users := []slackgo.User{
		{ID: "U_WF1", Name: "wf_bot_a0b8vjflq1l", RealName: "Wabster", IsBot: true},
		{ID: "U_APP", Name: "wabster", RealName: "Wabster", IsBot: true},
		{ID: "U_GONE", Name: "wabster_old", RealName: "Wabster", IsBot: true, Deleted: true},
		{ID: "U_HUMAN", Name: "wab", RealName: "Wab Human"},
		{ID: "U_OTHER", Name: "deploy", RealName: "deployment-waba", IsBot: true},
	}
	got := botItems(users, "wab")
	want := []string{"U_APP:Wabster", "U_OTHER:deployment-waba", "U_WF1:Wabster (workflow)"}
	if len(got) != len(want) {
		t.Fatalf("got %d items %+v, want %v", len(got), got, want)
	}
	for i, w := range want {
		if g := got[i].ID + ":" + got[i].Name; g != w {
			t.Errorf("item %d = %q, want %q", i, g, w)
		}
	}
}
