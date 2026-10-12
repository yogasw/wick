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
	yoga := []teamlink.Peer{yogaCap, research}
	hanaCap := teamlink.Peer{ID: "hc", OwnerID: "hana", Handle: "captain", Name: "Captain", IsCaptain: true}
	hanaRes := teamlink.Peer{ID: "hr", OwnerID: "hana", Handle: "research", Name: "Research"}
	hanaWriter := teamlink.Peer{ID: "hw", OwnerID: "hana", Handle: "writer", Name: "Writer"}
	hanaOff := teamlink.Peer{ID: "ho", OwnerID: "hana", Handle: "off", Disabled: true}
	hana := []teamlink.Peer{hanaCap, hanaRes, hanaWriter, hanaOff}

	t.Run("owner: always the home agent, whatever is named", func(t *testing.T) {
		// Their @mentions go through the existing Team hand-off.
		for _, text := range []string{"check this", "@captain check", "@research dig", "@yoga/research dig", "@hana/captain hi", "@nobody hi"} {
			if p := planTicketSessionMessage(text, "yoga", "yoga", "yoga", yoga); !p.Home || p.HasTarget || len(p.Unknown)+len(p.Foreign) != 0 {
				t.Fatalf("%q: %+v", text, p)
			}
		}
	})
	t.Run("owner naming a sub-agent role and an own agent: nothing refused", func(t *testing.T) {
		p := planTicketSessionMessage("@investigator check\n@research dig", "yoga", "yoga", "yoga", yoga)
		if !p.Home || len(p.Unknown) != 0 || len(p.Refused) != 0 {
			t.Fatalf("%+v", p)
		}
	})
	t.Run("someone else naming an own agent: only that one", func(t *testing.T) {
		p := planTicketSessionMessage("@research dig", "yoga", "hana", "hana", hana)
		if p.Home || !p.HasTarget || p.Target.ID != "hr" || p.Captain {
			t.Fatalf("%+v", p)
		}
	})
	t.Run("own owner/handle form is an own mention", func(t *testing.T) {
		p := planTicketSessionMessage("@hana/research dig", "yoga", "hana", "hana", hana)
		if !p.HasTarget || p.Target.ID != "hr" || len(p.Foreign) != 0 {
			t.Fatalf("%+v", p)
		}
	})
	t.Run("two agents named: one answers, never a broadcast", func(t *testing.T) {
		p := planTicketSessionMessage("@research dig\n@writer draft", "yoga", "hana", "hana", hana)
		if !p.HasTarget || p.Target.ID != "hr" || len(p.Extra) != 1 || p.Extra[0] != "writer" {
			t.Fatalf("%+v", p)
		}
	})
	t.Run("someone else, no @: their own Captain, flagged to join", func(t *testing.T) {
		p := planTicketSessionMessage("hi", "yoga", "hana", "hana", hana)
		if p.Home || !p.HasTarget || p.Target.ID != "hc" || !p.Captain {
			t.Fatalf("%+v", p)
		}
	})
	t.Run("someone else with no Captain: the project default, as them", func(t *testing.T) {
		p := planTicketSessionMessage("hi", "yoga", "ana", "ana", nil)
		if p.Home || !p.HasTarget || !p.Default {
			t.Fatalf("%+v", p)
		}
	})
	t.Run("someone else naming the owner's agent: refused, nobody runs", func(t *testing.T) {
		p := planTicketSessionMessage("@yoga/research dig", "yoga", "hana", "hana", hana)
		if p.Home || p.HasTarget || len(p.Foreign) != 1 || p.Foreign[0].Owner != "yoga" {
			t.Fatalf("%+v", p)
		}
	})
	t.Run("someone else's @captain is their own Captain", func(t *testing.T) {
		p := planTicketSessionMessage("@captain look", "yoga", "hana", "hana", hana)
		if !p.HasTarget || p.Target.ID != "hc" || p.Captain {
			t.Fatalf("%+v", p)
		}
	})
	t.Run("unknown or disabled names run nobody", func(t *testing.T) {
		if p := planTicketSessionMessage("@nobody hi", "yoga", "hana", "hana", hana); p.Home || p.HasTarget || len(p.Unknown) != 1 {
			t.Fatalf("%+v", p)
		}
		if p := planTicketSessionMessage("@off hi", "yoga", "hana", "hana", hana); p.Home || p.HasTarget || len(p.Refused) != 1 {
			t.Fatalf("%+v", p)
		}
	})
	t.Run("a session with no owner: everyone is answered by their own Captain", func(t *testing.T) {
		p := planTicketSessionMessage("hi", "", "hana", "hana", hana)
		if p.Home || !p.HasTarget || p.Target.ID != "hc" || !p.Captain {
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
	if got != `"Research" only takes instructions from @yoga. Ask them, or mention your own @captain.` {
		t.Fatalf("refusal %q", got)
	}
	if got := foreignRefusal(foreignMention{Owner: "yoga", Handle: "writer"}, own, hist); !strings.HasPrefix(got, "@writer only takes instructions from @yoga.") {
		t.Fatalf("unnamed refusal %q", got)
	}
	if !spokeIn(hist, "yr", "yoga") || spokeIn(hist, "yr", "hana") {
		t.Fatal("spokeIn")
	}
}
