import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, within } from "@testing-library/svelte";
import ManagedBinaryPanel from "../ManagedBinaryPanel.svelte";
import * as mb from "$lib/managedbin.js";

vi.mock("$lib/managedbin.js", async (importOriginal) => {
  const orig = await importOriginal<typeof import("$lib/managedbin.js")>();
  return {
    ...orig,
    apiManagedList: vi.fn(),
    apiManagedDownload: vi.fn(),
    apiManagedInstall: vi.fn(),
    apiManagedActivate: vi.fn(),
    apiManagedRemove: vi.fn(),
    apiManagedCheck: vi.fn(),
    apiManagedVerify: vi.fn(),
  };
});
vi.mock("@wick-fe/common-stores", () => ({
  toastOk: vi.fn(),
  toastWarn: vi.fn(),
  toastError: vi.fn(),
  toasts: { subscribe: vi.fn(() => vi.fn()) },
}));
import { toastError } from "@wick-fe/common-stores";

const base = mb.normalizeManaged({
  type: "omp",
  enabled: true,
  host_label: "linux-x64 · glibc · AVX2",
  current: "18.4.3",
  current_path: "/data/providers/bin/omp/versions/18.4.3/omp",
  installed: [
    { version: "18.4.3", tag: "v18.4.3", current: true, sha256: "afcecdff1f421f3c", installed_at: "2026-09-29T00:00:00Z" },
    { version: "18.4.2", tag: "v18.4.2", in_use: 2, removable: false, sha256: "55016ef5317af556" },
    { version: "18.4.1", tag: "v18.4.1", removable: true, sha256: "1111" },
  ],
  latest: { tag: "v18.4.4", version: "18.4.4" },
  releases: [
    { tag: "v18.4.4", version: "18.4.4" },
    { tag: "v18.4.3", version: "18.4.3" },
    { tag: "v18.4.2", version: "18.4.2" },
    { tag: "v18.4.1", version: "18.4.1" },
  ],
  update_available: true,
  sessions_on_old: { "18.4.2": 2 },
});

const job = (over: Partial<mb.ManagedJob> = {}): mb.ManagedJob => ({
  id: "j", type: "omp", tag: "v18.4.4", version: "18.4.4", activate: false, phase: "download",
  done: 45 << 20, total: 100 << 20, bytesPerSec: 0, message: "", error: "", startedAt: "", finishedAt: "", ...over,
});

function row(version: string): HTMLElement {
  return screen.getAllByTestId("managed-version-row").find((r) => r.dataset.version === version)!;
}

beforeEach(() => {
  vi.resetAllMocks();
});

