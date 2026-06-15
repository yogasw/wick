<script lang="ts">
  /* The shared draft form behind the review page, the edit page, and the
     manual builder. Renders Meta, Access & behavior, Configs (CRUD), and
     Operations (CRUD) as bindable sections plus a live JSON preview.
     Parent owns the draft object and the save/delete actions; this
     component only mutates the draft in place and pings onChange so the
     preview + toolbar stay in sync. Mirrors customDraftForm + the form
     half of custom_review.js. visibleSteps lets the manual stepper show
     one section at a time; default (undefined) shows everything. */
  import { TextInput, Select } from "@wick-fe/common-ui";
  import FieldRow from "./FieldRow.svelte";
  import OpCard from "./OpCard.svelte";
  import { newField, newOp, serialize } from "./draft.js";
  import type { Draft } from "$lib/types.js";

  type Step = "meta" | "configs" | "ops";
  type Props = {
    draft: Draft;
    categories: string[];
    editMode: boolean;
    onChange: () => void;
    visibleSteps?: Step[];
  };
  let { draft, categories, editMode, onChange, visibleSteps }: Props = $props();

  function shows(step: Step): boolean {
    return !visibleSteps || visibleSteps.includes(step);
  }

  /* Access & behavior rides with the configs step in the manual stepper. */
  let showAccess = $derived(!visibleSteps || visibleSteps.includes("configs"));

  function set<K extends keyof Draft>(k: K, v: Draft[K]) {
    draft[k] = v;
    onChange();
  }

  function addConfig() {
    draft.configs = [...draft.configs, newField()];
    onChange();
  }

  function removeConfig(i: number) {
    draft.configs = draft.configs.filter((_, idx) => idx !== i);
    onChange();
  }

  function addOp() {
    draft.ops = [...draft.ops, newOp()];
    onChange();
  }

  function removeOp(i: number) {
    draft.ops = draft.ops.filter((_, idx) => idx !== i);
    onChange();
  }

  let categoryOptions = $derived([{ label: "Other (no category)", value: "" }, ...categories.map((c) => ({ label: c, value: c }))]);

  let healthOptions = $derived([
    { label: "— No health check —", value: "" },
    ...draft.ops.filter((o) => o.key).map((o) => ({ label: `${o.name || o.key} (${o.key})`, value: o.key })),
  ]);

  let tagName = $derived(`custom:${draft.key || "…"}`);
  let previewJson = $derived(JSON.stringify(serialize(draft), null, 2));

  function setHealthOp(v: string) {
    draft.health_op = v;
    onChange();
  }
</script>

