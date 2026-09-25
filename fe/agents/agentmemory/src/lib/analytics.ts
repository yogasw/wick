import { formatBytes, formatCount, humanDuration, share } from "./format.js";
import type { IndexState, IngestState, InstanceRef, SpoolState, StoreCounts, StoreStatus } from "./types.js";

// Presentation logic for the Analytics tab.
//
// EVERY number this module produces is STORE-WIDE. It all comes from
// `status`, which takes no --project, so none of it may ever be shown as a
// project's own figure — that mixing is the confusion PLAN §13.5 opens with.
// The tab labels each card accordingly and the Projects tab keeps its own
// scope; nothing crosses.

export const STORE_SCOPE_LABEL = "whole store — every workspace and project";

// ── growth ───────────────────────────────────────────────────────────

export type GrowthFact = { label: string; value: string; note: string };

// growthFacts is what "growth" can honestly be here.
//
// There is no time series anywhere on the surface wick reads: `status` is a
// snapshot and the per-day / 7d / 30d series lives only in the backend's MCP
// briefing, which the dashboard deliberately does not call (PLAN §13.2.1).
// So this reports lifetime totals plus the ratios between them — which is
// where the actionable shape actually is: observations per session says how
// much each session leaves behind, and the version gap says how much history
// a compaction would reclaim.
export function growthFacts(counts: StoreCounts | undefined): GrowthFact[] {
  const c = counts;
  const sessions = c?.sessions ?? 0;
  const gap = Math.max(0, (c?.pages_all ?? 0) - (c?.pages_latest ?? 0));
  return [
    {
      label: "Sessions",
      value: formatCount(c?.sessions),
      note: "captured end to end, lifetime",
    },
    {
      label: "Observations",
      value: formatCount(c?.observations),
      note: sessions ? `${((c?.observations ?? 0) / sessions).toFixed(1)} per session` : "raw facts behind the pages",
    },
    {
      label: "Pages",
      value: formatCount(c?.pages_latest),
      note: "latest version of each",
    },
    {
      label: "Page versions kept",
      value: formatCount(c?.pages_all),
      note: gap ? `${formatCount(gap)} older versions still stored` : "no older versions",
    },
  ];
}

// GROWTH_NOTE explains the absent axis. An analytics tab that shows totals
// where someone expects a curve has to say why, or it reads as a broken chart.
export const GROWTH_NOTE =
  "Lifetime totals. Day-by-day growth and the 7d/30d split come from the backend's MCP briefing call, which this dashboard deliberately does not make — so there is no series to plot here rather than an invented one.";

// ── ingest ───────────────────────────────────────────────────────────

export type IngestPart = {
  id: string;
  label: string;
  value: number;
  pct: string;
  // bar is the fill class for this slice. Accepted is the only healthy one,
  // so it is the only green.
  bar: string;
  note: string;
};

export type IngestBreakdown = {
  total: number;
  parts: IngestPart[];
  // lost is everything that was not accepted — the single number that says
  // whether capture is actually working.
  lost: number;
  lastPersisted: number;
};

// ingestBreakdown splits what the server did with the events hooks sent it.
// The three failure modes are kept apart because their fixes are opposite:
// policy drops mean the capture rules are too tight, shedding means the
// server could not keep up.
export function ingestBreakdown(ing: IngestState | undefined): IngestBreakdown {
  const accepted = ing?.accepted ?? 0;
  const dropped = ing?.dropped_by_policy ?? 0;
  const saturated = ing?.shed_saturated ?? 0;
  const limited = ing?.shed_rate_limited ?? 0;
  const total = accepted + dropped + saturated + limited;
  const part = (id: string, label: string, value: number, bar: string, note: string): IngestPart => ({
    id,
    label,
    value,
    pct: share(value, total),
    bar,
    note,
  });
  return {
    total,
    lost: dropped + saturated + limited,
    lastPersisted: ing?.last_persisted_ms ?? 0,
    parts: [
      part("accepted", "Accepted", accepted, "bg-green-500", "stored — this is the only outcome that becomes memory"),
      part(
        "dropped",
        "Dropped (capture policy)",
        dropped,
        "bg-white-400 dark:bg-navy-500",
        "the capture rules refused these on purpose; a rising share means the rules are too tight",
      ),
      part(
        "saturated",
        "Shed (saturated)",
        saturated,
        "bg-rose-500",
        "the server could not keep up and threw these away — they are gone, not queued",
      ),
      part(
        "limited",
        "Shed (rate limited)",
        limited,
        "bg-rose-500",
        "the hook rate limit refused these; raise hook_rate_per_sec if agents are busier than the limit",
      ),
    ],
  };
}

