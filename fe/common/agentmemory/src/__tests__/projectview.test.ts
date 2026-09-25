import { describe, test, expect } from "vitest";
import {
  cardAge,
  cardsFrom,
  cardsFromHits,
  checkpointLabel,
  checkpointsForPath,
  counterRow,
  hasUnsavedWork,
  pathError,
  scopeHeading,
  snippetOf,
} from "../projectview.js";
import type { Checkpoint, ProjectBriefing, ProjectScope, RecentPage, SearchHit } from "../types.js";

/* The project view's rules (PLAN §22) — the parts that decide what a card
   says and what an edit is allowed to be. */

const PAGES: RecentPage[] = [
  { path: "pages/kasir_prod_db.md", title: "kasir_prod_db", kind: "fact", updated_at: "2026-09-25T09:58:00Z" },
  { path: "sessions/1c0f.md", updated_at: "2026-09-24T09:58:00Z" },
];

describe("cards", () => {
  test("a page with no title is identified by its path, not by nothing", () => {
    const cards = cardsFrom(PAGES);
    expect(cards[0].title).toBe("kasir_prod_db");
    expect(cards[1].title).toBe("sessions/1c0f.md");
  });

  test("a search decorates the list rather than replacing it", () => {
    const hits: SearchHit[] = [{ path: "pages/kasir_prod_db.md", snippet: "the <mark>replica</mark>" }];
    const cards = cardsFrom(PAGES, hits);
    expect(cards).toHaveLength(2);
    expect(cards[0].snippet).toContain("<mark>");
    expect(cards[1].snippet).toBeUndefined();
  });

  // The listing only carries the recent pages, so search is the only way to
  // reach an older one — those have to appear as cards too.
  test("hits the listing does not hold become their own cards", () => {
    const hits: SearchHit[] = [
      { path: "pages/kasir_prod_db.md", snippet: "known" },
      { path: "pages/old_decision.md", title: "old decision", snippet: "older" },
    ];
    const known = cardsFrom(PAGES, hits);
    const extra = cardsFromHits(hits, known);
    expect(extra).toHaveLength(1);
    expect(extra[0].path).toBe("pages/old_decision.md");
  });

  test("an empty listing is empty, not a crash", () => {
    expect(cardsFrom(null)).toEqual([]);
    expect(cardsFromHits(undefined, [])).toEqual([]);
  });

  test("an undated page says so instead of rendering a blank", () => {
    expect(cardAge({ path: "x.md", title: "x" })).toBe("date unknown");
  });
});

describe("snippetOf", () => {
  // A card titled "kasir_prod_db" above a snippet reading "# kasir_prod_db"
  // has told the reader nothing twice.
  test("skips the heading and the frontmatter fence", () => {
    expect(snippetOf("---\nkind: fact\n---\n# kasir_prod_db\n\nRead replica, not the primary.")).toBe(
      "Read replica, not the primary.",
    );
  });

  test("truncates with an ellipsis rather than mid-word forever", () => {
    const long = "x".repeat(300);
    const s = snippetOf(long, 40);
    expect(s.length).toBeLessThanOrEqual(40);
    expect(s.endsWith("…")).toBe(true);
  });

  test("a page with only a heading has no snippet to give", () => {
    expect(snippetOf("# just a title")).toBe("");
    expect(snippetOf(undefined)).toBe("");
  });
});

describe("pathError", () => {
  test("accepts an ordinary wiki path", () => {
    expect(pathError("notes/deploy.md")).toBeNull();
  });

  test("refuses what the store would refuse or normalise, and says why", () => {
    expect(pathError("")).toMatch(/required/i);
    expect(pathError("/notes/deploy.md")).toMatch(/leading slash/i);
    expect(pathError("../escape.md")).toMatch(/climb out/i);
    expect(pathError("notes/my page.md")).toMatch(/spaces/i);
    expect(pathError("notes/deploy")).toMatch(/\.md/);
  });
});

describe("hasUnsavedWork", () => {
  test("a trailing newline is not an edit", () => {
    expect(hasUnsavedWork("body", "body\n")).toBe(false);
  });

  test("a real change is", () => {
    expect(hasUnsavedWork("body", "body and more")).toBe(true);
  });
});

