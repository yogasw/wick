import { describe, test, expect, beforeEach } from "vitest";
import { get } from "svelte/store";
import { currentApproval, showApproval, hideApproval } from "../approvals.js";
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