// ingestVerdict is the one sentence above the bars. Zero events at all is its
// own state and by far the most common real problem: hooks that were never
// installed produce exactly this, and a page of 0% would not say so.
export function ingestVerdict(b: IngestBreakdown): string {
  if (b.total === 0) {
    return "No events have reached the server at all. Either no harness hook is installed, or nothing has run since the daemon last started — the Health tab's capture coverage says which.";
  }
  if (b.lost === 0) return `Every one of ${formatCount(b.total)} events was accepted.`;
  return `${formatCount(b.lost)} of ${formatCount(b.total)} events (${share(b.lost, b.total)}) never became memory.`;
}

// ── spool ────────────────────────────────────────────────────────────

export type SpoolFacts = { pending: number; oldest: string; retries: number; verdict: string; healthy: boolean };

// spoolFacts reads the hook-side queue. Pending piling up is the signature of
// hooks firing into a server that is not absorbing them — the store looks
// idle while agents are working.
export function spoolFacts(sp: SpoolState | undefined): SpoolFacts {
  const pending = sp?.pending ?? 0;
  const age = sp?.oldest_age_ms ?? 0;
  const retries = sp?.retries_total ?? 0;
  return {
    pending,
    oldest: age > 0 ? humanDuration(age) : "—",
    retries,
    healthy: pending === 0,
    verdict:
      pending === 0
        ? "Nothing is waiting. Hooks are handing events over as fast as they fire them."
        : `${formatCount(pending)} event${pending === 1 ? "" : "s"} written by hooks and not yet absorbed${
            age > 0 ? `, the oldest ${humanDuration(age)} old` : ""
          }. The daemon is down or refusing them — until it drains, work is happening that the store will not remember.`,
  };
}

// ── index coverage ───────────────────────────────────────────────────

export type Coverage = {
  id: string;
  label: string;
  covered: number;
  total: number;
  pct: string;
  ok: boolean;
  note: string;
};

// indexCoverage is the "can search actually find this?" block. FTS rows below
// their table's row count means full-text search is blind to the difference;
// zero embeddings means semantic search is off entirely, which is a feature
// being absent rather than a number being low.
export function indexCoverage(ix: IndexState | undefined): Coverage[] {
  const pages = ix?.pages_rows ?? 0;
  const pagesFTS = ix?.pages_fts_rows ?? 0;
  const obs = ix?.observations_rows ?? 0;
  const obsFTS = ix?.observations_fts_rows ?? 0;
  const emb = ix?.embedding_rows ?? 0;
  const missing = ix?.latest_pages_missing_embeddings ?? 0;
  return [
    {
      id: "pages-fts",
      label: "Full-text index · pages",
      covered: pagesFTS,
      total: pages,
      pct: share(pagesFTS, pages),
      ok: pages === 0 || pagesFTS >= pages,
      note:
        pagesFTS >= pages
          ? "every page is searchable"
          : `${formatCount(pages - pagesFTS)} pages are not in the index — search cannot find them at all. Rebuild with \`ai-memory reindex\` (daemon stopped).`,
    },
    {
      id: "observations-fts",
      label: "Full-text index · observations",
      covered: obsFTS,
      total: obs,
      pct: share(obsFTS, obs),
      ok: obs === 0 || obsFTS >= obs,
      note:
        obsFTS >= obs
          ? "every observation is searchable"
          : `${formatCount(obs - obsFTS)} observations are not in the index.`,
    },
    {
      id: "embeddings",
      label: "Embeddings",
      covered: emb,
      total: ix?.pages_rows ?? 0,
      pct: emb === 0 ? "0%" : share(emb, pages),
      ok: emb > 0 && missing === 0,
      note:
        emb === 0
          ? "0 rows — semantic search is OFF. Every query falls back to whole-token full-text matching, so a search only hits an identifier when it is typed in full."
          : missing > 0
            ? `${formatCount(missing)} of the latest pages have no embedding, so semantic search silently skips them.`
            : "every latest page is embedded",
    },
  ];
}

// ── embeddings per provider ──────────────────────────────────────────

