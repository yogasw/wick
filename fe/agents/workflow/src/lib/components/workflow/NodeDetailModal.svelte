<script lang="ts">
  // n8n-style 3-column modal for editing a node. Opened by
  // double-clicking a node on the canvas (legacy editor parity).
  //
  // Columns:
  //   LEFT   — Input: data from upstream nodes (mocked "empty" until
  //                    run history wiring lands).
  //   MIDDLE — Parameters / Settings tabs + Execute step CTA + Output
  //                    refs panel + Delete node.
  //   RIGHT  — Output: last recorded JSON / Schema.
  //
  // Per-type parameter forms live in this file (one #if per
  // NodeType), mirroring the legacy editor's per-node InspectorPartial
  // partials in internal/tools/agents/workflow/nodes/*/inspector.templ.
  // Field set tracked against the v1 audit — keep this 1:1; improve
  // where it helps but never reduce the surface.

  import { detailNodeID, draftWorkflow, removeNode, updateNode, isValidLabel, LABEL_FORMAT_HINT, stepResultsByNode, type StepResult } from "$lib/stores/editor";
  import JsonViewer from "./fields/JsonViewer.svelte";
  import { inferSchema } from "./fields/jsonSchema";
  import { catalog } from "$lib/stores/catalog";
  import { workflowAPI } from "$lib/api/workflow";
  import { toastError } from "$lib/stores/toast";
  import type { Node } from "$lib/types/workflow";
  import ArgField from "./fields/ArgField.svelte";
  import KvListField from "./fields/KvListField.svelte";
  import Field from "./fields/Field.svelte";
  import SchemaForm from "./fields/SchemaForm.svelte";
  import CodeEditor from "./fields/CodeEditor.svelte";
  import DatatableForm from "./nodes/DatatableForm.svelte";

  // Catalog-derived helpers for the channel / connector forms below.
  // Drive every dropdown + arg row off the registry so adding a new
  // channel or connector lights up the inspector with zero FE edits.
  const currentChannelOps = $derived.by(() => {
    if (!node || node.type !== "channel") return [];
    return $catalog?.channels?.find((c) => c.name === node.channel)?.ops ?? [];
  });
  const currentChannelOp = $derived.by(() => {
    return currentChannelOps.find((o) => o.id === node?.op);
  });
  const currentConnectorOps = $derived.by(() => {
    if (!node || node.type !== "connector") return [];
    return $catalog?.connectors?.find((c) => c.module === node.module)?.ops ?? [];
  });
  const currentConnectorOp = $derived.by(() => {
    return currentConnectorOps.find((o) => o.id === node?.op);
  });

  const node = $derived.by<Node | null>(() => {
    const id = $detailNodeID;
    if (!id || !$draftWorkflow) return null;
    return $draftWorkflow.graph?.nodes?.find((n) => n.id === id) ?? null;
  });

  let activeTab = $state<"params" | "settings">("params");

  function close() {
    detailNodeID.set(null);
  }

  // Generic field patch — for scalar / map fields lifted straight onto
  // the node body. Trigger-shape fields live on TriggerDetailModal.
  function patch(field: keyof Node, value: unknown) {
    if (!node) return;
    updateNode(node.id, { [field]: value } as Partial<Node>);
  }

  // Set the editor mode pill for a given field key. Mode lives in
  // node.arg_modes[<key>]; the engine treats both modes as Go
  // templates, the pill is purely a UX hint.
  function patchMode(key: string, mode: string) {
    if (!node) return;
    const next = { ...(node.arg_modes ?? {}) };
    next[key] = mode;
    updateNode(node.id, { arg_modes: next });
  }

  function modeFor(key: string): "fixed" | "expression" {
    const m = node?.arg_modes?.[key];
    return m === "expression" ? "expression" : "fixed";
  }

  function patchArgs(field: "args" | "headers" | "query" | "env" | "match", next: Record<string, unknown>) {
    if (!node) return;
    updateNode(node.id, { [field]: next } as Partial<Node>);
  }

  // Stringy-only map helper — most kv-list fields (headers, query,
  // env, channel/connector args) carry string values that template at
  // render time. patchArgs accepts a wider shape so the same call site
  // can write nested objects too.
  function patchStringMap(field: "headers" | "query" | "env" | "args", next: Record<string, string>) {
    patchArgs(field, next);
  }

  function patchModeMap(field: "arg_modes", next: Record<string, string>) {
    if (!node) return;
    updateNode(node.id, { [field]: next } as Partial<Node>);
  }

  function deleteSelf() {
    if (!node) return;
    if (!confirm(`Delete node "${node.label || node.id}"?`)) return;
    removeNode(node.id);
    close();
  }

  // Execute-step state. The result store survives modal close/reopen
  // and lets a child node's INPUT pane read its parent's last output.
  let executing = $state(false);
  const lastRun = $derived.by<StepResult | null>(() => {
    if (!node) return null;
    return $stepResultsByNode[node.id] ?? null;
  });

  // Find the upstream node id that flows into this node, if any. For
  // merge / fan-in shapes we just take the first parent — Execute step
  // is a debugging convenience, not a faithful replay.
  const parentNodeID = $derived.by<string | null>(() => {
    if (!node) return null;
    const wf = $draftWorkflow;
    const edge = wf?.graph?.edges?.find((e) => e.to === node!.id);
    return edge?.from ?? null;
  });

  // Every node reachable upstream from this one (BFS through edges).
  // Drives the INPUT-pane parent dropdown so the operator can read
  // not just the direct parent's output but any earlier step's.
  const upstreamIDs = $derived.by<string[]>(() => {
    if (!node) return [];
    const wf = $draftWorkflow;
    const edges = wf?.graph?.edges ?? [];
    const seen = new Set<string>();
    const queue = [node.id];
    while (queue.length) {
      const cur = queue.shift()!;
      for (const e of edges) {
        if (e.to !== cur || seen.has(e.from)) continue;
        seen.add(e.from);
        queue.push(e.from);
      }
    }
    return [...seen];
  });

  // Upstream nodes that have a stored output, in upstream order.
  const upstreamWithOutput = $derived.by(() => {
    const wf = $draftWorkflow;
    const all = wf?.graph?.nodes ?? [];
    return upstreamIDs
      .map((id) => {
        const n = all.find((x) => x.id === id);
        const run = $stepResultsByNode[id];
        return { id, label: n?.label || id, run };
      })
      .filter((row) => row.run?.output);
  });

  // Which parent output is being shown in the INPUT pane. Defaults to
  // direct parent when it has data, falls back to first available.
  let selectedInputSource = $state<string | null>(null);
  $effect(() => {
    if (!node) return;
    const available = upstreamWithOutput.map((r) => r.id);
    if (selectedInputSource && available.includes(selectedInputSource)) return;
    if (parentNodeID && available.includes(parentNodeID)) {
      selectedInputSource = parentNodeID;
    } else {
      selectedInputSource = available[0] ?? null;
    }
  });

  // INPUT/OUTPUT view mode pills (JSON vs Schema) — local UI state.
  let inputView = $state<"json" | "schema">("json");
  let outputView = $state<"json" | "schema">("json");

  // Resolved INPUT data. Two cases:
  //   (a) The dropdown points at an upstream node — show that node's
  //       output. The drag template prefix is `.Node.<label>` so refs
  //       drop in as {{.Node.parent_label.row.id}} matching the engine's
  //       Outputs map.
  //   (b) No dropdown selection but a mock_input is set → show that
  //       as the active sample (prefix `.Input` since that's how the
  //       backend wires it into rc.Outputs["input"]).
  const inputResolved = $derived.by<{ data: unknown; prefix: string; source: "upstream" | "mock" | "none"; sourceLabel: string }>(() => {
    if (!node) return { data: null, prefix: "", source: "none", sourceLabel: "" };
    if (selectedInputSource) {
      const row = upstreamWithOutput.find((r) => r.id === selectedInputSource);
      if (row?.run?.output) {
        return {
          data: row.run.output,
          prefix: ".Node." + row.label,
          source: "upstream",
          sourceLabel: row.label,
        };
      }
    }
    const mockRaw = (node as { mock_input?: string }).mock_input ?? "";
    if (mockRaw.trim()) {
      try {
        return {
          data: JSON.parse(mockRaw),
          prefix: ".Input",
          source: "mock",
          sourceLabel: "mock",
        };
      } catch {
        /* fall through */
      }
    }
    return { data: null, prefix: "", source: "none", sourceLabel: "" };
  });

  async function runStep() {
    if (!node) return;
    const mockRaw = (node as { mock_input?: string }).mock_input ?? "";
    if (mockRaw.trim()) {
      try {
        JSON.parse(mockRaw);
      } catch {
        toastError("Bad mock JSON", "Settings → Mock input must be valid JSON.");
        return;
      }
    }
    // The middle-pane Execute uses whatever the INPUT pane is showing
    // so it stays consistent with what the user just inspected.
    const inputForRun = (inputResolved.data as Record<string, unknown> | null) ?? undefined;
    const parentForRun = inputResolved.source === "upstream" ? selectedInputSource : null;
    // Forward every upstream output the FE has cached. Backend
    // hydrates rc.NodeOutputs from this so template refs
    // {{.Node.<upstream_label>.row}} resolve in single-node runs the
    // same way they do during full workflow runs.
    const upstreamSnapshot: Record<string, Record<string, unknown>> = {};
    for (const upID of upstreamIDs) {
      const stored = $stepResultsByNode[upID];
      if (stored?.output) upstreamSnapshot[upID] = stored.output;
    }
    executing = true;
    const nodeID = node.id;
    try {
      const wf = $draftWorkflow;
      if (!wf) return;
      const res = await workflowAPI.execNode(wf.id, {
        node,
        input: inputForRun,
        parent_id: parentForRun ?? undefined,
        node_outputs: Object.keys(upstreamSnapshot).length > 0 ? upstreamSnapshot : undefined,
      });
      const entry: StepResult = {
        ok: res.ok,
        output: res.output,
        input: inputForRun,
        parent_id: parentForRun ?? undefined,
        error: res.error,
        latency_ms: res.latency_ms,
        at: Date.now(),
      };
      stepResultsByNode.update((m) => ({ ...m, [nodeID]: entry }));
      if (!res.ok) {
        toastError("Execute step failed", res.error ?? "Executor returned an error.");
      }
    } catch (e) {
      const errMsg = e instanceof Error ? e.message : String(e);
      stepResultsByNode.update((m) => ({
        ...m,
        [nodeID]: { ok: false, error: errMsg, input: inputForRun, parent_id: parentForRun ?? undefined, at: Date.now() },
      }));
      toastError("Execute step failed", errMsg);
    } finally {
      executing = false;
    }
  }

  // ── switch rule drag-reorder ──────────────────────────────────────
  // First-match-wins routing in `switch` nodes is order-sensitive,
  // so the operator needs to drag rules into place. Mirrors v1's
  // switchnode/inspector.js drag handle pattern: a small grab area
  // arms the row's `draggable` attribute on mousedown, the drop
  // event reorders the cases array, autosave flushes the result.
  let switchDragFrom = $state<number | null>(null);
  let switchDragOver = $state<number | null>(null);
  function onSwitchDragStart(i: number, e: DragEvent) {
    switchDragFrom = i;
    e.dataTransfer?.setData("text/plain", String(i));
    if (e.dataTransfer) e.dataTransfer.effectAllowed = "move";
  }
  function onSwitchDragOver(i: number, e: DragEvent) {
    if (switchDragFrom === null) return;
    e.preventDefault();
    switchDragOver = i;
  }
  function onSwitchDrop(i: number, e: DragEvent) {
    e.preventDefault();
    if (switchDragFrom === null || !node) {
      switchDragFrom = null;
      switchDragOver = null;
      return;
    }
    const from = switchDragFrom;
    switchDragFrom = null;
    switchDragOver = null;
    if (from === i) return;
    const next = [...(node.cases ?? [])];
    const [moved] = next.splice(from, 1);
    next.splice(i, 0, moved);
    updateNode(node.id, { cases: next });
  }
  function onSwitchDragEnd() {
    switchDragFrom = null;
    switchDragOver = null;
  }

  // Inline duplicate check for the Label input — flags when the
  // current node's label is also held by another node in the graph.
  // Save is blocked client-side by editor.saveDraft; this surfaces
  // the conflict immediately at the row that caused it.
  function labelClashesWith(target: Node): boolean {
    if (!target.label) return false;
    const wf = $draftWorkflow;
    if (!wf) return false;
    return (wf.graph?.nodes ?? []).some(
      (n) => n.id !== target.id && (n.label === target.label || n.id === target.label),
    );
  }

  // Output refs — list of templating refs operators can copy-paste
  // into downstream fields. Static per-type table; mirrors the green
  // "Output refs available" box at the bottom of the legacy
  // Parameters tab.
  const outputRefs = $derived.by<string[]>(() => {
    if (!node) return [];
    const id = node.id;
    const base = `{{.Node.${id}.result}}`;
    switch (node.type) {
      case "http":
        return [
          `{{.Node.${id}.status_code}}`,
          `{{.Node.${id}.body}}`,
          `{{.Node.${id}.headers}}`,
        ];
      case "classify":
        return [`{{.Node.${id}.case}}`, `{{.Node.${id}.confidence}}`];
      case "branch":
      case "switch":
        return [`{{.Node.${id}.case}}`];
      case "db_query":
        return [`{{.Node.${id}.rows}}`, `{{.Node.${id}.row_count}}`];
      case "shell":
        return [
          `{{.Node.${id}.stdout}}`,
          `{{.Node.${id}.stderr}}`,
          `{{.Node.${id}.exit_code}}`,
        ];
      case "datatable_get":
        return [`{{.Node.${id}.row}}`, `{{.Node.${id}.verdict}}`];
      case "datatable_exists":
        return [`{{.Node.${id}.verdict}}`];
      case "datatable_query":
        return [`{{.Node.${id}.rows}}`, `{{.Node.${id}.row_count}}`];
      case "datatable_count":
        return [`{{.Node.${id}.count}}`];
      case "end":
        return [`{{.Run.final_result}}`];
      default:
        return [base];
    }
  });
