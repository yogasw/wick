import { describe, test, expect, beforeEach } from "vitest";
import { get } from "svelte/store";
import { APIError } from "@wick-fe/common-api";
import { currentApproval, showApproval, hideApproval, isExpiredApprovalError } from "../approvals.js";
import type { ApprovalRequest } from "../../types/agents.js";

const REQ_A: ApprovalRequest = {
  id: "appr-1",
  agent_name: "claude",
  tool: "bash",
  work_dir: "/home/user",
  cmd: "ls -la",
  match_key: "abc123",
};

const REQ_B: ApprovalRequest = {
  id: "appr-2",
  agent_name: "claude",
  tool: "bash",
  work_dir: "/tmp",
  cmd: "cat /etc/passwd",
  match_key: "def456",
};

beforeEach(() => {
  hideApproval();
});

describe("currentApproval store", () => {
  test("showApproval sets currentApproval", () => {
    showApproval(REQ_A);
    expect(get(currentApproval)).toEqual(REQ_A);
  });

  test("hideApproval clears currentApproval when no payload", () => {
    showApproval(REQ_A);
    hideApproval();
    expect(get(currentApproval)).toBeNull();
  });

  test("hideApproval clears currentApproval when id matches", () => {
    showApproval(REQ_A);
    hideApproval({ id: "appr-1" });
    expect(get(currentApproval)).toBeNull();
  });

  test("hideApproval is a no-op when id does not match", () => {
    showApproval(REQ_A);
    hideApproval({ id: "appr-2" });
    expect(get(currentApproval)).toEqual(REQ_A);
  });

  test("showApproval replaces existing approval", () => {
    showApproval(REQ_A);
    showApproval(REQ_B);
    expect(get(currentApproval)).toEqual(REQ_B);
  });
});

// A decision POSTed for a request the daemon already resolved comes back
// 410. Reopening the modal on that error strands the user: every button
// re-POSTs the same dead id and gets 410 again, so only a page reload
// clears it. Expired errors must close the modal instead.
describe("isExpiredApprovalError", () => {
  test("true for an APIError with status 410", () => {
    expect(isExpiredApprovalError(new APIError(410, `{"error":"request id no longer pending"}`))).toBe(true);
  });

  test("false for other API failures", () => {
    expect(isExpiredApprovalError(new APIError(500, `{"error":"server blew up"}`))).toBe(false);
    expect(isExpiredApprovalError(new APIError(400, `{"error":"bad request"}`))).toBe(false);
  });

  test("true when the message says the request is no longer pending", () => {
    expect(
      isExpiredApprovalError(new Error("request id no longer pending (timed out or already resolved)")),
    ).toBe(true);
  });

  test("false for an unrelated error", () => {
    expect(isExpiredApprovalError(new Error("network down"))).toBe(false);
    expect(isExpiredApprovalError("nope")).toBe(false);
  });
});
