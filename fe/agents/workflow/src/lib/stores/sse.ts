import { get } from "svelte/store";
import {
  runStatusByNode,
  lastRunSummary,
  draftWorkflow,
  triggerRunStatus,
  lastFiredTriggerID,
  stepResultsByNode,
  type StepResult,
} from "./editor";

// SSE wire event shape from `agents.Event` (internal/tools/agents/
// stream.go). For workflow events, `type` is `wf_<engine_event>`
// (wf_workflow_started / wf_node_started / wf_node_completed / …)
// and `data` is a JSON-encoded `wf.RunEvent` envelope.
type WireEvent = {
  type: string;
  data: string;
};

// RunEvent matches workflow.RunEvent (Go side).
type RunEvent = {
  id: string;        // workflow id
  run_id: string;
  event: string;     // "workflow_started" | "node_completed" | …
  node?: string;
  case?: string;
  data?: Record<string, unknown>;
  ts: string;
};

// LogLine drives the Logs bottom tab. Each engine event becomes one
// row; the tab renders newest-first by default.
export type LogLine = {
  ts: string;
  event: string;
  node?: string;
  case?: string;
  status: "started" | "completed" | "failed" | "skipped" | "info";
};

// Per-workflow log buffer. Subscribers (LogsTab) consume directly.
import { writable } from "svelte/store";
export const logLines = writable<LogLine[]>([]);

let es: EventSource | null = null;
let runStartedAt = 0;

// connectSSE opens an EventSource for the given workflow id. Safe to
// call multiple times — closes any prior connection first.
export function connectSSE(workflowID: string) {
  if (es) {
    es.close();
    es = null;
  }
  const url = `/tools/agents/stream?session=wf:${encodeURIComponent(workflowID)}`;
  es = new EventSource(url);
  // Server emits named events ("event: agent\ndata: …") so we have to
  // attach via addEventListener("agent", …) — the default onmessage
  // handler only catches anonymous messages, which is why the toast +
  // node-status overlay sat silent until this fix.
  es.addEventListener("agent", (m: MessageEvent) => handle(m.data, workflowID));
  es.onerror = () => {
    // Connection dropped — EventSource auto-reconnects. Don't spam the
    // console; just let it ride.
  };
}

export function disconnectSSE() {
  if (es) {
    es.close();
    es = null;
  }
}

function handle(raw: string, workflowID: string) {
  let evt: WireEvent;
  try {
    evt = JSON.parse(raw);
  } catch {
    return;
  }
  if (!evt.type?.startsWith("wf_")) return;

  let payload: RunEvent;
  try {
    payload = JSON.parse(evt.data);
  } catch {
    return;
  }
  if (payload.id !== workflowID) return;

  const line: LogLine = {
    ts: payload.ts,
    event: payload.event,
    node: payload.node,
    case: payload.case,
    status:
      payload.event === "workflow_started" || payload.event === "node_started"
        ? "started"
        : payload.event === "node_completed" || payload.event === "workflow_completed"
          ? "completed"
          : payload.event === "node_failed" || payload.event === "workflow_failed"
            ? "failed"
            : payload.event === "node_skipped"
              ? "skipped"
              : "info",
  };
  logLines.update((cur) => [line, ...cur].slice(0, 500));

  // Per-node status overlay drives the green check / red cross on each
  // node card while the workflow runs. We also capture the input that
  // entered the node and the output it produced so the inspector's
  // INPUT/OUTPUT panes can render real data after a full workflow run
  // (not just per-node Execute step).
  if (payload.node) {
    const nodeID = payload.node;
    const data = (payload.data ?? {}) as Record<string, unknown>;
    if (payload.event === "node_started") {
      runStatusByNode.update((m) => ({ ...m, [nodeID]: "running" }));
      const input = (data.input ?? undefined) as Record<string, unknown> | undefined;
      // node_started lands before the result is known — seed an
      // "in-flight" entry so the INPUT pane reflects what's about to
      // run; node_completed / node_failed will fill in output + ok.
      stepResultsByNode.update((m) => {
        const next: StepResult = {
          ok: false,
          input,
          at: Date.now(),
        };
        return { ...m, [nodeID]: next };
      });
    } else if (payload.event === "node_completed") {
      runStatusByNode.update((m) => ({ ...m, [nodeID]: "success" }));
      const output = (data.output ?? undefined) as Record<string, unknown> | undefined;
      const latency = typeof data.latency_ms === "number" ? data.latency_ms : undefined;
      stepResultsByNode.update((m) => ({
        ...m,
        [nodeID]: {
          ...(m[nodeID] ?? { at: Date.now() }),
          ok: true,
          output,
          latency_ms: latency,
          at: Date.now(),
        },
      }));
    } else if (payload.event === "node_failed") {
      runStatusByNode.update((m) => ({ ...m, [nodeID]: "failed" }));
      const errMsg = typeof data.error === "string" ? data.error : undefined;
      const latency = typeof data.latency_ms === "number" ? data.latency_ms : undefined;
      stepResultsByNode.update((m) => ({
        ...m,
        [nodeID]: {
          ...(m[nodeID] ?? { at: Date.now() }),
          ok: false,
          error: errMsg,
          latency_ms: latency,
          at: Date.now(),
        },
      }));
    }
  }

  // Workflow-level events drive the top-bar "Run completed in Xms"
  // toast. Track the start moment so we can compute duration on
  // completion (engine emits the duration too but we surface either).
  if (payload.event === "workflow_started") {
    runStartedAt = Date.parse(payload.ts) || Date.now();
    runStatusByNode.set({}); // reset per-node overlay for the new run
    stepResultsByNode.set({}); // and per-node input/output snapshots
    const fired = get(lastFiredTriggerID);
    if (fired) {
      triggerRunStatus.update((m) => ({ ...m, [fired]: "running" }));
    }
  } else if (
    payload.event === "workflow_completed" ||
    payload.event === "workflow_failed"
  ) {
    const endMs = Date.parse(payload.ts) || Date.now();
    const duration = runStartedAt > 0 ? endMs - runStartedAt : 0;
    lastRunSummary.set({
      runID: payload.run_id,
      status: payload.event === "workflow_completed" ? "success" : "failed",
      durationMs: duration,
    });
    const fired = get(lastFiredTriggerID);
    if (fired) {
      const final: "success" | "failed" =
        payload.event === "workflow_completed" ? "success" : "failed";
      triggerRunStatus.update((m) => ({ ...m, [fired]: final }));
      // Clear the badge after 5s so it doesn't linger as stale state.
      setTimeout(() => {
        triggerRunStatus.update((m) => {
          if (m[fired] !== final) return m;
          const { [fired]: _, ...rest } = m;
          return rest;
        });
      }, 5000);
    }
    // Auto-clear the toast after 5s.
    setTimeout(() => {
      const current = get(lastRunSummary);
      if (current && current.runID === payload.run_id) {
        lastRunSummary.set(null);
      }
    }, 5000);
  }

  void draftWorkflow; // keep import used; future: refetch workflow on completion
}