describe("ManagedBinaryPanel", () => {
  it("summary: host, active version, update flag, sessions on an old version", async () => {
    vi.mocked(mb.apiManagedList).mockResolvedValue({ types: [base], isAdmin: true });
    render(ManagedBinaryPanel, { props: { base: "", type: "omp" } });
    expect((await screen.findByTestId("managed-current")).textContent).toContain("v18.4.3");
    expect(screen.getByTestId("managed-host").textContent).toBe("linux-x64 · glibc · AVX2");
    expect(screen.getByTestId("managed-update-available").textContent).toContain("v18.4.4");
    expect(screen.getByTestId("managed-sessions-old").textContent).toBe("2 sessions still on v18.4.2");
    expect(screen.getByTestId("managed-download-latest").textContent).toBe("Download v18.4.4");
  });

  it("table lists only downloaded versions; the rest sit behind one picker", async () => {
    vi.mocked(mb.apiManagedList).mockResolvedValue({ types: [base], isAdmin: true });
    render(ManagedBinaryPanel, { props: { base: "", type: "omp" } });
    await screen.findByTestId("managed-version-list");
    expect(screen.getAllByTestId("managed-version-row").map((r) => `${r.dataset.version}:${r.dataset.status}`)).toEqual([
      "18.4.3:active", "18.4.2:downloaded", "18.4.1:downloaded",
    ]);
    // not downloaded → only in the picker, Download disabled until one is picked
    const picker = screen.getByTestId("managed-other-version");
    expect(within(picker).getByTestId("wick-select-trigger")).toBeTruthy();
    expect((within(picker).getByTestId("managed-row-download") as HTMLButtonElement).disabled).toBe(true);
    // active → no Activate, Remove disabled
    expect(within(row("18.4.3")).queryByTestId("managed-row-activate")).toBeNull();
    expect((within(row("18.4.3")).getByTestId("managed-row-remove") as HTMLButtonElement).disabled).toBe(true);
    // in use → Remove disabled + note
    expect((within(row("18.4.2")).getByTestId("managed-row-remove") as HTMLButtonElement).disabled).toBe(true);
    expect(within(row("18.4.2")).getByTestId("managed-row-inuse").textContent).toBe("2 sessions using it");
    expect((within(row("18.4.1")).getByTestId("managed-row-remove") as HTMLButtonElement).disabled).toBe(false);
  });

  it("Download calls the download-only endpoint; Activate switches", async () => {
    vi.mocked(mb.apiManagedList).mockResolvedValue({ types: [base], isAdmin: true });
    vi.mocked(mb.apiManagedDownload).mockResolvedValue({ state: "started", job: job({ phase: "resolve" }) });
    vi.mocked(mb.apiManagedActivate).mockResolvedValue(base);
    render(ManagedBinaryPanel, { props: { base: "", type: "omp" } });
    await fireEvent.click(await screen.findByTestId("managed-download-latest"));
    expect(mb.apiManagedDownload).toHaveBeenCalledWith("", "omp", "v18.4.4");
    expect(mb.apiManagedInstall).not.toHaveBeenCalled();
    await vi.waitFor(() => expect((within(row("18.4.1")).getByTestId("managed-row-activate") as HTMLButtonElement).disabled).toBe(false));
    await fireEvent.click(within(row("18.4.1")).getByTestId("managed-row-activate"));
    expect(mb.apiManagedActivate).toHaveBeenCalledWith("", "omp", "18.4.1");
  });

  it("downloaded latest → summary offers Activate", async () => {
    const dl = { ...base, installed: [...base.installed, { ...base.installed[2], version: "18.4.4", tag: "v18.4.4" }] };
    vi.mocked(mb.apiManagedList).mockResolvedValue({ types: [dl], isAdmin: true });
    vi.mocked(mb.apiManagedActivate).mockResolvedValue(dl);
    render(ManagedBinaryPanel, { props: { base: "", type: "omp" } });
    const btn = await screen.findByTestId("managed-activate-latest");
    expect(btn.textContent).toBe("Activate v18.4.4");
    await fireEvent.click(btn);
    expect(mb.apiManagedActivate).toHaveBeenCalledWith("", "omp", "18.4.4");
  });

  it("running job: progress in the summary and on the picker's button; every action disabled", async () => {
    vi.mocked(mb.apiManagedList).mockResolvedValue({ types: [{ ...base, job: job() }], isAdmin: true });
    render(ManagedBinaryPanel, { props: { base: "", type: "omp" } });
    const summary = await screen.findByTestId("managed-job");
    expect(summary.textContent).toContain("Downloading v18.4.4… 45%");
    expect(summary.querySelector("[role=progressbar]")?.getAttribute("aria-valuenow")).toBe("41"); // overall: 5 + 45% of the 80-point download band
    expect(screen.getByTestId("managed-row-download").textContent?.trim()).toBe("Downloading v18.4.4… 45%");
    expect(screen.getByTestId("managed-download-latest").textContent).toBe("Downloading v18.4.4… 45%");
    for (const b of screen.getAllByRole("button")) expect((b as HTMLButtonElement).disabled).toBe(true);
  });

  it("a second click that hits a running job follows it — no error toast", async () => {
    vi.mocked(mb.apiManagedList)
      .mockResolvedValueOnce({ types: [base], isAdmin: true })
      .mockResolvedValue({ types: [{ ...base, job: job() }], isAdmin: true });
    vi.mocked(mb.apiManagedDownload).mockResolvedValue({ state: "following", job: job() });
    render(ManagedBinaryPanel, { props: { base: "", type: "omp" } });
    await fireEvent.click(await screen.findByTestId("managed-download-latest"));
    expect((await screen.findByTestId("managed-job")).textContent).toContain("45%");
    expect(toastError).not.toHaveBeenCalled();
  });

  it("not installed: first download from GitHub; non-admins get no buttons", async () => {
    const empty = { ...base, current: "", installed: [], updateAvailable: false, sessionsOnOld: {} };
    vi.mocked(mb.apiManagedList).mockResolvedValue({ types: [empty], isAdmin: true });
    vi.mocked(mb.apiManagedDownload).mockResolvedValue({ state: "started", job: null });
    const { unmount } = render(ManagedBinaryPanel, { props: { base: "", type: "omp", compact: true } });
    expect(await screen.findByTestId("managed-not-installed")).toBeTruthy();
    await fireEvent.click(screen.getByTestId("managed-download-first"));
    expect(mb.apiManagedDownload).toHaveBeenCalledWith("", "omp", "");
    expect(screen.queryByTestId("managed-version-list")).toBeNull(); // compact
    unmount();
    vi.mocked(mb.apiManagedList).mockResolvedValue({ types: [empty], isAdmin: false });
    render(ManagedBinaryPanel, { props: { base: "", type: "omp" } });
    await screen.findByTestId("managed-not-installed");
    expect(screen.queryByTestId("managed-download-first")).toBeNull();
    expect(screen.queryByTestId("managed-row-download")).toBeNull();
  });
});

