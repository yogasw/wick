<script module lang="ts">
  export type Advanced = { people: string[]; providers: string[]; models: string[] };
  export const filterRule = "A conversation that switched provider or model matches when any of them is picked.";
</script>

<script lang="ts">
  /* The filters that would crowd the bar: people (forty accounts are not
     a chip row), providers and models. Edits stay a draft until Apply, so
     ticking three people costs one reload, not three. */
  import { Modal } from "@wick-fe/common-ui";
  import { channelColor } from "$lib/format";

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

  let draft = $state<Advanced>({ people: [], providers: [], models: [] });
  let search = $state("");
  // A fresh draft each time it opens: a cancelled edit must not linger.
  $effect(() => {
    if (open) {
      draft = { people: [...selected.people], providers: [...selected.providers], models: [...selected.models] };
      search = "";
    }
  });

  const shown = $derived.by(() => {
    const q = search.trim().toLowerCase();
    return q ? people.filter((p) => p.name.toLowerCase().includes(q) || p.id.toLowerCase().includes(q)) : people;
  });

  function toggle(kind: keyof Advanced, v: string) {
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
        onclick={() => (draft = { people: [], providers: [], models: [] })}
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
