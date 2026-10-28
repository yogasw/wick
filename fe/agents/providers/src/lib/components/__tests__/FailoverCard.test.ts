import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor, within } from "@testing-library/svelte";
import FailoverCard from "../FailoverCard.svelte";
import * as api from "$lib/api.js";
import type { FailoverConfig, FailoverResponse } from "$lib/api.js";
import { MAX_FAILOVER_TARGETS } from "$lib/failover.js";

vi.mock("$lib/api.js");
vi.mock("@wick-fe/common-stores", () => ({
  toastOk: vi.fn(),
  toastError: vi.fn(),
  toasts: { subscribe: vi.fn(() => vi.fn()) },
}));

const props = { base: "/tools/agents", type: "claude", name: "mine" };

function resp(cfg: Partial<FailoverConfig> = {}, extra: Partial<FailoverResponse> = {}): FailoverResponse {
  return {
    config: { enabled: true, triggers: ["limit", "auth", "unavailable"], chain: [], recheck_minutes: 10, return_home: true, ...cfg },
    warnings: [],
    errors: [],
    can_edit: true,
    ...extra,
  };
}

function targets(): string[] {
  return screen.getAllByTestId(/^failover-target-pick-/).map((el) => el.getAttribute("data-value") ?? "");
}

beforeEach(() => {
  vi.resetAllMocks();
  vi.mocked(api.apiFailoverOptions).mockResolvedValue([
    { type: "claude", name: "mine" },
    { type: "claude", name: "sibling" },
    { type: "codex", name: "waba" },
    { type: "omp", name: "wick" },
  ]);
  vi.mocked(api.apiGetFailover).mockResolvedValue(resp());
});

