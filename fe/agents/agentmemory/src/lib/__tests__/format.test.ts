import { describe, test, expect } from "vitest";
import {
  badgeFor,
  captureChip,
  dotFor,
  errText,
  formatBytes,
  formatCount,
  humanDuration,
  instanceLabel,
  isZeroLLM,
  measured,
  portLabel,
  reasonHint,
  reasonTitle,
  uptimeOf,
  warningsFor,
} from "../format.js";
import { emptySettings } from "../settings.js";
import type { Overview, Status, StoreStatus } from "../types.js";

const status = (p: Partial<Status> = {}): Status => ({
  installed: true,
  version: "2.4.0",
  running: true,
  managed: true,
  state: "running",
  pref_port: 49374,
  bound_port: 49374,
  base_url: "http://127.0.0.1:49374",
  ...p,
});

const store = (p: Partial<StoreStatus> = {}): StoreStatus => ({
  version: "2.4.0",
  data_dir: "/srv/mem",
  db_path: "/srv/mem/db.sqlite",
  bind: "127.0.0.1:49374",
  capture_mode: "denylist",
  counts: { pages_latest: 4, pages_all: 6, sessions: 3, observations: 40 },
  index: {
    pages_rows: 4,
    pages_fts_rows: 4,
    observations_rows: 40,
    observations_fts_rows: 40,
    embedding_rows: 12,
    latest_pages_missing_embeddings: 0,
  },
  storage: { database_bytes: 1024, reclaimable_bytes: 0, data_dir_free_bytes: 1024 },
  ingest: { accepted: 40, dropped_by_policy: 0, shed_saturated: 0, shed_rate_limited: 0 },
  spool: { pending: 0, retries_total: 0 },
  llm: { status: "ok", provider: "ollama" },
  embedding: { status: "ok" },
  ...p,
});

const overview = (p: Partial<Overview> = {}): Overview => ({
  backend: { id: "ai-memory", name: "ai-memory", blurb: "", has_data: true },
  daemon: status(),
  settings: { ...emptySettings(), data_dir: "/srv/mem", port: 49374, backfill_max_sessions: 2000 },
  resources: { pid: 42, rss_bytes: 150 * 1024 * 1024, rss_known: true, data_dir_bytes: 4096, data_dir_known: true },
  used_by: [],
  autostart_lock: { locked: false },
  store: store(),
  ...p,
});

describe("daemon presentation", () => {
  test("badge and dot follow the state, using only defined palette shades", () => {
    expect(badgeFor(status({ state: "running" })).text).toBe("Running");
    expect(badgeFor(status({ state: "starting" })).text).toBe("Starting…");
    expect(badgeFor(status({ state: "not-installed" })).text).toBe("Not installed");
    expect(badgeFor(undefined).text).toBe("Stopped");
    // black stops at 600 in the wick palette, so a bg-black-400 dot would be
    // purged and render as nothing.
    expect(dotFor(undefined)).not.toContain("black-400");
    expect(dotFor(status())).toBe("bg-green-500");
    expect(dotFor(status({ state: "starting" }))).toContain("animate-pulse");
  });

  test("uptime is a dash when the daemon is down", () => {
    expect(uptimeOf(status({ running: false, state: "stopped" }))).toBe("—");
  });

  test("uptime of an adopted daemon says unknown rather than inventing one", () => {
    expect(uptimeOf(status({ managed: false }))).toContain("unknown");
  });

  test("uptime counts from the recorded spawn time", () => {
    const now = 1_000_000;
    expect(uptimeOf(status({ started_at_ms: now - 90_000 }), now)).toBe("1m 30s");
  });

  test("humanDuration keeps two units of precision", () => {
    expect(humanDuration(5_000)).toBe("5s");
    expect(humanDuration(3_600_000)).toBe("1h 0m");
    expect(humanDuration(90_000_000)).toBe("1d 1h");
  });

  test("portLabel flags a preference the running daemon has not taken yet", () => {
    expect(portLabel(status())).toBe("49374");
    expect(portLabel(status({ pref_port: 50000 }))).toContain("prefers 50000");
    expect(portLabel(status({ bound_port: 0 }))).toBe("—");
  });
});

describe("numbers", () => {
  test("formatBytes uses binary units", () => {
    expect(formatBytes(0)).toBe("0 B");
    expect(formatBytes(512)).toBe("512 B");
    expect(formatBytes(1536)).toBe("1.5 KiB");
    expect(formatBytes(150 * 1024 * 1024)).toBe("150 MiB");
  });

  test("an unmeasured figure reads as unknown, never as zero", () => {
    expect(measured(0, false)).toBe("unknown");
    expect(measured(0, true)).toBe("0 B");
  });

  test("formatCount groups thousands and dashes a missing number", () => {
    expect(formatCount(48231)).toBe("48,231");
    expect(formatCount(undefined)).toBe("—");
  });
});

