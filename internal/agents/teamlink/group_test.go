package teamlink

import (
	"reflect"
	"testing"
)

func groupPeersFixture() []Peer {
	return []Peer{
		{ID: "a-anton", Handle: "anton"},
		{ID: "a-cap", Handle: "captain", IsCaptain: true},
		{ID: "a-vera", Handle: "vera", MaxHops: 2},
	}
}

func handlesOf(ps []Peer) []string {
	out := []string{}
	for _, p := range ps {
		out = append(out, p.Handle)
	}
	return out
}

func TestHumanTargets(t *testing.T) {
	m := groupPeersFixture()
	cases := []struct {
		name, text, mode string
		want, refused    []string
	}{
		{"no @ goes to the captain", "status?", ResponderCaptain, []string{"captain"}, nil},
		{"no @ with first goes to the first member", "status?", ResponderFirst, []string{"anton"}, nil},
		{"one @", "@vera check logs", ResponderCaptain, []string{"vera"}, nil},
		{"bare @ line from a person counts", "@vera\nwhat broke?", ResponderCaptain, []string{"vera"}, nil},
		{"several @ in line order, once each", "@vera logs\n@anton deploy\n@vera again", ResponderCaptain, []string{"vera", "anton"}, nil},
		{"mid-line @ is not a mention", "ask @vera later", ResponderCaptain, []string{"captain"}, nil},
		{"unknown handle refused, known still go", "@ghost hi\n@anton hi", ResponderCaptain, []string{"anton"}, []string{"ghost"}},
		{"email is not a mention", "@foo.bar@x.com hi", ResponderCaptain, []string{"captain"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, refused, err := HumanTargets(tc.text, m, tc.mode)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(handlesOf(got), tc.want) || (len(refused) > 0 || len(tc.refused) > 0) && !reflect.DeepEqual(refused, tc.refused) {
				t.Fatalf("targets %v refused %v, want %v / %v", handlesOf(got), refused, tc.want, tc.refused)
			}
		})
	}
}

func TestDefaultResponderSkipsDisabled(t *testing.T) {
	m := groupPeersFixture()
	m[1].Disabled = true // the captain
	p, err := DefaultResponderOf(m, ResponderCaptain)
	if err != nil || p.Handle != "anton" {
		t.Fatalf("got %v, %v", p.Handle, err)
	}
	for i := range m {
		m[i].Disabled = true
	}
	if _, err := DefaultResponderOf(m, ResponderFirst); err != ErrNoResponder {
		t.Fatalf("err = %v", err)
	}
}

func TestGroupLimitOverrideOnlyLowers(t *testing.T) {
	m := groupPeersFixture() // vera caps at 2, the others default 4
	if got := GroupLimit(m, 0); got != 2 {
		t.Fatalf("min = %d", got)
	}
	if got := GroupLimit(m, 1); got != 1 {
		t.Fatalf("lower override = %d", got)
	}
	if got := GroupLimit(m, 9); got != 2 {
		t.Fatalf("higher override raised the cap: %d", got)
	}
	if !ValidGroupOverride(m, 0) || !ValidGroupOverride(m, 2) || ValidGroupOverride(m, 3) || ValidGroupOverride(m, -1) {
		t.Fatal("ValidGroupOverride")
	}
}

func TestGroupMembers(t *testing.T) {
	own := groupPeersFixture()
	got, err := GroupMembers([]string{"a-vera", "a-anton", "a-vera"}, own)
	if err != nil || !reflect.DeepEqual(got, []string{"a-vera", "a-anton"}) {
		t.Fatalf("got %v, %v", got, err)
	}
	if _, err := GroupMembers([]string{"a-vera"}, own); err == nil {
		t.Fatal("one member accepted")
	}
	if _, err := GroupMembers([]string{"a-vera", "a-mallory"}, own); err == nil {
		t.Fatal("another owner's agent accepted")
	}
}

func TestAgentReplyMentionsNeedABody(t *testing.T) {
	hit, _ := LeadMentions("done.\n@vera\n@anton please deploy", groupPeersFixture(), true)
	if !reflect.DeepEqual(handlesOf(hit), []string{"anton"}) {
		t.Fatalf("hit = %v", handlesOf(hit))
	}
}
