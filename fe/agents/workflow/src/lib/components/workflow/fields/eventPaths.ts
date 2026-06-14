// Flattens a replayed trigger event payload into Go-template dot-paths so
// the expression autocomplete can suggest the REAL fields a run carried
// (e.g. .Event.Payload.body.action) instead of a static channel-event
// guess. Mirrors how .Node.<label>.<key> completions read live outputs.

// eventPayloadPaths walks an object/array payload and returns dot-paths
// rooted at ".Event.Payload". Depth-limited so a huge github payload
// doesn't explode the suggestion list; arrays use [0] for the first item
// (Go templates index with `index`, but [0] reads naturally as a hint).
export function eventPayloadPaths(payload: unknown, maxDepth = 4): string[] {
  const out: string[] = [];
  const root = ".Event.Payload";

  function walk(node: unknown, prefix: string, depth: number) {
    if (node === null || node === undefined) return;
    if (depth > maxDepth) return;
    if (Array.isArray(node)) {
      // Surface the array itself; descend into the first element so nested
      // shapes are discoverable without listing every index.
      out.push(prefix);
      if (node.length > 0) walk(node[0], `${prefix}.0`, depth + 1);
      return;
    }
    if (typeof node === "object") {
      for (const key of Object.keys(node as Record<string, unknown>)) {
        const path = `${prefix}.${key}`;
        out.push(path);
        walk((node as Record<string, unknown>)[key], path, depth + 1);
      }
      return;
    }
    // scalar — the path to it was already pushed by the parent
  }

  walk(payload, root, 0);
  return out;
}

// directChildren returns just the immediate child paths under a given
// ".Event.Payload..." prefix — used to populate the dropdown when the
// user has typed exactly that prefix (matches the .Node.<label>. UX).
export function directChildren(allPaths: string[], prefix: string): string[] {
  const norm = prefix.endsWith(".") ? prefix.slice(0, -1) : prefix;
  const depthOfPrefix = norm.split(".").length;
  return allPaths.filter((p) => {
    if (!p.startsWith(norm + ".")) return false;
    return p.split(".").length === depthOfPrefix + 1;
  });
}

// bracesBalanced reports whether every "{{" has a matching "}}". Used to
// skip the live template preview mid-type — otherwise a half-typed
// "{{.Node." flashes a "template parse: unclosed action" error in the
// RESULT row before the user finishes the closing "}}".
export function bracesBalanced(s: string): boolean {
  const opens = (s.match(/\{\{/g) ?? []).length;
  const closes = (s.match(/\}\}/g) ?? []).length;
  return opens === closes;
}

// buildPreviewRequest assembles the {context, sampleEvent} the template
// preview endpoint needs. When a run has been replayed (eventPayload set)
// it sends the REAL event in context.Event and clears sampleEvent — the
// backend OVERWRITES context.Event with a sample preset when sample_event
// is non-empty (see mcp/template_check.go), so passing "cron" alongside a
// real payload would clobber it and {{.Event.Payload.body.x}} would
// evaluate against the wrong shape (the bug: nil-pointer in preview while
// Execute step worked). Without a replayed event, fall back to the sample
// preset so the preview still renders something.
export function buildPreviewRequest(
  nodeOutputs: Record<string, Record<string, unknown>>,
  eventPayload: unknown,
  fallbackSampleEvent: string,
): { context: string | undefined; sampleEvent: string } {
  const ctx: Record<string, unknown> = {};
  if (Object.keys(nodeOutputs).length > 0) ctx.Node = nodeOutputs;
  if (eventPayload != null) {
    ctx.Event = { type: "webhook", payload: eventPayload };
    return { context: JSON.stringify(ctx), sampleEvent: "" };
  }
  return {
    context: Object.keys(ctx).length > 0 ? JSON.stringify(ctx) : undefined,
    sampleEvent: fallbackSampleEvent,
  };
}
