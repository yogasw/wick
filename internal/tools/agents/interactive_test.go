package agents

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/askuser"
	"github.com/yogasw/wick/internal/agents/config"
)

func TestAnswerLabel(t *testing.T) {
	req := askuser.AskRequest{
		Options: []askuser.Option{{Label: "Ship it", Value: "ship"}},
		Fields:  []askuser.Field{{Key: "token", Type: "secret"}, {Key: "env"}},
	}
	cases := []struct {
		ans  askuser.Answer
		want string
	}{
		{askuser.Answer{Value: "ship"}, "Ship it"},
		{askuser.Answer{Value: "other"}, "other"},
		{askuser.Answer{Text: "later"}, "later"},
		{askuser.Answer{Values: map[string]string{"token": "s3cr3t", "env": "prod"}}, "env: prod, token: ••••"},
		{askuser.Answer{Values: map[string]string{}}, "submitted"},
	}
	for _, c := range cases {
		if got := answerLabel(req, c.ans); got != c.want {
			t.Errorf("answerLabel(%+v) = %q, want %q", c.ans, got, c.want)
		}
	}
}

// The question and its answer stay in the thread; a secret never does.
func TestRecordAskWritesInputRequestTurns(t *testing.T) {
	layout := config.Layout{BaseDir: t.TempDir()}
	prevL, prevB := globalLayout, globalBcast
	globalLayout, globalBcast = layout, nil
	defer func() { globalLayout, globalBcast = prevL, prevB }()

	req := askuser.AskRequest{ID: "ask1", SessionID: "S1", Question: "Deploy?", AgentName: "main",
		Fields: []askuser.Field{{Key: "token", Type: "secret", Value: "prefilled-secret"}}}
	RecordAskRequest(req)
	RecordAskSettled(req, askuser.Answer{Values: map[string]string{"token": "typed-secret"}}, askuser.OutcomeAnswered)

	raw, err := os.ReadFile(layout.SessionConversation("S1"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	if strings.Contains(body, "prefilled-secret") || strings.Contains(body, "typed-secret") {
		t.Fatalf("secret written to thread: %s", body)
	}
	lines := strings.Split(strings.TrimSpace(body), "\n")
	var states []string
	for _, l := range lines {
		var turn struct {
			Kind   string            `json:"kind"`
			Text   string            `json:"text"`
			Extras map[string]string `json:"extras"`
		}
		if json.Unmarshal([]byte(l), &turn) != nil || turn.Kind != "input_request" {
			continue
		}
		if turn.Extras["ask_id"] != "ask1" {
			t.Fatalf("ask_id = %v", turn.Extras)
		}
		states = append(states, turn.Extras["state"]+"|"+turn.Text)
	}
	if got := strings.Join(states, " / "); got != "pending|Deploy? / answered|answered: token: ••••" {
		t.Fatalf("turns = %q", got)
	}
}

func TestAskPendingInChecksSession(t *testing.T) {
	m := askuser.NewManager(askuser.Options{})
	go func() { _, _ = m.Ask(askuser.Question{SessionID: "mine", Question: "q", Timeout: time.Second}, nil) }()
	var id string
	for i := 0; i < 200 && id == ""; i++ {
		if p := m.PendingFor("mine"); len(p) == 1 {
			id = p[0].ID
		}
		time.Sleep(time.Millisecond)
	}
	if id == "" {
		t.Fatal("ask never pending")
	}
	if !askPendingIn(m, "mine", id) || askPendingIn(m, "theirs", id) {
		t.Fatal("ask id must only resolve in its own session")
	}
	m.Resolve(id, askuser.Answer{Value: "x"})
}
