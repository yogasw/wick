// Rename-cascade: when a node's label changes, every template reference
// {{.Node.<oldLabel>.…}} elsewhere in the workflow must follow so it stays
// valid. The engine resolves .Node.<label> and .Node.<id> interchangeably
// (see executor.go RenderCtx), but UI authors write labels — so a rename
// would otherwise orphan every ref that used the old label. n8n does the
// same cascade on node rename.
//
// Pure + deep: walks any node string field (url, prompt, expression, and
// nested args / headers maps) and rewrites matching refs. Keys that carry
// a node's own identity (id, label) are never touched.

// rewriteNodeRef replaces `.Node.<old>` with `.Node.<new>` inside a single
// string, but only when <old> is the WHOLE segment — i.e. followed by `.`,
// `}`, or whitespace — so renaming "check" doesn't corrupt
// ".Node.check_action". Handles both label and the `index .Node "old"`
// form authors sometimes use for non-identifier labels.
export function rewriteNodeRef(s: string, oldLabel: string, newLabel: string): string {
  if (!oldLabel || oldLabel === newLabel || !s.includes(oldLabel)) return s;
  // Escape regex metachars in the label.
  const esc = oldLabel.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  // .Node.<old> where <old> ends at a boundary: dot, closing brace,
  // whitespace, or end-of-string.
  const dotForm = new RegExp(`(\\.Node\\.)${esc}(?=[.}\\s]|$)`, "g");
  // index .Node "<old>" form.
  const indexForm = new RegExp(`(\\.Node\\s+["'])${esc}(["'])`, "g");
  return s.replace(dotForm, `$1${newLabel}`).replace(indexForm, `$1${newLabel}$2`);
}

// deepRewrite recursively rewrites strings inside maps/arrays, skipping
// the identity keys so a node's own id/label aren't rewritten.
const IDENTITY_KEYS = new Set(["id", "label"]);
function deepRewrite(v: unknown, oldLabel: string, newLabel: string): unknown {
  if (typeof v === "string") return rewriteNodeRef(v, oldLabel, newLabel);
  if (Array.isArray(v)) return v.map((x) => deepRewrite(x, oldLabel, newLabel));
  if (v && typeof v === "object") {
    const out: Record<string, unknown> = {};
    for (const [k, val] of Object.entries(v as Record<string, unknown>)) {
      out[k] = IDENTITY_KEYS.has(k) ? val : deepRewrite(val, oldLabel, newLabel);
    }
    return out;
  }
  return v;
}

export type GraphNode = Record<string, unknown> & { id: string; label?: string };

// renameNodeRefs returns a NEW nodes array with the renamed node's label
// updated AND every {{.Node.<old>.…}} ref across all nodes rewritten to the
// new label. Pure — does not mutate the input. `renamedID` is the node
// being renamed; `oldLabel` is what its label was before (callers capture
// it before mutating, since the node object may already hold the new one).
export function renameNodeRefs(
  nodes: GraphNode[],
  renamedID: string,
  oldLabel: string,
  newLabel: string,
): GraphNode[] {
  return nodes.map((n) => {
    const withRefs = deepRewrite(n, oldLabel, newLabel) as GraphNode;
    // The renamed node itself also gets its label field set to the new
    // value (deepRewrite skipped it as an identity key).
    if (n.id === renamedID) return { ...withRefs, label: newLabel };
    return withRefs;
  });
}