<div class="grid grid-cols-12 gap-6">
  <div class="col-span-12 space-y-6 lg:col-span-7">
    {#if shows("meta")}
      <section class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-6">
        <div class="grid grid-cols-12 gap-4">
          <div class="col-span-12 sm:col-span-3">
            <span class="block text-xs font-medium text-black-800 dark:text-black-600">Icon</span>
            <TextInput value={draft.icon} onChange={(v) => set("icon", v)} placeholder="🔌" ariaLabel="Icon" class="mt-1" />
          </div>
          <div class="col-span-12 sm:col-span-9">
            <span class="block text-xs font-medium text-black-800 dark:text-black-600">Key (slug, unique)</span>
            <TextInput
              value={draft.key}
              onChange={(v) => set("key", v)}
              disabled={editMode}
              placeholder="my_api"
              ariaLabel="Key"
              class="mt-1 font-mono"
            />
            <p class="mt-1 text-[11px] text-black-700 dark:text-black-600">
              {editMode ? "Immutable after create." : "Used in the MCP tool id. Lowercase slug."}
            </p>
          </div>
          <div class="col-span-12">
            <span class="block text-xs font-medium text-black-800 dark:text-black-600">Display name</span>
            <TextInput value={draft.name} onChange={(v) => set("name", v)} placeholder="My API" ariaLabel="Display name" class="mt-1" />
          </div>
          <div class="col-span-12">
            <span class="block text-xs font-medium text-black-800 dark:text-black-600">Description</span>
            <TextInput value={draft.description} onChange={(v) => set("description", v)} placeholder="What this connector does, shown to the LLM and on cards." ariaLabel="Description" class="mt-1" />
          </div>
        </div>
      </section>
    {/if}

    {#if showAccess}
      <section class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-6">
        <h2 class="text-base font-semibold text-black-900 dark:text-white-100">Access &amp; behavior</h2>
        <p class="mt-1 text-sm text-black-800 dark:text-black-600">
          Wick auto-creates the filter tag
          <span class="mx-1 inline-flex items-center rounded-full border border-green-500 bg-green-200 px-2.5 py-0.5 font-mono text-[11px] font-medium text-green-700">{tagName}</span>
          on save.
        </p>
        <div class="mt-4 max-w-xs">
          <span class="block text-xs font-medium text-black-800 dark:text-black-600">Category (visual group on the connector index)</span>
          <Select value={draft.category} options={categoryOptions} onChange={(v) => set("category", v)} class="mt-1" />
        </div>
        <div class="mt-4 divide-y divide-white-300 rounded-xl border border-white-300 dark:divide-navy-600 dark:border-navy-600">
          <label class="flex cursor-pointer items-center justify-between gap-4 px-4 py-3 hover:bg-white-200 dark:hover:bg-navy-800">
            <div class="min-w-0 flex-1">
              <p class="text-sm font-medium text-black-900 dark:text-white-100">Single instance only</p>
              <p class="mt-0.5 text-xs text-black-700 dark:text-black-600">Default off — admins can add and duplicate instance rows, each with its own credentials.</p>
            </div>
            <input type="checkbox" class="accent-green-500" checked={draft.single} onchange={(e) => set("single", (e.target as HTMLInputElement).checked)} aria-label="Single instance only" />
          </label>
          <label class="flex cursor-pointer items-center justify-between gap-4 px-4 py-3 hover:bg-white-200 dark:hover:bg-navy-800">
            <div class="min-w-0 flex-1">
              <p class="text-sm font-medium text-black-900 dark:text-white-100">Allow per-session config override</p>
              <p class="mt-0.5 text-xs text-black-700 dark:text-black-600">Default off. When on, this connector can be cloned into a per-session instance from the session Config tab.</p>
            </div>
            <input type="checkbox" class="accent-green-500" checked={draft.allow_session_config} onchange={(e) => set("allow_session_config", (e.target as HTMLInputElement).checked)} aria-label="Allow per-session config override" />
          </label>
        </div>
        <div class="mt-4 rounded-xl border border-white-300 dark:border-navy-600 p-4">
          <p class="text-sm font-medium text-black-900 dark:text-white-100">Health check</p>
          <p class="mt-0.5 text-xs text-black-700 dark:text-black-600">Optional. Pick a read-only operation to run as a probe.</p>
          <div class="mt-3 grid grid-cols-1 gap-3 sm:grid-cols-2">
            <div class="min-w-0">
              <span class="mb-1 block text-xs font-medium text-black-800 dark:text-black-600">Probe operation</span>
              <Select value={draft.health_op} options={healthOptions} onChange={setHealthOp} />
            </div>
            <div class="min-w-0">
              <span class="mb-1 block text-xs font-medium text-black-800 dark:text-black-600">Expected text in response (optional)</span>
              <TextInput value={draft.health_expect} onChange={(v) => set("health_expect", v)} placeholder={'e.g. "ok":true'} ariaLabel="Expected text" class="font-mono" />
            </div>
          </div>
        </div>
        <div class="mt-4 rounded-lg border border-cau-400 bg-cau-100 px-4 py-3">
          <p class="text-xs text-black-800"><span class="font-semibold text-cau-400">⚠ Default = admin-only.</span> No user carries the auto-created tag at save, so non-admins cannot see or call this connector until you assign the tag.</p>
        </div>
      </section>
    {/if}

    {#if shows("configs")}
      <section class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-6">
        <div class="flex items-center justify-between gap-3">
          <h2 class="text-base font-semibold text-black-900 dark:text-white-100">Configs</h2>
          <button type="button" class="inline-flex items-center gap-1 rounded-lg border border-white-400 dark:border-navy-600 px-3 py-1.5 text-xs font-medium text-black-800 dark:text-black-600 hover:border-green-400 hover:text-green-600" onclick={addConfig}>+ Add field</button>
        </div>
        <p class="mt-1 text-sm text-black-800 dark:text-black-600">Stable per-instance values such as base URLs and credentials. Secret fields encrypt at rest.</p>
        <div class="mt-4 space-y-2">
          {#if draft.configs.length === 0}
            <p class="text-xs text-black-700 dark:text-black-600">No fields yet.</p>
          {/if}
          {#each draft.configs as field, i (i)}
            <FieldRow {field} {onChange} onRemove={() => removeConfig(i)} />
          {/each}
        </div>
      </section>
    {/if}

    {#if shows("ops")}
      <section class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-6">
        <div class="flex items-center justify-between gap-3">
          <h2 class="text-base font-semibold text-black-900 dark:text-white-100">Operations</h2>
          <button type="button" class="inline-flex items-center gap-1 rounded-lg border border-white-400 dark:border-navy-600 px-3 py-1.5 text-xs font-medium text-black-800 dark:text-black-600 hover:border-green-400 hover:text-green-600" onclick={addOp}>+ Add operation</button>
        </div>
        <p class="mt-1 text-sm text-black-800 dark:text-black-600">Inputs are per-call values the LLM provides. Templates render against {"{{.cfg.*}}"} and {"{{.in.*}}"}.</p>
        <div class="mt-4 space-y-4">
          {#if draft.ops.length === 0}
            <p class="text-xs text-black-700 dark:text-black-600">No operations yet. Add at least one.</p>
          {/if}
          {#each draft.ops as op, i (i)}
            <OpCard {op} {onChange} onRemove={() => removeOp(i)} />
          {/each}
        </div>
      </section>
    {/if}
  </div>

  <div class="col-span-12 lg:col-span-5">
    <div class="lg:sticky lg:top-36">
      <div class="rounded-xl border border-white-300 dark:border-navy-600">
        <div class="border-b border-white-300 px-3 py-2 text-xs font-medium text-black-800 dark:border-navy-600 dark:text-black-600">Live preview (stored row)</div>
        <pre class="max-h-[calc(100vh-14rem)] overflow-auto rounded-b-xl bg-navy-800 p-4 font-mono text-xs leading-relaxed text-white-100">{previewJson}</pre>
      </div>
    </div>
  </div>
</div>
