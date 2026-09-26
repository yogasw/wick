import { describe, test, expect } from "vitest";
import {
  BRIEFING_UNAVAILABLE,
  BACKFILL_EXPLAINER,
  BACKFILL_SELECTED_NOTE,
  backfillCapWarning,
  countsNote,
  harnessRows,
  localTotal,
  backfillConfirmBody,
  backfillSummary,
  briefingReason,
  handoffCellText,
  handoffDetail,
  latestPages,
  metricText,
  metricTitle,
  oldestHandoff,
  pageLabel,
  projectKey,
  projectMetrics,
  scopeCaveat,
  scopeKeyOf,
  scopeOrigin,
  sortProjects,
} from "../projects.js";
import type { BackfillReport, ProjectBriefing, ProjectRow } from "../types.js";

const row = (project: string, last?: string, pages = 3): ProjectRow => ({
  workspace: "default",
  project,
  page_count: pages,
  last_updated: last,
});

// briefing mirrors a real memory_briefing payload (ai-memory 2.4.0,
// default/proj2, captured 2026-09-25) with the two activity windows given
// DIFFERENT numbers — equal ones would not catch 30d being wired to 7d.
const briefing = (over: Partial<ProjectBriefing> = {}): ProjectBriefing => ({
  counts: { pages_latest: 6, pages_all: 15, sessions: 6, observations: 172, evidence_rows: 0 },
  activity_7d: { days: 7, sessions: 4, observations: 120, pages_updated: 5 },
  activity_30d: { days: 30, sessions: 6, observations: 172, pages_updated: 6 },
  last_observation_at: "2026-09-24T17:04:40.437113Z",
  pending_handoff_count: 2,
  pending_message_count: 0,
  recent_pages: [
    { path: "sessions/b047548c.md", title: "Panggil tool memory_status", kind: "session", updated_at: "2026-09-24T17:04:40.440131Z" },
    { path: "sessions/227a6362.md", title: "", kind: "session", updated_at: "2026-09-24T17:04:39.641325Z" },
  ],
  cross_project_dependents: 0,
  cross_project_dependencies: 0,
  ...over,
});

const briefed = (project: string, last?: string, over?: Partial<ProjectBriefing>): ProjectRow => ({
  ...row(project, last),
  briefing: briefing(over),
});

const report = (over: Partial<BackfillReport> = {}): BackfillReport => ({
  selected: 0,
  imported_sessions: 0,
  imported_events: 0,
  skipped_for_cap: 0,
  failed_sessions: 0,
  skipped_non_empty: false,
  dry_run: false,
  ...over,
});

describe("sortProjects", () => {
  test("most recently touched first", () => {
    const rows = sortProjects([
      row("old", "2026-01-01T00:00:00Z"),
      row("new", "2026-09-01T00:00:00Z"),
    ]);
    expect(rows.map((r) => r.project)).toEqual(["new", "old"]);
  });

  // A project the store has never dated is not "the oldest" — it has no date
  // at all, and sorting it to the top would put the least informative row in
  // the place the eye lands first.
  test("an undated project sorts last, not first", () => {
    const rows = sortProjects([row("undated"), row("dated", "2026-01-01T00:00:00Z")]);
    expect(rows.map((r) => r.project)).toEqual(["dated", "undated"]);
  });

  test("does not mutate its input", () => {
    const input = [row("b", "2026-01-01T00:00:00Z"), row("a", "2026-09-01T00:00:00Z")];
    sortProjects(input);
    expect(input.map((r) => r.project)).toEqual(["b", "a"]);
  });
});

