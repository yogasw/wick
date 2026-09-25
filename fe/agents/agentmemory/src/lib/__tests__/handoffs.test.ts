import { describe, expect, test } from "vitest";
import {
  cancelConfirmBody,
  cancelOutcome,
  handoffAge,
  handoffParties,
  messageSender,
  messageStateLabel,
  messageSummary,
} from "../handoffs.js";

describe("cancelOutcome", () => {
  // The finding this whole function exists for. The backend answers a second
  // cancel with cancelled:false and no error — the baton was already gone.
  test("cancelled:false is reported as 'already gone', never as a success", () => {
    const out = cancelOutcome({ handoff_id: "01a0d641", cancelled: false, state: "expired" });
    expect(out.kind).toBe("gone");
    expect(out.title).not.toMatch(/cancelled/i);
    expect(out.body).toMatch(/nothing was cancelled/i);
    // And it points at the consequence the operator has to act on.
    expect(out.body).toMatch(/another agent accepted it/i);
  });

  test("cancelled:true is the success", () => {
    const out = cancelOutcome({ handoff_id: "abc", cancelled: true, state: "expired" });
    expect(out.kind).toBe("cancelled");
    expect(out.body).toContain("abc");
  });

  test("an error is a failure, not an 'already gone'", () => {
    expect(cancelOutcome(null, "invalid handoff_id").kind).toBe("failed");
  });

  test("no result at all is a failure rather than a silent success", () => {
    expect(cancelOutcome(undefined).kind).toBe("failed");
  });
});

describe("handoff rows", () => {
  const now = Date.UTC(2026, 8, 25, 12, 0, 0);

  test("age reads in the unit that matters", () => {
    expect(handoffAge({ id: "a", created_at_ms: now - 30_000 }, now)).toBe("just now");
    expect(handoffAge({ id: "a", created_at_ms: now - 5 * 60_000 }, now)).toBe("5m ago");
    expect(handoffAge({ id: "a", created_at_ms: now - 3 * 3_600_000 }, now)).toBe("3h ago");
    expect(handoffAge({ id: "a", created_at_ms: now - 4 * 86_400_000 }, now)).toBe("4d ago");
  });

  test("an unknown creation time says so instead of reading as 'just now'", () => {
    expect(handoffAge({ id: "a" }, now)).toBe("age unknown");
  });

  test("an unaddressed handoff says what that means", () => {
    expect(handoffParties({ id: "a", from_agent: "claude-code" })).toMatch(/whoever picks it up next/);
    expect(handoffParties({ id: "a", from_agent: "claude-code", to_agent: "codex" })).toBe("claude-code → codex");
  });

  test("the confirmation names what is lost, not just that something is", () => {
    const body = cancelConfirmBody({ id: "a", from_agent: "claude-code", cwd: "/srv/proj2" });
    expect(body).toContain("/srv/proj2");
    expect(body).toMatch(/open questions/i);
    expect(body).toMatch(/cannot be restored/i);
  });
});

describe("cross-project messages", () => {
  // The backend gives a UUID for the sender and no name, anywhere. The UI must
  // not invent one: an operator acts on who sent a message.
  test("the sender stays an id — shortened, never turned into a name", () => {
    const got = messageSender({ id: "m", from_agent: "other", from_project_id: "01a0d63f-cd05-7591-af8d-18e7d68ce8fa" });
    expect(got).toContain("01a0d63f");
    expect(got).toContain("other");
    expect(got).not.toMatch(/proj2|scratch|default/);
  });

  test("no sender id at all falls back to the agent, then to 'unknown'", () => {
    expect(messageSender({ id: "m", from_agent: "other" })).toBe("other");
    expect(messageSender({ id: "m" })).toBe("unknown sender");
  });

  test("a message with no subject previews its body instead of showing a blank row", () => {
    expect(messageSummary({ id: "m", subject: "wick 4C probe" })).toBe("wick 4C probe");
    expect(messageSummary({ id: "m", body: "shape   verification\nonly" })).toBe("shape verification only");
    expect(messageSummary({ id: "m" })).toBe("(no subject or body)");
  });

  test("a long body is truncated rather than breaking the row", () => {
    const got = messageSummary({ id: "m", body: "x".repeat(200) });
    expect(got.length).toBeLessThanOrEqual(81);
    expect(got.endsWith("…")).toBe(true);
  });

  test("pending is spelled out — it is the state that means nobody has it", () => {
    expect(messageStateLabel({ id: "m", state: "pending" })).toBe("waiting to be claimed");
    expect(messageStateLabel({ id: "m" })).toBe("state unknown");
  });
});
