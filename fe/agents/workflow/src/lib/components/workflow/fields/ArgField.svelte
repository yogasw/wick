<script lang="ts">
  // Single text / textarea field with Fixed ⇄ Expression mode toggle,
  // inline result preview (n8n style), and context autocomplete.
  //
  // Mode controls engine behaviour:
  //   fixed      → value passed verbatim, {{ }} NOT rendered
  //   expression → value rendered as Go template ({{ }} evaluated)
  // Stored in node.arg_modes[<key>]. Absent key = expression (render).

  import { untrack } from "svelte";
  import { workflowAPI } from "$lib/api/workflow";
  import { triggerEventByID, draftWorkflow, stepResultsByNode } from "$lib/stores/editor";
  import { eventPayloadPaths, directChildren, buildPreviewRequest, bracesBalanced } from "./eventPaths";
  import { expressionSegments } from "./splitTemplate";

  type Mode = "fixed" | "expression";
  type Props = {
    label: string;
    value: string;
    mode?: Mode;
    // lockedMode greys out the Fixed/Expression pill so the operator can't
    // change it — set when the field's wick tag pinned a mode. The pill
    // still renders (so the chosen mode is visible) but is non-interactive.
    lockedMode?: boolean;
    placeholder?: string;
    rows?: number;
    multiline?: boolean;
    helper?: string;
    workflowId?: string;
    nodeLabels?: string[];
    nodeOutputs?: Record<string, Record<string, unknown>>;
    onValueChange: (v: string) => void;
    onModeChange?: (m: Mode) => void;
  };

  let {
    label,
    value,
    mode = "fixed",
    lockedMode = false,
    placeholder,
    rows = 4,
    multiline = false,
    helper,
    workflowId,
    nodeLabels = [],
    nodeOutputs = {} as Record<string, Record<string, unknown>>,
    onValueChange,
    onModeChange,
  }: Props = $props();

  // ── Drag-drop (unchanged) ──────────────────────────────────────────
  let dragHover = $state(false);

  function onDragOver(e: DragEvent) {
    if (!e.dataTransfer?.types.includes("text/plain")) return;
    e.preventDefault();
    e.dataTransfer.dropEffect = "copy";
    dragHover = true;
  }
  function onDragLeave() { dragHover = false; }
  function onDrop(e: DragEvent) {
    e.preventDefault();
    dragHover = false;
    const text = e.dataTransfer?.getData("text/plain");
    if (!text) return;
    const el = e.currentTarget as HTMLInputElement | HTMLTextAreaElement;
    const start = el.selectionStart ?? value.length;
    const end   = el.selectionEnd   ?? value.length;
    onValueChange(value.slice(0, start) + text + value.slice(end));
    onModeChange?.("expression");
    requestAnimationFrame(() => {
      try {
        const caret = start + text.length;
        el.setSelectionRange(caret, caret);
        el.focus();
      } catch { /* gone */ }
    });
  }

  // ── Fixed+template warning ─────────────────────────────────────────
  let fixedWithTemplate = $derived(mode === "fixed" && value.includes("{{"));

  // ── Autocomplete ───────────────────────────────────────────────────
  const CTX_ROOTS   = [".Event.", ".Node.", ".Env.", ".Run.", ".Workflow."];
  const EVENT_PATHS = [".Event.Type", ".Event.Subtype", ".Event.Channel", ".Event.At", ".Event.Payload."];
  // Fallback channel-event keys, used only when no run has been replayed
  // so there's no real payload to read from.
  const PAYLOAD_KEYS_FALLBACK = ["text","user","channel_id","ts","thread","trigger_id","action_id",
                       "value","callback_id","schedule"].map(k => `.Event.Payload.${k}`);
  const FUNCS       = ["now","timeFormat","toJSON","fromJSON","jsonEscape","upper","lower",
                       "trim","default","truncate","index"].map(f => f + " ");

  // Dynamic .Event.Payload.* paths from the replayed run's event (any
  // trigger that has a pinned payload). Lets autocomplete suggest the
  // REAL fields a run carried — e.g. .Event.Payload.body.action for a
  // webhook — instead of the static channel-event guess. Empty until a
  // run is replayed, in which case we fall back to PAYLOAD_KEYS_FALLBACK.
  // The replayed event payload for the active trigger (any trigger that
  // has a pinned payload). Null until a run is replayed.
  const replayedEventPayload = $derived.by<unknown>(() => {
    const events = $triggerEventByID;
    const ids = ($draftWorkflow?.triggers ?? []).map((t) => t.id);
    const tid = ids.find((id) => events[id] != null) ?? Object.keys(events)[0];
    return tid ? events[tid] ?? null : null;
  });
  const eventPaths = $derived<string[]>(
    replayedEventPayload != null ? eventPayloadPaths(replayedEventPayload) : [],
  );

  // Node labels for `.Node.<label>.` autocomplete. The nodeLabels prop is
  // almost never wired by callers, so derive from the live workflow graph —
  // this is why the dropdown listed every node (it reads the graph) while
  // autocomplete only offered ".Node.trigger." (the hardcoded fallback).
  // Prefer an explicit prop when given, else all graph node labels.
  const acNodeLabels = $derived<string[]>(
    nodeLabels.length > 0
      ? nodeLabels
      : ($draftWorkflow?.graph?.nodes ?? []).map((n) => n.label || n.id).filter(Boolean),
  );

  // Per-node output fields for `.Node.<label>.<field>` autocomplete. The
  // nodeOutputs prop is likewise rarely wired (connector-arg fields go
  // through Field/SchemaForm which don't forward it), so derive from the
  // run-step store, KEYED BY LABEL (stepResultsByNode is keyed by node id).
  // This is why ".Node.session_init_1." offered no field keys even though
  // the INPUT pane showed result/session_id — the store had them, the
  // autocomplete didn't see them. Prop wins when explicitly provided.
  const acNodeOutputs = $derived.by<Record<string, Record<string, unknown>>>(() => {
    if (Object.keys(nodeOutputs).length > 0) return nodeOutputs;
    const out: Record<string, Record<string, unknown>> = {};
    const nodes = $draftWorkflow?.graph?.nodes ?? [];
    const steps = $stepResultsByNode;
    for (const n of nodes) {
      const o = steps[n.id]?.output;
      if (o && Object.keys(o).length > 0) out[n.label || n.id] = o;
    }
    return out;
  });

  // workflowId for the live preview — fall back to the draft's id when the
  // prop isn't threaded (connector-arg fields), otherwise preview never
  // fires (RESULT panel stays blank with no error).
  const acWorkflowId = $derived<string | undefined>(workflowId || $draftWorkflow?.id || undefined);

  let acSuggestions  = $state<string[]>([]);
  let acSelected     = $state(0);
  let inputEl        = $state<HTMLTextAreaElement | HTMLInputElement | null>(null);
  let dropdownStyle  = $state("");
  // Backend-provided env/secret keys — updated from template_test response.
  let acEnvKeys    = $state<string[]>([]);
  let acSecretKeys = $state<string[]>([]);

  function detectPartial(val: string, cursor: number): string | null {
    const before  = val.slice(0, cursor);
    const openAt  = before.lastIndexOf("{{");
    if (openAt < 0 || before.slice(openAt).includes("}}")) return null;
    return before.slice(openAt + 2).trimStart();
  }

  function buildSuggestions(partial: string, labels: string[], outputs: Record<string, Record<string, unknown>>, payloadPaths: string[]): string[] {
    // Real replayed-payload keys take priority; fall back to the static
    // channel guess only when no run has been replayed.
    const payloadKeys = payloadPaths.length > 0 ? payloadPaths : PAYLOAD_KEYS_FALLBACK;

    if (partial === "")                return [...CTX_ROOTS, ...FUNCS];
    if (partial === ".")               return CTX_ROOTS;
    if (partial === ".Event.")         return EVENT_PATHS;
    if (partial === ".Event.Payload.") {
      return payloadPaths.length > 0 ? directChildren(payloadPaths, ".Event.Payload.") : PAYLOAD_KEYS_FALLBACK;
    }
    if (partial === ".Node.") {
      return [...new Set([...labels, "trigger"])].map(l => `.Node.${l}.`);
    }
    if (partial === ".Env.") {
      return acEnvKeys.length > 0 ? acEnvKeys.map(k => `.Env.${k}`) : [".Env."];
    }

    // ".Node.<label>." → suggest actual output keys from run results.
    const nodeFieldMatch = partial.match(/^\.Node\.([^.]+)\.$/);
    if (nodeFieldMatch) {
      const label = nodeFieldMatch[1];
      const vals = outputs[label];
      if (vals && Object.keys(vals).length > 0) {
        return Object.keys(vals).map(k => `.Node.${label}.${k}`);
      }
    }

    // Nested ".Event.Payload.<...>." → drill into the replayed payload so
    // e.g. typing ".Event.Payload.body." lists body's keys.
    if (payloadPaths.length > 0 && /^\.Event\.Payload\..+\.$/.test(partial)) {
      const kids = directChildren(payloadPaths, partial);
      if (kids.length > 0) return kids;
    }

    const envSuggs = acEnvKeys.map(k => `.Env.${k}`);
    const pool = [
      ...CTX_ROOTS,
      ...EVENT_PATHS,
      ...payloadKeys,
      ...[...new Set([...labels, "trigger"])].map(l => `.Node.${l}.`),
      ...Object.entries(outputs).flatMap(([l, vals]) => Object.keys(vals).map(k => `.Node.${l}.${k}`)),
      ...envSuggs,
      ...FUNCS,
    ];
    const lo = partial.toLowerCase();
    return pool.filter(s => s.toLowerCase().startsWith(lo)).slice(0, 16);
  }

  function positionDropdown() {
    if (!inputEl) return;
    const r = inputEl.getBoundingClientRect();
    dropdownStyle = `top:${r.bottom + 2}px;left:${r.left}px;width:${r.width}px`;
  }

  function handleInput(e: Event) {
    const el = e.target as HTMLTextAreaElement | HTMLInputElement;
    onValueChange(el.value);
    // Typing `{{` in a fixed field is almost always intent to template —
    // flip to expression so it actually renders (and the publish gate
    // stays green). Skipped when the mode is locked by config.
    if (mode === "fixed" && !lockedMode && el.value.includes("{{")) {
      onModeChange?.("expression");
    }
    if (mode === "expression") {
      const partial = detectPartial(el.value, el.selectionStart ?? 0);
      if (partial !== null) {
        const suggs = buildSuggestions(partial, acNodeLabels, acNodeOutputs, eventPaths);
        acSuggestions = suggs;
        acSelected = 0;
        if (suggs.length > 0) positionDropdown();
      } else {
        acSuggestions = [];
      }
    } else {
      acSuggestions = [];
    }
    schedulePreview(el.value);
  }

  function handleKeyDown(e: KeyboardEvent) {
    if (acSuggestions.length === 0) return;
    if (e.key === "ArrowDown") {
      e.preventDefault();
      acSelected = (acSelected + 1) % acSuggestions.length;
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      acSelected = (acSelected - 1 + acSuggestions.length) % acSuggestions.length;
    } else if (e.key === "Tab" || e.key === "Enter") {
      e.preventDefault();
      applySuggestion(acSuggestions[acSelected]);
    } else if (e.key === "Escape") {
      acSuggestions = [];
    }
  }

  function handleBlur() {
    // Delay so mousedown on a suggestion fires before we clear the list.
    setTimeout(() => { acSuggestions = []; }, 150);
  }

  function applySuggestion(sugg: string) {
    const el = inputEl;
    if (!el) return;
    const cursor = el.selectionStart ?? value.length;
    const openAt = value.slice(0, cursor).lastIndexOf("{{");
    if (openAt < 0) return;
    const next = value.slice(0, openAt + 2) + sugg + value.slice(cursor);
    onValueChange(next);
    acSuggestions = [];
    requestAnimationFrame(() => {
      el.focus();
      const pos = openAt + 2 + sugg.length;
      el.setSelectionRange(pos, pos);
    });
  }

  // ── Preview ────────────────────────────────────────────────────────
  const SAMPLE_EVENTS = ["cron", "slack.message", "slack.block_action", "slack.view_submission"];
  let sampleEvent  = $state("cron");
  let preview      = $state<{ok:boolean;rendered?:string;error?:string;hint?:string}|null>(null);
  let previewing   = $state(false);
  // Per-expression breakdown — one row per {{…}} in the template. Lets a
  // single failing ref show as its own error/empty row while the others
  // still render, instead of one bad ref blanking the whole preview.
  type ExprRow = { raw: string; ok: boolean; rendered?: string; error?: string; isControl?: boolean };
  let exprRows = $state<ExprRow[]>([]);
  // Plain variables — not $state to avoid reactive write-in-effect loops.
  let previewTimer: ReturnType<typeof setTimeout> | null = null;
  let previewAbort: AbortController | null = null;

  function schedulePreview(val: string) {
    if (!acWorkflowId || mode !== "expression" || !val.includes("{{")) {
      preview = null;
      exprRows = [];
      previewing = false;
      return;
    }
    // Skip while the user is mid-type with an unclosed {{ — otherwise the
    // RESULT row flashes "template parse: unclosed action". Keep the last
    // good preview until the braces balance again.
    if (!bracesBalanced(val)) return;
    if (previewTimer) clearTimeout(previewTimer);
    previewTimer = setTimeout(() => runPreview(val), 600);
  }

  async function runPreview(val: string) {
    if (!acWorkflowId || !val.trim()) {
      previewing = false;
      return;
    }
    // Cancel any in-flight request before sending a new one.
    previewAbort?.abort();
    previewAbort = new AbortController();
    const signal = previewAbort.signal;
    previewing = true;
    try {
      // Use the replayed run's real event when available so the preview
      // renders {{.Event.Payload.x}} against the actual payload — not the
      // synthetic sample (which clobbers context.Event on the backend).
      const req = buildPreviewRequest(acNodeOutputs, replayedEventPayload, sampleEvent);
      // Per-expression breakdown rides along in ONE request via
      // `expressions` — the backend resolves the context once and renders
      // each. (Firing N parallel calls tripped the 200ms rate limiter, so
      // every row came back 429 → "nil/error" while the combined render
      // succeeded.) Only the VALUE segments are sent to render; control-flow
      // segments (if/else/end) get a labelled row but aren't evaluated.
      const segs = expressionSegments(val);
      const valueSegs = segs.filter((s) => !s.isControl);
      const wantBreakdown = segs.length > 1;
      const result = await workflowAPI.templateTest(acWorkflowId, {
        template: val,
        sample_event: req.sampleEvent,
        context: req.context,
        expressions: wantBreakdown ? valueSegs.map((e) => e.raw) : undefined,
      }, signal);
      if (!signal.aborted) {
        preview = result;
        if (result.env_keys) acEnvKeys = result.env_keys;
        if (result.secret_keys) acSecretKeys = result.secret_keys;
        if (wantBreakdown) {
          // Merge backend value-render results back into the full segment
          // list (in original order), tagging control-flow rows so they
          // render as a "control flow" label instead of an evaluated value.
          const byRaw = new Map((result.results ?? []).map((r) => [r.expression, r]));
          exprRows = segs.map((s) => {
            if (s.isControl) return { raw: s.raw, ok: true, isControl: true };
            const r = byRaw.get(s.raw);
            return r
              ? { raw: s.raw, ok: r.ok, rendered: r.rendered, error: r.error }
              : { raw: s.raw, ok: false, error: "not evaluated" };
          });
        } else if (!result.ok && valueSegs.length === 1) {
          exprRows = [{ raw: valueSegs[0].raw, ok: false, error: result.error }];
        } else {
          exprRows = [];
        }
      }
    } catch (e: any) {
      // 429 = rate limited — silently skip, keep previous result.
      if (!signal.aborted && e?.status !== 429) {
        preview = { ok: false, error: "request failed" };
      }
    } finally {
      // Always clear the spinner for THIS request's controller. An
      // aborted request hands off to its successor (which set its own
      // previewing=true), so only clear when we're still the active
      // controller — prevents both a stuck spinner (abort never cleared)
      // and a flicker (clearing the successor's spinner).
      if (previewAbort?.signal === signal) previewing = false;
    }
  }

  // Seed env/secret keys as soon as workflowId is known — no need to
  // wait for a preview request. Re-runs whenever workflowId changes.
  $effect(() => {
    const wid = acWorkflowId;
    if (!wid) return;
    workflowAPI.envGet(wid).then((res) => {
      // All keys (plain + secret) accessible via {{.Env.X}} — suggest them all.
      acEnvKeys    = Object.keys(res.values ?? {});
      acSecretKeys = []; // .Secret namespace no longer user-facing
    }).catch(() => {});
  });

  // Trigger preview on value/mode/workflowId change.
  // Early-exit (outside untrack) registers mode+workflowId as deps.
  // untrack() around schedulePreview prevents reactive writes inside it
  // (preview = null) from re-triggering this effect.
  $effect(() => {
    // Read deps OUTSIDE untrack so effect re-runs when they change.
    const v = value, m = mode, wid = acWorkflowId;
    // ALL state writes go inside untrack — prevents write→re-run loop.
    untrack(() => {
      if (m !== "expression" || !wid || !v.includes("{{")) {
        preview = null;
        return;
      }
      schedulePreview(v);
    });
  });

  // Re-run preview when sample event changes
  function changeSample(ev: Event) {
    sampleEvent = (ev.target as HTMLSelectElement).value;
    if (acWorkflowId && mode === "expression" && value.includes("{{")) {
      runPreview(value);
    }
  }
