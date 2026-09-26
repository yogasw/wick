package agentmemory

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Putting what the store knows INTO the session, rather than offering it
// (Yoga, 2026-09-26: "biar memory nya jadi satu").
//
// Two memory systems with different delivery is why behaviour was
// inconsistent. claude's file memory is pasted into the prompt, so it is in
// context at every call. ai-memory is an MCP server, so the model has to
// decide to call it — and codex was never told the tools existed at all: its
// instruction file mentions ai-memory zero times. No amount of importing fixes
// that, because the problem is delivery, not storage.
//
// So wick pastes ai-memory's OWN brief into the instruction surface it already
// writes. What is in a brief is ai-memory's decision — which pages are rules,
// which slots are filled, what the caps are — and wick does not re-derive any
// of it. Growing a second copy of somebody else's selection rule is the bug
// that cost this morning.

// briefingTTL is how long one project's brief is reused. Short, because a rule
// written during a session should reach the next one; long enough that a burst
// of spawns does not pay an HTTP round trip each.
const briefingTTL = 30 * time.Second

// briefingBudget caps the pasted block.
//
// The standing objection to this whole idea is that instruction files keep
// growing, and a brief that could balloon would prove it right. 6000 characters
// is roughly a tenth of the 51 KB codex file it joins: enough for a handful of
// rules and a page list, small enough that nobody has to audit it.
const briefingBudget = 6000

// bodyBudget is how many pages are read in full.
//
// The briefing hands back rules as REFERENCES — path and title, no body
// (verified against the live daemon). A title alone does not tell an agent
// what to do, so wick follows the pointers ai-memory gave it. Following a
// pointer is not the same as deciding what a rule is: the selection stays the
// backend's, and this only dereferences it.
const bodyBudget = 5

// indexLines caps the one-line index of rules.
//
// One line each is what makes this scale — the prompt grows a line per rule
// instead of a page — but a project with a thousand rules would still flood
// it. claude's own file memory on this host is the shape being copied: a
// 220-line MEMORY.md of pointers with the bodies in 223 separate files.
const indexLines = 40

// briefingCache holds one rendered block per project.
var (
	briefingMu    sync.Mutex
	briefingCache = map[string]cachedBriefing{}
)

type cachedBriefing struct {
	block string
	at    time.Time
}

// briefingsOmitted counts sessions that got no memory block because the store
// could not be reached. It drives a Health finding, for the same reason the
// no-memory spawn does: wick may degrade in what it DOES, never in what it
// says.
var briefingsOmitted atomic.Int64

// BriefingsOmitted reports that count.
func BriefingsOmitted() int64 { return briefingsOmitted.Load() }

// skippedPages records rule or slot pages whose body could not be read.
//
// A page that will not open is skipped rather than allowed to take the whole
// block down with it — but skipping it silently means an agreed rule is simply
// absent from the instructions, and nobody can explain why it was not
// followed. So the path is kept and reported.
var (
	skippedMu    sync.Mutex
	skippedPages []string
)

func noteBriefingSkips(paths []string) {
	if len(paths) == 0 {
		return
	}
	skippedMu.Lock()
	defer skippedMu.Unlock()
	seen := map[string]bool{}
	for _, p := range skippedPages {
		seen[p] = true
	}
	for _, p := range paths {
		if !seen[p] {
			skippedPages = append(skippedPages, p)
			seen[p] = true
		}
	}
}

// SkippedBriefingPages lists them, for the Health tab.
func SkippedBriefingPages() []string {
	skippedMu.Lock()
	defer skippedMu.Unlock()
	return append([]string(nil), skippedPages...)
}

// InstructionBlock is the text to paste into a session's instructions for one
// project, or "" when there is nothing to say.
//
// Empty is a normal answer and is never an error: a daemon that is down, a
// project with no memory yet, a backend with no briefing surface. The session
// runs either way — a memory store being unreachable must not stop an agent
// working — and the omission is counted so the panel can say it happened.
func InstructionBlock(ctx context.Context, be *Backend, sc ReadScope) string {
	if be == nil || sc.Project == "" {
		return ""
	}
	key := sc.Workspace + "/" + sc.Project
	briefingMu.Lock()
	if c, ok := briefingCache[key]; ok && time.Since(c.at) < briefingTTL {
		briefingMu.Unlock()
		return c.block
	}
	briefingMu.Unlock()

	block := buildInstructionBlock(ctx, be, sc)
	if block == "" {
		briefingsOmitted.Add(1)
		// A miss is not cached: the usual cause is a daemon that is down,
		// and the next spawn should see it come back.
		return ""
	}
	briefingMu.Lock()
	briefingCache[key] = cachedBriefing{block: block, at: time.Now()}
	briefingMu.Unlock()
	return block
}

