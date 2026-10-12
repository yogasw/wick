package agents

import (
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/store"
	"github.com/yogasw/wick/internal/agents/teamlink"
)

func TestPlanTicketSessionMessage(t *testing.T) {
	yogaCap := teamlink.Peer{ID: "yc", OwnerID: "yoga", Handle: "captain", Name: "Captain", IsCaptain: true}
	research := teamlink.Peer{ID: "yr", OwnerID: "yoga", Handle: "research", Name: "Research"}
	writer := teamlink.Peer{ID: "yw", OwnerID: "yoga", Handle: "writer", Name: "Writer"}
	off := teamlink.Peer{ID: "yo", OwnerID: "yoga", Handle: "off", Disabled: true}
	yoga := []teamlink.Peer{yogaCap, research, writer, off}
	hanaCap := teamlink.Peer{ID: "hc", OwnerID: "hana", Handle: "captain", Name: "Captain", IsCaptain: true}
	hana := []teamlink.Peer{hanaCap}

	t.Run("owner, no @: the home agent", func(t *testing.T) {
		p := planTicketSessionMessage("check this", "yoga", "yc", "yoga", "yoga", yoga)
		if !p.Home || p.HasTarget {
			t.Fatalf("%+v", p)
		}
	})
	t.Run("owner naming the home agent: still the home agent", func(t *testing.T) {
		if p := planTicketSessionMessage("@captain check", "yoga", "yc", "yoga", "yoga", yoga); !p.Home {
			t.Fatalf("%+v", p)
		}
	})
	t.Run("owner naming another own agent: only that one", func(t *testing.T) {
		p := planTicketSessionMessage("@research dig", "yoga", "yc", "yoga", "yoga", yoga)
		if p.Home || !p.HasTarget || p.Target.ID != "yr" || p.Captain {
			t.Fatalf("%+v", p)
		}
	})
	t.Run("own owner/handle form is an own mention", func(t *testing.T) {
		p := planTicketSessionMessage("@yoga/research dig", "yoga", "yc", "yoga", "yoga", yoga)
		if !p.HasTarget || p.Target.ID != "yr" || len(p.Foreign) != 0 {
			t.Fatalf("%+v", p)
		}
	})
	t.Run("two agents named: one answers, never a broadcast", func(t *testing.T) {
		p := planTicketSessionMessage("@research dig\n@writer draft", "yoga", "yc", "yoga", "yoga", yoga)
		if !p.HasTarget || p.Target.ID != "yr" || len(p.Extra) != 1 || p.Extra[0] != "writer" {
			t.Fatalf("%+v", p)
		}
	})
	t.Run("someone else, no @: their own Captain, flagged to join", func(t *testing.T) {
		p := planTicketSessionMessage("hi", "yoga", "yc", "hana", "hana", hana)
		if p.Home || !p.HasTarget || p.Target.ID != "hc" || !p.Captain {
			t.Fatalf("%+v", p)
		}
	})
	t.Run("someone else with no Captain: the project default, as them", func(t *testing.T) {
		p := planTicketSessionMessage("hi", "yoga", "yc", "ana", "ana", nil)
		if p.Home || !p.HasTarget || !p.Default {
			t.Fatalf("%+v", p)
		}
	})
	t.Run("someone else naming the owner's agent: refused, nobody runs", func(t *testing.T) {
		p := planTicketSessionMessage("@yoga/research dig", "yoga", "yc", "hana", "hana", hana)
		if p.Home || p.HasTarget || len(p.Foreign) != 1 || p.Foreign[0].Owner != "yoga" {
			t.Fatalf("%+v", p)
		}
	})
	t.Run("someone else's @captain is their own Captain", func(t *testing.T) {
		p := planTicketSessionMessage("@captain look", "yoga", "yc", "hana", "hana", hana)
		if !p.HasTarget || p.Target.ID != "hc" || p.Captain {
			t.Fatalf("%+v", p)
		}
	})
	t.Run("unknown or disabled names run nobody", func(t *testing.T) {
		if p := planTicketSessionMessage("@nobody hi", "yoga", "yc", "hana", "hana", hana); p.Home || p.HasTarget || len(p.Unknown) != 1 {
			t.Fatalf("%+v", p)
		}
		if p := planTicketSessionMessage("@off hi", "yoga", "yc", "yoga", "yoga", yoga); p.Home || p.HasTarget || len(p.Refused) != 1 {
			t.Fatalf("%+v", p)
		}
	})
	t.Run("the owner's other @words stay with the home agent (sub-agent roles)", func(t *testing.T) {
		if p := planTicketSessionMessage("@investigator check", "yoga", "yc", "yoga", "yoga", yoga); !p.Home {
			t.Fatalf("%+v", p)
		}
	})
	t.Run("Agents side: a project-default home answers its owner only", func(t *testing.T) {
		if p := planTicketSessionMessage("hi", "yoga", "", "yoga", "yoga", yoga); !p.Home {
			t.Fatalf("%+v", p)
		}
		if p := planTicketSessionMessage("hi", "yoga", "", "hana", "hana", hana); p.Home || p.Target.ID != "hc" {
			t.Fatalf("%+v", p)
		}
	})
}

func TestIsTicketSession(t *testing.T) {
	base := session.Meta{TicketID: "T-1", ProjectID: "p"}
	if !isTicketSession(session.Session{Meta: base}) {
		t.Fatal("plain ticket session")
	}
	for name, edit := range map[string]func(*session.Meta){
		"no ticket": func(m *session.Meta) { m.TicketID = "" },
		"room":      func(m *session.Meta) { m.TicketRoom = &session.TicketRoom{} },
		"group":     func(m *session.Meta) { m.AgentGroup = &session.AgentGroup{} },
		"backing":   func(m *session.Meta) { m.GroupSessionID = "g" },
	} {
		m := base
		edit(&m)
		if isTicketSession(session.Session{Meta: m}) {
			t.Fatalf("%s counted as a ticket session", name)
		}
	}
}

func TestTicketSessionTurnOwnerAndRefusal(t *testing.T) {
	owner := ticketSessionTurnOwner("yoga")
	if got := owner(store.ConversationTurn{Role: "assistant", Text: "home reply"}); got != "yoga" {
		t.Fatalf("home turn owner %q", got)
	}
	if got := owner(store.ConversationTurn{Role: "assistant", Speaker: &store.Speaker{AgentID: "hc", OwnerID: "hana"}}); got != "hana" {
		t.Fatalf("speaker owner %q", got)
	}
	if got := owner(store.ConversationTurn{Role: "system"}); got != "" {
		t.Fatalf("system owner %q", got)
	}
	hist := []store.ConversationTurn{{Role: "assistant", Speaker: &store.Speaker{AgentID: "yr", Handle: "research", Name: "Research", OwnerID: "yoga", OwnerHandle: "yoga"}}}
	own := []teamlink.Peer{{ID: "hc", Handle: "captain", IsCaptain: true}}
	got := foreignRefusal(foreignMention{Owner: "yoga", Handle: "research"}, own, hist)
	if got != "Research only takes instructions from @yoga. Ask them, or mention your own @captain." {
		t.Fatalf("refusal %q", got)
	}
	if got := foreignRefusal(foreignMention{Owner: "yoga", Handle: "writer"}, own, hist); !strings.HasPrefix(got, "@writer only takes instructions from @yoga.") {
		t.Fatalf("unnamed refusal %q", got)
	}
	if !spokeIn(hist, "yr", "yoga") || spokeIn(hist, "yr", "hana") {
		t.Fatal("spokeIn")
	}
}
