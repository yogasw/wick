import { get } from "svelte/store";
import {
  runStatusByNode,
  lastRunSummary,
  draftWorkflow,
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
  // node card while the workflow runs.
  if (payload.node) {
    if (payload.event === "node_started") {
      runStatusByNode.update((m) => ({ ...m, [payload.node!]: "running" }));
    } else if (payload.event === "node_completed") {
      runStatusByNode.update((m) => ({ ...m, [payload.node!]: "success" }));
    } else if (payload.event === "node_failed") {
      runStatusByNode.update((m) => ({ ...m, [payload.node!]: "failed" }));
    }
  }

  // Workflow-level events drive the top-bar "Run completed in Xms"
  // toast. Track the start moment so we can compute duration on
  // completion (engine emits the duration too but we surface either).
  if (payload.event === "workflow_started") {
    runStartedAt = Date.parse(payload.ts) || Date.now();
    runStatusByNode.set({}); // reset per-node overlay for the new run
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
