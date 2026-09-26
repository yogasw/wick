import { describe, expect, test } from "vitest";
import {
  STORE_LOCATION_UNKNOWN,
  STORE_REPLACE_NOTE,
  accessWarning,
  autostartNote,
  backfillCapNote,
  emptySettings,
  isDirty,
  nonLoopbackHosts,
  portNote,
  providerMode,
  restartNote,
  retentionSummary,
  storeFacts,
  storeLocationLine,
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

/* Where the store actually is (Yoga, 2026-09-26: "di kasih info juga itu
   storage nya di mana biar jelas, sama bisa di replace ngak").

   The field showed the placeholder "Backend default" and, on this host, is
   empty — so the panel stated a preference and never the fact. It is the port
   bug's family: an operator told "default" and left to guess which directory,
   while the store in use holds 2 pages and the 81 they want sit elsewhere. */

describe("storeFacts", () => {
  const store = (over: object = {}) =>
    ({
      data_dir: "/home/ubuntu/.local/share/ai-memory",
      counts: { pages_latest: 2, pages_all: 4, sessions: 1, observations: 9 },
      ...over,
    }) as never;

  test("an empty field still states the path the daemon resolved", () => {
    const f = storeFacts("", store(), null);
    expect(f.path).toBe("/home/ubuntu/.local/share/ai-memory");
    // Not "configured": it is the backend's own default, and the difference
    // matters the moment someone types into the field.
    expect(f.configured).toBe(false);
    expect(storeLocationLine(f)).toMatch(/Resolved by the backend/);
  });

  test("what is in there, so a wrong path is obvious at a glance", () => {
    const f = storeFacts("", store(), { data_dir_bytes: 1048576, data_dir_known: true } as never);
    expect(f.contents).toContain("2 pages");
    expect(f.contents).toContain("1 session");
    expect(f.contents).toContain("1.0 MiB");
    expect(storeLocationLine(f)).toContain("It holds 2 pages");
  });

  test("a configured path that the running daemon opened is reported as set here", () => {
    const f = storeFacts("/srv/mem", store({ data_dir: "/srv/mem" }), null);
    expect(f.configured).toBe(true);
    expect(storeLocationLine(f)).toMatch(/The path set here/);
  });

  // The pending-restart window: the field says one thing, the daemon opened
  // another. Reporting the field as fact there would be the original bug.
  test("a saved-but-not-restarted change reports what the daemon actually opened", () => {
    const f = storeFacts("/srv/new", store({ data_dir: "/srv/old" }), null);
    expect(f.path).toBe("/srv/old");
    expect(f.configured).toBe(false);
  });

  test("with the daemon down and nothing configured, it says it cannot tell", () => {
    const f = storeFacts("", null, null);
    expect(f.path).toBe("");
    expect(storeLocationLine(f)).toBe(STORE_LOCATION_UNKNOWN);
    expect(STORE_LOCATION_UNKNOWN).toMatch(/cannot tell/i);
  });

  test("with the daemon down, a configured path is still worth showing", () => {
    const f = storeFacts("/srv/mem", null, null);
    expect(f.path).toBe("/srv/mem");
  });

  test("a store with no counts says the path and claims nothing about contents", () => {
    const f = storeFacts("", store({ counts: undefined }), null);
    expect(f.contents).toBe("");
    expect(storeLocationLine(f)).not.toContain("It holds");
  });
});

// Every clause here was checked against the code before it was written, and
// this is what keeps it honest if any of them changes.
describe("STORE_REPLACE_NOTE", () => {
  test("says nothing is moved, copied or merged", () => {
    expect(STORE_REPLACE_NOTE).toMatch(/Nothing is moved, copied or merged/i);
  });

  test("says the old store stays where it is", () => {
    expect(STORE_REPLACE_NOTE).toMatch(/stays on disk exactly where it is/i);
  });

  // ai-memory creates a missing directory — verified by running it against a
  // fresh path — and an empty one opens as an empty store.
  test("says a new path is created and starts empty", () => {
    expect(STORE_REPLACE_NOTE).toMatch(/created if it does not exist/i);
    expect(STORE_REPLACE_NOTE).toMatch(/starts empty/i);
    expect(STORE_REPLACE_NOTE).toMatch(/forgotten everything/i);
  });

  test("says when it takes effect, because saving is not it", () => {
    expect(STORE_REPLACE_NOTE).toMatch(/next time the daemon starts, not when you save/i);
  });
});
