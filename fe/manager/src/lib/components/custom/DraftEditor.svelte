<script lang="ts">
  /* The shared draft form behind the review page, the edit page, and the
     manual builder. Renders Meta, Access & behavior, Configs (CRUD), and
     Operations (CRUD) as bindable sections plus a live JSON preview.
     Parent owns the draft object and the save/delete actions; this
     component only mutates the draft in place and pings onChange so the
     preview + toolbar stay in sync. Mirrors customDraftForm + the form
     half of custom_review.js. visibleSteps lets the manual stepper show
     one section at a time; default (undefined) shows everything. */
  import { tick } from "svelte";
  import { TextInput, Select } from "@wick-fe/common-ui";
  import FieldRow from "./FieldRow.svelte";
  import OpCard from "./OpCard.svelte";
  import IconPicker from "../icon/IconPicker.svelte";
  import { newField, newOp, newCategory, allOps, serialize } from "./draft.js";
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

  /* Section (category) CRUD. Each section owns its ops; ops are added into
     a section and can be dragged between sections. */
  function addSection() {
    draft.ops = [...draft.ops, newCategory()];
    onChange();
  }

  function removeSection(ci: number) {
    draft.ops = draft.ops.filter((_, idx) => idx !== ci);
    if (draft.ops.length === 0) draft.ops = [newCategory()];
    onChange();
  }

  function setSection<K extends "title" | "description">(ci: number, k: K, v: string) {
    draft.ops[ci][k] = v;
    onChange();
  }

  function addOp(ci: number) {
    draft.ops[ci].ops = [...draft.ops[ci].ops, newOp()];
    onChange();
  }

  function removeOp(ci: number, oi: number) {
    draft.ops[ci].ops = draft.ops[ci].ops.filter((_, idx) => idx !== oi);
    onChange();
  }

  /* Drag-drop: move an op from (fromCat, fromOp) into toCat, appended.
     dragState carries the source coordinates between dragstart and drop. */
  let dragState = $state<{ ci: number; oi: number } | null>(null);

  function onDragStart(ci: number, oi: number) {
    dragState = { ci, oi };
  }

  function onDropToSection(toCi: number) {
    const d = dragState;
    dragState = null;
    if (!d || d.ci === toCi) return;
    const moved = draft.ops[d.ci].ops[d.oi];
    draft.ops[d.ci].ops = draft.ops[d.ci].ops.filter((_, idx) => idx !== d.oi);
    draft.ops[toCi].ops = [...draft.ops[toCi].ops, moved];
    onChange();
  }

  /* Connector-level visual group on the index page (draft.category) — a
     separate concept from the op sections above. */
  let categoryOptions = $derived([{ label: "Other (no category)", value: "" }, ...categories.map((c) => ({ label: c, value: c }))]);

  let healthOptions = $derived([
    { label: "— No health check —", value: "" },
    ...allOps(draft).filter((o) => o.key).map((o) => ({ label: `${o.name || o.key} (${o.key})`, value: o.key })),
  ]);

  let tagName = $derived(`custom:${draft.key || "…"}`);
  let previewJson = $derived(JSON.stringify(serialize(draft), null, 2));

  /* Jump panel is a collapsible mini-map. Top-level rows (Meta / Access /
     Configs / Operations) plus, under Operations, one expandable node per op
     section that reveals its operations. A scroll spy (IntersectionObserver)
     highlights whatever is in view and auto-expands its section. */
  type TopItem = { id: string; label: string };
  type OpItem = { id: string; label: string };
  type SectionNode = { id: string; label: string; count: number; ops: OpItem[] };

  let topItems = $derived.by<TopItem[]>(() => {
    const items: TopItem[] = [];
    if (shows("meta")) items.push({ id: "cc-section-meta", label: "Meta" });
    if (showAccess) items.push({ id: "cc-section-access", label: "Access & behavior" });
    if (shows("configs")) items.push({ id: "cc-section-configs", label: "Configs" });
    return items;
  });

  let sectionNodes = $derived.by<SectionNode[]>(() =>
    draft.ops.map((s, ci) => ({
      id: opSectionId(ci),
      label: s.title || "(untitled section)",
      count: s.ops.length,
      ops: s.ops.map((op, oi) => ({ id: opAnchorId(ci, oi), label: op.name || op.key || "(unnamed op)" })),
    })),
  );

  /* Every anchor id the spy watches, in document order — used to bind the
     observer and to map an active op back to its owning section. */
  let spyIds = $derived.by<string[]>(() => {
    const ids = topItems.map((t) => t.id);
    if (shows("ops")) {
      ids.push("cc-section-ops");
      for (const s of sectionNodes) {
        ids.push(s.id);
        for (const o of s.ops) ids.push(o.id);
      }
    }
    return ids;
  });

  let navTab = $state<"jump" | "json">("jump");
  let navOpen = $state(false);
  /* Desktop collapse: hides the whole side panel, leaving a thin re-open
     rail. Mobile uses navOpen (slide-in drawer) instead. */
  let navCollapsed = $state(false);
  /* Manually toggled-open sections (by section id). The active section (from
     the spy) is always shown expanded on top of this set. */
  let expanded = $state<Record<string, boolean>>({});
  let activeId = $state("");
  const activeSection = $derived(
    sectionNodes.find((s) => s.id === activeId || s.ops.some((o) => o.id === activeId))?.id ?? "",
  );

  function sectionOpen(id: string): boolean {
    return !!expanded[id] || activeSection === id;
  }

  function toggleSection(id: string): void {
    expanded = { ...expanded, [id]: !sectionOpen(id) };
  }

  function jumpTo(id: string) {
    document.getElementById(id)?.scrollIntoView({ behavior: "smooth", block: "start" });
    navOpen = false;
  }

  /* Scroll spy: observe every anchor; the topmost intersecting one is
     "active". Re-bind whenever the set of watched ids changes. tick() lets
     newly-added section/op anchors paint before we query them. The keep-last
     fallback (don't clear activeId when nothing intersects) means the bottom
     section stays active when scrolled to the end of the page. */
  $effect(() => {
    const ids = spyIds;
    if (typeof IntersectionObserver === "undefined") return;
    let obs: IntersectionObserver | null = null;
    let cancelled = false;
    const visible = new Set<string>();
    (async () => {
      await tick();
      if (cancelled) return;
      obs = new IntersectionObserver(
        (entries) => {
          for (const e of entries) {
            if (e.isIntersecting) visible.add(e.target.id);
            else visible.delete(e.target.id);
          }
          const first = ids.find((id) => visible.has(id));
          if (first) activeId = first; // keep last when nothing is visible
        },
        { rootMargin: "-72px 0px -45% 0px", threshold: 0 },
      );
      for (const id of ids) {
        const el = document.getElementById(id);
        if (el) obs.observe(el);
      }
    })();
    return () => {
      cancelled = true;
      obs?.disconnect();
    };
  });

  function opSectionId(ci: number): string {
    return `cc-section-ops-${ci}`;
  }

  function opAnchorId(ci: number, oi: number): string {
    return `cc-op-${ci}-${oi}`;
  }

  function setHealthOp(v: string) {
    draft.health_op = v;
    onChange();
  }
