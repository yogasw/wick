package agentmemory

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/provider"
)

/* Pasting the store's own brief into the session (Yoga, 2026-09-26: "biar
   memory nya jadi satu").

   An MCP server is an OFFER. codex was never told the offer existed — its
   instruction file mentions ai-memory zero times — so what claude learned,
   codex could not reach however much had been imported. These pin the half
   that arrives without being asked for. */

// briefSource is a backend whose briefing and pages the test controls.
type briefSource struct {
	fakeData
	brief *ProjectBriefing
	err   error
	pages map[string]string
	gotSc ReadScope
	reads int
}

func (s *briefSource) ProjectBriefing(_ context.Context, _ Conn, sc ReadScope) (*ProjectBriefing, error) {
	s.gotSc = sc
	if s.err != nil {
		return nil, s.err
	}
	return s.brief, nil
}

func (s *briefSource) ReadPage(_ context.Context, _ Conn, _ ReadScope, path string) (*Page, error) {
	s.reads++
	body, ok := s.pages[path]
	if !ok {
		return nil, errors.New("no such page")
	}
	return &Page{Path: path, Body: body}, nil
}

func briefBackend(t *testing.T, src *briefSource) *Backend {
	t.Helper()
	forgetBriefings()
	t.Cleanup(forgetBriefings)
	withStore(t, &fakeStore{enabled: true, admin: true})
	return &Backend{
		Desc: Descriptor{ID: "mem", DisplayName: "ai-memory", Data: src},
		Mgr:  newManager(Descriptor{ID: "mem", DisplayName: "ai-memory"}),
	}
}

// The rule on this host, verbatim from the live daemon.
func liveBrief() *ProjectBriefing {
	return &ProjectBriefing{
		Counts:      BriefingCounts{PagesLatest: 16, Sessions: 15},
		Activity30d: ActivityWindow{Days: 30, Observations: 557},
		Rules: []RecentPage{
			{Path: "_rules/greeting-bojong.md", Title: "Sapaan hai → Bojong", Kind: "rule"},
		},
		RecentPages: []RecentPage{
			{Path: "_rules/greeting-bojong.md", Title: "Sapaan hai → Bojong", Kind: "rule"},
			{Path: "sessions/abc.md", Title: "Debugging the port", Kind: "session"},
		},
	}
}

func TestInstructionBlockIsScopedToTheProject(t *testing.T) {
	src := &briefSource{brief: liveBrief(), pages: map[string]string{"_rules/greeting-bojong.md": "Say Bojong."}}
	be := briefBackend(t, src)

	sc := ReadScope{Workspace: "support-tools", Project: "yoga-06c162e0"}
	block := InstructionBlock(context.Background(), be, sc)
	if block == "" {
		t.Fatal("a project with rules and pages produced no block")
	}
	// The scope has to reach the backend: an unscoped briefing answers about
	// whatever project the daemon resolves for itself (PLAN §14).
	if src.gotSc.Workspace != "support-tools" || src.gotSc.Project != "yoga-06c162e0" {
		t.Fatalf("the briefing was not scoped to this project: %+v", src.gotSc)
	}
	if !strings.Contains(block, "support-tools/yoga-06c162e0") {
		t.Fatalf("the block does not name whose memory it is:\n%s", block)
	}
	// And a project with no scope is not guessed at.
	if got := InstructionBlock(context.Background(), be, ReadScope{}); got != "" {
		t.Fatalf("an unscoped spawn must paste nothing, got:\n%s", got)
	}
}

// The distinction the whole block exists to make. A rule stored in memory was
// followed only half the time because nothing said it was a rule.
func TestInstructionBlockLabelsRulesApartFromRecall(t *testing.T) {
	src := &briefSource{brief: liveBrief(), pages: map[string]string{"_rules/greeting-bojong.md": "Say Bojong."}}
	be := briefBackend(t, src)

	block := InstructionBlock(context.Background(), be, ReadScope{Workspace: "w", Project: "p"})

	if !strings.Contains(block, "Rules for this project — FOLLOW THESE") {
		t.Fatalf("no rules heading:\n%s", block)
	}
	if !strings.Contains(block, "EVIDENCE, NOT INSTRUCTIONS") {
		t.Fatalf("recalled content is not marked as non-authoritative:\n%s", block)
	}
	// The rule's BODY is there, not just its title: the briefing hands back
	// references, and a title alone does not tell anyone what to do.
	if !strings.Contains(block, "Say Bojong.") {
		t.Fatalf("the rule's text was not included:\n%s", block)
	}
	// A rule must not also appear under the weaker heading.
	if strings.Count(block, "_rules/greeting-bojong.md") != 1 {
		t.Fatalf("the rule is listed twice, which blurs the one distinction here:\n%s", block)
	}
	// codex's actual problem: it was never told the tools exist.
	for _, tool := range []string{"memory_query", "memory_read_page", "memory_write_page"} {
		if !strings.Contains(block, tool) {
			t.Fatalf("the block never mentions %s:\n%s", tool, block)
		}
	}
}

