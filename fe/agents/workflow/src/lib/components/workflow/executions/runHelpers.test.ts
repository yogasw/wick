import { describe, it, expect } from "vitest";
import { triggerIDOf, eventPayloadOf } from "./runHelpers";

describe("triggerIDOf", () => {
  // Reproduces the github_pr webhook run (state.json): trigger_id lives at
  // event.trigger_id, and the payload is the webhook envelope with NO
  // trigger_id inside it. Before the fix this returned null → Replay pinned
  // nothing and the trigger OUTPUT pane stayed empty.
  it("reads event.trigger_id for webhook runs", () => {
    const runDetail = {
      event: {
        type: "webhook",
        trigger_id: "trigger-webhook",
        payload: { body: { test: "test" }, method: "POST", headers: {} },
      },
    };
    expect(triggerIDOf(runDetail)).toBe("trigger-webhook");
  });

  it("falls back to event.payload.trigger_id (legacy spa runs)", () => {
    const runDetail = {
      event: { payload: { source: "spa", trigger_id: "trg-legacy" } },
    };
    expect(triggerIDOf(runDetail)).toBe("trg-legacy");
  });

  it("falls back to top-level trigger_id", () => {
    expect(triggerIDOf({ trigger_id: "trg-flat", event: {} })).toBe("trg-flat");
  });

  it("returns null when nothing carries it", () => {
    expect(triggerIDOf(null)).toBeNull();
    expect(triggerIDOf({ event: { payload: {} } })).toBeNull();
  });
});

describe("eventPayloadOf", () => {
  it("returns the webhook envelope verbatim", () => {
    const payload = { body: { test: "test" }, method: "POST" };
    expect(eventPayloadOf({ event: { payload } })).toEqual(payload);
  });

  it("returns null when no payload landed", () => {
    expect(eventPayloadOf(null)).toBeNull();
    expect(eventPayloadOf({ event: {} })).toBeNull();
  });
});
