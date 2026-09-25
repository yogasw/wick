import { formatCount, WATCHDOG_CHURN, watchdogDetail } from "./format.js";
import type {
  CollisionCheck,
  ContaminationReport,
  DoctorReport,
  DoctorRow,
  StoreStatus,
  WatchdogState,
} from "./types.js";

// Presentation logic for the Health tab: turn the two diagnostics plus the
// provider state into findings that each carry their own fix.
//
// The rule this module follows is that a finding without an action is only
// half a finding (PLAN §13.3). Every entry below either names the command
// that repairs it, or says plainly that the repair is a decision rather than
// a button.

export type Level = "critical" | "warn" | "ok" | "info";

export type Finding = {
  id: string;
  level: Level;
  title: string;
  body: string;
  // fix is the repair, when one exists. `command` is shown as copyable code;
  // `where` points at a place in wick instead.
  fix?: { label: string; command?: string; where?: string };
};

// levelClasses maps a level onto the wick palette. Only two visual weights
// exist on purpose — an operator triaging a page needs "act" told apart from
// "read", not five shades of yellow.
export function levelClasses(level: Level): { border: string; title: string; chip: string } {
  if (level === "critical" || level === "warn") {
    return {
      border: "border-rose-300 dark:border-rose-700",
      title: "text-rose-700 dark:text-rose-300",
      chip: "bg-rose-100 dark:bg-navy-600 text-rose-700 dark:text-rose-300",
    };
  }
  if (level === "ok") {
    return {
      border: "border-white-300 dark:border-navy-600",
      title: "text-green-600 dark:text-green-300",
      chip: "bg-green-200 dark:bg-green-800 text-green-700 dark:text-green-300",
    };
  }
  return {
    border: "border-white-300 dark:border-navy-600",
    title: "text-black-900 dark:text-white-100",
    chip: "bg-white-300 dark:bg-navy-600 text-black-800 dark:text-black-600",
  };
}

// ── doctor: capture coverage ─────────────────────────────────────────

// doctorFindings turns the capture-coverage rows into findings.
//
// The row that matters is a harness that RAN here recently and has zero
// captured sessions: the harness works, the store looks fine, and nothing it
// does is ever remembered. That is the half-wired state a read-only provider
// produces, and its repair is one command (verified from the CLI's own
// output, ai-memory 2.4.0).
export function doctorFindings(d: DoctorReport | undefined): Finding[] {
  if (!d) return [];
  if (d.error) {
    return [
      {
        id: "doctor-error",
        level: "warn",
        title: "Capture coverage could not be checked",
        body: d.error,
        fix: {
          label: "Most often the scope: the project has to exist in the store before it can be audited.",
          where: "Projects",
        },
      },
    ];
  }
  const rows = d.rows ?? [];
  const scope = `${d.workspace ?? "default"}/${d.project ?? "?"}`;
  if (!rows.length) {
    return [
      {
        id: "doctor-empty",
        level: "info",
        title: `No harness has run in ${scope}`,
        body: `Nothing ran here in the last ${d.since_days ?? 30} days, so there is nothing to compare against the store. This is not a problem — it just means the check has nothing to say yet.`,
      },
    ];
  }
  const out: Finding[] = [];
  for (const r of rows) {
    out.push(captureFinding(r, scope));
  }
  return out;
}