</script>

<div class="space-y-1">
  <!-- Label + mode toggle -->
  <div class="flex items-center justify-between gap-2">
    <span class="text-xs font-medium">{label}</span>
    {#if onModeChange}
      <div
        class="inline-flex rounded border border-slate-300 dark:border-navy-600 overflow-hidden text-[10px] uppercase tracking-wide"
        class:opacity-50={lockedMode}
        title={lockedMode ? "Mode locked by this field's config — cannot change" : undefined}
      >
        {#each (["fixed","expression"] as const) as m}
          <button
            type="button"
            class="px-2 py-0.5 transition-colors"
            class:bg-emerald-500={mode === m}
            class:text-white-100={mode === m}
            class:text-black-700={mode !== m}
            class:text-black-600={mode !== m}
            class:cursor-not-allowed={lockedMode}
            disabled={lockedMode}
            onclick={() => { if (!lockedMode) onModeChange?.(m); }}
            title={m === "fixed" ? "Literal value — {{ }} NOT rendered (verbatim output)" : "Go template — {{ ... }} evaluated at runtime"}
          >{m}</button>
        {/each}
      </div>
    {/if}
  </div>

  <!-- Input area -->
  {#if multiline}
    <textarea
      bind:this={inputEl as HTMLTextAreaElement}
      class="w-full rounded border border-slate-200 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-3 py-1.5 font-mono text-sm transition-colors"
      class:text-emerald-700={mode === "expression"}
      class:text-emerald-400={mode === "expression"}
      class:border-emerald-500={dragHover}
      class:bg-emerald-50={dragHover}
      class:bg-emerald-950={dragHover}
      {placeholder}
      {rows}
      {value}
      oninput={handleInput}
      onkeydown={handleKeyDown}
      onblur={handleBlur}
      ondragover={onDragOver}
      ondragleave={onDragLeave}
      ondrop={onDrop}
    ></textarea>
  {:else}
    <input
      bind:this={inputEl as HTMLInputElement}
      class="w-full rounded border border-slate-200 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-3 py-1.5 font-mono text-sm transition-colors"
      class:text-emerald-700={mode === "expression"}
      class:text-emerald-400={mode === "expression"}
      class:border-emerald-500={dragHover}
      class:bg-emerald-50={dragHover}
      class:bg-emerald-950={dragHover}
      {placeholder}
      {value}
      oninput={handleInput}
      onkeydown={handleKeyDown}
      onblur={handleBlur}
      ondragover={onDragOver}
      ondragleave={onDragLeave}
      ondrop={onDrop}
    />
  {/if}

  <!-- Autocomplete — inline (no absolute positioning to avoid overflow clipping) -->
  {#if acSuggestions.length > 0}
    <div class="rounded border border-white-400 dark:border-navy-500 bg-white-100 dark:bg-navy-800 text-sm font-mono overflow-hidden">
      {#each acSuggestions as sugg, i}
        <button
          type="button"
          class="w-full text-left px-3 py-1.5 transition-colors border-b border-white-300 dark:border-navy-600/60 last:border-0 flex items-center gap-2 text-black-900 dark:text-white-100 hover:bg-white-200 dark:hover:bg-navy-700"
          class:bg-white-300={i === acSelected}
          class:dark:bg-navy-600={i === acSelected}
          onmousedown={(e) => { e.preventDefault(); applySuggestion(sugg); }}
        >
          {#if sugg.trim().startsWith(".")}
            <span class="text-black-700 dark:text-black-600 text-[10px]">ctx</span>
            <span class="text-green-500">{sugg.trim()}</span>
          {:else}
            <span class="text-black-700 dark:text-black-600 text-[10px]">fn</span>
            <span class="text-amber-400">{sugg.trim()}</span>
          {/if}
        </button>
      {/each}
    </div>
  {/if}

  <!-- Inline result preview (n8n style) — visible when expression mode + has {{ -->
  {#if acWorkflowId && mode === "expression" && value.includes("{{")}
    <div class="rounded border border-white-300 dark:border-navy-600 dark:border-navy-600 bg-white-100 dark:bg-navy-800/60 text-xs overflow-hidden">
      <div class="flex items-center justify-between px-3 py-1 border-b border-white-300 dark:border-navy-600/60">
        <span class="font-semibold text-black-800 dark:text-black-400 tracking-wide uppercase text-[10px]">Result</span>
        <div class="flex items-center gap-2">
          {#if previewing}
            <svg class="animate-spin h-3 w-3 text-black-700 dark:text-black-500" fill="none" viewBox="0 0 24 24">
              <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"/>
              <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8v8z"/>
            </svg>
          {/if}
          <!-- Manual refresh — re-render the preview against the latest
               context (e.g. after running an upstream node, when the field
               text didn't change so the auto-trigger wouldn't fire). -->
          <button
            type="button"
            class="text-black-700 dark:text-black-500 hover:text-black-900 dark:hover:text-white-100 disabled:opacity-40 leading-none"
            title="Refresh preview"
            disabled={previewing}
            onclick={() => runPreview(value)}
          >↻</button>
          <select
            value={sampleEvent}
            onchange={changeSample}
            class="text-[10px] bg-white-200 dark:bg-navy-700 rounded px-1.5 py-0.5 border border-white-300 dark:border-navy-600 text-black-800 dark:text-black-400 cursor-pointer"
          >
            {#each SAMPLE_EVENTS as ev}
              <option value={ev}>{ev}</option>
            {/each}
          </select>
        </div>
      </div>
      <div class="px-3 py-2 min-h-[28px]">
        {#if exprRows.length > 0}
          <!-- Per-expression table: one row per {{…}}. A failing ref shows
               its own error/empty cell; the rest still render. -->
          <div class="flex flex-col gap-1">
            {#each exprRows as row}
              <div class="flex items-start gap-2 font-mono text-[11px]">
                <span class="shrink-0 text-black-700 dark:text-black-500 break-all max-w-[45%] truncate" title={row.raw}>{row.raw}</span>
                <span class="text-black-600 dark:text-black-600 shrink-0">→</span>
                {#if row.isControl}
                  <span class="text-black-600 dark:text-black-500 italic" title="Control-flow keyword — evaluated as part of the full render below, not standalone">control flow</span>
                {:else if row.ok}
                  <span class="text-emerald-400 whitespace-pre-wrap break-all">{row.rendered === "" || row.rendered === "<no value>" ? "(empty)" : row.rendered}</span>
                {:else}
                  <span class="text-red-400 break-all" title={row.error}>nil / error</span>
                {/if}
              </div>
            {/each}
          </div>
          <!-- Combined render below the per-expression rows when it succeeds. -->
          {#if preview?.ok}
            <div class="mt-2 pt-2 border-t border-white-300/60 dark:border-navy-600/60 text-[11px]">
              <span class="text-black-700 dark:text-black-500 mr-2">full:</span>
              <span class="text-emerald-400 font-mono whitespace-pre-wrap break-all">{preview.rendered}</span>
            </div>
          {/if}
        {:else if preview?.ok}
          <span class="text-emerald-400 font-mono whitespace-pre-wrap break-all">{preview.rendered}</span>
        {:else if preview?.error}
          <span class="text-red-400 font-mono">{preview.error}</span>
          {#if preview.hint}
            <span class="text-amber-400 ml-2 italic">{preview.hint}</span>
          {/if}
        {:else if previewing}
          <span class="text-black-700 dark:text-black-600 italic">waiting…</span>
        {:else}
          <span class="text-black-700 dark:text-black-600 italic">—</span>
        {/if}
      </div>
    </div>
  {/if}

  <!-- Fixed + {{ warning — editable now (save is fine), but this is a
       publish-blocking validation error: the template would never render.
       Red to match the publish gate; the toolbar disables Publish until
       you switch to expression or drop the {{...}}. -->
  {#if fixedWithTemplate}
    <p class="text-[11px] text-rose-600 dark:text-rose-400 flex items-center gap-1">
      <svg width="11" height="11" viewBox="0 0 16 16" fill="currentColor"><path d="M8 1a7 7 0 1 0 0 14A7 7 0 0 0 8 1zm0 12.5A5.5 5.5 0 1 1 8 2.5a5.5 5.5 0 0 1 0 11zM7.25 5.5h1.5v4h-1.5zm0 5h1.5v1.5h-1.5z"/></svg>
      mode=fixed but value has a template — it will NOT render; blocks publish. Set mode=expression.
    </p>
  {/if}

  {#if helper}
    <span class="text-[11px] text-black-700 dark:text-black-600">{helper}</span>
  {/if}
</div>
