package pool

import (
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/config"
	systemprompt "github.com/yogasw/wick/internal/agents/system-prompt"
)

const (
	testOperator     = "OPERATOR-RULES: set the session title, pick a Slack identity."
	testTeamOperator = "TEAM-OPERATOR-RULES"
	testPersona      = "You are Bob, a cheerful analyst."
	testTeamBlock    = "## You are a Team agent\n\n## Who you are\nYour name is Bob."
	testAccessBlock  = "## Your access\nAnything not listed is not available; the owner can change it in Settings."
)

func composeFactory(t *testing.T, team bool, teamOperator string) *ClaudeFactory {
	t.Helper()
	return &ClaudeFactory{
		Layout:             config.NewLayout(t.TempDir()),
		SystemPromptLoader: func() string { return testOperator },
		TeamPromptLoader: func(string, bool) string {
			if team {
				return testTeamBlock
			}
			return ""
		},
		TeamSpawnLoader: func(string) (TeamSpawn, bool) {
			return TeamSpawn{Prompt: testTeamBlock, Access: testAccessBlock, Subagents: false, Schedule: true}, team
		},
		TeamSystemPromptLoader: func() string { return teamOperator },
		TicketPointerLoader:    func(string) string { return "TICKET-POINTER" },
	}
}

// inOrder fails unless each needle appears in got, each after the last.
func inOrder(t *testing.T, got string, needles ...string) {
	t.Helper()
	at := 0
	for _, n := range needles {
		i := strings.Index(got[at:], n)
		if i < 0 {
			t.Fatalf("%q missing or out of order (after byte %d)", n, at)
		}
		at += i + len(n)
	}
}

// An ordinary session keeps the old order: the operator prompt still comes
// after the session's addon, and the full main overlay is used.
func TestComposePrompt_NonTeamOrderUnchanged(t *testing.T) {
	f := composeFactory(t, false, testTeamOperator)
	got := f.composePrompt(FactoryOptions{SessionID: "s1", SystemAddon: testPersona}, "claude")
	want := systemprompt.ImmutableFor("claude", false) + "\n\n" + testPersona + "\n\n" + testOperator + "\n\n" +
		sessionIdentityBlock("s1", "", "", false, "") + "\n\nTICKET-POINTER"
	if got != want {
		t.Fatalf("non-Team prompt changed:\n%s", got)
	}
	if strings.Contains(got, testTeamOperator) || strings.Contains(got, "## Your persona") {
		t.Error("Team-only blocks leaked into an ordinary session")
	}
}

// A Team agent's session: immutable → Team overlay + Who you are → Your
// access → system_prompt_team → "## Your persona" → session block, and
// never the old system_prompt.
func TestComposePrompt_TeamOrder(t *testing.T) {
	f := composeFactory(t, true, testTeamOperator)
	got := f.composePrompt(FactoryOptions{SessionID: "s1", SystemAddon: testPersona}, "claude")
	inOrder(t, got,
		"## Long work reports back", // immutable main overlay
		"## Who you are",
		"## Your access",
		testTeamOperator,
		"## Your persona\n\n"+testPersona,
		"## This session",
		"TICKET-POINTER",
	)
	if strings.Contains(got, testOperator) {
		t.Error("Team session carries the system_prompt row")
	}
	// Gates follow the TeamSpawn: no session title, no delegation (off),
	// scheduling kept (on).
	if strings.Contains(got, "## Session title") || strings.Contains(got, "## Delegating work") {
		t.Error("gated section kept")
	}
	if !strings.Contains(got, "## Scheduling yourself") {
		t.Error("scheduling dropped although the agent has it")
	}
	// Persona is the last word before the session block.
	if between := got[strings.Index(got, testPersona)+len(testPersona) : strings.Index(got, "## This session")]; strings.TrimSpace(between) != "" {
		t.Errorf("something sits between persona and session block: %q", between)
	}
}

// Empty system_prompt_team means no operator prompt, not a fallback.
func TestComposePrompt_TeamEmptyOperatorNoFallback(t *testing.T) {
	f := composeFactory(t, true, "")
	got := f.composePrompt(FactoryOptions{SessionID: "s1", SystemAddon: testPersona}, "claude")
	if strings.Contains(got, testOperator) {
		t.Error("empty system_prompt_team fell back to system_prompt")
	}
}