describe("instances", () => {
  test("the per-type default instance collapses to its bare type", () => {
    expect(instanceLabel({ type: "claude", name: "claude", capture: true })).toBe("claude");
    expect(instanceLabel({ type: "claude", name: "work", capture: true })).toBe("claude/work");
  });

  test("capture off is its own state, not a dimmer green", () => {
    const on = captureChip({ type: "claude", name: "claude", capture: true });
    const off = captureChip({ type: "codex", name: "codex", capture: false });
    expect(on.text).toBe("recording");
    expect(off.text).toBe("read only");
    expect(off.cls).not.toContain("green");
    expect(off.title).toContain("writes nothing back");
  });
});

describe("warnings", () => {
  test("a healthy store with nothing wired raises nothing", () => {
    expect(warningsFor(overview())).toEqual([]);
  });

  test("no overview yet raises nothing", () => {
    expect(warningsFor(null)).toEqual([]);
  });

  test("zero-LLM explains the real consequence, not the flag name", () => {
    const w = warningsFor(overview({ store: store({ llm: { status: "disabled" } }) }));
    expect(w.map((x) => x.id)).toContain("zero-llm");
    expect(w.find((x) => x.id === "zero-llm")!.body).toContain("truncated");
  });

  test("an LLM stuck in error warns the same way a disabled one does", () => {
    expect(isZeroLLM("ok")).toBe(false);
    expect(isZeroLLM("OK")).toBe(false);
    expect(isZeroLLM("error")).toBe(true);
    expect(isZeroLLM(undefined)).toBe(true);
  });

  test("zero embeddings warns that semantic search is off", () => {
    const w = warningsFor(
      overview({ store: store({ index: { ...store().index, embedding_rows: 0, latest_pages_missing_embeddings: 4 } }) }),
    );
    const e = w.find((x) => x.id === "no-embeddings")!;
    expect(e.body).toContain("4 latest pages");
  });

  test("a piling spool warns, since the store looks idle while agents work", () => {
    const w = warningsFor(overview({ store: store({ spool: { pending: 12, retries_total: 3 } }) }));
    expect(w.find((x) => x.id === "spool")!.body).toContain("12");
  });

  test("an unreadable store surfaces the named reason with its fix", () => {
    const w = warningsFor(overview({ store: undefined, store_error: "nope", store_reason: "web_disabled" }));
    const s = w.find((x) => x.id === "store")!;
    expect(s.title).toBe("The backend's web API is off");
    expect(s.body).toContain("restart the daemon");
    expect(s.level).toBe("warn");
  });

  test("a locked autostart says who holds the lock and how to release it", () => {
    const w = warningsFor(
      overview({ autostart_lock: { locked: true, reason: "Autostart is on because claude uses Agent Memory." } }),
    );
    const l = w.find((x) => x.id === "autostart-lock")!;
    expect(l.body).toContain("claude");
    expect(l.body).toContain("turn the instance's toggle off first");
  });
});

describe("reasons", () => {
  test("the two fixable reasons are told apart", () => {
    expect(reasonTitle("web_disabled")).toContain("web API");
    expect(reasonTitle("daemon_not_running")).toContain("not running");
    expect(reasonHint("daemon_not_running")).toContain("Nothing is lost");
  });

  test("an unnamed reason keeps the raw error rather than inventing advice", () => {
    expect(reasonHint("", "connection refused")).toBe("connection refused");
    expect(reasonTitle("")).toBe("The store could not be read");
  });
});

describe("errText", () => {
  // The exact shape that reached the panel on 2026-09-25: Effect's rendered
  // failure, with a minified bundle frame glued to the end.
  test("an Effect FiberFailure is reduced to the sentence", () => {
    const e = new Error(
      '(FiberFailure) Error: ai-memory not installed: exec: "ai-memory": executable file not found in $PATH at https://support-assistant.qiscus.io/tools/agents/workflow/agentmemory/assets/index-CUGFQL-J.js:23:113516',
    );
    expect(errText(e)).toBe(
      'ai-memory not installed: exec: "ai-memory": executable file not found in $PATH',
    );
  });

  test("the server's own sentence wins over the thrown message", () => {
    expect(errText({ detail: "the web API is disabled", message: "Error: 503" })).toBe(
      "the web API is disabled",
    );
  });

  test("a stack is cut at the first line", () => {
    expect(errText(new Error("boom\n    at foo (bar.js:1:2)"))).toBe("boom");
  });

  // Something has to be said. A blank line where a reason belongs reads as a
  // UI that lost the answer, not as a failure with no message.
  test("an empty failure still says something", () => {
    expect(errText(new Error(""))).toContain("said nothing");
  });
});