</script>

<div class="grid grid-cols-12 gap-6">
  <div class="col-span-12 space-y-6 {navCollapsed ? 'lg:col-span-11' : 'lg:col-span-7'}">
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
      <section id="cc-section-ops" class="space-y-4">
        <div class="flex items-center justify-between gap-3">
          <div>
            <h2 class="text-base font-semibold text-black-900 dark:text-white-100">Operations</h2>
            <p class="mt-1 text-sm text-black-800 dark:text-black-600">Group operations into sections. Drag an operation by its handle to move it between sections. Templates render against {"{{.cfg.*}}"} and {"{{.in.*}}"}.</p>
          </div>
          <button type="button" class="inline-flex items-center gap-1 rounded-lg border border-white-400 dark:border-navy-600 px-3 py-1.5 text-xs font-medium text-black-800 dark:text-black-600 hover:border-green-400 hover:text-green-600" onclick={addSection}>+ Add section</button>
        </div>

        {#each draft.ops as section, ci (ci)}
          <div
            id={opSectionId(ci)}
            role="group"
            class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-4 {dragState && dragState.ci !== ci ? 'ring-2 ring-green-400/40' : ''}"
            ondragover={(e) => { if (dragState) e.preventDefault(); }}
            ondrop={() => onDropToSection(ci)}
          >
            <div class="flex flex-wrap items-start gap-2">
              <div class="min-w-0 flex-1 space-y-2">
                <TextInput value={section.title} onChange={(v) => setSection(ci, "title", v)} placeholder="Section title (e.g. Rooms) — leave empty for default" ariaLabel={`Section ${ci + 1} title`} />
                <TextInput value={section.description} onChange={(v) => setSection(ci, "description", v)} placeholder="Section description (optional)" ariaLabel={`Section ${ci + 1} description`} />
              </div>
              <div class="flex items-center gap-2">
                <span class="rounded-full bg-white-300 dark:bg-navy-600 px-2 py-0.5 text-[10px] font-medium text-black-700 dark:text-black-600">{section.ops.length}</span>
                {#if draft.ops.length > 1}
                  <button type="button" class="flex h-8 w-8 items-center justify-center rounded-lg text-black-700 transition-colors hover:bg-neg-100 hover:text-neg-400 dark:text-black-600" title="Delete section" aria-label={`Delete section ${ci + 1}`} onclick={() => removeSection(ci)}>
                    <svg class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><path d="M3 6h18M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2m3 0v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6" stroke-linecap="round" stroke-linejoin="round"/></svg>
                  </button>
                {/if}
              </div>
            </div>

            <div class="mt-4 space-y-4">
              {#if section.ops.length === 0}
                <p class="rounded-lg border border-dashed border-white-400 dark:border-navy-600 px-3 py-4 text-center text-xs text-black-700 dark:text-black-600">No operations in this section. Add one, or drag one here.</p>
              {/if}
              {#each section.ops as op, oi (oi)}
                <div id={opAnchorId(ci, oi)} class="scroll-mt-24" draggable="true" ondragstart={() => onDragStart(ci, oi)} ondragend={() => (dragState = null)}>
                  <OpCard {op} {onChange} onRemove={() => removeOp(ci, oi)} />
                </div>
              {/each}
            </div>

            <button type="button" class="mt-3 inline-flex items-center gap-1 rounded-lg border border-white-400 dark:border-navy-600 px-3 py-1.5 text-xs font-medium text-black-800 dark:text-black-600 hover:border-green-400 hover:text-green-600" onclick={() => addOp(ci)}>+ Add operation</button>
          </div>
        {/each}
      </section>
    {/if}
  </div>

  <!-- Collapsed rail (desktop): a thin re-open button when the panel is hidden -->
  {#if navCollapsed}
    <div class="hidden lg:col-span-1 lg:block">
      <button type="button" title="Show panel" aria-label="Show navigator" class="sticky top-36 flex w-full items-center justify-center rounded-xl border border-white-300 bg-white-100 py-3 text-black-700 hover:text-green-600 dark:border-navy-600 dark:bg-navy-700 dark:text-black-600" onclick={() => (navCollapsed = false)}>
        <svg class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><path d="M4 6h16M4 12h10M4 18h7" stroke-linecap="round"></path></svg>
      </button>
    </div>
  {:else}
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
          <div class="flex items-center gap-1">
            <!-- Desktop collapse -->
            <button type="button" title="Collapse panel" aria-label="Collapse navigator" class="hidden rounded-lg p-1.5 text-black-700 transition-colors hover:bg-white-200 hover:text-green-600 dark:text-black-600 dark:hover:bg-navy-800 lg:inline-flex" onclick={() => (navCollapsed = true)}>
              <svg class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><path d="M9 6l6 6-6 6" stroke-linecap="round" stroke-linejoin="round"></path></svg>
            </button>
            <!-- Mobile close -->
            <button type="button" title="Hide panel" aria-label="Hide navigator" class="rounded-lg p-1.5 text-black-700 transition-colors hover:bg-white-200 hover:text-green-600 dark:text-black-600 dark:hover:bg-navy-800 lg:hidden" onclick={() => (navOpen = false)}>
              <svg class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 16 16"><path d="M4 4l8 8M12 4l-8 8" stroke-linecap="round"></path></svg>
            </button>
          </div>
        </div>
        <div class="min-h-0 flex-1 overflow-y-auto">
          {#if navTab === "jump"}
            <nav class="p-2">
              {#each topItems as item (item.id)}
                <button
                  type="button"
                  class="block w-full truncate rounded-lg py-1.5 pl-3 pr-3 text-left text-xs font-semibold hover:bg-white-200 hover:text-green-600 dark:hover:bg-navy-800 {activeId === item.id ? 'bg-white-200 text-green-600 dark:bg-navy-800' : 'text-black-900 dark:text-white-100'}"
                  onclick={() => jumpTo(item.id)}
                >{item.label}</button>
              {/each}
              {#if shows("ops")}
                <button
                  type="button"
                  class="block w-full truncate rounded-lg py-1.5 pl-3 pr-3 text-left text-xs font-semibold hover:bg-white-200 hover:text-green-600 dark:hover:bg-navy-800 {activeId === 'cc-section-ops' ? 'bg-white-200 text-green-600 dark:bg-navy-800' : 'text-black-900 dark:text-white-100'}"
                  onclick={() => jumpTo("cc-section-ops")}
                >Operations</button>
                {#each sectionNodes as s (s.id)}
                  {@const open = sectionOpen(s.id)}
                  <div>
                    <div class="flex items-center gap-1 rounded-lg pl-5 pr-2 {activeSection === s.id ? 'bg-white-200 dark:bg-navy-800' : ''}">
                      <button type="button" class="flex h-5 w-5 flex-shrink-0 items-center justify-center text-black-700 hover:text-green-600 dark:text-black-600" aria-label={open ? `Collapse ${s.label}` : `Expand ${s.label}`} onclick={() => toggleSection(s.id)}>
                        <svg class="h-3 w-3 transition-transform {open ? 'rotate-90' : ''}" fill="none" stroke="currentColor" stroke-width="2.5" viewBox="0 0 24 24"><path d="M9 6l6 6-6 6" stroke-linecap="round" stroke-linejoin="round"></path></svg>
                      </button>
                      <button type="button" class="flex min-w-0 flex-1 items-center justify-between gap-2 truncate py-1.5 text-left text-xs font-medium hover:text-green-600 {activeSection === s.id ? 'text-green-600' : 'text-black-800 dark:text-black-600'}" onclick={() => jumpTo(s.id)}>
                        <span class="truncate">{s.label}</span>
                        <span class="flex-shrink-0 rounded-full bg-white-300 dark:bg-navy-600 px-1.5 py-0.5 text-[10px] font-medium text-black-700 dark:text-black-600">{s.count}</span>
                      </button>
                    </div>
                    {#if open}
                      {#each s.ops as o (o.id)}
                        <button
                          type="button"
                          class="block w-full truncate rounded-lg py-1 pl-12 pr-3 text-left text-[11px] hover:bg-white-200 hover:text-green-600 dark:hover:bg-navy-800 {activeId === o.id ? 'bg-white-200 font-medium text-green-600 dark:bg-navy-800' : 'font-normal text-black-700 dark:text-black-600'}"
                          onclick={() => jumpTo(o.id)}
                        >{o.label}</button>
                      {/each}
                    {/if}
                  </div>
                {/each}
              {/if}
            </nav>
          {:else}
            <div class="p-2"><pre class="overflow-auto rounded-lg bg-navy-800 p-4 font-mono text-xs leading-relaxed text-white-100">{previewJson}</pre></div>
          {/if}
        </div>
      </div>
    </div>
  {/if}

  <button type="button" aria-label="Open navigator" class="fixed bottom-4 right-4 z-30 inline-flex items-center gap-1.5 rounded-full bg-green-500 px-4 py-2.5 text-sm font-medium text-white-100 shadow-lg transition-colors hover:bg-green-600 lg:hidden" onclick={() => (navOpen = true)}>
    <svg class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><path d="M4 6h16M4 12h10M4 18h7" stroke-linecap="round"></path></svg>
    Jump
  </button>
</div>