// A sub-agent under a Team session keeps the ordinary assembly even when
// the Team loader would answer for it.
func TestComposePrompt_SubAgentOfTeamUnchanged(t *testing.T) {
	f := composeFactory(t, true, testTeamOperator)
	got := f.composePrompt(FactoryOptions{SessionID: "s1", IsSubAgent: true, SystemAddon: "ROLE"}, "claude")
	if !strings.HasPrefix(got, systemprompt.ImmutableFor("claude", true)) || strings.Contains(got, "## Your persona") ||
		!strings.Contains(got, testOperator) {
		t.Errorf("sub-agent assembly changed:\n%s", got)
	}
}

// Size before/after for a Team agent with the shipped default operator
// prompt standing in for the live row. Logged, and bounded: dropping the
// operator prompt and the gated sections must make the prompt smaller.
func TestComposePrompt_TeamSize(t *testing.T) {
	operator := systemprompt.DefaultSystemPrompt()
	for _, tc := range []struct {
		name string
		ts   TeamSpawn
	}{
		{"captain (all access)", TeamSpawn{Prompt: systemprompt.ImmutableTeam(), Access: testAccessBlock, Subagents: true, Schedule: true}},
		{"agent (no sub-agents, no schedule)", TeamSpawn{Prompt: systemprompt.ImmutableTeam(), Access: testAccessBlock}},
	} {
		before := systemprompt.ImmutableFor("claude", false) + "\n\n" + tc.ts.Prompt + "\n\n" + testPersona + "\n\n" + operator
		f := &ClaudeFactory{
			Layout:          config.NewLayout(t.TempDir()),
			TeamSpawnLoader: func(string) (TeamSpawn, bool) { return tc.ts, true },
		}
		after := f.composePrompt(FactoryOptions{SessionID: "s1", SystemAddon: testPersona}, "claude")
		after = after[:strings.Index(after, "\n\n## This session")]
		t.Logf("%s: before=%d chars (~%d tok) after=%d chars (~%d tok)", tc.name, len(before), len(before)/4, len(after), len(after)/4)
		if len(after) >= len(before) {
			t.Errorf("%s: Team prompt did not shrink (%d → %d)", tc.name, len(before), len(after))
		}
	}
}

// An agent converted from a project (UseGlobalPrompt) carries the global
// system_prompt in system_prompt_team's place.
func TestComposePrompt_TeamUseGlobalPrompt(t *testing.T) {
	f := composeFactory(t, true, testTeamOperator)
	f.TeamSpawnLoader = func(string) (TeamSpawn, bool) {
		return TeamSpawn{Prompt: testTeamBlock, Access: testAccessBlock, UseGlobalPrompt: true}, true
	}
	got := f.composePrompt(FactoryOptions{SessionID: "s1", SystemAddon: testPersona}, "claude")
	inOrder(t, got, testTeamBlock, testAccessBlock, testOperator, "## Your persona\n\n"+testPersona)
	if strings.Contains(got, testTeamOperator) {
		t.Error("system_prompt_team used despite UseGlobalPrompt")
	}
}

// The owner's Team instructions sit after the operator prompt and before
// the persona; empty ones leave no heading, and a sub-agent never gets
// them.
func TestComposePrompt_TeamInstructions(t *testing.T) {
	f := composeFactory(t, true, testTeamOperator)
	f.TeamSpawnLoader = func(string) (TeamSpawn, bool) {
		return TeamSpawn{Prompt: testTeamBlock, Access: testAccessBlock, TeamInstructions: "  OWNER-TEAM-RULES\n"}, true
	}
	got := f.composePrompt(FactoryOptions{SessionID: "s1", SystemAddon: testPersona}, "claude")
	inOrder(t, got, testTeamOperator, "## Team instructions\n\nOWNER-TEAM-RULES\n\n## Your persona\n\n"+testPersona, "## This session")

	f.TeamSpawnLoader = func(string) (TeamSpawn, bool) {
		return TeamSpawn{Prompt: testTeamBlock, Access: testAccessBlock, TeamInstructions: " \n "}, true
	}
	if got := f.composePrompt(FactoryOptions{SessionID: "s1", SystemAddon: testPersona}, "claude"); strings.Contains(got, "## Team instructions") {
		t.Error("blank Team instructions still got a heading")
	}

	f.TeamSpawnLoader = func(string) (TeamSpawn, bool) {
		return TeamSpawn{Prompt: testTeamBlock, TeamInstructions: "OWNER-TEAM-RULES"}, true
	}
	if got := f.composePrompt(FactoryOptions{SessionID: "s1", IsSubAgent: true, SystemAddon: "ROLE"}, "claude"); strings.Contains(got, "OWNER-TEAM-RULES") {
		t.Error("a sub-agent got the Team instructions")
	}
}