// captureFinding is one harness's row.
function captureFinding(r: DoctorRow, scope: string): Finding {
  const ran = `${formatCount(r.local_recent)} recent of ${formatCount(r.local_total)} local sessions`;
  if (r.uncaptured && r.local_recent > 0) {
    return {
      id: `doctor-${r.agent}`,
      level: "critical",
      title: `${r.agent} ran here but nothing was captured`,
      body:
        `${r.agent} has ${ran} on disk for ${scope} and the store holds ${formatCount(r.captured)} of them. ` +
        "The harness is working; its hook is not reporting. Everything it has learned here is being lost as it happens.",
      fix: {
        label: "Install its lifecycle hook, then re-run this check:",
        command: `ai-memory install-hooks --agent ${r.agent} --apply`,
      },
    };
  }
  if (r.captured < r.local_recent) {
    return {
      id: `doctor-${r.agent}`,
      level: "warn",
      title: `${r.agent} is only partly captured`,
      body: `${ran} for ${scope}, of which ${formatCount(r.captured)} are in the store. The hook is installed but did not catch every session — sessions that ended before it was installed stay missing until they are backfilled.`,
      fix: { label: "Import the missing history from the project's detail panel.", where: "Projects" },
    };
  }
  return {
    id: `doctor-${r.agent}`,
    level: "ok",
    title: `${r.agent} is fully captured`,
    body: `${ran} for ${scope}, and the store holds ${formatCount(r.captured)}.`,
  };
}

// ── contamination ────────────────────────────────────────────────────

// contaminationFinding reports the cross-project audit.
//
// This one is weighted harder here than upstream and the body says why: wick
// sessions carry client data, so a page filed under the wrong project is a
// disclosure to whoever opens that project, not untidiness (PLAN §13.3). The
// cause is known too — project scope is resolved from the FOLDER NAME, so two
// different checkouts that happen to share a basename merge silently
// (PLAN §14.3).
export function contaminationFinding(c: ContaminationReport | undefined): Finding {
  if (!c) {
    return { id: "audit-missing", level: "info", title: "Contamination audit did not run", body: "No result was returned." };
  }
  if (c.error) {
    return {
      id: "audit-error",
      level: "warn",
      title: "Contamination audit could not run",
      body: c.error,
    };
  }
  const n = c.sessions_misbucketed ?? 0;
  if (n === 0) {
    return {
      id: "audit-clean",
      level: "ok",
      title: "No cross-project contamination found",
      body: "Every session's working directory resolves to the project it is filed under, and no observation disagrees with its session.",
    };
  }
  return {
    id: "audit-dirty",
    level: "critical",
    title: `${formatCount(n)} session${n === 1 ? " is" : "s are"} filed under the wrong project`,
    body:
      "Their working directory resolves to a different project than the one they are stored in. This matters more here than it looks: a wick session holds client data, " +
      "so a page under the wrong project is readable by anyone working in that project. Project scope comes from the FOLDER NAME, so two unrelated checkouts sharing a basename merge into one project without any warning.",
    fix: {
      label: "Re-file sessions per working directory, then re-run the audit:",
      command: "ai-memory reorg",
    },
  };
}

// contaminationRows flattens the raw findings for display. Their row shape is
// unverified — the only run available to read off was an empty one — so the
// table renders whatever keys arrive instead of dropping the rows that
// matter most.
export function contaminationRows(c: ContaminationReport | undefined): Record<string, string>[] {
  const rows = c?.findings;
  if (!Array.isArray(rows)) return [];
  return rows
    .filter((r): r is Record<string, unknown> => !!r && typeof r === "object")
    .map((r) => {
      const out: Record<string, string> = {};
      for (const [k, v] of Object.entries(r)) {
        out[k] = v === null || v === undefined ? "—" : typeof v === "object" ? JSON.stringify(v) : String(v);
      }
      return out;
    });
}

// ── zero-LLM ─────────────────────────────────────────────────────────