// forgetBriefings drops the cache. Used by tests and after a setting that
// changes which store is read.
func forgetBriefings() {
	briefingMu.Lock()
	briefingCache = map[string]cachedBriefing{}
	briefingMu.Unlock()
	skippedMu.Lock()
	skippedPages = nil
	skippedMu.Unlock()
}

func buildInstructionBlock(ctx context.Context, be *Backend, sc ReadScope) string { //nolint:gocritic // one path, read top to bottom
	br, ok := be.Desc.Data.(ProjectBriefer)
	if !ok {
		return ""
	}
	set := settingsFor(be.Desc.ID)
	conn := connFor(be, set)
	b, err := br.ProjectBriefing(ctx, conn, sc)
	if err != nil || b == nil {
		return ""
	}
	block, skipped := renderBriefing(ctx, be, conn, sc, b)
	noteBriefingSkips(skipped)
	return block
}

// renderBriefing turns one briefing into the pasted block, in three tiers.
//
// Pasting every rule in full does not scale, and scale is the real concern:
// the file memory on this host runs to 417 pages across projects, 223 in one
// of them. At a 6000-character cap that block clips, and when it clips RULES
// DISAPPEAR SILENTLY — the important ones drowning among the trivial on the
// way out. So the shape is the one every harness with real memory converges
// on, and the one claude's own file memory already uses here: a 220-line index
// of pointers, bodies in separate files.
//
//	always, in full  — _slots/*, the few pages ai-memory keeps loaded every
//	                   session: project context and invariants. They do not
//	                   grow on their own, because a person decides what is
//	                   a slot.
//	index, one line  — every other _rules/* page, title and path, no body.
//	                   The prompt grows a line per rule, not a page.
//	not at all       — everything else. It is one `memory_read_page` away
//	                   and the preamble above says so.
//
// PINNED pages belong in the first tier and are absent from it, because they
// are not reachable: `pinned` appears nowhere in the briefing payload, nowhere
// in `memory_read_page`'s document (path/title/body/frontmatter), and is not a
// briefing parameter — checked against the live daemon. Inventing a second
// source for them would be the mistake this file keeps warning about, so this
// ships slots-only and the gap is reported rather than papered over.
func renderBriefing(ctx context.Context, be *Backend, conn Conn, sc ReadScope, b *ProjectBriefing) (string, []string) {
	budget := bodyBudget
	slots, skipped := renderPages(ctx, be, conn, sc, b.Slots, &budget)
	index := renderIndex(b.Rules)

	// Nothing to say. An empty heading is worse than no block: it spends
	// instruction budget announcing that there is nothing to announce.
	if slots == "" && index == "" && b.PendingHandoffs == 0 {
		return "", skipped
	}

	var s strings.Builder
	s.WriteString("# Project memory — " + sc.Workspace + "/" + sc.Project + "\n\n")
	s.WriteString(memoryPreamble(be.Desc.DisplayName))

	// First, because it is the tier that has to be obeyed without being
	// fetched.
	if slots != "" {
		s.WriteString("\n## Project context — ALWAYS LOADED\n\n")
		s.WriteString("The pages this project keeps in every session: its context and its invariants. They are the\n")
		s.WriteString("project's own record of itself — treat them as what was true when they were written, and as\n")
		s.WriteString("binding on how you work here unless something in front of you plainly contradicts them.\n\n")
		s.WriteString(slots)
	}

	if index != "" {
		s.WriteString("\n## Rules for this project — FOLLOW THESE\n\n")
		s.WriteString("Standing instructions this project has agreed on, listed by title and path. The bodies are NOT\n")
		s.WriteString("here: read one with `memory_read_page` before doing anything it covers. A rule you have not read\n")
		s.WriteString("is still a rule.\n\n")
		s.WriteString(index)
	}

	// One line, and only when there is something to pick up. It earns its
	// place by being actionable — the agent can accept it — where a counter
	// would only be trivia.
	if b.PendingHandoffs > 0 {
		s.WriteString(fmt.Sprintf("\nA handoff from a previous session is waiting to be picked up (%d). Use `memory_handoff_list`.\n",
			b.PendingHandoffs))
	}

	return clip(s.String(), briefingBudget), skipped
}

