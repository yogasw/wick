import { describe, test, expect } from "vitest";
import {
  compactConfirmBody,
  embeddingTriples,
  growthFacts,
  indexCoverage,
  ingestBreakdown,
  ingestVerdict,
  providerSplit,
  spoolFacts,
  storageFacts,
} from "../analytics.js";
import type { IngestState, StoreStatus } from "../types.js";

// RAW is the `derived` block of a real `ai-memory 2.4.0 status --json`
// response, captured on 2026-09-25. It is the fixture for the one field wick
// does not model but the FE still reads through `raw`.
const RAW = {
  derived: {
    embedding_rows: 7,
    embedding_triples: [{ provider: "local", model: "all-MiniLM-L6-v2", dim: 384, count: 7 }],
  },
};

const ingest = (over: Partial<IngestState> = {}): IngestState => ({
  accepted: 0,
  dropped_by_policy: 0,
  shed_saturated: 0,
  shed_rate_limited: 0,
  ...over,
});

describe("growthFacts", () => {
  test("observations are reported per session, which is the shape worth reading", () => {
    const g = growthFacts({ pages_latest: 7, pages_all: 16, sessions: 7, observations: 180 });
    expect(g.find((f) => f.label === "Observations")?.note).toBe("25.7 per session");
    expect(g.find((f) => f.label === "Page versions kept")?.note).toContain("9 older versions");
  });

  test("an empty store does not divide by zero", () => {
    const g = growthFacts({ pages_latest: 0, pages_all: 0, sessions: 0, observations: 0 });
    expect(g.find((f) => f.label === "Observations")?.note).toBe("raw facts behind the pages");
    expect(g.find((f) => f.label === "Page versions kept")?.note).toBe("no older versions");
  });
});

describe("ingestBreakdown", () => {
  test("splits the three failure modes apart — their fixes are opposite", () => {
    const b = ingestBreakdown(ingest({ accepted: 90, dropped_by_policy: 5, shed_saturated: 3, shed_rate_limited: 2 }));
    expect(b.total).toBe(100);
    expect(b.lost).toBe(10);
    expect(b.parts.map((p) => p.pct)).toEqual(["90%", "5.0%", "3.0%", "2.0%"]);
    // Accepted is the only healthy outcome, so it is the only green.
    expect(b.parts[0].bar).toBe("bg-green-500");
    expect(b.parts[1].bar).not.toContain("green");
  });

  test("a zero total renders as '—', not as 0%", () => {
    const b = ingestBreakdown(ingest());
    expect(b.parts.every((p) => p.pct === "—")).toBe(true);
  });

  // No events at all is by far the most common real problem — a hook that
  // was never installed produces exactly this — and a page of 0% would not
  // say so.
  test("no events at all gets its own sentence pointing at the Health tab", () => {
    const v = ingestVerdict(ingestBreakdown(ingest()));
    expect(v).toContain("No events have reached the server");
    expect(v).toContain("Health");
  });

  test("a clean run and a lossy run read differently", () => {
    expect(ingestVerdict(ingestBreakdown(ingest({ accepted: 12 })))).toContain("Every one of 12");
    expect(ingestVerdict(ingestBreakdown(ingest({ accepted: 90, shed_saturated: 10 })))).toContain(
      "10 of 100 events (10%) never became memory",
    );
  });
});

