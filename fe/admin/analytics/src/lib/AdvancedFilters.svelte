<script module lang="ts">
  import type { CondField, CondOp, Conditions } from "$lib/urlFilter";
  export type Advanced = { people: string[]; providers: string[]; models: string[]; match: Conditions };
  export const condFieldLabels: Record<CondField, string> = {
    title: "Title",
    session: "Session id",
    any: "Title or session id",
    model: "Model",
    provider: "Provider",
    person: "Person",
    channel: "Channel",
    project: "Project",
    agent: "Agent",
  };
  export const condOpLabels: Record<CondOp, string> = {
    contains: "contains",
    not_contains: "does not contain",
    exact: "is",
    not_exact: "is not",
    regex: "matches regex",
    not_regex: "does not match regex",
  };
  export const filterRule = "A conversation that switched provider or model matches when any of them is picked.";
</script>

<script lang="ts">
  /* The filters that would crowd the bar: people (forty accounts are not
     a chip row), providers, models — and, at the bottom, conditions for
     anything the chips cannot say ("title contains X AND model is not Y").
     Edits stay a draft until Apply, so ticking three people costs one
     reload, not three. */
  import { Modal } from "@wick-fe/common-ui";
  import { channelColor } from "$lib/format";
  import type { Condition } from "$lib/urlFilter";

  type Props = {
    open: boolean;
    /** Accounts by name, plus the unattributed bucket when the range has one. */
    people: Array<{ id: string; name: string }>;
    providers: string[];
    models: string[];
    /** Values offered while typing a condition, per field. */
    suggest?: Partial<Record<CondField, string[]>>;
    selected: Advanced;
    onApply: (next: Advanced) => void;
    onClose: () => void;
  };
  let { open, people, providers, models, suggest = {}, selected, onApply, onClose }: Props = $props();

  const emptyDraft = (): Advanced => ({ people: [], providers: [], models: [], match: { join: "and", rules: [] } });
  let draft = $state<Advanced>(emptyDraft());
  let search = $state("");
  // A fresh draft each time it opens: a cancelled edit must not linger.
  $effect(() => {
    if (open) {
      draft = {
        people: [...selected.people],
        providers: [...selected.providers],
        models: [...selected.models],
        match: { join: selected.match.join, rules: selected.match.rules.map((r) => ({ ...r })) },
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
  function regexError(r: Condition): string {
    if (!r.op.endsWith("regex") || !r.value.trim()) return "";
    try {
      new RegExp(r.value.trim());
      return "";
    } catch (e) {
      return e instanceof Error ? e.message : String(e);
    }
  }

  function addCondition() {
    draft.match.rules = [...draft.match.rules, { field: "title", op: "contains", value: "" }];
  }
  function removeCondition(i: number) {
    draft.match.rules = draft.match.rules.filter((_, j) => j !== i);
  }

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
    <section data-testid="conditions">
      <div class="mb-1.5 flex flex-wrap items-center gap-2">
        <h3 class="text-xs font-medium uppercase tracking-wide text-black-700 dark:text-black-600">Conditions</h3>
        {#if draft.match.rules.length > 1}
          <div class="flex overflow-hidden rounded-lg border border-white-300 text-xs dark:border-navy-600" data-testid="cond-join">
            {#each [{ v: "and", label: "Match all (AND)" }, { v: "or", label: "Match any (OR)" }] as j}
              <button
                type="button"
                onclick={() => (draft.match.join = j.v as "and" | "or")}
                class="px-2 py-1 {draft.match.join === j.v
                  ? 'bg-green-500/10 text-green-700 dark:text-green-300'
                  : 'text-black-700 dark:text-black-600'}"
              >
                {j.label}
              </button>
            {/each}
          </div>
        {/if}
      </div>
      <div class="space-y-2">
        {#each draft.match.rules as rule, i (i)}
          <div data-testid="cond-row">
            <div class="flex flex-wrap items-center gap-2">
              {#if i > 0}
                <span class="w-8 text-center text-[10px] font-semibold uppercase text-black-600 dark:text-black-700">
                  {draft.match.join}
                </span>
              {:else if draft.match.rules.length > 1}
                <span class="w-8"></span>
              {/if}
              <select
                bind:value={rule.field}
                data-testid="cond-field"
                class="rounded-lg border border-white-400 bg-white-100 px-2 py-1.5 text-xs text-black-900 dark:border-navy-600 dark:bg-navy-800 dark:text-white-100"
              >
                {#each Object.entries(condFieldLabels) as [v, label]}
                  <option value={v}>{label}</option>
                {/each}
              </select>
              <select
                bind:value={rule.op}
                data-testid="cond-op"
                class="rounded-lg border border-white-400 bg-white-100 px-2 py-1.5 text-xs text-black-900 dark:border-navy-600 dark:bg-navy-800 dark:text-white-100"
              >
                {#each Object.entries(condOpLabels) as [v, label]}
                  <option value={v}>{label}</option>
                {/each}
              </select>
              <input
                type="text"
                bind:value={rule.value}
                list={suggest[rule.field]?.length ? `cond-suggest-${rule.field}` : undefined}
                placeholder={rule.op.endsWith("regex") ? "^daily-\\d+" : "value"}
                data-testid="cond-value"
                class="min-w-0 flex-1 rounded-lg border border-white-400 bg-white-100 px-3 py-1.5 font-mono text-xs text-black-900 dark:border-navy-600 dark:bg-navy-800 dark:text-white-100"
              />
              <button
                type="button"
                aria-label="Remove condition"
                onclick={() => removeCondition(i)}
                class="rounded px-1.5 text-sm text-black-600 hover:bg-white-300 dark:text-black-700 dark:hover:bg-navy-600"
              >
                ×
              </button>
            </div>
            {#if regexError(rule)}
              <p class="mt-1 text-xs text-red-600 dark:text-red-400" data-testid="cond-regex-error">{regexError(rule)}</p>
            {/if}
          </div>
        {/each}
      </div>
      {#each Object.entries(suggest) as [field, values]}
        {#if values?.length}
          <datalist id={`cond-suggest-${field}`}>
            {#each values as v}<option value={v}></option>{/each}
          </datalist>
        {/if}
      {/each}
      <button
        type="button"
        onclick={addCondition}
        data-testid="cond-add"
        class="mt-2 rounded-lg border border-dashed border-white-400 px-2 py-1 text-xs text-black-700 dark:border-navy-600 dark:text-black-600"
      >
        + Add condition
      </button>
      <p class="mt-1 text-xs text-black-600 dark:text-black-700">
        Case-insensitive. Applies on top of the picks above and narrows every number on the page, token usage
        included. Person and project match by name or id.
      </p>
    </section>
  </div>
  {#snippet footer()}
    <div class="flex justify-end gap-2">
      <button
        type="button"
        onclick={() => (draft = emptyDraft())}
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
