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
  import IconPicker from "../icon/IconPicker.svelte";
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

  /* Access & behavior rides with the operations step in the manual stepper. */
  let showAccess = $derived(!visibleSteps || visibleSteps.includes("ops"));

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

  type NavItem = { id: string; label: string };
  let navItems = $derived(
    [
      shows("meta") ? { id: "cc-section-meta", label: "Meta" } : null,
      showAccess ? { id: "cc-section-access", label: "Access & behavior" } : null,
      shows("configs") ? { id: "cc-section-configs", label: "Configs" } : null,
      shows("ops") ? { id: "cc-section-ops", label: "Operations" } : null,
    ].filter((x): x is NavItem => x !== null),
  );
  let navTab = $state<"jump" | "json">("jump");
  let navOpen = $state(false);
  function jumpTo(id: string) {
    document.getElementById(id)?.scrollIntoView({ behavior: "smooth", block: "start" });
    navOpen = false;
  }

  function setHealthOp(v: string) {
    draft.health_op = v;
    onChange();
  }
</script>

<div class="grid grid-cols-12 gap-6">
  <div class="col-span-12 space-y-6 lg:col-span-7">
    {#if shows("meta")}
      <section id="cc-section-meta" class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-6">
        <div class="grid grid-cols-12 gap-4">
          <div class="col-span-12 sm:col-span-3">
            <span class="block text-xs font-medium text-black-800 dark:text-black-600">Icon</span>
            <div class="mt-1">
              <IconPicker value={draft.icon} onChange={(v) => set("icon", v)} ariaLabel="Icon" />
            </div>
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
      <section id="cc-section-access" class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-6">
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
          <div class="flex items-center justify-between gap-4 px-4 py-3">
            <div class="min-w-0 flex-1">
              <p class="text-sm font-medium text-black-900 dark:text-white-100">Single instance only</p>
              <p class="mt-0.5 text-xs text-black-700 dark:text-black-600">Default off — admins can add and duplicate instance rows, each with its own credentials.</p>
            </div>
            <button type="button" role="switch" aria-checked={draft.single} aria-label="Single instance only" onclick={() => set("single", !draft.single)} class="relative inline-flex h-5 w-9 flex-shrink-0 items-center rounded-full transition-colors {draft.single ? 'bg-green-500' : 'bg-white-400 dark:bg-navy-600'}">
              <span class="absolute left-0.5 top-0.5 h-4 w-4 rounded-full bg-white-100 shadow transition-transform {draft.single ? 'translate-x-4' : ''}"></span>
            </button>
          </div>
          <div class="flex items-center justify-between gap-4 px-4 py-3">
            <div class="min-w-0 flex-1">
              <p class="text-sm font-medium text-black-900 dark:text-white-100">Allow per-session config override</p>
              <p class="mt-0.5 text-xs text-black-700 dark:text-black-600">Default off. When on, this connector can be cloned into a per-session instance from the session Config tab.</p>
            </div>
            <button type="button" role="switch" aria-checked={draft.allow_session_config} aria-label="Allow per-session config override" onclick={() => set("allow_session_config", !draft.allow_session_config)} class="relative inline-flex h-5 w-9 flex-shrink-0 items-center rounded-full transition-colors {draft.allow_session_config ? 'bg-green-500' : 'bg-white-400 dark:bg-navy-600'}">
              <span class="absolute left-0.5 top-0.5 h-4 w-4 rounded-full bg-white-100 shadow transition-transform {draft.allow_session_config ? 'translate-x-4' : ''}"></span>
            </button>
          </div>
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
      <section id="cc-section-configs" class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-6">
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
      <section id="cc-section-ops" class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-6">
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
    {#if navOpen}
      <button type="button" aria-label="Close navigator" class="fixed inset-0 z-40 bg-black-900/40 lg:hidden" onclick={() => (navOpen = false)}></button>
    {/if}
    <div class="fixed inset-y-0 right-0 z-50 flex w-[22rem] max-w-[85vw] flex-col border-l border-white-300 bg-white-100 shadow-xl transition-transform dark:border-navy-600 dark:bg-navy-700 lg:sticky lg:inset-y-auto lg:top-36 lg:z-auto lg:max-h-[calc(100vh-11rem)] lg:w-auto lg:max-w-none lg:translate-x-0 lg:rounded-xl lg:border lg:shadow-none lg:transition-none {navOpen ? 'translate-x-0' : 'translate-x-full lg:translate-x-0'}">
      <div class="flex items-center justify-between gap-2 border-b border-white-300 px-3 py-2 dark:border-navy-600">
        <div class="flex items-center gap-1">
          <button type="button" class="rounded-lg px-3 py-1.5 text-xs font-medium {navTab === 'jump' ? 'bg-white-200 text-green-600 dark:bg-navy-800' : 'text-black-800 dark:text-black-600'}" onclick={() => (navTab = "jump")}>Jump</button>
          <button type="button" class="rounded-lg px-3 py-1.5 text-xs font-medium {navTab === 'json' ? 'bg-white-200 text-green-600 dark:bg-navy-800' : 'text-black-800 dark:text-black-600'}" onclick={() => (navTab = "json")}>JSON</button>
        </div>
        <button type="button" title="Hide panel" aria-label="Hide navigator" class="rounded-lg p-1.5 text-black-700 transition-colors hover:bg-white-200 hover:text-green-600 dark:text-black-600 dark:hover:bg-navy-800 lg:hidden" onclick={() => (navOpen = false)}>
          <svg class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 16 16"><path d="M4 4l8 8M12 4l-8 8" stroke-linecap="round"></path></svg>
        </button>
      </div>
      <div class="min-h-0 flex-1 overflow-y-auto">
        {#if navTab === "jump"}
          <nav class="p-2">
            {#each navItems as item (item.id)}
              <button type="button" class="block w-full rounded-lg px-3 py-1.5 text-left text-xs font-medium text-black-800 hover:bg-white-200 hover:text-green-600 dark:text-black-600 dark:hover:bg-navy-800" onclick={() => jumpTo(item.id)}>{item.label}</button>
            {/each}
          </nav>
        {:else}
          <div class="p-2"><pre class="overflow-auto rounded-lg bg-navy-800 p-4 font-mono text-xs leading-relaxed text-white-100">{previewJson}</pre></div>
        {/if}
      </div>
    </div>
  </div>

  <button type="button" aria-label="Open navigator" class="fixed bottom-4 right-4 z-30 inline-flex items-center gap-1.5 rounded-full bg-green-500 px-4 py-2.5 text-sm font-medium text-white-100 shadow-lg transition-colors hover:bg-green-600 lg:hidden" onclick={() => (navOpen = true)}>
    <svg class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><path d="M4 6h16M4 12h10M4 18h7" stroke-linecap="round"></path></svg>
    Jump
  </button>
</div>
