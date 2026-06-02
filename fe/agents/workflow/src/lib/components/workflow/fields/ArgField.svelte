<script lang="ts">
  // Single text / textarea field with Fixed ⇄ Expression mode toggle,
  // inline result preview (n8n style), and context autocomplete.
  //
  // Mode controls engine behaviour:
  //   fixed      → value passed verbatim, {{ }} NOT rendered
  //   expression → value rendered as Go template ({{ }} evaluated)
  // Stored in node.arg_modes[<key>]. Absent key = expression (render).

  import { workflowAPI } from "$lib/api/workflow";

  type Mode = "fixed" | "expression";
  type Props = {
    label: string;
    value: string;
    mode?: Mode;
    placeholder?: string;
    rows?: number;
    multiline?: boolean;
    helper?: string;
    workflowId?: string;
    nodeLabels?: string[];
    onValueChange: (v: string) => void;
    onModeChange?: (m: Mode) => void;
  };

  let {
    label,
    value,
    mode = "fixed",
    placeholder,
    rows = 4,
    multiline = false,
    helper,
    workflowId,
    nodeLabels = [],
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
  const CTX_ROOTS   = [".Event.", ".Node.", ".Env.", ".Secret.", ".Run.", ".Workflow."];
  const EVENT_PATHS = [".Event.Type", ".Event.Subtype", ".Event.Channel", ".Event.At", ".Event.Payload."];
  const PAYLOAD_KEYS= ["text","user","channel_id","ts","thread","trigger_id","action_id",
                       "value","callback_id","schedule"].map(k => `.Event.Payload.${k}`);
  const FUNCS       = ["now","timeFormat","toJSON","fromJSON","jsonEscape","upper","lower",
                       "trim","default","truncate","index"].map(f => f + " ");

  let acSuggestions = $state<string[]>([]);
  let acSelected    = $state(0);
  let inputEl       = $state<HTMLTextAreaElement | HTMLInputElement | null>(null);

  function detectPartial(val: string, cursor: number): string | null {
    const before  = val.slice(0, cursor);
    const openAt  = before.lastIndexOf("{{");
    if (openAt < 0 || before.slice(openAt).includes("}}")) return null;
    return before.slice(openAt + 2).trimStart();
  }

  function buildSuggestions(partial: string, labels: string[]): string[] {
    if (partial === "")               return [...CTX_ROOTS, ...FUNCS];
    if (partial === ".")              return CTX_ROOTS;
    if (partial === ".Event.")        return EVENT_PATHS;
    if (partial === ".Event.Payload.") return PAYLOAD_KEYS;
    if (partial === ".Node.") {
      return [...new Set([...labels, "trigger"])].map(l => `.Node.${l}.`);
    }
    const pool = [
      ...CTX_ROOTS,
      ...EVENT_PATHS,
      ...PAYLOAD_KEYS,
      ...new Set([...labels, "trigger"]).values().map((l: string) => `.Node.${l}.`),
      ...FUNCS,
    ];
    const lo = partial.toLowerCase();
    return pool.filter(s => s.toLowerCase().startsWith(lo)).slice(0, 10);
  }

  function handleInput(e: Event) {
    const el = e.target as HTMLTextAreaElement | HTMLInputElement;
    onValueChange(el.value);
    if (mode === "expression") {
      const partial = detectPartial(el.value, el.selectionStart ?? 0);
      if (partial !== null) {
        acSuggestions = buildSuggestions(partial, nodeLabels);
        acSelected = 0;
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
  const SAMPLE_EVENTS = ["manual", "cron", "slack.message", "slack.block_action", "slack.view_submission"];
  let sampleEvent  = $state("manual");
  let preview      = $state<{ok:boolean;rendered?:string;error?:string;hint?:string}|null>(null);
  let previewing   = $state(false);
  let previewTimer = $state<ReturnType<typeof setTimeout>|null>(null);

  function schedulePreview(val: string) {
    if (!workflowId || mode !== "expression" || !val.includes("{{")) {
      preview = null;
      return;
    }
    if (previewTimer) clearTimeout(previewTimer);
    previewTimer = setTimeout(() => runPreview(val), 400);
  }

  async function runPreview(val: string) {
    if (!workflowId) return;
    previewing = true;
    try {
      preview = await workflowAPI.templateTest(workflowId, { template: val, sample_event: sampleEvent });
    } catch { preview = { ok: false, error: "request failed" }; }
    finally { previewing = false; }
  }

  $effect(() => { schedulePreview(value); });

  // Re-run preview when sample event changes
  function changeSample(ev: Event) {
    sampleEvent = (ev.target as HTMLSelectElement).value;
    if (workflowId && mode === "expression" && value.includes("{{")) {
      runPreview(value);
    }
  }
</script>

<div class="space-y-1">
  <!-- Label + mode toggle -->
  <div class="flex items-center justify-between gap-2">
    <span class="text-xs font-medium">{label}</span>
    {#if onModeChange}
      <div class="inline-flex rounded border border-slate-300 dark:border-slate-700 overflow-hidden text-[10px] uppercase tracking-wide">
        {#each (["fixed","expression"] as const) as m}
          <button
            type="button"
            class="px-2 py-0.5 transition-colors"
            class:bg-emerald-500={mode === m}
            class:text-white={mode === m}
            class:text-slate-500={mode !== m}
            class:dark:text-slate-400={mode !== m}
            onclick={() => onModeChange?.(m)}
            title={m === "fixed" ? "Literal value — {{ }} NOT rendered (verbatim output)" : "Go template — {{ ... }} evaluated at runtime"}
          >{m}</button>
        {/each}
      </div>
    {/if}
  </div>

  <!-- Input area + autocomplete dropdown -->
  <div class="relative">
    {#if multiline}
      <textarea
        bind:this={inputEl as HTMLTextAreaElement}
        class="w-full rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-3 py-1.5 font-mono text-sm transition-colors"
        class:text-emerald-700={mode === "expression"}
        class:dark:text-emerald-400={mode === "expression"}
        class:border-emerald-500={dragHover}
        class:bg-emerald-50={dragHover}
        class:dark:bg-emerald-950={dragHover}
        {placeholder}
        {rows}
        {value}
        oninput={handleInput}
        onkeydown={handleKeyDown}
        ondragover={onDragOver}
        ondragleave={onDragLeave}
        ondrop={onDrop}
      ></textarea>
    {:else}
      <input
        bind:this={inputEl as HTMLInputElement}
        class="w-full rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-3 py-1.5 font-mono text-sm transition-colors"
        class:text-emerald-700={mode === "expression"}
        class:dark:text-emerald-400={mode === "expression"}
        class:border-emerald-500={dragHover}
        class:bg-emerald-50={dragHover}
        class:dark:bg-emerald-950={dragHover}
        {placeholder}
        {value}
        oninput={handleInput}
        onkeydown={handleKeyDown}
        ondragover={onDragOver}
        ondragleave={onDragLeave}
        ondrop={onDrop}
      />
    {/if}

    <!-- Autocomplete dropdown -->
    {#if acSuggestions.length > 0}
      <div class="absolute z-50 left-0 top-full mt-0.5 w-full max-h-44 overflow-y-auto rounded border border-slate-600 dark:border-slate-600 bg-slate-900 shadow-xl text-sm font-mono">
        {#each acSuggestions as sugg, i}
          <button
            type="button"
            class="w-full text-left px-3 py-1.5 transition-colors border-b border-slate-800 last:border-0"
            class:bg-slate-700={i === acSelected}
            class:text-emerald-400={sugg.startsWith(".")}
            class:text-amber-400={!sugg.startsWith(".")}
            class:text-slate-300={!sugg.startsWith(".") && !sugg.startsWith(" ")}
            onmousedown={(e) => { e.preventDefault(); applySuggestion(sugg); }}
          >{sugg.trim()}</button>
        {/each}
      </div>
    {/if}
  </div>

  <!-- Inline result preview (n8n style) — visible when expression mode + has {{ -->
  {#if workflowId && mode === "expression" && value.includes("{{")}
    <div class="rounded border border-slate-700 dark:border-slate-700 bg-slate-900/60 text-xs overflow-hidden">
      <div class="flex items-center justify-between px-3 py-1 border-b border-slate-700/60">
        <span class="font-semibold text-slate-300 tracking-wide uppercase text-[10px]">Result</span>
        <div class="flex items-center gap-2">
          {#if previewing}
            <svg class="animate-spin h-3 w-3 text-slate-400" fill="none" viewBox="0 0 24 24">
              <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"/>
              <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8v8z"/>
            </svg>
          {/if}
          <select
            value={sampleEvent}
            onchange={changeSample}
            class="text-[10px] bg-slate-800 rounded px-1.5 py-0.5 border border-slate-700 text-slate-300 cursor-pointer"
          >
            {#each SAMPLE_EVENTS as ev}
              <option value={ev}>{ev}</option>
            {/each}
          </select>
        </div>
      </div>
      <div class="px-3 py-2 min-h-[28px]">
        {#if preview?.ok}
          <span class="text-emerald-400 font-mono whitespace-pre-wrap break-all">{preview.rendered}</span>
        {:else if preview?.error}
          <span class="text-red-400 font-mono">{preview.error}</span>
          {#if preview.hint}
            <span class="text-amber-400 ml-2 italic">{preview.hint}</span>
          {/if}
        {:else}
          <span class="text-slate-500 italic">waiting…</span>
        {/if}
      </div>
    </div>
  {/if}

  <!-- Fixed + {{ warning -->
  {#if fixedWithTemplate}
    <p class="text-[11px] text-amber-600 dark:text-amber-400 flex items-center gap-1">
      <svg width="11" height="11" viewBox="0 0 16 16" fill="currentColor"><path d="M8 1a7 7 0 1 0 0 14A7 7 0 0 0 8 1zm0 12.5A5.5 5.5 0 1 1 8 2.5a5.5 5.5 0 0 1 0 11zM7.25 5.5h1.5v4h-1.5zm0 5h1.5v1.5h-1.5z"/></svg>
      mode=fixed — template will NOT render, set mode=expression
    </p>
  {/if}

  {#if helper}
    <span class="text-[11px] text-slate-500 dark:text-slate-400">{helper}</span>
  {/if}
</div>