describe("projectMetrics", () => {
  const now = Date.parse("2026-09-25T12:00:00Z");

  test("pages and last activity are the two the store can actually answer", () => {
    const m = projectMetrics(row("files", "2026-09-25T10:00:00Z", 1234), undefined, now);
    expect(m.pages).toEqual({ available: true, value: "1,234", note: "latest version of each" });
    expect(metricText(m.lastActive)).toBe("2h 0m ago");
  });

  // The three briefing-fed columns, filled from a real payload.
  test("sessions, observations and activity come from the briefing", () => {
    const m = projectMetrics(briefed("proj2", "2026-09-25T10:00:00Z"), undefined, now);
    expect(metricText(m.sessions)).toBe("6");
    expect(metricText(m.observations)).toBe("172");
    // Both windows, in that order — 0 in 7d alone reads as a dead project
    // until the 30d number says it was busy last month.
    expect(metricText(m.activity)).toBe("4 / 6");
    expect(metricTitle(m.activity)).toContain("4 in the last 7 days");
    expect(metricTitle(m.activity)).toContain("6 in the last 30");
    // The unit has to be named: "4 / 6" under a header reading "Activity
    // 7d/30d" does not say whether it counts sessions, pages or observations.
    expect(metricTitle(m.activity)).toContain("120 / 172 observations");
  });

  // Still the rule §13.5 opens with: a row the backend could not brief shows
  // an explicit absence carrying the reason, NEVER the store-wide total
  // sitting one tab away.
  test("a row with no briefing is unavailable WITH the reason", () => {
    const m = projectMetrics(row("files"), undefined, now);
    for (const cell of [m.sessions, m.observations, m.activity]) {
      expect(cell.available).toBe(false);
      expect(metricText(cell)).toBe("n/a");
      expect(metricTitle(cell)).toBe(BRIEFING_UNAVAILABLE);
    }
    expect(BRIEFING_UNAVAILABLE).toMatch(/briefing/i);
    expect(BRIEFING_UNAVAILABLE).toMatch(/store-wide/i);
  });

  // Zero is a FACT when the briefing answered. A project that exists and has
  // never been written to must read "0", not "n/a" — the two say different
  // things and only one of them is measured.
  test("a briefed but empty project shows 0, not n/a", () => {
    const empty = briefed("scratch", undefined, {
      counts: { pages_latest: 0, pages_all: 0, sessions: 0, observations: 0, evidence_rows: 0 },
      activity_7d: { days: 7, sessions: 0, observations: 0, pages_updated: 0 },
      activity_30d: { days: 30, sessions: 0, observations: 0, pages_updated: 0 },
    });
    const m = projectMetrics(empty, undefined, now);
    expect(metricText(m.sessions)).toBe("0");
    expect(metricText(m.activity)).toBe("0 / 0");
    expect(m.sessions.available).toBe(true);
  });

  // The backend's own message is what tells a daemon that died mid-list apart
  // from a project renamed out from under the listing.
  test("a per-row briefing error carries the backend's message", () => {
    const failed: ProjectRow = { ...row("gone"), briefing_error: "project 'gone' not found in workspace 'default'" };
    expect(briefingReason(failed)).toContain("not found in workspace");
    expect(metricTitle(projectMetrics(failed, undefined, now).sessions)).toContain("not found in workspace");
  });

  // The swap point. If the briefing call is ever allowed, filling it in here
  // is the only edit — so the override has to actually win.
  test("an override fills an unavailable cell without touching the rest", () => {
    const m = projectMetrics(row("files", "2026-09-25T10:00:00Z"), {
      sessions: { available: true, value: "12", note: "captured" },
    }, now);
    expect(metricText(m.sessions)).toBe("12");
    expect(m.observations.available).toBe(false);
    expect(m.pages.available).toBe(true);
  });

  // …and it still wins over a briefing that DID answer, which is what makes
  // this the one seam a caller can reach through.
  test("an override beats the briefing's own number", () => {
    const m = projectMetrics(briefed("proj2"), { sessions: { available: false, reason: "suppressed" } }, now);
    expect(metricText(m.sessions)).toBe("n/a");
    expect(metricText(m.observations)).toBe("172");
  });

  test("a project that was never written to says so", () => {
    const m = projectMetrics(row("fresh"), undefined, now);
    expect(metricText(m.lastActive)).toBe("never");
    expect(metricTitle(m.lastActive)).toBe("never written to");
  });
});