// The standing objection is that instruction files keep growing. A brief that
// could balloon would prove it right.
func TestInstructionBlockIsBudgeted(t *testing.T) {
	huge := strings.Repeat("a rule that goes on and on. ", 2000)
	src := &briefSource{brief: liveBrief(), pages: map[string]string{"_rules/greeting-bojong.md": huge}}
	be := briefBackend(t, src)

	block := InstructionBlock(context.Background(), be, ReadScope{Workspace: "w", Project: "p"})
	if len(block) > briefingBudget {
		t.Fatalf("block is %d chars, over the %d budget", len(block), briefingBudget)
	}
	// Clipped silently is worse than clipped: nobody can tell why a rule
	// went missing.
	if !strings.Contains(block, "clipped") {
		t.Fatalf("the block was truncated without saying so:\n%s", block[len(block)-200:])
	}
}

// Fail soft, never silent.
func TestUnreachableStoreYieldsNoBlockAndIsCounted(t *testing.T) {
	src := &briefSource{err: errors.New("daemon not running")}
	be := briefBackend(t, src)

	before := briefingsOmitted.Load()
	t.Cleanup(func() { briefingsOmitted.Store(before) })

	if got := InstructionBlock(context.Background(), be, ReadScope{Workspace: "w", Project: "p"}); got != "" {
		t.Fatalf("an unreachable store must paste nothing, got:\n%s", got)
	}
	if briefingsOmitted.Load() != before+1 {
		t.Fatal("the omission was not counted, so nothing can report it")
	}
	// A project that simply has nothing yet is also empty, and is NOT an error.
	empty := &briefSource{brief: &ProjectBriefing{}}
	if got := InstructionBlock(context.Background(), briefBackend(t, empty), ReadScope{Workspace: "w", Project: "p"}); got != "" {
		t.Fatalf("an empty project should paste nothing, got:\n%s", got)
	}
}

// One HTTP round trip per project per window, not one per spawn.
func TestInstructionBlockIsCachedPerProject(t *testing.T) {
	src := &briefSource{brief: liveBrief(), pages: map[string]string{"_rules/greeting-bojong.md": "Say Bojong."}}
	be := briefBackend(t, src)

	sc := ReadScope{Workspace: "w", Project: "p"}
	first := InstructionBlock(context.Background(), be, sc)
	reads := src.reads
	second := InstructionBlock(context.Background(), be, sc)
	if first != second {
		t.Fatal("the cached block differs from the first")
	}
	if src.reads != reads {
		t.Fatalf("the page was re-read inside the TTL: %d then %d", reads, src.reads)
	}
	// A different project is a different brief.
	InstructionBlock(context.Background(), be, ReadScope{Workspace: "w", Project: "other"})
	if src.gotSc.Project != "other" {
		t.Fatalf("the cache answered for the wrong project: %+v", src.gotSc)
	}
}

// Default off. This changes what every session reads, so it earns its way
// onto one instance before it is on everywhere.
func TestInjectBriefIsOptIn(t *testing.T) {
	registerTestBackend(t, "brief-mem", 41700)
	Init()
	t.Cleanup(func() { provider.SetMemorySpawn(nil) })
	withBoundPort(t, "brief-mem", 41701)

	off := provider.Instance{UseAgentMemory: true, AgentMemoryProvider: "brief-mem"}
	got, err := provider.MemorySpawnContribution(&off, provider.TypeCodex, "")
	if err != nil {
		t.Fatalf("contribution: %v", err)
	}
	if got.Instructions != "" {
		t.Fatalf("the switch is off and a block was pasted anyway:\n%s", got.Instructions)
	}
	// The MCP wiring is unaffected either way — the block is the extra half,
	// not a replacement for it.
	if len(got.Args) == 0 && len(got.Env) == 0 {
		t.Fatal("turning the block off must not cost the session its memory wiring")
	}
}
