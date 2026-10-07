package slack

import "testing"

// A schedule's Slack thread opens with a top-level post (no thread_ts) and
// is keyed and bound like any thread of the instance; an Instant agent's
// post carries its name.
func TestOpenThread(t *testing.T) {
	f := newFakeSlack(t)
	c := &Channel{api: f.client(), turns: map[string]*turn{}, sessionPrefix: "slackagent-a1-"}

	key, b, err := c.OpenThread("C1", "⏰ Scheduled “Digest”", Persona{AgentID: "a1", Username: "Alpha"})
	if err != nil {
		t.Fatal(err)
	}
	if key != "slackagent-a1-1700000000.000200" {
		t.Errorf("key = %q", key)
	}
	if b.Channel != "slack" || b.ChatID != "C1" || b.ThreadID != "1700000000.000200" || b.Instance != "slackagent-a1-" {
		t.Errorf("binding = %+v", b)
	}
	posts := f.posts()
	if len(posts) != 1 || posts[0].Get("thread_ts") != "" || posts[0].Get("channel") != "C1" || posts[0].Get("username") != "Alpha" {
		t.Fatalf("posts = %v", posts)
	}
	if p, ok := c.personaFor(key); !ok || p.Username != "Alpha" {
		t.Errorf("persona = %+v %v", p, ok)
	}

	if _, _, err := c.OpenThread("", "x", Persona{}); err == nil {
		t.Error("empty channel accepted")
	}
	if _, _, err := (&Channel{}).OpenThread("C1", "x", Persona{}); err == nil {
		t.Error("a bot without a client posted")
	}
}