describe("FailoverCard", () => {
  it("renders the stored config", async () => {
    vi.mocked(api.apiGetFailover).mockResolvedValue(
      resp({ triggers: ["limit"], chain: [{ instance: "claude/sibling", model_id: "opus" }], recheck_minutes: 15, return_home: false }),
    );
    render(FailoverCard, { props });
    await screen.findByTestId("failover-target");
    expect(screen.getByTestId("failover-toggle").getAttribute("aria-checked")).toBe("true");
    expect((screen.getByTestId("failover-trigger-limit") as HTMLInputElement).checked).toBe(true);
    expect((screen.getByTestId("failover-trigger-auth") as HTMLInputElement).checked).toBe(false);
    expect(targets()).toEqual(["claude/sibling::opus"]);
    expect((screen.getByTestId("failover-recheck") as HTMLInputElement).value).toBe("15");
    expect(screen.getByTestId("failover-return-home").getAttribute("aria-checked")).toBe("false");
    expect(screen.getByTestId("failover-health").textContent).toContain("Healthy");
    expect(screen.queryByTestId("failover-crosstype-0")).toBeNull();
  });

  it("adds up to MAX_FAILOVER_TARGETS targets, removes and reorders", async () => {
    vi.mocked(api.apiGetFailover).mockResolvedValue(
      resp({ chain: [{ instance: "claude/sibling" }, { instance: "codex/waba" }] }),
    );
    render(FailoverCard, { props });
    await screen.findAllByTestId("failover-target");
    await fireEvent.click(screen.getByTestId("failover-down-0"));
    expect(targets()).toEqual(["codex/waba", "claude/sibling"]);
    await fireEvent.click(screen.getByTestId("failover-up-1"));
    expect(targets()).toEqual(["claude/sibling", "codex/waba"]);
    expect((screen.getByTestId("failover-up-0") as HTMLButtonElement).disabled).toBe(true);

    const add = screen.getByTestId("failover-add") as HTMLButtonElement;
    expect(MAX_FAILOVER_TARGETS).toBe(6);
    for (let i = 2; i < MAX_FAILOVER_TARGETS; i++) {
      expect(add.disabled).toBe(false);
      await fireEvent.click(add);
    }
    expect(screen.getAllByTestId("failover-target")).toHaveLength(MAX_FAILOVER_TARGETS);
    expect(add.disabled).toBe(true);
    await fireEvent.click(add);
    expect(screen.getAllByTestId("failover-target")).toHaveLength(MAX_FAILOVER_TARGETS);
    // An empty row cannot be saved.
    expect((screen.getByTestId("failover-save") as HTMLButtonElement).disabled).toBe(true);

    for (let i = MAX_FAILOVER_TARGETS - 1; i >= 2; i--) {
      await fireEvent.click(screen.getByTestId(`failover-remove-${i}`));
    }
    await fireEvent.click(screen.getByTestId("failover-remove-0"));
    expect(targets()).toEqual(["codex/waba"]);
    expect(add.disabled).toBe(false);
  });

  it("flags itself and duplicates and blocks the save", async () => {
    vi.mocked(api.apiGetFailover).mockResolvedValue(
      resp({ chain: [{ instance: "claude/mine" }, { instance: "omp/wick" }, { instance: "omp/wick", model_id: "x" }] }),
    );
    render(FailoverCard, { props });
    expect((await screen.findByTestId("failover-problem-0")).textContent).toContain("provider itself");
    expect(screen.queryByTestId("failover-problem-1")).toBeNull();
    expect(screen.getByTestId("failover-problem-2").textContent).toContain("already in the list");
    expect((screen.getByTestId("failover-save") as HTMLButtonElement).disabled).toBe(true);
  });

  it("warns about another provider type but still saves", async () => {
    const cfg = { chain: [{ instance: "codex/waba" }] };
    vi.mocked(api.apiGetFailover).mockResolvedValue(
      resp(cfg, { warnings: [{ instance: "codex/waba", message: "history does not carry over to codex" }, { instance: "codex/waba", message: "model x is not offered by codex/waba; its default model runs instead" }] }),
    );
    vi.mocked(api.apiSaveFailover).mockResolvedValue(resp(cfg));
    render(FailoverCard, { props });
    const alert = await screen.findByTestId("failover-crosstype-0");
    expect(alert.textContent).toBe(
      "Conversation history does not carry over to codex. Wick will send the last message and a short summary instead.",
    );
    // The server's short history warning is replaced by the alert; the rest show.
    const ws = screen.getAllByTestId("failover-warning-0");
    expect(ws).toHaveLength(1);
    expect(ws[0].textContent).toContain("is not offered");

    const save = screen.getByTestId("failover-save") as HTMLButtonElement;
    expect(save.disabled).toBe(false);
    await fireEvent.click(save);
    await waitFor(() => expect(api.apiSaveFailover).toHaveBeenCalled());
    expect(vi.mocked(api.apiSaveFailover).mock.calls[0][3]).toMatchObject({
      enabled: true,
      chain: [{ instance: "codex/waba", model_id: "" }],
      recheck_minutes: 10,
      return_home: true,
    });
  });

  it("lists the server's errors when a save is refused", async () => {
    vi.mocked(api.apiGetFailover).mockResolvedValue(resp({ chain: [{ instance: "omp/wick" }] }));
    vi.mocked(api.apiSaveFailover).mockResolvedValue(resp({}, { errors: ["unknown provider instance omp/wick"] }));
    render(FailoverCard, { props });
    await screen.findByTestId("failover-target");
    await fireEvent.click(screen.getByTestId("failover-save"));
    const box = await screen.findByTestId("failover-errors");
    expect(box.textContent).toContain("unknown provider instance omp/wick");
    // The form keeps what was typed.
    expect(targets()).toEqual(["omp/wick"]);
  });

  it("accepts a recheck of 1..60 minutes only", async () => {
    render(FailoverCard, { props });
    const input = (await screen.findByTestId("failover-recheck")) as HTMLInputElement;
    const save = screen.getByTestId("failover-save") as HTMLButtonElement;
    for (const [v, ok] of [["0", false], ["61", false], ["", false], ["1", true], ["60", true]] as const) {
      await fireEvent.input(input, { target: { value: v } });
      expect(save.disabled, `recheck ${v}`).toBe(!ok);
      expect(screen.queryByTestId("failover-recheck-error") === null, `recheck ${v}`).toBe(ok);
    }
  });

  it("shows the health status and resets it", async () => {
    vi.mocked(api.apiResetProviderHealth).mockResolvedValue(undefined);
    const onChanged = vi.fn();
    const until = new Date();
    until.setHours(14, 30, 0, 0);
    render(FailoverCard, {
      props: { ...props, onChanged, health: { Kind: "limit", Since: until.toISOString(), Until: until.toISOString(), Reason: "session limit" } },
    });
    const label = await screen.findByTestId("failover-health-label");
    expect(label.textContent).toBe("Limited until 14:30");
    expect(label.className).toContain("amber");
    await fireEvent.click(await screen.findByTestId("failover-reset"));
    await waitFor(() => expect(api.apiResetProviderHealth).toHaveBeenCalledWith("/tools/agents", "claude", "mine"));
    await waitFor(() => expect(onChanged).toHaveBeenCalled());
  });

  it("is read-only without can_edit", async () => {
    vi.mocked(api.apiGetFailover).mockResolvedValue(resp({ chain: [{ instance: "omp/wick" }] }, { can_edit: false }));
    render(FailoverCard, { props: { ...props, health: { Kind: "auth", Since: "", Until: "", Reason: "" } } });
    await screen.findByTestId("failover-target");
    expect(screen.queryByTestId("failover-save")).toBeNull();
    expect(screen.queryByTestId("failover-add")).toBeNull();
    expect(screen.queryByTestId("failover-reset")).toBeNull();
    expect((screen.getByTestId("failover-toggle") as HTMLButtonElement).disabled).toBe(true);
    const label = screen.getByTestId("failover-health-label");
    expect(label.textContent).toBe("Auth failed");
    expect(label.className).toContain("red");
    // No picker to fiddle with: the target is plain text.
    expect(screen.getByTestId("failover-target-text-0").textContent).toContain("omp/wick");
    expect(screen.getByTestId("failover-target-pick-0").querySelector("button")).toBeNull();
  });

  it("labels a login being retried instead of calling it healthy", async () => {
    render(FailoverCard, { props: { ...props, health: { Kind: "auth", Since: "", Until: "", Reason: "", Retrying: true } } });
    const label = await screen.findByTestId("failover-health-label");
    expect(label.textContent).toBe("Retrying auth");
    expect(screen.getByTestId("failover-health").textContent).not.toContain("Healthy");
    expect(await screen.findByTestId("failover-reset")).toBeTruthy();
  });

  it("keeps a sub-model pin verbatim through the picker value", async () => {
    vi.mocked(api.apiGetFailover).mockResolvedValue(resp({ chain: [{ instance: "omp/wick", model_id: "anthropic/acct%201@claude-opus" }] }));
    vi.mocked(api.apiSaveFailover).mockImplementation(async (_b, _t, _n, cfg) => resp(cfg));
    render(FailoverCard, { props });
    await screen.findByTestId("failover-target");
    expect(targets()).toEqual(["omp/wick::anthropic/acct%201@claude-opus"]);
    await fireEvent.click(screen.getByTestId("failover-save"));
    await waitFor(() => expect(api.apiSaveFailover).toHaveBeenCalled());
    expect(vi.mocked(api.apiSaveFailover).mock.calls[0][3].chain).toEqual([{ instance: "omp/wick", model_id: "anthropic/acct%201@claude-opus" }]);
  });

  it("drops a target's server warnings once its row is changed (L4)", async () => {
    vi.mocked(api.apiGetFailover).mockResolvedValue(
      resp({ chain: [{ instance: "codex/waba", model_id: "x" }] }, { warnings: [{ instance: "codex/waba", message: "model x is not offered by codex/waba; its default model runs instead" }] }),
    );
    render(FailoverCard, { props });
    expect(await screen.findAllByTestId("failover-warning-0")).toHaveLength(1);
    // The picker's menu is portaled to <body>: omp drills into omp/wick,
    // whose default model is the pick.
    await fireEvent.click(within(screen.getByTestId("failover-target-pick-0")).getAllByRole("button")[0]);
    await fireEvent.click(await screen.findByRole("button", { name: /omp/i }));
    await fireEvent.click(await screen.findByRole("button", { name: /use default model/i }));
    expect(targets()).toEqual(["omp/wick"]);
    expect(screen.queryAllByTestId("failover-warning-0")).toHaveLength(0);
  });

  it("moves each row's picker with the row on reorder (L8)", async () => {
    vi.mocked(api.apiGetFailover).mockResolvedValue(
      resp({ chain: [{ instance: "claude/sibling" }, { instance: "codex/waba" }] }),
    );
    render(FailoverCard, { props });
    await screen.findAllByTestId("failover-target");
    // Keyed per row: the picker (and whatever it has open) is the same
    // element after the move, now in the second slot.
    const picker = within(screen.getByTestId("failover-target-pick-0")).getAllByRole("button")[0];
    await fireEvent.click(screen.getByTestId("failover-down-0"));
    expect(targets()).toEqual(["codex/waba", "claude/sibling"]);
    expect(screen.getByTestId("failover-target-pick-1").contains(picker)).toBe(true);
  });
});
