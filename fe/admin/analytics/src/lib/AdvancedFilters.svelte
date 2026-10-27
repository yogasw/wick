<script module lang="ts">
  import type { TextFilter } from "$lib/urlFilter";
  export type Advanced = { people: string[]; providers: string[]; models: string[]; match: TextFilter };
  export const filterRule = "A conversation that switched provider or model matches when any of them is picked.";
</script>

<script lang="ts">
  /* The filters that would crowd the bar: a title / session id pattern,
     people (forty accounts are not a chip row), providers and models.
     Edits stay a draft until Apply, so ticking three people costs one
     reload, not three. */
  import { Modal } from "@wick-fe/common-ui";
  import { channelColor } from "$lib/format";
  import { emptyText } from "$lib/urlFilter";

  type Props = {
    open: boolean;
    /** Accounts by name, plus the unattributed bucket when the range has one. */
    people: Array<{ id: string; name: string }>;
    providers: string[];
    models: string[];
    selected: Advanced;
    onApply: (next: Advanced) => void;
    onClose: () => void;
  };
  let { open, people, providers, models, selected, onApply, onClose }: Props = $props();

  let draft = $state<Advanced>({ people: [], providers: [], models: [], match: { ...emptyText } });
  let search = $state("");
  // A fresh draft each time it opens: a cancelled edit must not linger.
  $effect(() => {
    if (open) {
      draft = {
        people: [...selected.people],
        providers: [...selected.providers],
        models: [...selected.models],
        match: { ...selected.match },
      };
      search = "";
    }
  });

  const shown = $derived.by(() => {
    const q = search.trim().toLowerCase();
    return q ? people.filter((p) => p.name.toLowerCase().includes(q) || p.id.toLowerCase().includes(q)) : people;
  });

  /* A hint only: the browser's regex dialect is close to the server's but
     not the same, so the server's own answer (match_error) is what the
     page shows after Apply. */
  const regexError = $derived.by(() => {
    if (draft.match.mode !== "regex" || !draft.match.text.trim()) return "";
    try {
      new RegExp(draft.match.text.trim());
      return "";
    } catch (e) {
      return e instanceof Error ? e.message : String(e);
    }
  });

  function toggle(kind: "people" | "providers" | "models", v: string) {
    const list = draft[kind];
    draft[kind] = list.includes(v) ? list.filter((x) => x !== v) : [...list, v];
  }
</script>

