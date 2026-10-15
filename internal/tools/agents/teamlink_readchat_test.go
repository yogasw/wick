package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	agentstore "github.com/yogasw/wick/internal/agents/store"
	"github.com/yogasw/wick/internal/agents/teamlink"
	"github.com/yogasw/wick/internal/agents/ticket"
	teamlinkconn "github.com/yogasw/wick/internal/connectors/team-link"
	ticketconn "github.com/yogasw/wick/internal/connectors/tickets"
	"github.com/yogasw/wick/pkg/connector"
)

// writeTurns appends n alternating user/assistant turns to id's transcript.
func writeTurns(t *testing.T, id string, n int) {
	t.Helper()
	f, err := os.OpenFile(globalLayout.SessionConversation(id), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	base := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	for i := 0; i < n; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		line, _ := json.Marshal(agentstore.ConversationTurn{Role: role, Text: fmt.Sprintf("turn %d", i), Timestamp: base.Add(time.Duration(i) * time.Minute)})
		if _, err := f.Write(append(line, '\n')); err != nil {
			t.Fatal(err)
		}
	}
}

type pageMessage struct {
	Index int    `json:"index"`
	Text  string `json:"text"`
}

func pageMessages(t *testing.T, res map[string]any) []pageMessage {
	t.Helper()
	raw, err := json.Marshal(res["messages"])
	if err != nil {
		t.Fatal(err)
	}
	var out []pageMessage
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// readChat runs the team read_chat op as callerAgent from callerSession,
// wired the way server.go wires it.
func readChat(t *testing.T, callerAgent, callerSession string, in map[string]string) (map[string]any, error) {
	t.Helper()
	hub := NewTeamLinkHub(globalTeam, func(context.Context, string, string) error { return nil })
	d := teamlinkconn.Deps{
		Hub:         func() *teamlink.Hub { return hub },
		AgentOf:     func(context.Context, string) string { return callerAgent },
		SessionUser: SessionUserOf,
		ReadChat: func(sessionID string, offset, limit int, order, detail string) (map[string]any, error) {
			return ticketconn.ReadConversation(globalLayout, sessionID, ticketconn.ConversationPage{Offset: offset, Limit: limit, Order: order, Detail: detail})
		},
	}
	var op connector.Operation
	for _, cat := range teamlinkconn.Operations(d) {
		for _, o := range cat.Ops {
			if o.Key == "read_chat" {
				op = o
			}
		}
	}
	if op.Execute == nil {
		t.Fatal("read_chat op not registered")
	}
	c := connector.NewCtx(context.Background(), "row", nil, in, nil, nil, nil)
	c.SetSessionID(callerSession)
	res, err := op.Execute(c)
	if err != nil {
		return nil, err
	}
	return res.(map[string]any), nil
}

// read_chat lets an overseeing agent read a session of an agent it may
// message there — the owner path, or one ticket — and nothing else.
func TestTeamReadChatAccess(t *testing.T) {
	withTeamWorld(t)
	seedTeamProject(t, "p1", "u1")
	seedTeamProject(t, "p2", "u1")
	seedTeamAgent(t, "u1", "franky", "p1")
	verifier := seedTeamAgent(t, "u1", "verifier", "p2")
	newSlackSession(t, "verifier-chat", "p2", "u1", "")
	newSlackSession(t, "slack-card", "p1", "u1", "")
	newSlackSession(t, "slack-req", "p1", "u2", "")
	writeTurns(t, "slack-card", 3)
	writeTurns(t, "slack-req", 2)

	res, err := readChat(t, verifier.ID, "verifier-chat", map[string]string{"to": "@franky", "chat": "slack-card"})
	if err != nil {
		t.Fatalf("owner path: %v", err)
	}
	if res["session_id"] != "slack-card" || res["total"] != 3 || res["returned"] != 3 {
		t.Fatalf("owner path result: %+v", res)
	}

	if _, err := readChat(t, verifier.ID, "verifier-chat", map[string]string{"to": "@franky", "chat": "slack-req"}); !errors.Is(err, teamlink.ErrUnknownChat) {
		t.Fatalf("another person's session without a ticket: %v", err)
	}
	if _, err := readChat(t, verifier.ID, "verifier-chat", map[string]string{"to": "@franky", "chat": "missing-1"}); err == nil || !strings.Contains(err.Error(), "session missing-1 not found") {
		t.Fatalf("missing session: %v", err)
	}
	if _, err := readChat(t, verifier.ID, "verifier-chat", map[string]string{"to": "@nobody", "chat": "slack-card"}); !errors.Is(err, teamlink.ErrUnknownHandle) {
		t.Fatalf("unknown agent: %v", err)
	}
	if _, err := readChat(t, verifier.ID, "verifier-chat", map[string]string{"to": "@verifier", "chat": "slack-card"}); !errors.Is(err, teamlink.ErrUnknownChat) {
		t.Fatalf("session of another agent named as own: %v", err)
	}

	tk, err := ticket.Create(globalLayout, ticket.CreateOptions{ProjectID: "p1", Title: "inquiry"})
	if err != nil {
		t.Fatal(err)
	}
	for _, sid := range []string{"verifier-chat", "slack-req"} {
		if err := ticket.AttachSession(globalLayout, "p1", tk.ID, sid); err != nil {
			t.Fatal(err)
		}
	}
	res, err = readChat(t, verifier.ID, "verifier-chat", map[string]string{"to": "@franky", "chat": "slack-req"})
	if err != nil {
		t.Fatalf("same ticket: %v", err)
	}
	if res["total"] != 2 {
		t.Fatalf("same ticket result: %+v", res)
	}
}

// read_chat pages like ticket_conversation_read, capped at 50 a page.
func TestTeamReadChatPaging(t *testing.T) {
	withTeamWorld(t)
	seedTeamProject(t, "p1", "u1")
	seedTeamProject(t, "p2", "u1")
	seedTeamAgent(t, "u1", "franky", "p1")
	verifier := seedTeamAgent(t, "u1", "verifier", "p2")
	newSlackSession(t, "verifier-chat", "p2", "u1", "")
	newSlackSession(t, "slack-card", "p1", "u1", "")
	writeTurns(t, "slack-card", 60)

	res, err := readChat(t, verifier.ID, "verifier-chat", map[string]string{"to": "franky", "chat": "slack-card"})
	if err != nil {
		t.Fatal(err)
	}
	if res["limit"] != 20 || res["returned"] != 20 || res["has_more"] != true || res["next_offset"] != 20 || res["order"] != "newest" || res["detail"] != "final" {
		t.Fatalf("default page: %+v", res)
	}
	if msgs := pageMessages(t, res); msgs[0].Index != 40 || msgs[19].Text != "turn 59" {
		t.Fatalf("newest page reads forwards from 40: first %d, last %q", msgs[0].Index, msgs[19].Text)
	}

	res, err = readChat(t, verifier.ID, "verifier-chat", map[string]string{"to": "franky", "chat": "slack-card", "limit": "100"})
	if err != nil {
		t.Fatal(err)
	}
	if res["limit"] != 50 || res["returned"] != 50 || res["next_offset"] != 50 {
		t.Fatalf("limit cap: %+v", res)
	}

	res, err = readChat(t, verifier.ID, "verifier-chat", map[string]string{"to": "franky", "chat": "slack-card", "order": "oldest", "offset": "50", "limit": "20"})
	if err != nil {
		t.Fatal(err)
	}
	if res["returned"] != 10 || res["has_more"] != nil {
		t.Fatalf("last oldest page: %+v", res)
	}
	if msgs := pageMessages(t, res); msgs[0].Text != "turn 50" {
		t.Fatalf("oldest page from 50: %q", msgs[0].Text)
	}
}
