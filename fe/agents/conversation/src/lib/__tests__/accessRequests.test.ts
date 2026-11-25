import { describe, expect, it } from "vitest";
import { accessCardOf, afterText, headerSuffix, itemLine, noAccess, kindHint, type AccessItemView, type AccessRequestView } from "../accessRequests.js";

const item = (over: Partial<AccessItemView> = {}): AccessItemView => ({
  id: "i1", n: 1, connector_key: "notes", connector_label: "Notes", op_key: "update_page", op_name: "", kind: "write",
  target: "page_id=p1", status: "pending", mode: "", decided_by: "", note: "", error: "", result: "",
  approvers: ["Approver A"], can_decide: false, modes: [], locked_reason: "", locked_code: "", params: [], params_json: "{}",
  ...over,
});

const view = (over: Partial<AccessRequestView> = {}): AccessRequestView => ({
  id: "r1", agent: "@helper", requester: "Owner", reason: "", status: "pending", pending: 1, url: "",
  items: [item()], viewer_can_decide: false, viewer_reason: "", viewer_reason_code: "", ...over,
});

describe("accessCardOf", () => {
  it("reads the latest view from extras", () => {
    const c = accessCardOf("x", { request_id: "r1", api: "/tools/agents/access/r1", view: JSON.stringify(view()) });
    expect(c.id).toBe("r1");
    expect(c.api).toBe("/tools/agents/access/r1");
    expect(c.view?.items[0].op_key).toBe("update_page");
    expect(c.open).toBe(true);
  });
  it("is closed once nothing waits or the request ended", () => {
    expect(accessCardOf("x", { view: JSON.stringify(view({ pending: 0, status: "done" })) }).open).toBe(false);
    expect(accessCardOf("x", { view: JSON.stringify(view({ status: "cancelled" })) }).open).toBe(false);
  });
  it("survives a malformed view", () => {
    const c = accessCardOf("x", { request_id: "r1", view: "{nope", state: "pending", pending: "2" });
    expect(c.view).toBeNull();
    expect(c.open).toBe(true);
  });
});

describe("itemLine", () => {
  it("says who can approve while waiting and what happened after", () => {
    expect(itemLine(item())).toContain("Approver A");
    expect(itemLine(item({ status: "ok", mode: "session", decided_by: "Owner" }))).toBe("Approved this session by Owner · OK");
    expect(itemLine(item({ status: "error", mode: "once", decided_by: "Owner", error: "channel_not_found" }))).toContain("channel_not_found");
    expect(itemLine(item({ status: "denied", decided_by: "Owner", note: "not now" }))).toBe("Denied by Owner: not now");
  });
});

describe("noAccess", () => {
  it("shows the panel only for a real lack of access", () => {
    expect(noAccess(view({ viewer_reason: "You do not have access", viewer_reason_code: "not_approver" }))).toBe(true);
    expect(noAccess(view({ viewer_reason: "All decided", viewer_reason_code: "already_decided" }))).toBe(false);
    expect(noAccess(view({ viewer_can_decide: true, viewer_reason: "" }))).toBe(false);
  });
});

describe("kindHint", () => {
  it("explains missing modes", () => {
    expect(kindHint("send")).toBe("");
    expect(kindHint("delete")).toContain("Once");
    expect(kindHint("write")).toBe("");
  });
});

describe("plan steps", () => {
  it("names the key and version, and a replaced card", () => {
    expect(headerSuffix(view({ key: "step-2", version: 2 }))).toBe(" · step-2 v2");
    expect(headerSuffix(view({ key: "step-2", version: 1, status: "superseded" }))).toBe(" · step-2 v1 · replaced");
    expect(headerSuffix(view())).toBe("");
    expect(accessCardOf("x", { view: JSON.stringify(view({ status: "superseded" })) }).open).toBe(false);
  });
  it("says which request it runs after", () => {
    expect(afterText(view())).toBe("");
    expect(afterText(view({ after: { id: "r0", name: "step-1 v1", url: "" }, after_state: "waiting" }))).toBe("Runs after step-1 v1 · waiting for it to finish OK");
  });
  it("labels queued and skipped actions", () => {
    expect(itemLine(item({ status: "queued", mode: "once", decided_by: "Owner" }))).toContain("waiting for the request it runs after");
    expect(itemLine(item({ status: "skipped", error: "skipped: action 2 failed" }))).toBe("Not run: action 2 failed");
  });
});
