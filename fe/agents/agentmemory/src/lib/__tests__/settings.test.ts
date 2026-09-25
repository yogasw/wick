import { describe, expect, test } from "vitest";
import {
  accessWarning,
  emptySettings,
  autostartNote,
  backfillCapNote,
  isDirty,
  nonLoopbackHosts,
  portNote,
  providerMode,
  restartNote,
  retentionSummary,
} from "../settings.js";
import type { Settings } from "../types.js";

const base: Settings = emptySettings();

describe("access & security", () => {
  test("the loopback default raises nothing", () => {
    expect(nonLoopbackHosts("localhost, 127.0.0.1, ::1")).toEqual([]);
    expect(accessWarning({ ...base, allowed_hosts: "localhost,127.0.0.1" })).toBeNull();
  });

  test("case does not hide a loopback name", () => {
    expect(nonLoopbackHosts("LocalHost,127.0.0.1")).toEqual([]);
  });

  // The one combination with no safe reading.
  test("a LAN host with no token is a danger, and the warning names the host", () => {
    const w = accessWarning({ ...base, allowed_hosts: "localhost,homelab" });
    expect(w?.level).toBe("danger");
    expect(w?.body).toContain("homelab");
    expect(w?.body).toMatch(/every captured prompt/i);
  });

  test("a LAN host WITH a token is a warning, not a danger", () => {
    const w = accessWarning({ ...base, allowed_hosts: "homelab", auth_token: "••••••••" });
    expect(w?.level).toBe("warn");
  });

  test("a whitespace-only token is no token", () => {
    expect(accessWarning({ ...base, allowed_hosts: "homelab", auth_token: "   " })?.level).toBe("danger");
  });
});

describe("retention", () => {
  // 0 is the default AND a real setting. It must never render as "unset".
  test("zero says 'never pruned' and explains the growth it implies", () => {
    const got = retentionSummary(0);
    expect(got.headline).toMatch(/never pruned/i);
    expect(got.danger).toBe(false);
    expect(got.body).toMatch(/growth driver/i);
  });

  test("a positive age states that pruning is irreversible and why", () => {
    const got = retentionSummary(90);
    expect(got.headline).toContain("90");
    expect(got.danger).toBe(true);
    expect(got.body).toMatch(/never be re-consolidated/i);
  });
});

describe("provider mode", () => {
  test("no provider is zero-LLM", () => {
    expect(providerMode(base)).toBe("zero");
  });

  test("a local runtime keeps data on this host", () => {
    expect(providerMode({ ...base, llm_provider: "ollama" })).toBe("local");
    expect(providerMode({ ...base, llm_provider: "vLLM" })).toBe("local");
  });

  // Anything unrecognised is treated as cloud — the safe direction, because
  // guessing "local" would understate an egress path.
  test("an unrecognised provider counts as cloud", () => {
    expect(providerMode({ ...base, llm_provider: "anthropic" })).toBe("cloud");
    expect(providerMode({ ...base, llm_provider: "something-new" })).toBe("cloud");
  });
});

describe("daemon notes", () => {
  test("a saved change on a RUNNING daemon says it is not live yet", () => {
    expect(restartNote(true, true)).toMatch(/restart/i);
    expect(restartNote(true, false)).toMatch(/when the daemon starts/i);
    expect(restartNote(false, false)).toMatch(/will start with these/i);
  });

  test("a locked autostart shows who holds it", () => {
    expect(autostartNote(true, "Autostart is on because claude uses Agent Memory.")).toContain("claude");
    expect(autostartNote(false, undefined)).toMatch(/when wick boots/i);
  });

  test("port 0 is explained as the backend default, and a remap is named", () => {
    expect(portNote(0, 49374, undefined)).toContain("49374");
    expect(portNote(49374, 49374, { bound_port: 49375 } as never)).toMatch(/taken/i);
    expect(portNote(49374, 49374, { bound_port: 49374 } as never)).toContain("127.0.0.1:49374");
  });
});

describe("backfill cap", () => {
  test("unset names wick's ceiling and why the backend's 25 is not inherited", () => {
    expect(backfillCapNote(0)).toContain("2000");
    expect(backfillCapNote(0)).toContain("25");
  });

  test("a small cap warns that the rest is silently skipped, not failed", () => {
    expect(backfillCapNote(25)).toMatch(/skipped for cap/i);
  });
});

describe("isDirty", () => {
  test("an untouched form is clean", () => {
    expect(isDirty({ ...base }, { ...base })).toBe(false);
  });

  test("one changed field is dirty", () => {
    expect(isDirty({ ...base, observation_retention_days: 90 }, base)).toBe(true);
  });

  test("nothing loaded yet is not dirty", () => {
    expect(isDirty(null, base)).toBe(false);
  });
});