export type EmbeddingTriple = { provider: string; model: string; dim: number; count: number };

// embeddingTriples reads `derived.embedding_triples` out of the backend's raw
// status document. wick's own StoreStatus does not model it, which is exactly
// what `raw` is kept for — and this is the only genuine per-provider split
// the status document carries (verified against ai-memory 2.4.0).
//
// It is defensive on purpose: `raw` is another program's JSON, so a shape
// change must produce an empty list, never a crashed tab.
export function embeddingTriples(raw: unknown): EmbeddingTriple[] {
  const derived = (raw as { derived?: { embedding_triples?: unknown } } | undefined)?.derived;
  const rows = derived?.embedding_triples;
  if (!Array.isArray(rows)) return [];
  const out: EmbeddingTriple[] = [];
  for (const r of rows) {
    if (!r || typeof r !== "object") continue;
    const t = r as Record<string, unknown>;
    out.push({
      provider: typeof t.provider === "string" ? t.provider : "unknown",
      model: typeof t.model === "string" ? t.model : "",
      dim: typeof t.dim === "number" ? t.dim : 0,
      count: typeof t.count === "number" ? t.count : 0,
    });
  }
  return out;
}

// ── capture per provider ─────────────────────────────────────────────

export type ProviderSplit = { type: string; total: number; recording: number; readOnly: number };

// providerSplit groups the wired provider instances by type.
//
// This is a CONFIGURATION split, not a captured-sessions split: no source wick
// reads breaks sessions down by harness store-wide (`status` has no such
// field). Per-project captured counts per harness do exist — they are the
// doctor rows on the Health tab — so this card says which providers are
// wired and whether they record, and points there for what actually landed.
export function providerSplit(used: InstanceRef[] | null | undefined): ProviderSplit[] {
  const by = new Map<string, ProviderSplit>();
  for (const r of used ?? []) {
    const row = by.get(r.type) ?? { type: r.type, total: 0, recording: 0, readOnly: 0 };
    row.total++;
    if (r.capture) row.recording++;
    else row.readOnly++;
    by.set(r.type, row);
  }
  return [...by.values()].sort((a, b) => a.type.localeCompare(b.type));
}

export const PROVIDER_SPLIT_NOTE =
  "Which providers are wired to Agent Memory and whether they record — not how many sessions each one captured. " +
  "No store-wide per-harness count exists on the surface this panel reads; the Health tab's capture coverage has the real per-harness numbers for one project.";

// ── storage ──────────────────────────────────────────────────────────

export type StorageFacts = {
  database: string;
  reclaimable: string;
  free: string;
  reclaimableBytes: number;
  // canCompact is false when there is nothing to win. Compaction still runs,
  // but blocking every write to reclaim 0 bytes is not a trade worth offering
  // without saying so.
  canCompact: boolean;
  verdict: string;
};

export function storageFacts(store: StoreStatus | undefined): StorageFacts {
  const st = store?.storage;
  const db = st?.database_bytes ?? 0;
  const reclaim = st?.reclaimable_bytes ?? 0;
  const free = st?.data_dir_free_bytes ?? 0;
  return {
    database: formatBytes(db),
    reclaimable: formatBytes(reclaim),
    free: formatBytes(free),
    reclaimableBytes: reclaim,
    canCompact: reclaim > 0,
    verdict:
      reclaim > 0
        ? `${formatBytes(reclaim)} of the ${formatBytes(db)} database is free pages a compaction would give back to the filesystem.`
        : `Nothing to reclaim: the ${formatBytes(db)} database has no free pages. Compacting now would block every write and return nothing.`,
  };
}

// COMPACT_CONFIRM_BODY is the ConfirmDialog text. Compaction deletes nothing,
// so the cost to name is not data loss — it is that every write blocks for the
// length of a full database rewrite, and that it needs roughly the database's
// own size in free disk (from `ai-memory compact --help`, 2.4.0).
export function compactConfirmBody(f: StorageFacts): string {
  const head =
    "Compaction deletes nothing. It rebuilds the search indexes and VACUUMs the database, taking an exclusive lock: every agent write blocks until it finishes, and it needs free disk of roughly the database's own size.";
  return f.canCompact
    ? `${head} There is ${f.reclaimable} to reclaim right now.`
    : `${head} There is nothing to reclaim right now (0 free pages), so this would pay the whole cost for no gain.`;
}
