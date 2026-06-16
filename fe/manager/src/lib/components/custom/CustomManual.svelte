<script lang="ts">
  /* Manual builder: a 3-step stepper (Meta -> Configs -> Operations) over the
     shared DraftEditor, starting from a blank draft. The first two steps
     show Back/Next; the last step swaps in the Save toolbar. Saving posts
     the same Draft to the same create endpoint as the paste flow. Mirrors
     custom_manual.templ + custom_manual.js (stepper) + the save half of
     custom_review.js. */
  import { Button } from "@wick-fe/common-ui";
  import { toastOk, toastError } from "@wick-fe/common-stores";
  import { push } from "$lib/router.js";
  import { getCustomMeta, saveCustomDraft } from "$lib/api.js";
  import { normalize, serialize } from "./draft.js";
  import { DRAFT_STORAGE_KEY } from "./storage.js";
  import DraftEditor from "./DraftEditor.svelte";
  import type { Draft } from "$lib/types.js";

  const STEPS = [
    { key: "meta", label: "Meta" },
    { key: "configs", label: "Configs" },
    { key: "ops", label: "Operations" },
  ] as const;
  type StepKey = (typeof STEPS)[number]["key"];

  let draft = $state<Draft>(normalize({ source: "manual" }));
  let categories = $state<string[]>([]);
  let loading = $state(true);
  let error = $state("");
  let saving = $state(false);
  let step = $state(0);

  let isLast = $derived(step === STEPS.length - 1);
  let visibleSteps = $derived<StepKey[]>([STEPS[step].key]);

  async function loadMeta() {
    try {
      categories = (await getCustomMeta()).categories;
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      loading = false;
    }
  }

  function onChange() {}

  function prev() {
    if (step > 0) step -= 1;
  }

  function next() {
    if (step < STEPS.length - 1) step += 1;
  }

  function pillClass(active: boolean): string {
    return active
      ? "flex items-center gap-2 rounded-lg bg-green-200 px-3 py-2 text-sm font-medium text-green-700"
      : "flex items-center gap-2 rounded-lg px-3 py-2 text-sm font-medium text-black-800 dark:text-black-600";
  }

  function numClass(active: boolean): string {
    return active
      ? "flex h-5 w-5 items-center justify-center rounded-full bg-green-500 text-[11px] font-semibold text-white-100"
      : "flex h-5 w-5 items-center justify-center rounded-full bg-white-300 dark:bg-navy-600 text-[11px] font-semibold text-black-800 dark:text-black-600";
  }

  async function save() {
    if (saving) return;
    saving = true;
    error = "";
    try {
      const res = await saveCustomDraft(serialize(draft));
      sessionStorage.removeItem(DRAFT_STORAGE_KEY);
      toastOk("Connector saved");
      if (res.redirect) {
        window.location.href = res.redirect;
      }
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
      toastError("Save failed", error);
    } finally {
      saving = false;
    }
  }

  $effect(() => { loadMeta(); });
</script>

{#if loading}
  <div class="px-5 py-12 text-center text-sm text-black-700 dark:text-black-600">Loading…</div>
{:else}
  <div class="space-y-6">
    <div>
      <h1 class="text-lg font-semibold text-black-900 dark:text-white-100">Build a connector by hand</h1>
      <p class="mt-1 text-sm text-black-800 dark:text-black-600">Define Meta, Configs, and Operations step by step. The right panel previews the stored row as you type.</p>
    </div>

    <div class="flex flex-wrap items-center gap-3">
      {#each STEPS as s, i (s.key)}
        {#if i > 0}
          <span class="text-black-700 dark:text-black-600">›</span>
        {/if}
        <div class={pillClass(i === step)}>
          <span class={numClass(i === step)}>{i + 1}</span>
          {s.label}
        </div>
      {/each}
    </div>

    {#if error}
      <div class="rounded-lg border border-neg-400 bg-neg-100 px-4 py-3 text-sm font-medium text-neg-400">✗ {error}</div>
    {/if}

    <DraftEditor {draft} {categories} editMode={false} {onChange} {visibleSteps} />

    {#if isLast}
      <div class="flex items-center justify-between">
        <Button variant="secondary" size="lg" onclick={prev}>← Back</Button>
        <Button variant="primary" size="lg" disabled={saving} onclick={save}>{saving ? "Saving…" : "Save connector →"}</Button>
      </div>
    {:else}
      <div class="flex items-center justify-between">
        <Button variant="secondary" size="lg" disabled={step === 0} onclick={prev}>← Back</Button>
        <Button variant="primary" size="lg" onclick={next}>{step === STEPS.length - 2 ? "Step 3 — Operations →" : "Next →"}</Button>
      </div>
    {/if}
  </div>
{/if}