</script>

<svelte:window onkeydown={(e) => node && e.key === "Escape" && close()} />

{#if node}
  <div
    class="fixed inset-0 z-50 bg-slate-900/70 backdrop-blur-sm"
    role="dialog"
    aria-modal="true"
    aria-label="Edit node"
    onclick={close}
  >
    <div
      class="rounded-lg overflow-hidden bg-white dark:bg-[#0f172a]
             text-slate-900 dark:text-slate-100 shadow-2xl flex flex-col"
      style="position:absolute; left:16px; right:16px; top:32px; bottom:32px;"
      onclick={(e) => e.stopPropagation()}
      role="presentation"
    >
      <!-- Header. -->
      <header class="flex items-center gap-3 px-5 py-3 border-b border-slate-200 dark:border-slate-700">
        <span class="h-2 w-2 rounded-full bg-amber-400"></span>
        <span class="text-sm font-semibold">{node.label || node.id}</span>
        <span class="text-xs text-slate-500 font-mono">{node.type}</span>
        <div class="flex-1"></div>
        <button class="text-slate-400 hover:text-slate-100 text-xl leading-none" onclick={close} aria-label="Close">✕</button>
      </header>

      <!-- 3-column body. -->
      <div class="flex-1 grid divide-x divide-slate-200 dark:divide-slate-800 min-h-0" style="grid-template-columns: 1fr 2fr 1fr;">
        <!-- LEFT: input. Source dropdown picks which upstream node's
             output feeds this pane (defaults to direct parent), then
             JSON / Schema tabs flip the view. JSON leaves are
             draggable — drop them on any ArgField in the middle pane
             to insert {{.Node.<label>.path}} and auto-flip the field
             into Expression mode. Matches the legacy editor's
             renderInteractiveJSON UX. -->
        <section class="flex flex-col p-3 overflow-y-auto">
          <div class="text-[11px] font-semibold tracking-wider text-slate-500 mb-2">INPUT</div>
          {#if upstreamWithOutput.length > 0 || inputResolved.source === "mock"}
            {#if upstreamWithOutput.length > 0}
              <select
                class="w-full mb-2 rounded border border-slate-300 dark:border-slate-700 bg-white dark:bg-slate-800 px-2 py-1 text-xs"
                bind:value={selectedInputSource}
              >
                {#each upstreamWithOutput as src}
                  <option value={src.id}>{src.label}</option>
                {/each}
              </select>
            {:else}
              <div class="mb-2 text-[10px] px-1.5 py-0.5 inline-block rounded bg-amber-100 text-amber-700 dark:bg-amber-900/40 dark:text-amber-300 self-start">mock</div>
            {/if}

            {#if inputResolved.data !== null}
              <div class="inline-flex rounded border border-slate-300 dark:border-slate-700 overflow-hidden text-[10px] uppercase tracking-wide self-start mb-2">
                {#each ["json", "schema"] as v}
                  <button
                    type="button"
                    class="px-2 py-0.5"
                    class:bg-rose-500={inputView === v}
                    class:text-white={inputView === v}
                    class:text-slate-500={inputView !== v}
                    onclick={() => (inputView = v as "json" | "schema")}
                  >{v}</button>
                {/each}
              </div>
              <div class="flex-1 overflow-auto rounded bg-slate-50 dark:bg-slate-900/40 p-2">
                {#if inputView === "json"}
                  <JsonViewer value={inputResolved.data} prefix={inputResolved.prefix} draggable={true} />
                {:else}
                  <pre class="font-mono text-[11px] text-slate-700 dark:text-slate-300 whitespace-pre-wrap">{inferSchema(inputResolved.data)}</pre>
                {/if}
              </div>
              <div class="mt-2 text-[10px] text-slate-500 dark:text-slate-400">
                Drag any value to an expression field on the right.
              </div>
            {/if}
          {:else}
            <div class="flex-1 flex flex-col items-center justify-center text-slate-400 text-xs gap-3">
              <div class="text-2xl">⤓</div>
              <div>No input data</div>
              {#if parentNodeID}
                {@const parentNode = $draftWorkflow?.graph?.nodes?.find((n) => n.id === parentNodeID)}
                <div class="text-[11px] text-center max-w-[200px]">
                  Run <span class="font-mono">{parentNode?.label || parentNodeID}</span> first, or set a Mock input under Settings.
                </div>
              {:else}
                <div class="text-[11px] text-center max-w-[200px]">
                  No upstream node. Set a Mock input under Settings to feed sample data.
                </div>
              {/if}
            </div>
          {/if}
        </section>

        <!-- MIDDLE: parameters. -->
        <section class="flex flex-col overflow-y-auto">
          <nav class="flex items-center border-b border-slate-200 dark:border-slate-700 px-4 text-sm">
            {#each ["params", "settings"] as t}
              <button
                class="px-3 py-2 capitalize border-b-2 transition-colors"
                class:border-rose-500={activeTab === t}
                class:text-rose-600={activeTab === t}
                class:border-transparent={activeTab !== t}
                class:text-slate-500={activeTab !== t}
                onclick={() => (activeTab = t as typeof activeTab)}
              >{t === "params" ? "Parameters" : "Settings"}</button>
            {/each}
            <div class="flex-1"></div>
            <button
              class="my-1.5 inline-flex items-center gap-1.5 px-3 py-1.5 rounded bg-rose-500 hover:bg-rose-600 text-white text-xs font-medium disabled:opacity-50"
              onclick={runStep}
              disabled={executing}
              title="Run only this node with the current input (no persistence)"
            >
              <span>▸</span> {executing ? "Running…" : "Execute step"}
            </button>
          </nav>

          <div class="p-4 space-y-3 text-sm">
            {#if activeTab === "params"}
              <!-- ── Common: id + label + description ────────────── -->
              <div>
                <div class="text-[11px] text-slate-500 uppercase mb-1">Node ID</div>
                <div class="font-mono text-[12px] text-slate-700 dark:text-slate-300">{node.id}</div>
              </div>
              {@const labelTaken = labelClashesWith(node)}
              {@const labelBadFormat = !!node.label && !isValidLabel(node.label)}
              {@const labelErr = labelTaken || labelBadFormat}
              <label class="flex flex-col gap-1">
                <span class="text-xs font-medium">Label</span>
                <input
                  class="rounded border bg-white dark:bg-slate-800 px-3 py-1.5 font-mono text-sm"
                  class:border-rose-500={labelErr}
                  class:border-slate-200={!labelErr}
                  class:dark:border-slate-700={!labelErr}
                  value={node.label ?? ""}
                  oninput={(e) => patch("label", (e.target as HTMLInputElement).value)}
                  placeholder="my_step"
                />
                {#if labelBadFormat}
                  <span class="text-[11px] text-rose-600 dark:text-rose-400">
                    Invalid format. Use {LABEL_FORMAT_HINT}.
                  </span>
                {:else if labelTaken}
                  <span class="text-[11px] text-rose-600 dark:text-rose-400">
                    Label "{node.label}" is already used by another node — pick a unique value.
                  </span>
                {:else}
                  <span class="text-[11px] text-slate-500 dark:text-slate-400">
                    {LABEL_FORMAT_HINT}
                  </span>
                {/if}
              </label>
              <label class="flex flex-col gap-1">
                <span class="text-xs font-medium">Description</span>
                <textarea
                  class="rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-3 py-1.5 text-sm"
                  rows="2"
                  placeholder="Notes for collaborators (optional)"
                  value={node.description ?? ""}
                  oninput={(e) => patch("description", (e.target as HTMLTextAreaElement).value)}
                ></textarea>
              </label>

              <!-- ── http ───────────────────────────────────────── -->
              {#if node.type === "http"}
                <label class="flex flex-col gap-1">
                  <span class="text-xs font-medium">Method</span>
                  <select
                    class="rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-3 py-1.5"
                    value={node.method ?? "GET"}
                    onchange={(e) => patch("method", (e.target as HTMLSelectElement).value)}
                  >
                    {#each ["GET", "POST", "PUT", "PATCH", "DELETE"] as m}
                      <option value={m}>{m}</option>
                    {/each}
                  </select>
                </label>
                <ArgField
                  label="URL"
                  value={node.url ?? ""}
                  mode={modeFor("url")}
                  multiline
                  rows={2}
                  placeholder="https://api.example.com/v1/things"
                  onValueChange={(v) => patch("url", v)}
                  onModeChange={(m) => patchMode("url", m)}
                />
                <KvListField
                  label="Headers"
                  entries={node.headers}
                  modes={node.arg_modes}
                  helper="Each value is rendered as a Go template."
                  keyPlaceholder="Authorization"
                  valuePlaceholder={"Bearer {{.Env.API_KEY}}"}
                  onChange={(next) => patchStringMap("headers", next)}
                  onModeChange={(m) => patchModeMap("arg_modes", m)}
                />
                <KvListField
                  label="Query"
                  entries={node.query}
                  modes={node.arg_modes}
                  helper="Encoded as URL query string."
                  keyPlaceholder="limit"
                  valuePlaceholder="25"
                  onChange={(next) => patchStringMap("query", next)}
                  onModeChange={(m) => patchModeMap("arg_modes", m)}
                />
                {#if (node.method ?? "GET") !== "GET"}
                  <ArgField
                    label="Body"
                    value={node.body ?? ""}
                    mode={modeFor("body")}
                    multiline
                    rows={6}
                    placeholder={"{\n  \"user\": \"{{.Event.Payload.user}}\"\n}"}
                    onValueChange={(v) => patch("body", v)}
                    onModeChange={(m) => patchMode("body", m)}
                  />
                {/if}
                <label class="flex flex-col gap-1">
                  <span class="text-xs font-medium">Parse response</span>
                  <select
                    class="rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-3 py-1.5"
                    value={node.parse_response ?? "json"}
                    onchange={(e) => patch("parse_response", (e.target as HTMLSelectElement).value)}
                  >
                    <option value="raw">raw</option>
                    <option value="json">json</option>
                    <option value="bytes">bytes</option>
                  </select>
                </label>
              {/if}

              <!-- ── shell ──────────────────────────────────────── -->
              {#if node.type === "shell"}
                <ArgField
                  label="Command"
                  value={(node.command ?? []).join("\n")}
                  mode={modeFor("command")}
                  multiline
                  rows={3}
                  placeholder={"bash\n-c\necho hello"}
                  helper="One argument per line. Fixed = literal argv. Expression = Go template per line."
                  onValueChange={(v) =>
                    patch(
                      "command",
                      v.split(/\r?\n/).filter((l) => l.length > 0),
                    )}
                  onModeChange={(m) => patchMode("command", m)}
                />
                <KvListField
                  label="Environment"
                  entries={node.env}
                  modes={node.arg_modes}
                  helper="Extra env vars merged on top of the wick process env."
                  keyPlaceholder="DATABASE_URL"
                  valuePlaceholder="postgres://…"
                  onChange={(next) => patchStringMap("env", next)}
                  onModeChange={(m) => patchModeMap("arg_modes", m)}
                />
                <ArgField
                  label="Working directory"
                  value={node.cwd ?? ""}
                  mode={modeFor("cwd")}
                  placeholder="/workspace/data"
                  onValueChange={(v) => patch("cwd", v)}
                  onModeChange={(m) => patchMode("cwd", m)}
                />
                <label class="flex flex-col gap-1">
                  <span class="text-xs font-medium">Parse output</span>
                  <select
                    class="rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-3 py-1.5"
                    value={node.parse_output ?? "raw"}
                    onchange={(e) => patch("parse_output", (e.target as HTMLSelectElement).value)}
                  >
                    <option value="raw">raw</option>
                    <option value="json">json</option>
                    <option value="lines">lines</option>
                  </select>
                </label>
                <label class="flex flex-col gap-1">
                  <span class="text-xs font-medium">Timeout</span>
                  <input
                    class="rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-3 py-1.5 font-mono"
                    placeholder="30s"
                    value={node.timeout ?? ""}
                    oninput={(e) => patch("timeout", (e.target as HTMLInputElement).value)}
                  />
                  <span class="text-[11px] text-slate-500">Go duration string (e.g. <code>30s</code>, <code>2m</code>).</span>
                </label>
              {/if}

              <!-- ── agent ──────────────────────────────────────── -->
              {#if node.type === "agent"}
                <ArgField
                  label="Prompt"
                  value={node.prompt ?? ""}
                  mode={modeFor("prompt")}
                  multiline
                  rows={6}
                  placeholder={"Help the user with {{.Event.Payload.text}}"}
                  onValueChange={(v) => patch("prompt", v)}
                  onModeChange={(m) => patchMode("prompt", m)}
                />
                <label class="flex flex-col gap-1">
                  <span class="text-xs font-medium">Prompt file</span>
                  <input
                    class="rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-3 py-1.5 font-mono"
                    placeholder="prompts/my-prompt.md"
                    value={node.prompt_file ?? ""}
                    oninput={(e) => patch("prompt_file", (e.target as HTMLInputElement).value)}
                  />
                </label>
                <Field
                  kind="select"
                  label="Provider"
                  value={node.provider ?? ""}
                  onChange={(v) => patch("provider", v)}
                  options={[
                    { label: "(default)", value: "" },
                    ...(($catalog?.providers ?? []).map((p) => ({
                      label: p.is_default ? `${p.name} · default` : p.name,
                      value: p.name,
                    }))),
                  ]}
                  helper="Override the workflow-level default. Empty = use engine default."
                />
                {#if ($catalog?.providers ?? []).length === 0}
                  <div class="text-[11px] text-amber-600 dark:text-amber-400 -mt-1">
                    No providers registered yet — set one up in the Providers settings page.
                  </div>
                {/if}
                <label class="flex flex-col gap-1">
                  <span class="text-xs font-medium">Skills (one per line)</span>
                  <textarea
                    class="rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-3 py-1.5 font-mono text-sm"
                    rows="3"
                    placeholder="skill_a&#10;skill_b"
                    value={(node.skills ?? []).join("\n")}
                    oninput={(e) =>
                      patch(
                        "skills",
                        (e.target as HTMLTextAreaElement).value.split(/\r?\n/).filter(Boolean),
                      )}
                  ></textarea>
                </label>
                <label class="flex flex-col gap-1">
                  <span class="text-xs font-medium">Tools (one per line)</span>
                  <textarea
                    class="rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-3 py-1.5 font-mono text-sm"
                    rows="3"
                    placeholder="tool_a&#10;tool_b"
                    value={(node.tools ?? []).join("\n")}
                    oninput={(e) =>
                      patch(
                        "tools",
                        (e.target as HTMLTextAreaElement).value.split(/\r?\n/).filter(Boolean),
                      )}
                  ></textarea>
                </label>
                <label class="flex flex-col gap-1">
                  <span class="text-xs font-medium">Max turns</span>
                  <input
                    type="number"
                    class="rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-3 py-1.5"
                    placeholder="0 = unlimited"
                    value={node.max_turns ?? 0}
                    oninput={(e) => patch("max_turns", Number((e.target as HTMLInputElement).value) || 0)}
                  />
                </label>
                <label class="flex flex-col gap-1">
                  <span class="text-xs font-medium">Session override</span>
                  <select
                    class="rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-3 py-1.5"
                    value={node.session ?? ""}
                    onchange={(e) => patch("session", (e.target as HTMLSelectElement).value)}
                  >
                    <option value="">(use default from session_init or engine)</option>
                    <option value="new">new — fresh subprocess per call</option>
                  </select>
                </label>
                <label class="flex flex-col gap-1">
                  <span class="text-xs font-medium">Reuse session from node</span>
                  <input
                    class="rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-3 py-1.5 font-mono"
                    placeholder="(node id of an upstream agent/session_init)"
                    value={node.session_from ?? ""}
                    oninput={(e) => patch("session_from", (e.target as HTMLInputElement).value)}
                  />
                </label>
              {/if}

              <!-- ── classify ───────────────────────────────────── -->
              {#if node.type === "classify"}
                <label class="flex flex-col gap-1">
                  <span class="text-xs font-medium">Output cases (one per line)</span>
                  <textarea
                    class="rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-3 py-1.5 font-mono text-sm"
                    rows="4"
                    placeholder="positive&#10;negative&#10;neutral"
                    value={(node.output_cases ?? []).join("\n")}
                    oninput={(e) =>
                      patch(
                        "output_cases",
                        (e.target as HTMLTextAreaElement).value.split(/\r?\n/).filter(Boolean),
                      )}
                  ></textarea>
                </label>
                {#if (node.output_cases ?? []).length > 0}
                  <!-- Case coverage — show each declared case + the
                       edge it routes to (if any). Mirrors v1's
                       editor_inspector.templ "Cases (branches)"
                       panel so the operator catches unrouted cases
                       before they fail at runtime. -->
                  <div class="rounded border border-slate-200 dark:border-slate-700 p-2 space-y-1">
                    <div class="text-[11px] font-medium text-slate-500 dark:text-slate-400">
                      Branch routing
                    </div>
                    {#each node.output_cases ?? [] as caseLabel}
                      {@const routedEdges = ($draftWorkflow?.graph?.edges ?? []).filter((e) => e.from === node!.id && e.case === caseLabel)}
                      <div class="flex items-center gap-2 text-[12px]">
                        <span class="font-mono px-1.5 py-0.5 rounded bg-emerald-100 text-emerald-800 dark:bg-emerald-900/40 dark:text-emerald-300 min-w-[80px]">{caseLabel}</span>
                        {#if routedEdges.length === 0}
                          <span class="text-rose-600 dark:text-rose-400 text-[11px] italic">unrouted — no outgoing edge with this case</span>
                        {:else}
                          <span class="text-slate-400">→</span>
                          {#each routedEdges as edge}
                            <button
                              type="button"
                              class="font-mono text-emerald-600 dark:text-emerald-400 hover:underline"
                              onclick={() => detailNodeID.set(edge.to)}
                              title="Open downstream node"
                            >{edge.to}</button>
                          {/each}
                        {/if}
                      </div>
                    {/each}
                  </div>
                {/if}
                <ArgField
                  label="Input (text to classify)"
                  value={node.input ?? ""}
                  mode={modeFor("input")}
                  multiline
                  rows={3}
                  placeholder={"{{.Event.Payload.text}}"}
                  onValueChange={(v) => patch("input", v)}
                  onModeChange={(m) => patchMode("input", m)}
                />
                <Field
                  kind="select"
                  label="Provider"
                  value={node.provider ?? ""}
                  onChange={(v) => patch("provider", v)}
                  options={[
                    { label: "(default)", value: "" },
                    ...(($catalog?.providers ?? []).map((p) => ({
                      label: p.is_default ? `${p.name} · default` : p.name,
                      value: p.name,
                    }))),
                  ]}
                  helper="Override the workflow-level default. Empty = use engine default."
                />
                {#if ($catalog?.providers ?? []).length === 0}
                  <div class="text-[11px] text-amber-600 dark:text-amber-400 -mt-1">
                    No providers registered yet — set one up in the Providers settings page.
                  </div>
                {/if}
                <label class="flex flex-col gap-1">
                  <span class="text-xs font-medium">Prompt file</span>
                  <input
                    class="rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-3 py-1.5 font-mono"
                    placeholder="prompts/classify.md"
                    value={node.prompt_file ?? ""}
                    oninput={(e) => patch("prompt_file", (e.target as HTMLInputElement).value)}
                  />
                </label>
                <label class="flex items-center gap-2">
                  <input
                    type="checkbox"
                    class="w-4 h-4 accent-emerald-500"
                    checked={node.fuzzy_match ?? false}
                    onchange={(e) => patch("fuzzy_match", (e.target as HTMLInputElement).checked)}
                  />
                  <span class="text-xs font-medium">Fuzzy match</span>
                </label>
                <label class="flex flex-col gap-1">
                  <span class="text-xs font-medium">Retry on mismatch</span>
                  <input
                    type="number"
                    class="rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-3 py-1.5"
                    placeholder="0"
                    value={node.retry_on_mismatch ?? 0}
                    oninput={(e) => patch("retry_on_mismatch", Number((e.target as HTMLInputElement).value) || 0)}
                  />
                </label>
              {/if}

              <!-- ── branch ─────────────────────────────────────── -->
              {#if node.type === "branch"}
                <ArgField
                  label="Expression"
                  value={node.expr ?? ""}
                  mode={modeFor("expr")}
                  multiline
                  rows={3}
                  placeholder={'{{ if eq .Node.classify.case "positive" }}positive{{ else }}other{{ end }}'}
                  helper="Output is a case label routing to the matching outgoing edge."
                  onValueChange={(v) => patch("expr", v)}
                  onModeChange={(m) => patchMode("expr", m)}
                />
              {/if}

              <!-- ── switch ─────────────────────────────────────── -->
              {#if node.type === "switch"}
                <div class="space-y-2">
                  <div class="flex items-center justify-between">
                    <span class="text-xs font-medium">Rules (first match wins)</span>
                    <button
                      type="button"
                      class="text-emerald-600 text-xs"
                      onclick={() => patch("cases", [...(node.cases ?? []), { when: "", case: "" }])}
                    >+ add rule</button>
                  </div>
                  {#each node.cases ?? [] as rule, i (i)}
                    <div
                      class="rounded border p-2 space-y-2 transition-colors"
                      class:border-slate-200={switchDragOver !== i}
                      class:dark:border-slate-700={switchDragOver !== i}
                      class:border-emerald-400={switchDragOver === i}
                      class:dark:border-emerald-500={switchDragOver === i}
                      class:opacity-50={switchDragFrom === i}
                      draggable="true"
                      ondragstart={(e) => onSwitchDragStart(i, e)}
                      ondragover={(e) => onSwitchDragOver(i, e)}
                      ondrop={(e) => onSwitchDrop(i, e)}
                      ondragend={onSwitchDragEnd}
                      role="listitem"
                    >
                      <div class="flex items-center gap-2 -mt-1 -mx-1">
                        <span
                          class="text-slate-400 dark:text-slate-500 text-xs cursor-grab select-none"
                          title="Drag to reorder — first match wins"
                          aria-label="Reorder handle"
                        >⋮⋮</span>
                        <span class="text-[10px] text-slate-400">#{i + 1}</span>
                      </div>
                      <label class="flex flex-col gap-1">
                        <span class="text-[11px] text-slate-500">When</span>
                        <input
                          class="rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-2 py-1 font-mono text-[12px]"
                          placeholder={'{{ eq .Node.x.case "yes" }}'}
                          value={rule.when}
                          oninput={(e) => {
                            const next = [...(node.cases ?? [])];
                            next[i] = { ...next[i], when: (e.target as HTMLInputElement).value };
                            patch("cases", next);
                          }}
                        />
                      </label>
                      <div class="flex items-center gap-2">
                        <input
                          class="rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-2 py-1 font-mono text-[12px] flex-1"
                          placeholder="case-label"
                          value={rule.case}
                          oninput={(e) => {
                            const next = [...(node.cases ?? [])];
                            next[i] = { ...next[i], case: (e.target as HTMLInputElement).value };
                            patch("cases", next);
                          }}
                        />
                        <button
                          type="button"
                          class="text-rose-500 text-xs px-2"
                          onclick={() => {
                            const next = [...(node.cases ?? [])];
                            next.splice(i, 1);
                            patch("cases", next);
                          }}
                        >✕</button>
                      </div>
                    </div>
                  {/each}
                  <label class="flex flex-col gap-1">
                    <span class="text-xs font-medium">Default case</span>
                    <input
                      class="rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-3 py-1.5 font-mono"
                      placeholder="fallback-label"
                      value={node.default_case ?? ""}
                      oninput={(e) => patch("default_case", (e.target as HTMLInputElement).value)}
                    />
                    <span class="text-[11px] text-slate-500">Verdict emitted when no rule matches. Leave blank to fail closed.</span>
                  </label>
                </div>
              {/if}

              <!-- ── go_script + python ─────────────────────────── -->
              {#if node.type === "go_script" || node.type === "python"}
                <div class="space-y-1">
                  <div class="flex items-center justify-between gap-2">
                    <span class="text-xs font-medium">Code</span>
                    <div class="inline-flex rounded border border-slate-300 dark:border-slate-700 overflow-hidden text-[10px] uppercase tracking-wide">
                      {#each ["fixed", "expression"] as m}
                        <button
                          type="button"
                          class="px-2 py-0.5 transition-colors"
                          class:bg-emerald-500={modeFor("code") === m}
                          class:text-white={modeFor("code") === m}
                          class:text-slate-500={modeFor("code") !== m}
                          class:dark:text-slate-400={modeFor("code") !== m}
                          onclick={() => patchMode("code", m)}
                        >{m}</button>
                      {/each}
                    </div>
                  </div>
                  <CodeEditor
                    value={node.code ?? ""}
                    language={node.type === "python" ? "python" : "go"}
                    rows={14}
                    onChange={(v) => patch("code", v)}
                  />
                  <span class="text-[11px] text-slate-500 dark:text-slate-400">
                    Full {node.type === "go_script" ? "Go" : "Python"} program. stdin = RenderCtx JSON, stdout = result JSON. Runs in a sandboxed subprocess.
                  </span>
                </div>
              {/if}

              <!-- ── transform ──────────────────────────────────── -->
              {#if node.type === "transform"}
                <label class="flex flex-col gap-1">
                  <span class="text-xs font-medium">Engine</span>
                  <select
                    class="rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-3 py-1.5"
                    value={node.engine ?? "gotemplate"}
                    onchange={(e) => patch("engine", (e.target as HTMLSelectElement).value)}
                  >
                    <option value="gotemplate">gotemplate</option>
                    <option value="jsonpath">jsonpath</option>
                    <option value="jq">jq</option>
                  </select>
                </label>
                <ArgField
                  label="Expression"
                  value={node.expression ?? ""}
                  mode={modeFor("expression")}
                  multiline
                  rows={6}
                  placeholder={"{{ .Event.Payload.text | upper }}"}
                  onValueChange={(v) => patch("expression", v)}
                  onModeChange={(m) => patchMode("expression", m)}
                />
                <ArgField
                  label="Input (optional)"
                  value={node.input ?? ""}
                  mode={modeFor("input")}
                  placeholder={"{{.Node.previous.result}}"}
                  helper="Defaults to the full RenderCtx when blank."
                  onValueChange={(v) => patch("input", v)}
                  onModeChange={(m) => patchMode("input", m)}
                />
              {/if}

              <!-- ── db_query ───────────────────────────────────── -->
              {#if node.type === "db_query"}
                <label class="flex flex-col gap-1">
                  <span class="text-xs font-medium">Database</span>
                  <input
                    class="rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-3 py-1.5 font-mono"
                    placeholder="DSN ref configured in workspace"
                    value={node.database ?? ""}
                    oninput={(e) => patch("database", (e.target as HTMLInputElement).value)}
                  />
                </label>
                <ArgField
                  label="SQL"
                  value={node.sql ?? ""}
                  mode={modeFor("sql")}
                  multiline
                  rows={6}
                  placeholder="SELECT * FROM users WHERE id = ?"
                  helper="Use ? for positional arguments; fill them in below."
                  onValueChange={(v) => patch("sql", v)}
                  onModeChange={(m) => patchMode("sql", m)}
                />
                <label class="flex flex-col gap-1">
                  <span class="text-xs font-medium">SQL args (one per line)</span>
                  <textarea
                    class="rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-3 py-1.5 font-mono text-sm"
                    rows="3"
                    placeholder={"{{.Event.Payload.user_id}}"}
                    value={(node.sql_args ?? []).join("\n")}
                    oninput={(e) =>
                      patch(
                        "sql_args",
                        (e.target as HTMLTextAreaElement).value.split(/\r?\n/).filter(Boolean),
                      )}
                  ></textarea>
                </label>
              {/if}

              <!-- ── end ───────────────────────────────────────── -->
              {#if node.type === "end"}
                <ArgField
                  label="Result"
                  value={(node as unknown as { result?: string }).result ?? ""}
                  mode={modeFor("result")}
                  multiline
                  rows={4}
                  placeholder={"{{.Node.previous.result}}"}
                  helper={"Template stored in {{.Run.final_result}} after the run completes."}
                  onValueChange={(v) => updateNode(node!.id, { ...(node as object), result: v } as unknown as Partial<Node>)}
                  onModeChange={(m) => patchMode("result", m)}
                />
              {/if}

              <!-- ── session_init ──────────────────────────────── -->
              {#if node.type === "session_init"}
                {@const sessionMode = node.session_id ? "custom" : (node.preset || "workflow_run")}
                <label class="flex flex-col gap-1">
                  <span class="text-xs font-medium">Sharing mode</span>
                  <select
                    class="rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-3 py-1.5"
                    value={sessionMode}
                    onchange={(e) => {
                      const m = (e.target as HTMLSelectElement).value;
                      if (m === "custom") {
                        // Mutex: switching to custom — auto-seed UUID
                        // when the field is empty so the operator sees
                        // a working id immediately, and clear preset
                        // since session_id wins on the engine side.
                        const seed = node.session_id || (crypto?.randomUUID?.() ?? "");
                        updateNode(node!.id, { preset: "", session_id: seed });
                      } else {
                        // Drop session_id so preset takes over —
                        // mirrors the v1 inspector.js mutex.
                        updateNode(node!.id, { preset: m, session_id: "" });
                      }
                    }}
                  >
                    <option value="workflow_run">workflow_run — reuse within this run</option>
                    <option value="workflow_global">workflow_global — reuse across runs of this workflow</option>
                    <option value="new">new — fresh session each call</option>
                    <option value="custom">custom — literal id below</option>
                  </select>
                </label>
                {#if sessionMode === "custom"}
                  <div class="flex items-end gap-2">
                    <div class="flex-1">
                      <ArgField
                        label="Session ID"
                        value={node.session_id ?? ""}
                        mode={modeFor("session_id")}
                        placeholder={"user-{{.Event.Payload.user}}"}
                        helper="Literal string or Go template. Wins over preset on the engine side."
                        onValueChange={(v) => patch("session_id", v)}
                        onModeChange={(m) => patchMode("session_id", m)}
                      />
                    </div>
                    <button
                      type="button"
                      class="h-9 px-3 rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 text-xs hover:bg-slate-50 dark:hover:bg-slate-700"
                      title="Generate a fresh UUID"
                      onclick={() => patch("session_id", crypto?.randomUUID?.() ?? "")}
                    >regen</button>
                  </div>
                {/if}
                <label class="flex flex-col gap-1">
                  <span class="text-xs font-medium">Workspace override (optional)</span>
                  <input
                    class="rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-3 py-1.5 font-mono"
                    placeholder="(use run workspace)"
                    value={node.workspace ?? ""}
                    oninput={(e) => patch("workspace", (e.target as HTMLInputElement).value)}
                  />
                </label>
              {/if}

              <!-- ── channel ────────────────────────────────────── -->
              {#if node.type === "channel"}
                {#if node.channel && node.op}
                  <!-- Locked channel + op — set by the palette drill
                       drop. Same rationale as the connector lock above. -->
                  <div class="rounded border border-slate-200 dark:border-slate-700 px-3 py-2 bg-slate-50 dark:bg-slate-800/40">
                    <div class="flex items-center justify-between gap-2">
                      <div class="flex flex-col">
                        <span class="text-[10px] uppercase tracking-wider text-slate-500">Action</span>
                        <span class="text-sm font-medium">
                          {node.channel}
                          <span class="text-slate-400 mx-1">›</span>
                          {node.op}
                        </span>
                      </div>
                      <span class="text-[10px] text-slate-400">locked</span>
                    </div>
                    {#if currentChannelOp?.description}
                      <div class="text-[11px] text-slate-500 dark:text-slate-400 mt-1">
                        {currentChannelOp.description}
                      </div>
                    {/if}
                  </div>
                {:else}
                  <Field
                    kind="select"
                    label="Channel"
                    value={node.channel ?? ""}
                    onChange={(v) => {
                      patch("channel", v);
                      // Reset op + args when switching channels — catalog
                      // resolves a different op set + arg schema per
                      // channel, leaving stale values around will fail
                      // validation on the server.
                      patch("op", "");
                      patch("args", {});
                    }}
                    options={[
                      { label: "(select channel)", value: "" },
                      ...($catalog?.channels ?? []).map((c) => ({
                        label: c.name,
                        value: c.name,
                      })),
                    ]}
                    helper="Channels registered with the wick channel registry."
                  />
                  {#if node.channel}
                    <Field
                      kind="select"
                      label="Op"
                      value={node.op ?? ""}
                      onChange={(v) => {
                        patch("op", v);
                        patch("args", {});
                      }}
                      options={[
                        { label: "(select op)", value: "" },
                        ...currentChannelOps.map((o) => ({ label: o.id, value: o.id })),
                      ]}
                    />
                    {#if currentChannelOp?.description}
                      <div class="text-[11px] text-slate-500 dark:text-slate-400 -mt-1">
                        {currentChannelOp.description}
                      </div>
                    {/if}
                  {/if}
                {/if}
                {#if currentChannelOp?.args_schema && currentChannelOp.args_schema.length > 0}
                  <!-- Schema-driven args — fields, types, picker
                       sources, visible_when predicates all come from
                       the Go ActionDescriptor.InputType wick tags. -->
                  <div class="rounded border border-slate-200 dark:border-slate-700 p-2">
                    <SchemaForm
                      schema={currentChannelOp.args_schema}
                      values={(node.args ?? {}) as Record<string, unknown>}
                      onChange={(k, v) => patchArgs("args", { ...(node.args ?? {}), [k]: v })}
                      onClear={(k) => {
                        const next = { ...(node.args ?? {}) };
                        delete next[k];
                        patchArgs("args", next);
                      }}
                    />
                  </div>
                {:else if node.op}
                  <!-- Op declares no schema — fall back to the legacy
                       free-form key/value editor so older channels
                       still work. -->
                  <KvListField
                    label="Args"
                    entries={(node.args ?? {}) as Record<string, string>}
                    modes={node.arg_modes}
                    helper="Op declared no schema. Free-form key/value — each value rendered as a Go template."
                    keyPlaceholder="to"
                    valuePlaceholder="#alerts"
                    onChange={(next) => patchArgs("args", next)}
                    onModeChange={(m) => patchModeMap("arg_modes", m)}
                  />
                {/if}
              {/if}

              <!-- ── connector ──────────────────────────────────── -->
              {#if node.type === "connector"}
                {#if node.module && node.op}
                  <!-- Locked module + op — set by the palette drill
                       drop. Changing them would invalidate the args
                       schema; if the user wants a different op they
                       delete the node and drop a new one. -->
                  <div class="rounded border border-slate-200 dark:border-slate-700 px-3 py-2 bg-slate-50 dark:bg-slate-800/40">
                    <div class="flex items-center justify-between gap-2">
                      <div class="flex flex-col">
                        <span class="text-[10px] uppercase tracking-wider text-slate-500">Action</span>
                        <span class="text-sm font-medium">
                          {($catalog?.connectors ?? []).find((c) => c.module === node.module)?.name || node.module}
                          <span class="text-slate-400 mx-1">›</span>
                          {currentConnectorOp?.name || node.op}
                        </span>
                      </div>
                      <span class="text-[10px] text-slate-400">locked</span>
                    </div>
                    {#if currentConnectorOp?.description}
                      <div class="text-[11px] text-slate-500 dark:text-slate-400 mt-1">
                        {currentConnectorOp.description}
                      </div>
                    {/if}
                  </div>
                {:else}
                  <!-- Manually created node (no drill drop) — keep the
                       editable dropdowns so the user can still wire it
                       up by hand. -->
                  <Field
                    kind="select"
                    label="Module"
                    value={node.module ?? ""}
                    onChange={(v) => {
                      patch("module", v);
                      patch("op", "");
                      patch("args", {});
                    }}
                    options={[
                      { label: "(select module)", value: "" },
                      ...($catalog?.connectors ?? []).map((c) => ({
                        label: c.name || c.module,
                        value: c.module,
                      })),
                    ]}
                    helper="Connector modules registered with the wick connector registry."
                  />
                  {#if node.module}
                    <Field
                      kind="select"
                      label="Op"
                      value={node.op ?? ""}
                      onChange={(v) => {
                        patch("op", v);
                        patch("args", {});
                      }}
                      options={[
                        { label: "(select op)", value: "" },
                        ...currentConnectorOps.map((o) => ({
                          label: o.name || o.id,
                          value: o.id,
                        })),
                      ]}
                    />
                    {#if currentConnectorOp?.description}
                      <div class="text-[11px] text-slate-500 dark:text-slate-400 -mt-1">
                        {currentConnectorOp.description}
                      </div>
                    {/if}
                  {/if}
                {/if}
                {#if currentConnectorOp?.args_schema && currentConnectorOp.args_schema.length > 0}
                  <div class="rounded border border-slate-200 dark:border-slate-700 p-2">
                    <SchemaForm
                      schema={currentConnectorOp.args_schema}
                      values={(node.args ?? {}) as Record<string, unknown>}
                      onChange={(k, v) => patchArgs("args", { ...(node.args ?? {}), [k]: v })}
                      onClear={(k) => {
                        const next = { ...(node.args ?? {}) };
                        delete next[k];
                        patchArgs("args", next);
                      }}
                    />
                  </div>
                {:else if node.op}
                  <KvListField
                    label="Args"
                    entries={(node.args ?? {}) as Record<string, string>}
                    modes={node.arg_modes}
                    helper="Op declared no schema. Free-form key/value — each value rendered as a Go template."
                    keyPlaceholder="title"
                    valuePlaceholder={"Bug: {{.Event.Payload.subject}}"}
                    onChange={(next) => patchArgs("args", next)}
                    onModeChange={(m) => patchModeMap("arg_modes", m)}
                  />
                {/if}
              {/if}

              <!-- ── parallel ───────────────────────────────────── -->
              {#if node.type === "parallel"}
                <label class="flex flex-col gap-1">
                  <span class="text-xs font-medium">Branches (one node id per line)</span>
                  <textarea
                    class="rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-3 py-1.5 font-mono text-sm"
                    rows="5"
                    placeholder="step_a&#10;step_b&#10;step_c"
                    value={(node.branches ?? []).join("\n")}
                    oninput={(e) =>
                      patch(
                        "branches",
                        (e.target as HTMLTextAreaElement).value.split(/\r?\n/).filter(Boolean),
                      )}
                  ></textarea>
                </label>
              {/if}

              <!-- ── merge ──────────────────────────────────────── -->
              {#if node.type === "merge"}
                <label class="flex flex-col gap-1">
                  <span class="text-xs font-medium">Inputs (one node id per line)</span>
                  <textarea
                    class="rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-3 py-1.5 font-mono text-sm"
                    rows="4"
                    placeholder="step_a&#10;step_b"
                    value={(node.inputs ?? []).join("\n")}
                    oninput={(e) =>
                      patch(
                        "inputs",
                        (e.target as HTMLTextAreaElement).value.split(/\r?\n/).filter(Boolean),
                      )}
                  ></textarea>
                </label>
                <label class="flex flex-col gap-1">
                  <span class="text-xs font-medium">Strategy</span>
                  <select
                    class="rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-3 py-1.5"
                    value={node.strategy ?? "all"}
                    onchange={(e) => patch("strategy", (e.target as HTMLSelectElement).value)}
                  >
                    <option value="all">all — wait for every input</option>
                    <option value="any">any — first to finish wins</option>
                  </select>
                </label>
              {/if}

              <!-- ── datatable_* (table + per-op builders) ──────── -->
              {#if node.type?.startsWith?.("datatable_")}
                <DatatableForm {node} />
              {/if}

              <!-- ── Output refs available ──────────────────────── -->
              <div class="mt-4 rounded-lg border border-emerald-500/30 bg-emerald-500/10 dark:bg-emerald-500/15 p-3 text-xs text-emerald-800 dark:text-emerald-300">
                <strong>Output refs available:</strong>
                <div class="mt-1 space-y-0.5 font-mono text-emerald-700 dark:text-emerald-400">
                  {#each outputRefs as ref}
                    <div>{ref}</div>
                  {/each}
                </div>
              </div>

              <!-- ── Delete node ───────────────────────────────── -->
              <button
                type="button"
                class="mt-4 w-full text-xs text-rose-600 dark:text-rose-400 hover:bg-rose-50 dark:hover:bg-rose-900/20 border border-rose-300 dark:border-rose-700 rounded-lg py-1.5 transition-colors"
                onclick={deleteSelf}
              >Delete node</button>
            {:else}
              <!-- ── Settings tab: mock input + execution policy ── -->
              <label class="flex flex-col gap-1">
                <span class="text-xs font-medium">Mock input (JSON)</span>
                <textarea
                  class="rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-3 py-1.5 font-mono text-sm"
                  rows="6"
                  placeholder={'{ "text": "hello" }'}
                  value={(node as unknown as { mock_input?: string }).mock_input ?? ""}
                  oninput={(e) =>
                    updateNode(node!.id, {
                      ...(node as object),
                      mock_input: (e.target as HTMLTextAreaElement).value,
                    } as unknown as Partial<Node>)}
                ></textarea>
                <span class="text-[11px] text-slate-500">
                  Used when Execute step has no parent output to read from.
                </span>
              </label>
              <label class="flex flex-col gap-1">
                <span class="text-xs font-medium">Timeout (sec)</span>
                <input
                  type="number"
                  class="rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-3 py-1.5"
                  placeholder="0"
                  value={node.timeout_sec ?? 0}
                  oninput={(e) => patch("timeout_sec", Number((e.target as HTMLInputElement).value) || 0)}
                />
              </label>
              <label class="flex flex-col gap-1">
                <span class="text-xs font-medium">On failure</span>
                <select
                  class="rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-3 py-1.5"
                  value={node.on_failure ?? "halt"}
                  onchange={(e) => patch("on_failure", (e.target as HTMLSelectElement).value)}
                >
                  <option value="halt">halt</option>
                  <option value="skip">skip</option>
                  <option value="fallback">fallback</option>
                </select>
              </label>
              <label class="flex flex-col gap-1">
                <span class="text-xs font-medium">Fallback node</span>
                <input
                  class="rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-3 py-1.5 font-mono"
                  placeholder="(node id used when on_failure = fallback)"
                  value={node.fallback ?? ""}
                  oninput={(e) => patch("fallback", (e.target as HTMLInputElement).value)}
                />
              </label>
              <label class="flex flex-col gap-1">
                <span class="text-xs font-medium">Retry — max attempts</span>
                <input
                  type="number"
                  class="rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-3 py-1.5"
                  placeholder="0"
                  value={node.retry?.max ?? 0}
                  oninput={(e) =>
                    patch("retry", {
                      ...(node?.retry ?? {}),
                      max: Number((e.target as HTMLInputElement).value) || 0,
                    })}
                />
              </label>
              <label class="flex flex-col gap-1">
                <span class="text-xs font-medium">Retry — backoff</span>
                <input
                  class="rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-3 py-1.5 font-mono"
                  placeholder="exponential / 500ms / 2s"
                  value={node.retry?.backoff ?? ""}
                  oninput={(e) =>
                    patch("retry", {
                      ...(node?.retry ?? {}),
                      backoff: (e.target as HTMLInputElement).value,
                    })}
                />
              </label>
            {/if}
          </div>
        </section>

        <!-- RIGHT: output. Same JSON / Schema toggle as the INPUT pane,
             with status line + latency derived from the stored
             stepResult so closing + reopening the modal keeps the
             last run visible. -->
        <section class="flex flex-col p-3 overflow-y-auto">
          <div class="text-[11px] font-semibold tracking-wider text-slate-500 mb-2 flex items-center justify-between gap-2">
            <span>OUTPUT</span>
            {#if lastRun}
              <span
                class="px-1.5 py-0.5 rounded text-[10px]"
                class:bg-emerald-100={lastRun.ok}
                class:text-emerald-700={lastRun.ok}
                class:dark:bg-emerald-900={lastRun.ok}
                class:dark:text-emerald-300={lastRun.ok}
                class:bg-rose-100={!lastRun.ok}
                class:text-rose-700={!lastRun.ok}
                class:dark:bg-rose-900={!lastRun.ok}
                class:dark:text-rose-300={!lastRun.ok}
              >
                {lastRun.ok ? "ok" : "fail"}{lastRun.latency_ms !== undefined ? ` · ${lastRun.latency_ms}ms` : ""}
              </span>
            {/if}
          </div>
          {#if lastRun}
            {#if lastRun.error}
              <div class="text-[11px] text-rose-700 dark:text-rose-300 whitespace-pre-wrap mb-2 rounded border border-rose-300 dark:border-rose-700 p-2 bg-rose-50 dark:bg-rose-950/40">
                ✕ {lastRun.error}
              </div>
            {:else if lastRun.output}
              <div class="text-[11px] text-emerald-700 dark:text-emerald-400 mb-2">
                ✓ Last recorded output
              </div>
            {/if}
            {#if lastRun.output}
              <div class="inline-flex rounded border border-slate-300 dark:border-slate-700 overflow-hidden text-[10px] uppercase tracking-wide self-start mb-2">
                {#each ["json", "schema"] as v}
                  <button
                    type="button"
                    class="px-2 py-0.5"
                    class:bg-rose-500={outputView === v}
                    class:text-white={outputView === v}
                    class:text-slate-500={outputView !== v}
                    onclick={() => (outputView = v as "json" | "schema")}
                  >{v}</button>
                {/each}
              </div>
              <div class="flex-1 overflow-auto rounded bg-slate-50 dark:bg-slate-900/40 p-2">
                {#if outputView === "json"}
                  <JsonViewer value={lastRun.output} prefix={`.Node.${node.label || node.id}`} draggable={true} />
                {:else}
                  <pre class="font-mono text-[11px] text-slate-700 dark:text-slate-300 whitespace-pre-wrap">{inferSchema(lastRun.output)}</pre>
                {/if}
              </div>
            {/if}
          {:else}
            <div class="flex-1 flex flex-col items-center justify-center text-slate-400 text-xs gap-3">
              <div class="text-2xl">⤒</div>
              <div>No output data</div>
              <button
                type="button"
                class="px-3 py-1.5 rounded bg-rose-500 hover:bg-rose-600 text-white text-xs font-medium disabled:opacity-50"
                onclick={runStep}
                disabled={executing}
              >{executing ? "Running…" : "Execute step"}</button>
              <div class="text-[11px] text-center max-w-[180px]">
                Or set mock data under Settings → Mock input to feed in a sample event.
              </div>
            </div>
          {/if}
        </section>
      </div>
    </div>
  </div>
{/if}
