import { describe, test, expect, vi, beforeEach } from "vitest";

const toastError = vi.fn();
vi.mock("@wick-fe/common-stores", () => ({ toastError: (t: string, b?: string) => toastError(t, b) }));
const made = { id: "a1", handle: "helper", is_captain: true, run_as: "caller", access_mode: "choose" };
const makeCaptain = vi.fn((_b: string, _id: string) => Promise.resolve({ agent: { ...made }, previous_id: "" }));
const updateAgent = vi.fn((_b: string, _id: string, body: object) => Promise.resolve({ ...made, ...body }));
vi.mock("./api/team.js", () => ({
  makeCaptain: (b: string, id: string) => makeCaptain(b, id),
  updateAgent: (b: string, id: string, body: object) => updateAgent(b, id, body),
  runApi: <T,>(p: Promise<T>) => p,
}));

import { accessToday, makeCaptainWithAccess } from "./captainAccess.js";

describe("captainAccess", () => {
  beforeEach(() => { makeCaptain.mockClear(); updateAgent.mockClear(); toastError.mockClear(); });

  test("accessToday reads the agent's run-as and connector access", () => {
    expect(accessToday({ run_as: "caller", access_mode: "choose", allowed_connectors: [] })).toBe("Caller · Choose connectors · 0");
    expect(accessToday({ run_as: "owner", access_mode: "choose", allowed_connectors: [
      { connector_id: "c1", level: "read", ops: [] }, { connector_id: "c2", level: "off", ops: [] },
    ] } as never)).toBe("Owner · Choose connectors · 1");
    expect(accessToday({ run_as: "caller", access_mode: "owner", allowed_connectors: [] })).toBe("Caller · Same as me");
  });

  test("ticked: makes the Captain, then runs it as the owner with Same as me", async () => {
    const { agent: a, accessFailed } = await makeCaptainWithAccess("/b", "a1", true);
    expect(makeCaptain).toHaveBeenCalledWith("/b", "a1");
    expect(updateAgent).toHaveBeenCalledTimes(1);
    expect(updateAgent).toHaveBeenCalledWith("/b", "a1", { run_as: "owner", access_mode: "owner" });
    expect(a).toMatchObject({ is_captain: true, run_as: "owner", access_mode: "owner" });
    expect(accessFailed).toBe(false);
    expect(toastError).not.toHaveBeenCalled();
  });

  test("unticked: only the role moves", async () => {
    const { agent: a, accessFailed } = await makeCaptainWithAccess("/b", "a1", false);
    expect(updateAgent).not.toHaveBeenCalled();
    expect(a).toMatchObject({ is_captain: true, run_as: "caller", access_mode: "choose" });
    expect(accessFailed).toBe(false);
    expect(toastError).not.toHaveBeenCalled();
  });

  test("already Owner + Same as me: no second call", async () => {
    makeCaptain.mockImplementationOnce(() => Promise.resolve({ agent: { ...made, run_as: "owner", access_mode: "owner" }, previous_id: "" }));
    await makeCaptainWithAccess("/b", "a1", true);
    expect(updateAgent).not.toHaveBeenCalled();
  });

  test("a failed access update toasts once, keeps the role and reports accessFailed", async () => {
    updateAgent.mockImplementationOnce(() => Promise.reject(new Error("forbidden")));
    const { agent: a, accessFailed } = await makeCaptainWithAccess("/b", "a1", true);
    expect(a).toMatchObject({ is_captain: true, run_as: "caller" });
    expect(accessFailed).toBe(true);
    expect(toastError).toHaveBeenCalledTimes(1);
    expect(toastError).toHaveBeenCalledWith("@helper is the Captain, but your access was not given", "forbidden Set it in Settings › Access.");
  });

  test("a failed Make Captain throws", async () => {
    makeCaptain.mockImplementationOnce(() => Promise.reject(new Error("shared")));
    await expect(makeCaptainWithAccess("/b", "a1", true)).rejects.toThrow("shared");
    expect(updateAgent).not.toHaveBeenCalled();
    // The caller toasts the thrown error; the helper adds none.
    expect(toastError).not.toHaveBeenCalled();
  });
});