describe("ManagedBinaryPanel - embedded (Detail page Binary section)", () => {
  it("renders no card header of its own; platform / active / latest as rows, controls kept", async () => {
    vi.mocked(mb.apiManagedList).mockResolvedValue({ types: [base], isAdmin: true });
    render(ManagedBinaryPanel, { props: { base: "", type: "omp", embedded: true } });
    expect((await screen.findByTestId("managed-current")).textContent).toBe("v18.4.3");
    expect(screen.queryByText("Binary · omp")).toBeNull();
    expect(screen.queryByText("managed by wick")).toBeNull(); // the section header shows it
    expect(screen.getByTestId("managed-host").textContent).toBe("linux-x64 · glibc · AVX2");
    expect(screen.getByText("Platform")).toBeTruthy();
    expect(screen.getByText("Latest")).toBeTruthy();
    expect(screen.getByTestId("managed-update-available").textContent).toContain("v18.4.4");
    expect(screen.getByTestId("managed-sessions-old").textContent).toBe("2 sessions still on v18.4.2");
    // the path is the section's Resolved path row, not repeated here
    expect(screen.queryByText("/data/providers/bin/omp/versions/18.4.3/omp")).toBeNull();
    expect(screen.getByTestId("managed-download-latest").textContent).toBe("Download v18.4.4");
    expect(screen.getByTestId("managed-version-list")).toBeTruthy();
    expect(screen.getByTestId("managed-binary-panel").className).not.toContain("rounded-xl");
  });
});

describe("ManagedBinaryPanel unmount", () => {
  it("does not re-arm the poll when unmounted while a load is in flight", async () => {
    let resolve!: (v: { types: mb.ManagedBinary[]; isAdmin: boolean }) => void;
    vi.mocked(mb.apiManagedList).mockReturnValue(new Promise((r) => { resolve = r; }));
    const { unmount } = render(ManagedBinaryPanel, { props: { base: "", type: "omp" } });
    unmount();
    const spy = vi.spyOn(globalThis, "setTimeout");
    resolve({ types: [base], isAdmin: true });
    await new Promise((r) => queueMicrotask(() => r(undefined)));
    await Promise.resolve();
    expect(spy.mock.calls.filter((c) => c[1] === 15000 || c[1] === 500)).toHaveLength(0);
    spy.mockRestore();
  });
});
