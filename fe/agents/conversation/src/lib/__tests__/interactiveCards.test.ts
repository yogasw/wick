import { describe, expect, test } from "vitest";
import { inputRequestView, approvalView } from "../interactiveCards.js";

describe("inputRequestView", () => {
  const request = JSON.stringify({ id: "a", question: "Deploy now?", options: [{ label: "Ship it", value: "ship" }, { label: "Wait", value: "wait" }] });
  test("pending shows the question and its options, no pill", () => {
    const v = inputRequestView("Deploy now?", { ask_id: "a", state: "pending", question: "Deploy now?", request });
    expect(v).toMatchObject({ pending: true, question: "Deploy now?", pill: "", options: ["Ship it", "Wait"] });
  });
  test("answered reads 'answered: X'", () => {
    const v = inputRequestView("answered: Ship it", { ask_id: "a", state: "answered", question: "Deploy now?", request, answer: "Ship it" });
    expect(v).toMatchObject({ pending: false, question: "Deploy now?", pill: "answered: Ship it" });
  });
  test("a masked secret stays masked", () => {
    expect(inputRequestView("answered: token: ••••", { state: "answered", answer: "token: ••••" }).pill).toBe("answered: token: ••••");
  });
  test("timeout uses the server's words; a bad request JSON is harmless", () => {
    const v = inputRequestView("no answer in time", { state: "timeout", question: "Q?", request: "{bad" });
    expect(v).toMatchObject({ pill: "no answer in time", options: [] });
  });
});

describe("approvalView", () => {
  test("pending carries tool and command", () => {
    const v = approvalView("Bash: rm -rf build", { approval_id: "ap-1", state: "pending", agent: "main", tool: "Bash", cmd: "rm -rf build" });
    expect(v).toMatchObject({ id: "ap-1", pending: true, tool: "Bash", cmd: "rm -rf build", outcome: "" });
  });
  test("settled decisions read as words", () => {
    expect(approvalView("accepted", { approval_id: "x", state: "approve_once" })).toMatchObject({ pending: false, outcome: "accepted", allowed: true });
    expect(approvalView("", { approval_id: "x", state: "approve_session" }).outcome).toBe("accepted for this agent");
    expect(approvalView("declined", { approval_id: "x", state: "block" })).toMatchObject({ outcome: "declined", allowed: false });
  });
});

describe("approvalView: Captain access change", () => {
  it("carries target and diff, no command", () => {
    const v = approvalView("@captain wants to change access of @worker", { approval_id: "ap-9", state: "pending", type: "access_change", agent: "captain", target: "worker", changes: "+Notion (read)\n-Slack", reason: "daily recap" });
    expect(v.access).toEqual({ target: "worker", changes: ["+Notion (read)", "-Slack"], reason: "daily recap" });
    expect(v.cmd).toBe("");
    expect(v.pending).toBe(true);
  });
  it("settles to applied / declined", () => {
    expect(approvalView("x", { approval_id: "ap-9", state: "approve_once", type: "access_change" }).outcome).toBe("applied");
    expect(approvalView("x", { approval_id: "ap-9", state: "block", type: "access_change" }).outcome).toBe("declined");
  });
  it("leaves gate cards alone", () => {
    expect(approvalView("Bash: ls", { approval_id: "a", state: "pending", tool: "Bash", cmd: "ls" }).access).toBeUndefined();
  });
});