// memoryPreamble names the tools. This is the part that fixes codex: its
// instruction file never mentioned that a memory server was attached, so the
// model had no reason to call anything.
func memoryPreamble(name string) string {
	return "This block was read out of " + name + " at spawn — you did not have to ask for it.\n" +
		"The same store is reachable as MCP tools for anything not included here: `memory_query` to search it,\n" +
		"`memory_read_page` to read a page in full by its path, `memory_write_page` to record something worth\n" +
		"keeping. What follows is a summary; the store holds more.\n"
}

// renderIndex is the middle tier: one line per rule, and a count when the line
// budget bites.
//
// Saying "and N more" rather than stopping silently is the whole difference
// between a list that is short and a list that is truncated — an agent that
// cannot tell which one it is reading has no reason to go looking.
func renderIndex(rules []RecentPage) string {
	if len(rules) == 0 {
		return ""
	}
	var s strings.Builder
	shown := 0
	for _, r := range rules {
		if shown >= indexLines {
			break
		}
		title := strings.TrimSpace(r.Title)
		if title == "" {
			title = r.Path
		}
		s.WriteString("- " + title + " — `" + r.Path + "`\n")
		shown++
	}
	if rest := len(rules) - shown; rest > 0 {
		s.WriteString(fmt.Sprintf("- …and %d more. Use `memory_query` to find the one you need.\n", rest))
	}
	return s.String()
}

// renderPages turns one list of page REFERENCES into text, reading each body
// while the shared budget lasts.
//
// The briefing hands back paths and titles with no body (verified against the
// live daemon), and a list of titles teaches an agent nothing. So wick follows
// the pointers ai-memory gave it. Following a pointer is not deciding what a
// rule is — the selection stays the backend's.
//
// A page that cannot be read is SKIPPED and its path returned, not allowed to
// take the rest of the block down with it. A project with forty rules does not
// fire forty reads at spawn either: past the budget a page is named with the
// way to fetch it, which is honest about stopping early.
func renderPages(ctx context.Context, be *Backend, conn Conn, sc ReadScope, pages []RecentPage, budget *int) (string, []string) {
	if len(pages) == 0 {
		return "", nil
	}
	rd, canRead := be.Desc.Data.(PageReader)
	var s strings.Builder
	var skipped []string
	for _, p := range pages {
		title := strings.TrimSpace(p.Title)
		if title == "" {
			title = p.Path
		}
		if !canRead || *budget <= 0 {
			s.WriteString("### " + title + "\n`" + p.Path + "`\n\n")
			s.WriteString("(not included here — read it with `memory_read_page` if it applies)\n\n")
			continue
		}
		*budget--
		pg, err := rd.ReadPage(ctx, conn, sc, p.Path)
		if err != nil || pg == nil || strings.TrimSpace(pg.Body) == "" {
			// Skipped, and named so the Health finding can say which.
			skipped = append(skipped, p.Path)
			continue
		}
		s.WriteString("### " + title + "\n`" + p.Path + "`\n\n")
		s.WriteString(strings.TrimSpace(pg.Body) + "\n\n")
	}
	return s.String(), skipped
}

// clip trims to the budget on a line boundary and says that it did.
//
// Silently truncated instructions are worse than short ones: the agent cannot
// tell that something was cut, and neither can the person wondering why a rule
// was ignored.
func clip(s string, max int) string {
	if len(s) <= max {
		return s
	}
	const notice = "\n\n(This block was clipped to fit its budget. The rest is in the store — use `memory_query`.)\n"
	cut := max - len(notice)
	if cut < 0 {
		cut = 0
	}
	trimmed := s[:cut]
	if i := strings.LastIndex(trimmed, "\n"); i > 0 {
		trimmed = trimmed[:i]
	}
	return strings.TrimRight(trimmed, "\n") + notice
}