describe("checkpoints", () => {
  const cps: Checkpoint[] = [
    { oid: "d544bc66fa5e33c4", short_oid: "d544bc66fa5e", time: 1790344784, summary: "write-page wick/demo: notes/hello.md" },
    { oid: "c84640f0a0f56e38", short_oid: "c84640f0a0f5", time: 1790344371, summary: "session 01a0c33c: hai" },
  ];

  test("a label is choosable: when, and what made it", () => {
    const label = checkpointLabel(cps[0], Date.parse("2026-09-25T14:00:00Z"));
    expect(label).toContain("d544bc66fa5e");
    expect(label).toContain("notes/hello.md");
  });

  // The history is store-wide, so an unfiltered list would offer to restore
  // this page from a commit that never touched it.
  test("the commits naming this page are separated from the rest", () => {
    const split = checkpointsForPath(cps, "notes/hello.md");
    expect(split.matching).toHaveLength(1);
    expect(split.others).toHaveLength(1);
    expect(split.matching[0].oid).toBe("d544bc66fa5e33c4");
  });

  test("with no page in hand nothing is claimed to match", () => {
    const split = checkpointsForPath(cps, "  ");
    expect(split.matching).toHaveLength(0);
    expect(split.others).toHaveLength(2);
  });
});

describe("header and counters", () => {
  const scope: ProjectScope = {
    project_id: "8c28230d",
    name: "Kasir",
    folder: "/srv/p/files",
    workspace: "wick",
    project: "kasir-8c28230d",
    source: "marker",
  };

  // Both, always: the name is what was clicked, the bucket is what the
  // agents actually write to.
  test("the heading carries the project's name and its bucket", () => {
    const h = scopeHeading(scope);
    expect(h.title).toBe("Kasir");
    expect(h.bucket).toBe("wick/kasir-8c28230d");
  });

  test("an unresolved scope still has something to call itself", () => {
    expect(scopeHeading(null).title).toMatch(/memory/i);
  });

  test("the counters are the project's own, from the briefing", () => {
    const b: ProjectBriefing = {
      counts: { pages_latest: 31, pages_all: 64, sessions: 12, observations: 880, evidence_rows: 210 },
      activity_7d: { days: 7, sessions: 3, observations: 120, pages_updated: 9 },
      activity_30d: { days: 30, sessions: 12, observations: 880, pages_updated: 31 },
      pending_handoff_count: 1,
      pending_message_count: 2,
      cross_project_dependents: 0,
      cross_project_dependencies: 0,
    };
    const row = counterRow(b);
    expect(row.map((c) => c.label)).toEqual(["Pages", "Sessions", "Observations", "Last 7 days", "Last 30 days"]);
    expect(row[0].value).toBe("31");
    expect(row[3].value).toContain("3 sessions");
  });

  test("no briefing means no counters rather than a row of zeros", () => {
    expect(counterRow(null)).toEqual([]);
  });
});

// ── this project's analytics ─────────────────────────────────────────

import { freshness, kindMix, linkRow, paceOf, STALE_DAYS, timelineCaveat, writeTimeline } from "../projectview.js";
import type { ProjectBriefing as PB } from "../types.js";

const NOW = Date.parse("2026-09-25T12:00:00Z");
const daysAgo = (n: number) => new Date(NOW - n * 86_400_000).toISOString();

function briefing(over: Partial<PB> = {}): PB {
  return {
    counts: { pages_latest: 10, pages_all: 20, sessions: 5, observations: 300, evidence_rows: 40 },
    activity_7d: { days: 7, sessions: 2, observations: 70, pages_updated: 4 },
    activity_30d: { days: 30, sessions: 5, observations: 300, pages_updated: 10 },
    pending_handoff_count: 1,
    pending_message_count: 0,
    cross_project_dependents: 2,
    cross_project_dependencies: 3,
    last_observation_at: daysAgo(1),
    recent_pages: [],
    ...over,
  } as PB;
}

