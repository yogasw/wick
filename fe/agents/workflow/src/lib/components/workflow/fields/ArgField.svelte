<script lang="ts">
  // Single text / textarea field with a Fixed ⇄ Expression mode toggle,
  // mirroring the legacy editor's `data-arg-mode` pill in
  // internal/tools/agents/view/workflow/editor_inspector.templ.
  //
  // The mode controls whether the engine renders the field as a Go template:
  //   fixed      → value passed verbatim (engine skips template rendering)
  //   expression → value rendered via Go template ({{ ... }} evaluated)
  // Stored in `node.arg_modes[<key>]`. Absent key = expression (render).
  //
  // The parent owns where the value + mode live (node field vs
  // arg_modes map vs nested config), so this component takes pure
  // callbacks instead of binding to a node directly.

  type Mode = "fixed" | "expression";
  type Props = {
    label: string;
    value: string;
    mode?: Mode;
    placeholder?: string;
    rows?: number;
    multiline?: boolean;
    helper?: string;
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
    onValueChange,
    onModeChange,
  }: Props = $props();

  let dragHover = $state(false);

  // Warn when fixed mode is set but the value contains {{ — the template
  // will NOT render and the literal {{ text will appear in the output.
  let fixedWithTemplate = $derived(mode === "fixed" && value.includes("{{"));

  // Drop a draggable JSON leaf from the INPUT pane: inserts the
  // template ref at the cursor and auto-flips this field into
  // Expression mode. Same UX as the legacy attachTemplateDropTarget.
  function onDragOver(e: DragEvent) {
    if (!e.dataTransfer?.types.includes("text/plain")) return;
    e.preventDefault();
    e.dataTransfer.dropEffect = "copy";
    dragHover = true;
  }
  function onDragLeave() {
    dragHover = false;
  }
  function onDrop(e: DragEvent) {
    e.preventDefault();
    dragHover = false;
    const text = e.dataTransfer?.getData("text/plain");
    if (!text) return;
    const el = e.currentTarget as HTMLInputElement | HTMLTextAreaElement;
    const start = el.selectionStart ?? value.length;
    const end = el.selectionEnd ?? value.length;
    const next = value.slice(0, start) + text + value.slice(end);
    onValueChange(next);
    onModeChange?.("expression");
    // Restore caret after Svelte updates the value.
    requestAnimationFrame(() => {
      try {
        const caret = start + text.length;
        el.setSelectionRange(caret, caret);
        el.focus();
      } catch {
        /* element gone — ignore */
      }
    });
  }
</script>

<div class="space-y-1">
  <div class="flex items-center justify-between gap-2">
    <span class="text-xs font-medium">{label}</span>
    {#if onModeChange}
      <div class="inline-flex rounded border border-slate-300 dark:border-slate-700 overflow-hidden text-[10px] uppercase tracking-wide">
        {#each ["fixed", "expression"] as m}
          <button
            type="button"
            class="px-2 py-0.5 transition-colors"
            class:bg-emerald-500={mode === m}
            class:text-white={mode === m}
            class:text-slate-500={mode !== m}
            class:dark:text-slate-400={mode !== m}
            onclick={() => onModeChange?.(m as Mode)}
            title={m === "fixed" ? "Literal value — {{ }} NOT rendered (verbatim output)" : "Go template — {{ ... }} evaluated at runtime"}
          >{m}</button>
        {/each}
      </div>
    {/if}
  </div>
  {#if multiline}
    <textarea
      class="w-full rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-3 py-1.5 font-mono text-sm transition-colors"
      class:text-emerald-700={mode === "expression"}
      class:dark:text-emerald-400={mode === "expression"}
      class:border-emerald-500={dragHover}
      class:bg-emerald-50={dragHover}
      class:dark:bg-emerald-950={dragHover}
      {placeholder}
      {rows}
      {value}
      oninput={(e) => onValueChange((e.target as HTMLTextAreaElement).value)}
      ondragover={onDragOver}
      ondragleave={onDragLeave}
      ondrop={onDrop}
    ></textarea>
  {:else}
    <input
      class="w-full rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-3 py-1.5 font-mono text-sm transition-colors"
      class:text-emerald-700={mode === "expression"}
      class:dark:text-emerald-400={mode === "expression"}
      class:border-emerald-500={dragHover}
      class:bg-emerald-50={dragHover}
      class:dark:bg-emerald-950={dragHover}
      {placeholder}
      {value}
      oninput={(e) => onValueChange((e.target as HTMLInputElement).value)}
      ondragover={onDragOver}
      ondragleave={onDragLeave}
      ondrop={onDrop}
    />
  {/if}
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