{#snippet chips(kind: "providers" | "models", keys: string[])}
  <div class="flex flex-wrap gap-1.5">
    {#each keys as key}
      <button
        type="button"
        onclick={() => toggle(kind, key)}
        title={filterRule}
        class="flex items-center gap-1.5 rounded-lg border px-2 py-1 text-xs {draft[kind].includes(key)
          ? 'border-green-400 bg-green-500/10'
          : 'border-white-300 dark:border-navy-600'}"
      >
        <span class="h-2 w-2 rounded-full" style={`background:${key === "(none)" ? "#9ca3af" : channelColor(key)}`}></span>
        <span class="font-medium text-black-900 dark:text-white-100">{key}</span>
      </button>
    {:else}
      <span class="text-xs text-black-600 dark:text-black-700">None in this range.</span>
    {/each}
  </div>
{/snippet}

<Modal {open} {onClose} title="Advanced filters" size="lg">
  <div class="space-y-5 text-sm">
    <section>
      <h3 class="mb-1.5 text-xs font-medium uppercase tracking-wide text-black-700 dark:text-black-600">
        Title or session id
      </h3>
      <div class="flex flex-wrap gap-2">
        <select
          bind:value={draft.match.in}
          data-testid="match-in"
          class="rounded-lg border border-white-400 bg-white-100 px-2 py-1.5 text-xs text-black-900 dark:border-navy-600 dark:bg-navy-800 dark:text-white-100"
        >
          <option value="title">Title</option>
          <option value="session">Session id</option>
          <option value="any">Title or session id</option>
        </select>
        <select
          bind:value={draft.match.mode}
          data-testid="match-mode"
          class="rounded-lg border border-white-400 bg-white-100 px-2 py-1.5 text-xs text-black-900 dark:border-navy-600 dark:bg-navy-800 dark:text-white-100"
        >
          <option value="contains">Contains</option>
          <option value="exact">Match exactly</option>
          <option value="regex">Regex</option>
        </select>
        <input
          type="text"
          bind:value={draft.match.text}
          placeholder={draft.match.mode === "regex" ? "^daily-\\d+" : "e.g. handover"}
          data-testid="match-text"
          class="min-w-0 flex-1 rounded-lg border border-white-400 bg-white-100 px-3 py-1.5 font-mono text-xs text-black-900 dark:border-navy-600 dark:bg-navy-800 dark:text-white-100"
        />
      </div>
      {#if regexError}
        <p class="mt-1 text-xs text-red-600 dark:text-red-400" data-testid="match-regex-error">{regexError}</p>
      {:else}
        <p class="mt-1 text-xs text-black-600 dark:text-black-700">
          Case-insensitive. Narrows every number on the page, token usage included — follow one use case across the
          range.
        </p>
      {/if}
    </section>
    <section>
      <div class="mb-1.5 flex items-center gap-2">
        <h3 class="text-xs font-medium uppercase tracking-wide text-black-700 dark:text-black-600">People</h3>
        {#if draft.people.length}
          <span class="text-xs text-green-700 dark:text-green-300">{draft.people.length} picked</span>
        {/if}
      </div>
      <input
        type="text"
        bind:value={search}
        placeholder="Search name or email"
        data-testid="people-search"
        class="mb-2 w-full rounded-lg border border-white-400 bg-white-100 px-3 py-1.5 text-xs text-black-900 dark:border-navy-600 dark:bg-navy-800 dark:text-white-100"
      />
      <div class="max-h-48 overflow-y-auto rounded-lg border border-white-300 dark:border-navy-600">
        {#each shown as p (p.id)}
          <label class="flex cursor-pointer items-center gap-2 px-3 py-1.5 text-xs hover:bg-white-200 dark:hover:bg-navy-600">
            <input type="checkbox" checked={draft.people.includes(p.id)} onchange={() => toggle("people", p.id)} />
            <span class="text-black-900 dark:text-white-100">{p.name}</span>
          </label>
        {:else}
          <p class="px-3 py-2 text-xs text-black-600 dark:text-black-700">Nobody matches.</p>
        {/each}
      </div>
    </section>
    <section>
      <h3 class="mb-1.5 text-xs font-medium uppercase tracking-wide text-black-700 dark:text-black-600" title={filterRule}>
        Providers <span class="normal-case">ⓘ</span>
      </h3>
      {@render chips("providers", providers)}
    </section>
    <section>
      <h3 class="mb-1.5 text-xs font-medium uppercase tracking-wide text-black-700 dark:text-black-600" title={filterRule}>
        Models <span class="normal-case">ⓘ</span>
      </h3>
      {@render chips("models", models)}
    </section>
    <p class="text-xs text-black-600 dark:text-black-700">{filterRule}</p>
  </div>
  {#snippet footer()}
    <div class="flex justify-end gap-2">
      <button
        type="button"
        onclick={() => (draft = { people: [], providers: [], models: [], match: { ...emptyText } })}
        class="rounded-lg border border-white-300 px-3 py-1.5 text-xs text-black-700 dark:border-navy-600 dark:text-black-600"
      >
        Reset
      </button>
      <button
        type="button"
        onclick={() => onApply(draft)}
        class="rounded-lg bg-green-500 px-3 py-1.5 text-xs font-medium text-white-100"
      >
        Apply
      </button>
    </div>
  {/snippet}
</Modal>
