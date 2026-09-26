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

// liveBrief mirrors what the daemon on this host returns: a stats object whose
// only instruction-worthy parts are the `rules` and `slots` arrays, both of
// which carry REFERENCES — path, title, kind, date — and no body.
//
// The counters, the activity windows and recent_pages are here precisely
// because they must NOT reach the block. recent_pages on this host is a list
// of sessions/<uuid>.md titled "[from: Admin]": noise that would cost budget
// and teach an agent nothing.
func liveBrief() *ProjectBriefing {
	return &ProjectBriefing{
		Counts:      BriefingCounts{PagesLatest: 16, PagesAll: 19, Sessions: 18, Observations: 581},
		Activity30d: ActivityWindow{Days: 30, Observations: 581},
		Rules: []RecentPage{
			{Path: "_rules/greeting-bojong.md", Title: "Sapaan hai → Bojong", Kind: "rule"},
		},
		RecentPages: []RecentPage{
			{Path: "sessions/01a0dbab.md", Title: "[from: Admin]", Kind: "session"},
		},
	}
}

func ruleBodies() map[string]string {
	return map[string]string{"_rules/greeting-bojong.md": "Kalau user menyapa dengan \"hai\", balas dengan \"Bojong\"."}
}

func TestInstructionBlockIsScopedToTheProject(t *testing.T) {
	src := &briefSource{brief: liveBrief(), pages: ruleBodies()}
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
	src := &briefSource{brief: liveBrief(), pages: ruleBodies()}
	src.brief.Slots = []RecentPage{{Path: "_slots/stack.md", Title: "Stack", Kind: "slot"}}
	src.pages["_slots/stack.md"] = "Go + Svelte."
	be := briefBackend(t, src)

	block := InstructionBlock(context.Background(), be, ReadScope{Workspace: "w", Project: "p"})

	if !strings.Contains(block, "Rules for this project — FOLLOW THESE") {
		t.Fatalf("no rules heading:\n%s", block)
	}
	if !strings.Contains(block, "EVIDENCE, NOT INSTRUCTIONS") {
		t.Fatalf("recorded state is not marked as non-authoritative:\n%s", block)
	}
	// Bodies, not titles: the briefing hands back references, and a list of
	// titles teaches an agent nothing.
	if !strings.Contains(block, "balas dengan") {
		t.Fatalf("the rule's text was not included:\n%s", block)
	}
	if !strings.Contains(block, "Go + Svelte.") {
		t.Fatalf("the slot's text was not included:\n%s", block)
	}
	// codex's actual problem: it was never told the tools exist.
	for _, tool := range []string{"memory_query", "memory_read_page", "memory_write_page"} {
		if !strings.Contains(block, tool) {
			t.Fatalf("the block never mentions %s:\n%s", tool, block)
		}
	}
}

// An instruction file is not where you read that a project has 581
// observations, and a list of sessions/<uuid>.md titled "[from: Admin]" is
// noise that costs budget and teaches nothing.
func TestInstructionBlockCarriesOnlyRulesAndSlots(t *testing.T) {
	src := &briefSource{brief: liveBrief(), pages: ruleBodies()}
	be := briefBackend(t, src)

	block := InstructionBlock(context.Background(), be, ReadScope{Workspace: "w", Project: "p"})
	for _, noise := range []string{"581", "sessions/01a0dbab.md", "[from: Admin]", "observation"} {
		if strings.Contains(block, noise) {
			t.Fatalf("panel material reached the instruction file (%q):\n%s", noise, block)
		}
	}
}

// Empty rules and slots is the state of this host today, and it is correct
// behaviour rather than a bug: an empty heading spends instruction budget
// announcing that there is nothing to announce.
func TestNoRulesOrSlotsMeansNoBlock(t *testing.T) {
	src := &briefSource{brief: &ProjectBriefing{
		Counts:      BriefingCounts{PagesLatest: 16, Sessions: 18},
		RecentPages: []RecentPage{{Path: "sessions/01a0dbab.md", Title: "[from: Admin]"}},
	}}
	be := briefBackend(t, src)

	if got := InstructionBlock(context.Background(), be, ReadScope{Workspace: "w", Project: "p"}); got != "" {
		t.Fatalf("a project with pages but no rules or slots must paste nothing, got:\n%s", got)
	}
}

// One line, and only because the agent can act on it.
func TestPendingHandoffEarnsOneLine(t *testing.T) {
	src := &briefSource{brief: &ProjectBriefing{PendingHandoffs: 1}}
	be := briefBackend(t, src)

	block := InstructionBlock(context.Background(), be, ReadScope{Workspace: "w", Project: "p"})
	if !strings.Contains(block, "handoff from a previous session is waiting") {
		t.Fatalf("a waiting handoff is actionable and was dropped:\n%s", block)
	}
	if strings.Count(block, "handoff") > 2 {
		t.Fatalf("one line, not a section:\n%s", block)
	}
}

// A page that will not open must not take the rest of the block down, and
// must not vanish without trace: an agreed rule quietly missing from the
// instructions is exactly the failure nobody can explain afterwards.
func TestAnUnreadablePageIsSkippedAndNamed(t *testing.T) {
	src := &briefSource{brief: liveBrief(), pages: ruleBodies()}
	src.brief.Rules = append(src.brief.Rules, RecentPage{Path: "_rules/broken.md", Title: "Broken", Kind: "rule"})
	be := briefBackend(t, src)

	block := InstructionBlock(context.Background(), be, ReadScope{Workspace: "w", Project: "p"})
	if !strings.Contains(block, "balas dengan") {
		t.Fatalf("one unreadable page took the readable one with it:\n%s", block)
	}
	if strings.Contains(block, "Broken") {
		t.Fatalf("a page that could not be read was announced as if it had been:\n%s", block)
	}
	skipped := SkippedBriefingPages()
	if len(skipped) != 1 || skipped[0] != "_rules/broken.md" {
		t.Fatalf("the skipped page is not named for the Health finding: %v", skipped)
	}
}

// A project with forty rules must not fire forty reads at spawn.
func TestPageReadsAreBounded(t *testing.T) {
	src := &briefSource{brief: &ProjectBriefing{}, pages: map[string]string{}}
	for i := 0; i < 40; i++ {
		path := "_rules/r" + string(rune('a'+i%26)) + string(rune('0'+i/26)) + ".md"
		src.brief.Rules = append(src.brief.Rules, RecentPage{Path: path, Title: path, Kind: "rule"})
		src.pages[path] = "body"
	}
	be := briefBackend(t, src)

	block := InstructionBlock(context.Background(), be, ReadScope{Workspace: "w", Project: "p"})
	if src.reads > pageBudget {
		t.Fatalf("%d reads at spawn, budget is %d", src.reads, pageBudget)
	}
	// Stopping early is said, not hidden.
	if !strings.Contains(block, "read it with `memory_read_page`") {
		t.Fatalf("the block stopped early without saying so:\n%s", block)
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
	src := &briefSource{brief: liveBrief(), pages: ruleBodies()}
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
