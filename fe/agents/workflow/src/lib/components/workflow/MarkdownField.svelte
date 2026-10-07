<script lang="ts">
  // Markdown text field: renders the value as markdown, double-click (or
  // the pencil) swaps in a textarea, blur / Esc swaps back. Used for node
  // + trigger descriptions and sticky_note content. HTML is escaped by
  // renderMarkdown, so {@html} only ever sees generated markup.
  import { tick } from "svelte";
  import { renderMarkdown } from "$lib/markdown";

  let {
    value = "",
    label = "Description",
    placeholder = "What is this for? **Kenapa:** why it exists. Markdown supported.",
    required = false,
    disabled = false,
    oncommit,
  }: {
    value?: string;
    label?: string;
    placeholder?: string;
    required?: boolean;
    disabled?: boolean;
    oncommit?: (v: string) => void;
  } = $props();

  let editing = $state(false);
  let draft = $state("");
  let el: HTMLTextAreaElement | undefined = $state();
  const html = $derived(renderMarkdown(value ?? ""));
  const empty = $derived(!(value ?? "").trim());

  async function start() {
    if (disabled) return;
    draft = value ?? "";
    editing = true;
    await tick();
    el?.focus();
  }

  function commit() {
    if (!editing) return;
    editing = false;
    if (draft !== (value ?? "")) oncommit?.(draft);
  }
</script>

<div class="flex flex-col gap-1">
  <div class="flex items-center gap-2">
    <span class="text-xs font-medium">{label}</span>
    {#if required && empty}
      <span class="rounded px-1 text-[10px] font-semibold bg-amber-400 text-black-800">required</span>
    {/if}
    <div class="flex-1"></div>
    {#if !editing && !disabled}
      <button
        type="button"
        class="h-6 w-6 rounded flex items-center justify-center text-black-700 dark:text-black-500 hover:bg-white-200 dark:hover:bg-navy-600 hover:text-black-800 dark:hover:text-white-100"
        title="Edit {label.toLowerCase()} (or double-click)"
        aria-label="Edit {label.toLowerCase()}"
        onclick={start}
      >
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><path d="M12 20h9"/><path d="M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4Z"/></svg>
      </button>
    {/if}
  </div>
  {#if editing}
    <textarea
      bind:this={el}
      bind:value={draft}
      class="rounded border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-3 py-1.5 text-sm font-mono min-h-[96px]"
      rows="4"
      {placeholder}
      onblur={commit}
      onkeydown={(e) => {
        e.stopPropagation();
        if (e.key === "Escape" || ((e.ctrlKey || e.metaKey) && e.key === "Enter")) {
          e.preventDefault();
          el?.blur();
        }
      }}
    ></textarea>
  {:else}
    <div
      class="wf-md rounded border px-3 py-1.5 text-sm min-h-[2.25rem] border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-700 text-black-800 dark:text-white-100"
      class:cursor-text={!disabled}
      class:border-amber-400={required && empty}
      class:dark:border-amber-400={required && empty}
      role="button"
      tabindex={disabled ? -1 : 0}
      title={disabled ? "" : "Double-click to edit"}
      ondblclick={start}
      onkeydown={(e) => e.key === "Enter" && !disabled && (e.preventDefault(), start())}
    >
      {#if empty}
        <p class="italic text-black-700 dark:text-black-500 text-xs">{placeholder}</p>
      {:else}
        {@html html}
      {/if}
    </div>
  {/if}
</div>

<style>
  .wf-md :global(*) { color: inherit; }
  .wf-md :global(p), .wf-md :global(li) { font-size: 0.8125rem; line-height: 1.45; margin: 0.15rem 0; }
  .wf-md :global(h1), .wf-md :global(h2) { font-size: 0.95rem; font-weight: 700; margin: 0.15rem 0; }
  .wf-md :global(h3), .wf-md :global(h4) { font-size: 0.875rem; font-weight: 600; margin: 0.15rem 0; }
  .wf-md :global(ul) { list-style: disc; padding-left: 1.1rem; }
  .wf-md :global(ol) { list-style: decimal; padding-left: 1.1rem; }
  .wf-md :global(a) { text-decoration: underline; }
</style>