// Palette discipline, as a guard rather than a claim: the bar classes are the
// only colours this module hands to the markup (PLAN §17.3).
test("ingest bar classes stay inside the wick palette", () => {
  for (const p of ingestBreakdown(ingest({ accepted: 1 })).parts) {
    expect(p.bar).not.toMatch(/#|slate-|gray-/);
    // A light surface needs its dark partner; a solid accent does not.
    if (p.bar.includes("white-")) expect(p.bar).toMatch(/dark:/);
  }
});

describe("spoolFacts", () => {
  test("an empty spool is healthy and says why that is good", () => {
    const s = spoolFacts({ pending: 0, retries_total: 0 });
    expect(s.healthy).toBe(true);
    expect(s.oldest).toBe("—");
  });

  // Hooks firing into a server that is not absorbing them is the state where
  // the store looks idle while agents are working.
  test("a backed-up spool names the consequence", () => {
    const s = spoolFacts({ pending: 42, oldest_age_ms: 3_600_000, retries_total: 9 });
    expect(s.healthy).toBe(false);
    expect(s.oldest).toBe("1h 0m");
    expect(s.verdict).toContain("the store will not remember");
  });
});

describe("indexCoverage", () => {
  test("full coverage passes on every row", () => {
    const rows = indexCoverage({
      pages_rows: 16,
      pages_fts_rows: 16,
      observations_rows: 180,
      observations_fts_rows: 180,
      embedding_rows: 7,
      latest_pages_missing_embeddings: 0,
    });
    expect(rows.every((r) => r.ok)).toBe(true);
  });

  // 0 embedding rows is a feature being absent, not a number being low — so
  // it has to say that semantic search is OFF.
  test("zero embeddings is reported as semantic search being off", () => {
    const rows = indexCoverage({
      pages_rows: 16,
      pages_fts_rows: 16,
      observations_rows: 180,
      observations_fts_rows: 180,
      embedding_rows: 0,
      latest_pages_missing_embeddings: 4,
    });
    const emb = rows.find((r) => r.id === "embeddings");
    expect(emb?.ok).toBe(false);
    expect(emb?.note).toContain("semantic search is OFF");
  });

  test("a partial full-text index names how many pages search cannot find", () => {
    const rows = indexCoverage({
      pages_rows: 16,
      pages_fts_rows: 10,
      observations_rows: 0,
      observations_fts_rows: 0,
      embedding_rows: 1,
      latest_pages_missing_embeddings: 0,
    });
    const pages = rows.find((r) => r.id === "pages-fts");
    expect(pages?.ok).toBe(false);
    expect(pages?.note).toContain("6 pages are not in the index");
  });
});

describe("embeddingTriples", () => {
  test("reads the real status document's derived block", () => {
    expect(embeddingTriples(RAW)).toEqual([
      { provider: "local", model: "all-MiniLM-L6-v2", dim: 384, count: 7 },
    ]);
  });

  // `raw` is another program's JSON. A shape change must produce an empty
  // list, never a crashed tab.
  test("anything unexpected produces an empty list rather than throwing", () => {
    expect(embeddingTriples(undefined)).toEqual([]);
    expect(embeddingTriples({})).toEqual([]);
    expect(embeddingTriples({ derived: { embedding_triples: "nope" } })).toEqual([]);
    expect(embeddingTriples({ derived: { embedding_triples: [null, 3] } })).toEqual([]);
    expect(embeddingTriples({ derived: { embedding_triples: [{}] } })).toEqual([
      { provider: "unknown", model: "", dim: 0, count: 0 },
    ]);
  });
});

describe("providerSplit", () => {
  test("groups instances by type and counts recording against read-only", () => {
    expect(
      providerSplit([
        { type: "claude", name: "claude", capture: true },
        { type: "claude", name: "second", capture: false },
        { type: "codex", name: "codex", capture: true },
      ]),
    ).toEqual([
      { type: "claude", total: 2, recording: 1, readOnly: 1 },
      { type: "codex", total: 1, recording: 1, readOnly: 0 },
    ]);
  });

  test("nothing wired is an empty list, not a fabricated row", () => {
    expect(providerSplit(null)).toEqual([]);
  });
});

describe("storage", () => {
  const status = (reclaim: number): StoreStatus =>
    ({
      storage: { database_bytes: 1_044_480, reclaimable_bytes: reclaim, data_dir_free_bytes: 8_391_405_568 },
    }) as StoreStatus;

  test("nothing to reclaim is said out loud, with the cost of doing it anyway", () => {
    const f = storageFacts(status(0));
    expect(f.canCompact).toBe(false);
    expect(f.verdict).toContain("Nothing to reclaim");
    expect(compactConfirmBody(f)).toContain("nothing to reclaim right now");
  });

  test("with free pages the confirm says how much is on offer", () => {
    const f = storageFacts(status(24_576));
    expect(f.canCompact).toBe(true);
    expect(f.reclaimable).toBe("24 KiB");
    expect(compactConfirmBody(f)).toContain("24 KiB to reclaim");
  });

  // Compaction deletes nothing, so the cost to name is not data loss — it is
  // that every agent write blocks for a full database rewrite.
  test("the confirm always states the lock, whichever way it goes", () => {
    for (const reclaim of [0, 24_576]) {
      expect(compactConfirmBody(storageFacts(status(reclaim)))).toContain("exclusive lock");
    }
  });

  test("an unreadable store reads as zeros rather than throwing", () => {
    expect(storageFacts(undefined).database).toBe("0 B");
  });
});
