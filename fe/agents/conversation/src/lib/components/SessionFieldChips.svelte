<script lang="ts">
  /* The values a plugin remote agent's chat runs with (repository, branch,
     …), as chips like Jules' composer. In a new chat each chip is editable
     inline and × resets it to the default; once the remote session exists
     they are locked and only show what the session uses. */
  import type { SessionField } from "../api/team.js";

  type Props = {
    fields: SessionField[];
    values: Record<string, string>;
    locked?: boolean;
    onChange?: (values: Record<string, string>) => void;
  };
  let { fields, values, locked = false, onChange }: Props = $props();

  let editing = $state<string | null>(null);
  let draft = $state("");

  const lockedTitle = "Set when the chat started. Start a new chat to change it.";

  function shown(f: SessionField): string {
    return values[f.key] || f.default || "";
  }

  function edit(f: SessionField) {
    if (locked) return;
    editing = f.key;
    draft = values[f.key] ?? "";
  }

  function commit() {
    if (editing === null) return;
    const key = editing;
    editing = null;
    const next = { ...values };
    const v = draft.trim();
    if (v) next[key] = v;
    else delete next[key];
    onChange?.(next);
  }

  function clear(key: string) {
    const next = { ...values };
    delete next[key];
    onChange?.(next);
  }

  function onKey(e: KeyboardEvent) {
    if (e.key === "Enter") {
      e.preventDefault();
      commit();
    } else if (e.key === "Escape") {
      e.preventDefault();
      editing = null;
    }
  }

  function focus(node: HTMLInputElement) {
    node.focus();
    node.select();
  }
</script>

{#if fields.length > 0}
  <div class="flex flex-wrap items-center gap-1.5" data-session-fields data-locked={locked ? "true" : "false"}>
    {#each fields as f (f.key)}
      {#if editing === f.key}
        <input
          use:focus
          type="text"
          class="w-40 rounded-full border border-white-400 bg-white-100 px-2.5 py-0.5 text-xs text-black-900 outline-none dark:text-white-100"
          aria-label={f.label}
          placeholder={f.default || f.placeholder || f.label}
          bind:value={draft}
          onkeydown={onKey}
          onblur={commit}
          data-session-field-input={f.key}
        />
      {:else}
        <span
          class="inline-flex max-w-full items-center gap-1 rounded-full border border-white-300 bg-white-100 text-xs text-black-900 dark:text-white-100"
          data-session-field={f.key}
        >
          <button
            type="button"
            class="min-w-0 truncate rounded-full px-2.5 py-0.5 {locked ? 'cursor-default' : 'hover:bg-white-200'}"
            title={locked ? `${f.label}: ${shown(f) || "default"}. ${lockedTitle}` : `${f.label} — click to change`}
            aria-label={f.label}
            disabled={locked}
            onclick={() => edit(f)}
          >
            {#if locked}<span aria-hidden="true">🔒 </span>{/if}{#if shown(f)}{shown(f)}{:else}<span class="text-black-700">{f.placeholder || f.label}</span>{/if}
          </button>
          {#if !locked && values[f.key]}
            <button
              type="button"
              class="-ml-1 rounded-full pr-2 text-black-700 hover:text-black-900 dark:hover:text-white-100"
              title={`Reset ${f.label} to the default`}
              aria-label={`Reset ${f.label}`}
              onclick={() => clear(f.key)}>×</button
            >
          {/if}
        </span>
      {/if}
    {/each}
  </div>
{/if}
