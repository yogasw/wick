import { describe, it, expect, beforeEach } from "vitest";
import { get } from "svelte/store";
import {
  hydrateFromRun,
  runStatusByNode,
  stepResultsByNode,
  triggerEventByID,
  triggerRunStatus,
  draftWorkflow,
} from "./editor";

// Mirrors the real failed run ef91913a (state.json): check_action passed,
// fetch_diff failed on a nil-pointer template error, webhook trigger event
// carried the envelope. hydrateFromRun should reproduce that on the canvas.
const RUN = {
  status: "failed",
  entry: "check_action",
  completed: ["check_action"],
  failed: ["fetch_diff"],
  outputs: {
    check_action: { result: "<no value>" },
  },
  event: {
    type: "webhook",
    trigger_id: "trigger-webhook",
    payload: { body: { test: "test" }, method: "POST" },
  },
  error: {
    node: "fetch_diff",
    type: "http",
    message: "pre-render url: nil pointer evaluating interface {}.full_name",
  },
};

describe("hydrateFromRun", () => {
  beforeEach(() => {
    runStatusByNode.set({});
    stepResultsByNode.set({});
    triggerEventByID.set({});
    triggerRunStatus.set({});
    draftWorkflow.set(null);
  });

  // Regression: a run with a real payload but NO event.trigger_id (re-run /
  // webhook-test runs stamp trigger_id=null) still pins the trigger by
  // falling back to the trigger whose entry_node matches the run's entry.
  // Without this the trigger OUTPUT imports empty ("event hilang pas replay").
  it("pins the trigger via entry_node fallback when trigger_id is null", () => {
    draftWorkflow.set({
      id: "wf1",
      triggers: [{ id: "trigger-webhook", type: "webhook", entry_node: "check_action" }],
      graph: { entry: "start", nodes: [], edges: [] },
    } as any);
    const run = {
      status: "failed",
      entry: "check_action",
      completed: ["check_action"],
      failed: [],
      outputs: { check_action: { result: "opened" } },
      event: { type: "webhook", trigger_id: null, payload: { body: { action: "opened" } } },
    };
    const summary = hydrateFromRun(run);
    expect(get(triggerEventByID)["trigger-webhook"]).toEqual({ body: { action: "opened" } });
    expect(summary.triggerPinned).toBe(true);
  });

  it("falls back to the sole trigger when entry doesn't match", () => {
    draftWorkflow.set({
      id: "wf1",
      triggers: [{ id: "only-trigger", type: "manual", entry_node: "somewhere_else" }],
      graph: { entry: "start", nodes: [], edges: [] },
    } as any);
    const run = {
      status: "success",
      entry: "n1",
      completed: ["n1"],
      outputs: { n1: { x: 1 } },
      event: { type: "manual", trigger_id: null, payload: { foo: "bar" } },
    };
    hydrateFromRun(run);
    expect(get(triggerEventByID)["only-trigger"]).toEqual({ foo: "bar" });
  });

  it("marks completed nodes success and failed nodes failed", () => {
    hydrateFromRun(RUN);
    const status = get(runStatusByNode);
    expect(status.check_action).toBe("success");
    expect(status.fetch_diff).toBe("failed");
  });

  it("marks the trigger success when any node ran (✓ overlay after import)", () => {
    hydrateFromRun(RUN);
    expect(get(triggerRunStatus)["trigger-webhook"]).toBe("success");
  });

  it("seeds each node's output and the failed node's error", () => {
    hydrateFromRun(RUN);
    const steps = get(stepResultsByNode);
    expect(steps.check_action.ok).toBe(true);
    expect(steps.check_action.output).toEqual({ result: "<no value>" });
    // fetch_diff had no Outputs entry — seeded from error.node + message.
    expect(steps.fetch_diff.ok).toBe(false);
    expect(steps.fetch_diff.error).toContain("nil pointer");
  });

  it("pins the trigger event payload keyed by event.trigger_id", () => {
    hydrateFromRun(RUN);
    const events = get(triggerEventByID);
    expect(events["trigger-webhook"]).toEqual({ body: { test: "test" }, method: "POST" });
  });

  it("is a no-op on null", () => {
    const summary = hydrateFromRun(null);
    expect(get(runStatusByNode)).toEqual({});
    expect(get(stepResultsByNode)).toEqual({});
    expect(summary).toEqual({ nodeCount: 0, outputCount: 0, triggerPinned: false, syntheticPayload: false });
  });

  it("reports a healthy summary for a run with outputs + real payload", () => {
    const summary = hydrateFromRun(RUN);
    expect(summary.nodeCount).toBe(2); // check_action + fetch_diff
    expect(summary.outputCount).toBe(1); // check_action
    expect(summary.triggerPinned).toBe(true);
    expect(summary.syntheticPayload).toBe(false);
  });

  // "replay sukses tapi kosong" case A: failed at the first node, no
  // outputs, but a real webhook payload still pins the trigger.
  it("failed-at-first-node still pins the real trigger payload", () => {
    const run = {
      status: "failed",
      completed: null,
      failed: ["check_action"],
      outputs: {},
      event: {
        type: "webhook",
        trigger_id: "trigger-webhook",
        payload: { body: { test: "test" }, method: "POST" },
      },
      error: { node: "check_action", message: "boom" },
    };
    const summary = hydrateFromRun(run);
    expect(summary.outputCount).toBe(0);
    expect(summary.triggerPinned).toBe(true); // payload is real → still useful
    expect(get(triggerEventByID)["trigger-webhook"]).toBeTruthy();
  });

  // "replay sukses tapi kosong" case B: legacy manual Execute — payload is
  // the {source:spa,trigger_id} placeholder, no real data.
  it("flags a legacy manual Execute as synthetic / not meaningfully pinned", () => {
    const run = {
      status: "failed",
      completed: ["check_action"],
      failed: ["fetch_diff"],
      outputs: { check_action: { result: "<no value>" } },
      event: {
        type: "webhook",
        trigger_id: null,
        payload: { source: "spa", trigger_id: "trigger-webhook" },
      },
    };
    const summary = hydrateFromRun(run);
    expect(summary.syntheticPayload).toBe(true);
    expect(summary.triggerPinned).toBe(false); // synthetic → not meaningful
    expect(summary.outputCount).toBe(1); // node output still hydrated
  });
});
