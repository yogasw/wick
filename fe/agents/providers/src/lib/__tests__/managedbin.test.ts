import { afterEach, describe, expect, it, vi } from "vitest";
import {
  normalizeManaged,
  jobLabel,
  jobPct,
  downloadPct,
  jobSummary,
  jobShort,
  sessionsNote,
  isRunning,
  versionRows,
  latestState,
  apiManagedDownload,
  apiManagedCheck,
  CheckTooSoonError,
} from "../managedbin";

describe("managedbin client", () => {
  const m = normalizeManaged({
    type: "omp",
    host_label: "linux-x64 · glibc · AVX2",
    current: "18.4.3",
    installed: [
      { version: "18.4.3", tag: "v18.4.3", current: true, in_use: 1 },
      { version: "18.4.2", tag: "v18.4.2", in_use: 2, removable: false },
      { version: "18.3.0", tag: "v18.3.0", removable: true },
    ],
    latest: { tag: "v18.4.4", version: "18.4.4" },
    releases: [
      { tag: "v18.4.4", version: "18.4.4" },
      { tag: "v18.4.3", version: "18.4.3" },
      { tag: "v18.4.2", version: "18.4.2" },
      { tag: "v18.5.0-rc.1", version: "18.5.0-rc.1", prerelease: true },
      { tag: "v18.10.0", version: "18.10.0" },
    ],
    update_available: true,
    sessions_on_old: { "18.4.2": 2 },
    job: { phase: "download", done: 50 << 20, total: 200 << 20, tag: "v18.4.4", version: "18.4.4", bytes_per_sec: 5 << 20 },
  });

  it("normalizes the wire shape", () => {
    expect(m.hostLabel).toBe("linux-x64 · glibc · AVX2");
    expect(m.latest).toBe("v18.4.4");
    expect(m.latestVersion).toBe("18.4.4");
    expect(m.releases).toHaveLength(5);
    expect(m.installed[1]).toMatchObject({ version: "18.4.2", inUse: 2, removable: false });
    expect(m.job).toMatchObject({ activate: false, bytesPerSec: 5 << 20 });
    expect(isRunning(m.job)).toBe(true);
  });

  it("labels job progress and its percentage", () => {
    // Label: step + byte-level download %. Bar: overall % weighted by phase,
    // so it always carries a number (download band = 5..85).
    expect(jobLabel(m.job!)).toBe("Step 2/5 · Downloading v18.4.4… 25% (50 MB / 200 MB · 5.0 MB/s)");
    expect(jobShort(m.job!)).toBe("Downloading v18.4.4… 25%");
    expect(downloadPct(m.job!)).toBe(25);
    expect(downloadPct({ ...m.job!, total: 0 })).toBe(-1);
    expect(jobPct(m.job!)).toBe(25);
    expect(jobPct({ ...m.job!, phase: "resolve" })).toBe(2);
    expect(jobPct({ ...m.job!, total: 0 })).toBe(5);
    expect(jobPct({ ...m.job!, phase: "verify" })).toBe(88);
    expect(jobPct({ ...m.job!, phase: "probe" })).toBe(93);
    expect(jobPct({ ...m.job!, phase: "save" })).toBe(97);
    expect(jobPct({ ...m.job!, phase: "done" })).toBe(100);
    expect(jobLabel({ ...m.job!, phase: "resolve" })).toBe("Step 1/5 · Looking up v18.4.4 on GitHub…");
    expect(jobShort({ ...m.job!, phase: "verify" })).toBe("Verifying sha256 of v18.4.4… (88%)");
  });

  it("summarises a finished job with size and duration", () => {
    const done = { ...m.job!, phase: "done", message: "", activate: true,
      startedAt: "2026-09-29T10:00:00Z", finishedAt: "2026-09-29T10:00:03.4Z" };
    expect(jobSummary(done)).toBe("Installed v18.4.4 · 200 MB in 3.4s");
    expect(jobSummary({ ...done, activate: false })).toBe("Downloaded v18.4.4 · 200 MB in 3.4s");
    expect(jobSummary({ ...done, message: "already downloaded", startedAt: "", finishedAt: "" })).toBe("already downloaded");
    expect(jobLabel({ ...m.job!, phase: "verify" })).toBe("Step 3/5 · Verifying sha256 of v18.4.4…");
    expect(jobLabel({ ...m.job!, phase: "save" })).toBe("Step 5/5 · Saving v18.4.4…");
    expect(jobLabel({ ...m.job!, phase: "error", error: "sha256 mismatch" })).toBe("sha256 mismatch");
    expect(isRunning({ ...m.job!, phase: "done" })).toBe(false);
  });

  it("says which sessions still run an old version", () => {
    expect(sessionsNote(m)).toBe("2 sessions still on v18.4.2");
    expect(sessionsNote({ ...m, sessionsOnOld: {} })).toBe("");
  });

  it("merges releases and downloaded versions into one list, newest first", () => {
    const rows = versionRows(m);
    expect(rows.map((r) => `${r.version}:${r.status}`)).toEqual([
      "18.10.0:not_downloaded",
      "18.5.0-rc.1:not_downloaded",
      "18.4.4:not_downloaded",
      "18.4.3:active",
      "18.4.2:downloaded",
      "18.3.0:downloaded", // downloaded but no longer in the release list
    ]);
    expect(rows.find((r) => r.version === "18.4.4")?.latest).toBe(true);
    expect(rows.find((r) => r.version === "18.5.0-rc.1")?.prerelease).toBe(true);
    expect(rows.find((r) => r.version === "18.4.2")?.installed?.inUse).toBe(2);
  });

  it("latestState: download → activate → nothing", () => {
    expect(latestState(m)).toBe("download");
    const dl = { ...m, installed: [...m.installed, { ...m.installed[1], version: "18.4.4", current: false, inUse: 0 }] };
    expect(latestState(dl)).toBe("activate");
    expect(latestState({ ...m, latestVersion: "18.4.3" })).toBe("");
    expect(latestState({ ...m, latest: "", latestVersion: "" })).toBe("");
  });
});

describe("managedbin HTTP", () => {
  afterEach(() => vi.unstubAllGlobals());

  function stub(status: number, body: unknown) {
    const f = vi.fn(async () => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("fetch", f);
    return f;
  }

  it("download posts to /download and returns the new job", async () => {
    const f = stub(202, { job: { id: "j1", phase: "resolve", tag: "v1.2.4" } });
    const r = await apiManagedDownload("", "omp", "v1.2.4");
    expect(String((f.mock.calls[0] as unknown[])[0])).toContain("/api/managed-binaries/omp/download?tag=v1.2.4");
    expect(r).toMatchObject({ state: "started", job: { id: "j1" } });
  });

  it("409 already_running follows the running job instead of throwing", async () => {
    stub(409, { error: "an install is already running for this type", code: "already_running", job: { id: "j0", phase: "download", done: 1, total: 2 } });
    const r = await apiManagedDownload("", "omp", "v1.2.4");
    expect(r).toMatchObject({ state: "following", job: { id: "j0", phase: "download" } });
  });

  it("other 409s still throw", async () => {
    stub(409, { error: "managed binaries are disabled for omp" });
    await expect(apiManagedDownload("", "omp")).rejects.toThrow();
  });

  it("429 on check becomes CheckTooSoonError", async () => {
    stub(429, { error: "checked less than a minute ago", retry_after_s: 42 });
    const e = await apiManagedCheck("", "omp").catch((x) => x);
    expect(e).toBeInstanceOf(CheckTooSoonError);
    expect((e as CheckTooSoonError).retryAfterS).toBe(42);
  });
});