// zeroLLMFinding is the §9 banner, and it is the reason this tab is not just
// a diagnostics dump.
//
// "Zero-LLM mode" says nothing to anyone. What actually happens is that a
// stored fact is cut at about 80 characters with no marker that anything was
// lost, so a long identifier — an app id, a room id, a job name, a token —
// disappears silently and what comes back later can be a plausible-looking
// guess at the rest of the string. That is the sentence a support engineer
// needs, so that is the sentence this writes.
export function zeroLLMFinding(store: StoreStatus | undefined): Finding | null {
  if (!store) return null;
  const llm = (store.llm?.status ?? "disabled").toLowerCase();
  if (llm === "ok") return null;
  const failing = llm !== "disabled";
  return {
    id: "zero-llm",
    level: "warn",
    title: failing ? `The language model provider is not usable (${store.llm?.status})` : "Running zero-LLM — long facts are stored truncated",
    body:
      (failing && store.llm?.last_error_message ? `${store.llm.last_error_message}. ` : "") +
      "With no working language model, a captured fact is stored roughly as it arrived and cut at about 80 characters — with nothing marking that it was cut. " +
      "Long identifiers are what gets lost: an app id like xwpcl-8enolllf1yukmbt, a room id, a long job name or a token ends up as a prefix, and a later answer can complete that prefix with a plausible guess instead of the real value. " +
      "Recall keeps working; what it recalls is quietly less trustworthy. Choosing a provider is a security decision too — a cloud model means session content leaves this host.",
    fix: { label: "Pick a local or cloud model provider (or accept this deliberately):", where: "Settings → model provider" },
  };
}

// embeddingFinding reports semantic search being off. Separate from the
// zero-LLM finding because they are separate providers and one can be healthy
// while the other is not.
export function embeddingFinding(store: StoreStatus | undefined): Finding | null {
  if (!store) return null;
  const rows = store.index?.embedding_rows ?? 0;
  const missing = store.index?.latest_pages_missing_embeddings ?? 0;
  if (rows > 0 && missing === 0) return null;
  if (rows === 0) {
    return {
      id: "no-embeddings",
      level: "warn",
      title: "Semantic search is off — 0 embeddings stored",
      body: "Every search falls back to whole-token full-text matching, which only matches an identifier typed in full: \"kasir\" finds nothing that \"kasir_prod_db\" finds. Meaning-based recall is unavailable.",
      fix: { label: "Enable an embedding provider, then embed the existing pages:", command: "ai-memory embed" },
    };
  }
  return {
    id: "partial-embeddings",
    level: "warn",
    title: `${formatCount(missing)} latest pages have no embedding`,
    body: "Semantic search silently skips them — they can only be found by an exact full-text token.",
    fix: { label: "Embed the missing pages:", command: "ai-memory embed" },
  };
}

// ── the tab's whole set ──────────────────────────────────────────────

// healthFindings assembles the tab, most urgent first. The ordering is by
// consequence, not by source: a harness capturing nothing and client data in
// the wrong project both cost more than a missing index.
export function healthFindings(
  doctor: DoctorReport | undefined,
  contamination: ContaminationReport | undefined,
  store: StoreStatus | undefined,
  collisions?: CollisionCheck,
  watchdog?: WatchdogState,
): Finding[] {
  const all = [
    ...doctorFindings(doctor),
    contaminationFinding(contamination),
    collisionFinding(collisions),
    watchdogFinding(watchdog),
    zeroLLMFinding(store),
    embeddingFinding(store),
  ].filter((f): f is Finding => f !== null);
  const rank: Record<Level, number> = { critical: 0, warn: 1, info: 2, ok: 3 };
  return all.sort((a, b) => rank[a.level] - rank[b.level]);
}

// ── project collisions ───────────────────────────────────────────────

