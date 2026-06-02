<script lang="ts">
  // Single text / textarea field with a Fixed ⇄ Expression mode toggle,
  // mirroring the legacy editor's `data-arg-mode` pill in
  // internal/tools/agents/view/workflow/editor_inspector.templ.
  //
  // The mode itself is purely an editor hint stored on
  // `node.arg_modes[<key>]`. The renderer treats every field as a Go
  // template anyway — the pill signals intent: in Fixed mode the value
  // is a literal string that happens to render through template
  // (template-friendly text); in Expression mode the value is a
  // template expression like `{{ .Event.Payload.user }}` and the
  // editor styles it accordingly.
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
            title={m === "fixed" ? "Literal value (still rendered as a Go template)" : "Go template expression — {{ ... }}"}
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
  {#if helper}
    <span class="text-[11px] text-slate-500 dark:text-slate-400">{helper}</span>
  {/if}
</div>