describe("handoff cells", () => {
  // An unread count is not zero. A project whose handoffs could not be read
  // has not been shown to have none.
  test("unknown and error never render as 0", () => {
    expect(handoffCellText(undefined)).toBe("—");
    expect(handoffCellText({ state: "error", message: "boom" })).toBe("?");
    expect(handoffCellText({ state: "ok", count: 0 })).toBe("0");
  });

  test("the detail line separates a failed read from an empty one", () => {
    expect(handoffDetail({ state: "ok", count: 0 })).toEqual({
      text: "None open — nothing is waiting to be picked up by another agent.",
      error: false,
    });
    expect(handoffDetail({ state: "ok", count: 2 }).text).toContain("2 open batons");
    expect(handoffDetail({ state: "error", message: "404" })).toEqual({
      text: "Could not be read: 404",
      error: true,
    });
    expect(handoffDetail(undefined).error).toBe(false);
  });

  test("oldestHandoff picks the one that has been waiting longest", () => {
    const rows = [
      { id: "new", created_at_ms: 2000 },
      { id: "old", created_at_ms: 1000 },
      { id: "undated" },
    ];
    expect(oldestHandoff(rows)?.id).toBe("old");
    expect(oldestHandoff([])).toBeNull();
  });
});

describe("backfill reporting", () => {
  // selected 0 + skipped_non_empty is the NORMAL outcome on a store that
  // already has sessions. Without this sentence the button looks broken.
  test("the no-op explains itself instead of reading as a failure", () => {
    const s = backfillSummary(report({ skipped_non_empty: true, selected: 0 }));
    expect(s).toContain("already has captured sessions");
    expect(s).toContain("observations a second time");
  });

  test("an empty project is told apart from a skipped one", () => {
    expect(backfillSummary(report({ selected: 0 }))).toContain("No harness session files were found");
  });

  // The bug Yoga hit first: the card printed "59 sessions selected, would
  // import 0" because the explanation was gated on selected === 0 as well. A
  // real run on a non-empty store reports BOTH — 59 found, every one skipped.
  test("sessions found and none imported is explained, not left as a bare 0", () => {
    const s = backfillSummary(report({ skipped_non_empty: true, selected: 59, dry_run: true }));
    expect(s).toContain("59 local sessions found for this project, none imported");
    expect(s).toContain("already has captured sessions");
    expect(s).not.toContain("would import 0");
  });

  /* The bug underneath that one: `ai-memory backfill --dry-run` NEVER fills
     imported_sessions. Measured on this host at --max-sessions 1, 2 and 2000,
     and on a scope with skipped_non_empty false where nothing was blocking an
     import — 0 every time. So "would import 0" was not a result, it was a
     field the preview does not fill, and the user read it as the feature
     being broken. A preview reports its selection and stops. */

  test("a preview reports what it would READ and never an import count", () => {
    const s = backfillSummary(report({ selected: 217, imported_sessions: 0, dry_run: true }));
    expect(s).toContain("217 session files would be read");
    expect(s).not.toMatch(/import 0|imported 0|0 sessions/);
    expect(s).toMatch(/not what importing them would write/i);
  });

  test("one selected file is still singular", () => {
    expect(backfillSummary(report({ selected: 1, dry_run: true }))).toContain("1 session file would be read");
  });

  test("a real import counts what it took", () => {
    const s = backfillSummary(report({ selected: 4, imported_sessions: 4, imported_events: 190 }));
    expect(s).toContain("4 session files read");
    expect(s).toContain("imported 4 sessions");
    expect(s).toContain("190 events");
  });

  // A real run that genuinely imported nothing is a fact and may say so —
  // that is exactly the case a preview cannot speak to.
  test("a real run that imported nothing says so", () => {
    expect(backfillSummary(report({ selected: 3, imported_sessions: 0 }))).toContain("imported 0 sessions");
  });

  // "No sessions found" while the panel lists wick sessions reads as a bug.
  // It is not one — they are different things — so the sentence says where it
  // looked and what it counted.
  test("nothing found names where it looked, and what it is not", () => {
    const s = backfillSummary(report({ selected: 0, dry_run: true }));
    expect(s).toMatch(/in this project's folder/i);
    expect(s).toMatch(/not the same as having no sessions/i);
    expect(s).toMatch(/claude and codex/i);
  });

  // "apakah buka claude codex atau gimana sih?" — the answer is no, said
  // outright rather than left to be inferred from "reads the local history".
  test("the explainer denies launching anything, in so many words", () => {
    expect(BACKFILL_EXPLAINER).toMatch(/does not start claude or codex/i);
    expect(BACKFILL_EXPLAINER).toMatch(/sign in as anyone/i);
    expect(BACKFILL_EXPLAINER).toMatch(/calls no model and no network service/i);
    expect(BACKFILL_EXPLAINER).toMatch(/one-time bootstrap rather than a sync/i);
    expect(BACKFILL_SELECTED_NOTE).toMatch(/harness transcript files/i);
  });


  // The backend's own cap default is 25 — low enough to leave most of a long
  // project behind without saying anything (PLAN §10.6).
  test("the max-sessions cap is surfaced, never swallowed", () => {
    expect(backfillCapWarning(report())).toBeNull();
    const w = backfillCapWarning(report({ skipped_for_cap: 30 }));
    expect(w).toContain("30 sessions were left out");
    expect(w).toContain("Settings");
  });

  // "Raise it in Settings" is advice you cannot act on without knowing what
  // the cap is now, and the report does not carry it — only the echoed
  // request the server sends back beside it does.
  test("the cap's actual value is named when the server sent it", () => {
    const w = backfillCapWarning(report({ skipped_for_cap: 30 }), {
      scope: { workspace: "w", project: "p" },
      dry_run: false,
      force: false,
      max_sessions: 2000,
    });
    expect(w).toContain("max-sessions cap of 2000");
  });

  // An older server, or a preview that resolved no cap, still gets a usable
  // sentence — just without the number. Silence would be worse than vague.
  test("without the echo the warning still reads", () => {
    const w = backfillCapWarning(report({ skipped_for_cap: 30 }), null);
    expect(w).toContain("30 sessions were left out");
    expect(w).not.toContain("cap of");
  });
});

describe("backfillConfirmBody", () => {
  test("the unforced import promises no duplication", () => {
    expect(backfillConfirmBody("default/files", false)).toContain("nothing is duplicated");
  });

  // §13.5 point 2: a confirmation states the consequence. "Are you sure?"
  // would not tell anyone that their observations are about to be doubled.
  test("the forced import names the consequence, not just the flag", () => {
    const body = backfillConfirmBody("default/files", true);
    expect(body).toContain("observations do NOT");
    expect(body).toContain("stored a second time");
  });
});

test("projectKey is workspace/project", () => {
  expect(projectKey(row("files"))).toBe("default/files");
});

describe("latestPages", () => {
  // Three states, because "no pages" and "could not be read" are different
  // facts and one line cannot serve both.
  test("a briefed project lists its recent pages, newest first as sent", () => {
    const p = latestPages(briefed("proj2"));
    expect(p.state).toBe("ok");
    if (p.state !== "ok") return;
    expect(p.pages).toHaveLength(2);
    expect(pageLabel(p.pages[0])).toBe("Panggil tool memory_status");
  });

  // A page whose title was never written still has to be identifiable, and
  // its store path identifies it.
  test("an untitled page falls back to its path, never to a blank", () => {
    const p = latestPages(briefed("proj2"));
    if (p.state !== "ok") throw new Error("expected ok");
    expect(pageLabel(p.pages[1])).toBe("sessions/227a6362.md");
  });

  test("a briefed project with no pages is empty, not unavailable", () => {
    expect(latestPages(briefed("scratch", undefined, { recent_pages: [] })).state).toBe("empty");
    expect(latestPages(briefed("scratch", undefined, { recent_pages: null })).state).toBe("empty");
  });

  test("an unbriefed project is unavailable WITH the reason", () => {
    const p = latestPages(row("files"));
    expect(p.state).toBe("unavailable");
    if (p.state !== "unavailable") return;
    expect(p.reason).toBe(BRIEFING_UNAVAILABLE);
  });

  test("no selected project is unavailable rather than a crash", () => {
    expect(latestPages(null).state).toBe("unavailable");
  });
});

// ── the project this panel was opened for (PLAN §22) ─────────────────

describe("scope of an opened project", () => {
  test("the selection key is the same form the table's rows use", () => {
    expect(scopeKeyOf({ workspace: "wick", project: "kasir-8c28230d" })).toBe("wick/kasir-8c28230d");
  });

  // Only the basename case can collide, so only it earns a caveat — and the
  // caveat has to name the collision, not just the mechanism.
  test("only a basename-derived bucket carries a caveat, and it names the risk", () => {
    expect(scopeCaveat("marker")).toBeNull();
    expect(scopeCaveat("wick")).toBeNull();
    const c = scopeCaveat("basename");
    expect(c).toContain("folder NAME");
    expect(c).toMatch(/same name would share this memory/i);
  });

  test("every source says in plain words where the bucket came from", () => {
    expect(scopeOrigin("marker")).toContain(".ai-memory.toml");
    expect(scopeOrigin("wick")).toContain("next time a session runs");
    expect(scopeOrigin("basename")).toContain("folder name");
  });
});

/* The decomposition behind the count (Yoga, 2026-09-26: "itu katanya found
   tapi aku kok ngak yakin itu ada beneran"). It is doctor's own output, from
   the same discovery the backfill uses — 3 + 8 on this host's project
   06c162e0, whose dry run selected exactly 11. */

describe("what is behind the found count", () => {
  const doctor = {
    rows: [
      { agent: "claude-code", local_total: 3, local_recent: 3, captured: 33, uncaptured: false },
      { agent: "codex", local_total: 8, local_recent: 2, captured: 26, uncaptured: true },
    ],
  };

  test("each harness's own numbers survive the mapping", () => {
    const rows = harnessRows(doctor);
    expect(rows).toHaveLength(2);
    expect(rows[0]).toEqual({ agent: "claude-code", local: 3, recent: 3, captured: 33, uncaptured: false });
    expect(rows[1].uncaptured).toBe(true);
  });

  test("the total is the sum the dry run should match", () => {
    expect(localTotal(doctor)).toBe(11);
  });

  // A failed check has not shown there is nothing there.
  test("a check that could not run is null, not zero", () => {
    expect(localTotal({ error: "daemon not running" })).toBeNull();
    expect(localTotal(null)).toBeNull();
    expect(harnessRows(null)).toEqual([]);
  });

  test("agreement is stated as agreement", () => {
    expect(countsNote(11, 11)).toMatch(/The two agree: 11 session files/);
  });

  // A visible disagreement is information; a hidden one is the bug that cost
  // this whole morning.
  test("a disagreement shows BOTH numbers rather than picking one", () => {
    const n = countsNote(5, 11) ?? "";
    expect(n).toMatch(/do not match/i);
    expect(n).toContain("11");
    expect(n).toContain("5");
    expect(n).toMatch(/neither is adjusted/i);
  });

  test("nothing is claimed when either number is missing", () => {
    expect(countsNote(null, 11)).toBeNull();
    expect(countsNote(11, null)).toBeNull();
  });
});
