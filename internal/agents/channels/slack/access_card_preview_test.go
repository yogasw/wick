package slack

import (
	"encoding/json"
	"strings"
	"testing"

	slackgo "github.com/slack-go/slack"

	"github.com/yogasw/wick/internal/accessreq"
)

func TestLinkedThread(t *testing.T) {
	cases := []struct{ text, chat, ts string }{
		{"see <https://acme.slack.com/archives/C01TESTCHAN/p1700000000123456|thread>", "C01TESTCHAN", "1700000000.123456"},
		{"https://acme.slack.com/archives/C01TESTCHAN/p1700000099000001?thread_ts=1700000000.123456&cid=C01TESTCHAN", "C01TESTCHAN", "1700000000.123456"},
		{"no link here", "", ""},
	}
	for _, c := range cases {
		chat, ts := linkedThread(c.text)
		if chat != c.chat || ts != c.ts {
			t.Errorf("linkedThread(%q) = %s %s, want %s %s", c.text, chat, ts, c.chat, c.ts)
		}
	}
}

func TestMirrorTargetOnlyForOwnStarter(t *testing.T) {
	s := &Channel{}
	s.botUserID = "UBOT"
	starter := slackgo.Msg{User: "UBOT", Timestamp: "1700000100.000001",
		Text: "First check — <https://acme.slack.com/archives/C01MAIN/p1700000000123456|inquiry>"}
	if got := s.mirrorTarget(starter, "C01CARDS", "1700000100.000001"); got != [2]string{"C01MAIN", "1700000000.123456"} {
		t.Fatalf("own mirror starter: got %v", got)
	}
	human := starter
	human.User = "UHUMAN"
	if got := s.mirrorTarget(human, "C01CARDS", "1700000100.000001"); got[0] != "" {
		t.Fatalf("a thread a person opened stays put, got %v", got)
	}
	self := starter
	self.Text = "<https://acme.slack.com/archives/C01CARDS/p1700000100000001|this>"
	if got := s.mirrorTarget(self, "C01CARDS", "1700000100.000001"); got[0] != "" {
		t.Fatalf("a link to the thread itself is no mirror, got %v", got)
	}
}

func TestPayloadPreviewUnfoldsAndCuts(t *testing.T) {
	draft := `{"intents":[{"name":"greeting","reply":"Hi <there>"}]}`
	raw, _ := json.Marshal(map[string]any{"project_code": "proj_x", "draft": draft})
	it := accessreq.ItemView{ParamsJSON: string(raw)}
	got := payloadPreview(it, 2000)
	for _, want := range []string{`"project_code": "proj_x"`, `"name": "greeting"`, `"reply": "Hi <there>"`} {
		if !strings.Contains(got, want) {
			t.Errorf("preview misses %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, `\"`) {
		t.Errorf("nested JSON should be unfolded, not escaped:\n%s", got)
	}
	cut := payloadPreview(it, 20)
	if !strings.Contains(cut, "more characters") {
		t.Errorf("a long payload says it was cut:\n%s", cut)
	}
	if payloadPreview(accessreq.ItemView{}, 100) != "(no parameters)" {
		t.Error("empty params")
	}
}

func TestCardHasApproveAllAndPayload(t *testing.T) {
	v := sampleAccessView()
	raw, _ := json.Marshal(accessCardBlocks(v))
	s := string(raw)
	for _, want := range []string{accessApproveAll, "Approve all (2)", "page_id", "Why:", "confirm"} {
		if !strings.Contains(s, want) {
			t.Errorf("card misses %q", want)
		}
	}
	// The quick button counts the actions still waiting for a decision.
	v.Pending = 1
	v.Items[1].Status = accessreq.ItemOK
	raw, _ = json.Marshal(accessCardBlocks(v))
	if !strings.Contains(string(raw), "Approve once") {
		t.Error("one waiting action: the quick button approves it once")
	}
	if _, err := blocksFromJSON(accessCardBlocks(v)); err != nil {
		t.Fatalf("card blocks: %v", err)
	}
}

func TestApproveAllDecisions(t *testing.T) {
	ds, locked := approveAllDecisions(sampleAccessView())
	if len(ds) != 1 || ds[0].ItemID != "i1" || ds[0].Mode != accessreq.ModeOnce {
		t.Fatalf("decisions = %+v", ds)
	}
	if len(locked) != 1 || !strings.Contains(locked[0], "Action 2") {
		t.Fatalf("locked = %v", locked)
	}
}