// collisionFinding reports wick projects whose folders land in ONE ai-memory
// project (PLAN §14.3, §18.1).
//
// This is the finding with no other symptom. Both projects keep working, the
// store looks healthy, and the only trace is one client's agent recalling
// another client's work — which is why it is critical rather than a warning,
// and why "not checked" is reported as its own state instead of as a pass.
export function collisionFinding(c: CollisionCheck | undefined): Finding | null {
  if (!c) return null;
  if (!c.checked) {
    return {
      id: "collisions-unchecked",
      level: "info",
      title: "Project overlap was not checked",
      body: c.reason ?? "wick could not read its own project list, so nothing was compared. This is not a clean result.",
    };
  }
  const rows = c.collisions ?? [];
  if (rows.length === 0) {
    return {
      id: "collisions-ok",
      level: "ok",
      title: "Every project has its own memory",
      body: `${c.scanned} project${c.scanned === 1 ? "" : "s"} checked — no two resolve to the same ai-memory project.`,
    };
  }
  const fixable = rows.some((r) => r.fixable);
  return {
    id: "collisions",
    level: "critical",
    title: rows.length === 1 ? "Two projects share one memory" : `${rows.length} groups of projects share a memory`,
    body:
      rows
        .map((r) => `${r.members.map((m) => m.name || m.folder).join(" and ")} all resolve to ${r.workspace}/${r.project}`)
        .join("; ") +
      ". Everything each of them learns is recalled into the others — including into a session for a different client.",
    fix: fixable
      ? {
          label:
            "Pin the folders that carry no marker of their own. wick writes one automatically for the projects it manages, and skips custom-path folders because they belong to you.",
          where: "the project's folder — add .ai-memory.toml with its own workspace and project",
        }
      : {
          label:
            "Every one of these folders already pins this scope by hand, so nothing here is accidental. If they were meant to stay separate, give each marker its own project name.",
        },
  };
}

// healthVerdict is the one line at the top: how many findings actually need
// acting on. "All clear" is only said when it is true.
export function healthVerdict(findings: Finding[]): string {
  const acting = findings.filter((f) => f.level === "critical" || f.level === "warn").length;
  if (!findings.length) return "Nothing has been checked yet.";
  if (!acting) return "Nothing needs attention — every check passed.";
  return `${acting} of ${findings.length} checks need attention.`;
}


// ── watchdog (PLAN §25) ──────────────────────────────────────────────

// watchdogFinding reports what supervision has had to do.
//
// Three readings, and only two of them belong on this page as something to
// act on. A watchdog that gave up is a daemon that is DOWN and staying down,
// which is the whole silent failure this feature exists to prevent — so it is
// critical. Repeated interventions are a warning: the daemon is coming back
// each time, and that is exactly how a real problem hides. A quiet watchdog
// is reported as a pass rather than omitted, because "nothing here" and "not
// checked" must not look the same.
export function watchdogFinding(w: WatchdogState | undefined): Finding | null {
  if (!w || !w.watching) return null;

  if (w.gave_up) {
    return {
      id: "watchdog-gave-up",
      level: "critical",
      title: "The watchdog gave up restarting this daemon",
      body:
        (w.gave_up_reason ?? `${w.consecutive_failures} starts in a row failed.`) +
        " While it is down, every agent keeps working and silently recalls and records nothing.",
      fix: {
        label: "Read the daemon log for the failure, fix it, then start the daemon — a healthy start resumes supervision.",
        where: "Overview",
      },
    };
  }

  if (w.stopped_by_operator) {
    return {
      id: "watchdog-paused",
      level: "info",
      title: "This daemon was stopped by hand",
      body: "The watchdog is leaving it down on purpose. Nothing is being captured or recalled while it stays that way.",
      fix: { label: "Start it from the Overview tab when it should be running again.", where: "Overview" },
    };
  }

  if (w.restarts >= WATCHDOG_CHURN) {
    return {
      id: "watchdog-churn",
      level: "warn",
      title: `This daemon has been restarted ${w.restarts} times`,
      body:
        watchdogDetail(w) +
        (w.hung_restarts >= WATCHDOG_CHURN
          ? " Repeated hangs are usually the store or the model provider, not the process."
          : " Each restart loses whatever was in flight, so this is worth a look even though the daemon is up."),
      fix: { label: "The daemon log covers the restarts — it is reset on each start, so read it soon after one.", where: "Overview" },
    };
  }

  return {
    id: "watchdog-ok",
    level: "ok",
    title: w.restarts === 0 ? "Supervised, with nothing to report" : "Supervised",
    body: watchdogDetail(w),
  };
}
