import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";
import { describe, expect, it, vi, beforeEach } from "vitest";
import { Effect } from "effect";

const calls: string[] = [];
vi.mock("../../api/team.js", async (orig) => ({
  ...(await orig<typeof import("../../api/team.js")>()),
  runApi: (eff: Effect.Effect<unknown>) => Effect.runPromise(eff as Effect.Effect<unknown, never, never>),
  listShareUsers: () => {
    calls.push("users");
    return Effect.succeed({ users: [{ id: "user-b", name: "User B" }] });
  },
  getAccessTrail: () => {
    calls.push("trail");
    return Effect.succeed({
      items: [{
        run_id: "r1", connector_id: "c1", connector_label: "Docs", operation: "read", status: "success",
        caller_id: "user-b", caller_name: "User B", on_behalf_of_id: "user-a", on_behalf_of_name: "User A",
        session_id: "s1", at: "2026-11-14T01:00:00Z",
      }],
    });
  },
  getAccessEffective: (_b: string, _id: string, as: string) => {
    calls.push("effective:" + as);
    return Effect.succeed({
      as, user_id: "",
      items: [{ connector_id: "c1", label: "Docs", key: "docs", level: "read", ops: ["read"], runs_as: as === "guest" ? "owner" : "self", lent: true, account: "acc-1", account_status: "connected" }],
    });
  },
}));

import AccessRoles from "../AccessRoles.svelte";

const catalog = [
  { id: "c1", key: "docs", label: "Docs", description: "", accounts: null, ops: [{ key: "read", name: "Read", destructive: false }], tier: "" as const },
  { id: "c2", key: "chat", label: "Chat", description: "", accounts: null, ops: [{ key: "post", name: "Post", destructive: true }], tier: "" as const },
];
const lentDocs = [{ connector_id: "c1", accounts: [], ops: [], level: "read" as const, lend: true }];

beforeEach(() => {
  calls.length = 0;
});

describe("AccessRoles", () => {
  it("shows the stored access as its mode and changes nothing on open", async () => {
    const onApply = vi.fn();
    render(AccessRoles, { base: "", agentId: "a1", catalog, savedGrants: lentDocs, savedMode: "choose", onApply });
    expect(screen.getByTestId("access-mode-borrow").getAttribute("aria-checked")).toBe("true");
    expect(screen.getByTestId("access-mode-open").hasAttribute("disabled")).toBe(true);
    expect(screen.queryByTestId("access-diff")).toBeNull();
    await waitFor(() => expect(screen.getByText("tersambung")).toBeTruthy());
    await waitFor(() => expect(screen.getByText(/atas nama User A/)).toBeTruthy());
    expect(onApply).not.toHaveBeenCalled();
    expect(calls.every((c) => c === "users" || c === "trail" || c.startsWith("effective:"))).toBe(true);
  });

  it("applies a mode only after Simpan and the confirmation", async () => {
    const onApply = vi.fn();
    render(AccessRoles, { base: "", agentId: "a1", catalog, savedGrants: lentDocs, savedMode: "choose", onApply });
    await fireEvent.click(screen.getByTestId("access-mode-team"));
    expect(screen.getByTestId("access-diff").textContent).toContain("Docs: tidak dipinjamkan lagi");
    expect(onApply).not.toHaveBeenCalled();
    await fireEvent.click(screen.getByTestId("access-save"));
    expect(onApply).not.toHaveBeenCalled();
    await fireEvent.click(screen.getByTestId("access-save-confirm"));
    expect(onApply).toHaveBeenCalledWith([{ connector_id: "c1", accounts: [], ops: [], level: "read" }], "owner");
  });

  it("shows the stored grants verbatim on Raw", async () => {
    render(AccessRoles, { base: "", agentId: "a1", catalog, savedGrants: [], savedMode: "owner", onApply: vi.fn() });
    expect(screen.getByTestId("access-mode-team").getAttribute("aria-checked")).toBe("true");
    await fireEvent.click(screen.getByTestId("access-roles-tab-raw"));
    expect(screen.getByTestId("access-raw").textContent).toContain('"access_mode": "owner"');
  });
});