describe("writeTimeline", () => {
  test("counts this project's own page writes per day, oldest first", () => {
    const t = writeTimeline(
      [
        { path: "a.md", updated_at: daysAgo(0) },
        { path: "b.md", updated_at: daysAgo(0) },
        { path: "c.md", updated_at: daysAgo(3) },
      ],
      5,
      NOW,
    );
    expect(t).toHaveLength(5);
    expect(t[t.length - 1].count).toBe(2);
    expect(t[1].count).toBe(1);
    expect(t[0].count).toBe(0);
  });

  test("a page with no date is skipped rather than counted as today", () => {
    const t = writeTimeline([{ path: "a.md" }], 3, NOW);
    expect(t.every((d) => d.count === 0)).toBe(true);
  });

  // The listing is capped, so a decline at the far end can be the edge of
  // the list rather than a quiet fortnight. Saying so is the difference
  // between a chart and a wrong conclusion.
  test("a full listing earns the undercount caveat; a short one does not", () => {
    const full = Array.from({ length: 50 }, (_, i) => ({ path: `p${i}.md`, updated_at: daysAgo(i % 30) }));
    expect(timelineCaveat(full)).toMatch(/undercounted/);
    expect(timelineCaveat(full.slice(0, 10))).toBeNull();
  });
});

describe("paceOf", () => {
  test("no briefing is an absence with a reason, not a zero", () => {
    const p = paceOf(null);
    expect(p.available).toBe(false);
    expect(p.detail).toMatch(/briefing/i);
  });

  test("a month with nothing in it says so plainly", () => {
    const p = paceOf(briefing({ activity_7d: { days: 7, sessions: 0, observations: 0, pages_updated: 0 }, activity_30d: { days: 30, sessions: 0, observations: 0, pages_updated: 0 } }));
    expect(p.label).toMatch(/No activity/i);
    expect(p.detail).toMatch(/hook/);
  });

  test("a busy week reads as busier than usual", () => {
    // 70 in the week against an average week of 300*7/30 = 70 → steady;
    // double it and the reading has to change.
    const p = paceOf(briefing({ activity_7d: { days: 7, sessions: 3, observations: 200, pages_updated: 9 } }));
    expect(p.label).toMatch(/Busier/i);
    expect(p.ratio).toBeGreaterThan(1.25);
  });

  test("the same volume spread evenly reads as steady", () => {
    expect(paceOf(briefing()).label).toBe("Steady");
  });

  test("a week with nothing in a busy month is called out", () => {
    const p = paceOf(briefing({ activity_7d: { days: 7, sessions: 0, observations: 0, pages_updated: 0 } }));
    expect(p.label).toMatch(/Quiet/i);
  });
});

describe("kindMix", () => {
  test("shares add up and the biggest kind leads", () => {
    const mix = kindMix([
      { path: "a.md", kind: "fact" },
      { path: "b.md", kind: "fact" },
      { path: "c.md", kind: "rule" },
    ]);
    expect(mix[0]).toMatchObject({ kind: "fact", count: 2 });
    expect(mix[0].share).toBeCloseTo(2 / 3);
    expect(mix.reduce((n, k) => n + k.share, 0)).toBeCloseTo(1);
  });

  test("a page with no kind is labelled, not dropped", () => {
    const mix = kindMix([{ path: "a.md" }]);
    expect(mix[0].kind).toBe("unlabelled");
  });

  test("no pages means no breakdown rather than a fake one", () => {
    expect(kindMix([])).toEqual([]);
  });
});

describe("freshness", () => {
  test("a recent capture is not stale", () => {
    const f = freshness(briefing(), NOW);
    expect(f.stale).toBe(false);
    expect(f.label).toBe("Yesterday");
  });

  // The silent failure this whole feature exists for: agents working while
  // the memory stands still.
  test("a fortnight of silence is called out with what to check", () => {
    const f = freshness(briefing({ last_observation_at: daysAgo(STALE_DAYS + 1) }), NOW);
    expect(f.stale).toBe(true);
    expect(f.detail).toMatch(/capture hook/i);
  });

  test("a project that has never captured anything says that, not 'n/a' alone", () => {
    const f = freshness(briefing({ last_observation_at: undefined }), NOW);
    expect(f.available).toBe(false);
    expect(f.detail).toMatch(/nothing has been recorded/i);
  });

  test("no briefing falls back to the briefing-unavailable sentence", () => {
    expect(freshness(null, NOW).detail).toMatch(/briefing/i);
  });
});

describe("linkRow", () => {
  test("carries the four per-project link counters, with what each means", () => {
    const rows = linkRow(briefing());
    expect(rows.map((r) => r.value)).toEqual(["3", "2", "1", "0"]);
    expect(rows[0].note).toMatch(/refers to/);
  });

  test("no briefing means no row rather than four zeros", () => {
    expect(linkRow(null)).toEqual([]);
  });
});
