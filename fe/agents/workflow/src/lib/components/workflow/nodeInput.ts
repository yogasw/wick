// Pure input-resolution logic for the node inspector's INPUT pane.
// Extracted from NodeDetailModal so the precedence rules are unit-testable
// without mounting the component. The svelte file feeds it the reactive
// stores' current values; this function holds no Svelte state.

export type InputSource = "upstream" | "mock" | "event" | "none";

export type ResolvedInput = {
  data: unknown;
  // Template prefix the JsonViewer prepends when a value is dragged into
  // an expression field — ".Node.<label>" / ".Input" / ".Event".
  prefix: string;
  source: InputSource;
  sourceLabel: string;
};

export type UpstreamRow = {
  id: string;
  label: string;
  output?: Record<string, unknown> | undefined;
};

export type ResolveArgs = {
  hasNode: boolean;
  // The parent dropdown selection, when the user picked an upstream node.
  selectedInputSource: string | null;
  // Upstream nodes that carry a stored output, in upstream order.
  upstreamWithOutput: UpstreamRow[];
  // node.mock_input raw string (Settings → Mock input).
  mockRaw: string;
  // Trigger ids on the workflow, used to find a replayed event payload.
  triggerIDs: string[];
  // Replayed event payloads keyed by trigger id (triggerEventByID store).
  triggerEvents: Record<string, unknown>;
};

// EVENT_SOURCE is the sentinel selectedInputSource value that means "show
// the replayed trigger event" — lets the INPUT dropdown offer the event
// alongside upstream nodes, not just as an entry-node fallback.
export const EVENT_SOURCE = "__event__";

// activeTriggerEvent returns the replayed payload for the first trigger
// that has one (the only-one-trigger common case), or null.
function activeTriggerEvent(args: ResolveArgs): unknown {
  const triggerID = args.triggerIDs.find((tid) => args.triggerEvents[tid] != null);
  return triggerID ? args.triggerEvents[triggerID] : null;
}

// resolveNodeInput decides what the INPUT pane shows, in precedence:
//   1. dropdown explicitly points at the trigger event (EVENT_SOURCE)
//   2. selected upstream node's output  (.Node.<label>)
//   3. valid mock_input JSON            (.Input)
//   4. for an entry node (no upstream output) — the replayed trigger event
//      payload, surfaced as {Payload: <env>} so refs drop in as
//      {{.Event.Payload.x}}                (.Event)
//   5. nothing
export function resolveNodeInput(args: ResolveArgs): ResolvedInput {
  const none: ResolvedInput = { data: null, prefix: "", source: "none", sourceLabel: "" };
  if (!args.hasNode) return none;

  const eventPayload = activeTriggerEvent(args);

  // 1. Explicit event selection from the dropdown.
  if (args.selectedInputSource === EVENT_SOURCE && eventPayload != null) {
    return { data: { Payload: eventPayload }, prefix: ".Event", source: "event", sourceLabel: "trigger event" };
  }

  // 2. Selected upstream node's output.
  if (args.selectedInputSource && args.selectedInputSource !== EVENT_SOURCE) {
    const row = args.upstreamWithOutput.find((r) => r.id === args.selectedInputSource);
    if (row?.output) {
      return { data: row.output, prefix: ".Node." + row.label, source: "upstream", sourceLabel: row.label };
    }
  }

  // 3. Mock input.
  const mockRaw = args.mockRaw ?? "";
  if (mockRaw.trim()) {
    try {
      return { data: JSON.parse(mockRaw), prefix: ".Input", source: "mock", sourceLabel: "mock" };
    } catch {
      /* invalid JSON — fall through */
    }
  }

  // 4. Entry node (no upstream output) with a replayed trigger event.
  if (args.upstreamWithOutput.length === 0 && eventPayload != null) {
    return { data: { Payload: eventPayload }, prefix: ".Event", source: "event", sourceLabel: "trigger event" };
  }

  return none;
}
