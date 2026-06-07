<script lang="ts">
  import { onMount } from "svelte";
  import { workflowAPI, type WorkflowSummary } from "$lib/api/workflow";
  import type { Workflow } from "$lib/types/workflow";

  type Props = { onpick?: (id: string) => void; base?: string };
  let { onpick, base = "" }: Props = $props();

  function open(id: string) {
    if (onpick) { onpick(id); return; }
    // Island mode: navigate directly to the editor.
    window.location.href = (base || "") + "/workflows/edit/" + id;
  }

  let items       = $state<WorkflowSummary[]>([]);
  let loading     = $state(true);
  let error       = $state<string | null>(null);
  let search      = $state("");
  let creating    = $state(false);
  let newName     = $state("");
  let newTemplate = $state("empty");
  let saving      = $state(false);
  let menuOpenId  = $state<string | null>(null);
  let actionMsg   = $state<string | null>(null);
  let createMode  = $state<"template" | "import">("template");
  let importFile  = $state<File | null>(null);
  let importing   = $state(false);
  let fileInput   : HTMLInputElement;

  type Template = { value: string; label: string; desc: string };
  let templates = $state<Template[]>([]);

  function nextName(): string {
    const taken = new Set(items.map(w => w.name));
    let i = items.length + 1;
    while (taken.has(`My workflow ${i}`)) i++;
    return `My workflow ${i}`;
  }

  function openCreateModal() {
    newName = nextName();
    newTemplate = "empty";
    createMode = "template";
    importFile = null;
    creating = true;
  }

  const filtered = $derived(
    search.trim()
      ? items.filter(w => w.name.toLowerCase().includes(search.toLowerCase()))
      : items
  );

  async function load() {
    loading = true;
    error = null;
    try {
      const res = await workflowAPI.list();
      items = res.workflows ?? [];
    } catch (e) {
      error = (e as Error).message;
    } finally {
      loading = false;
    }
  }

  onMount(async () => {
    await load();
    try {
      const r = await workflowAPI.templates();
      templates = r.templates ?? [];
    } catch { /* use empty, no UI needed */ }
  });

  async function createWorkflow() {
    if (!newName.trim()) return;
    saving = true;
    try {
      const res = await workflowAPI.create({ name: newName.trim(), template: newTemplate });
      await load();
      creating = false;
      newName = "";
      open(res.id);
    } catch (e) {
      flash((e as Error).message, true);
    } finally {
      saving = false;
    }
  }

  function pickImportFile(e: Event) {
    const input = e.currentTarget as HTMLInputElement;
    importFile = input.files?.[0] ?? null;
    input.value = "";
  }

  async function importWorkflow() {
    if (!importFile) return;
    if (importFile.size > 512 * 1024) {
      flash("File too large (max 512 KB)", true);
      return;
    }
    importing = true;
    try {
      const text = await importFile.text();
      let parsed: Workflow;
      try {
        parsed = JSON.parse(text) as Workflow;
      } catch {
        flash("Invalid JSON file", true);
        return;
      }
      const res = await workflowAPI.importWorkflow(parsed);
      await load();
      creating = false;
      importFile = null;
      flash(`Imported "${res.name}"`);
      open(res.id);
    } catch (e) {
      flash((e as Error).message, true);
    } finally {
      importing = false;
    }
  }

  async function duplicate(id: string) {
    menuOpenId = null;
    try {
      const res = await workflowAPI.duplicate(id);
      await load();
      flash(`Duplicated as "${res.name}"`);
    } catch (e) {
      flash((e as Error).message, true);
    }
  }

  async function deleteWorkflow(id: string, name: string) {
    menuOpenId = null;
    if (!confirm(`Delete "${name}"? This cannot be undone.`)) return;
    try {
      await workflowAPI.remove(id);
      items = items.filter(w => w.id !== id);
      flash("Deleted");
    } catch (e) {
      flash((e as Error).message, true);
    }
  }

  async function toggleEnabled(wf: WorkflowSummary) {
    menuOpenId = null;
    try {
      await workflowAPI.toggle(wf.id, !wf.enabled);
      items = items.map(w => w.id === wf.id ? { ...w, enabled: !wf.enabled } : w);
    } catch (e) {
      flash((e as Error).message, true);
    }
  }

  let flashTimer: ReturnType<typeof setTimeout>;
  let flashError = $state(false);
  function flash(msg: string, isError = false) {
    actionMsg = msg;
    flashError = isError;
    clearTimeout(flashTimer);
    flashTimer = setTimeout(() => { actionMsg = null; }, 3000);
  }

  function timeAgo(iso?: string): string {
    if (!iso) return "";
    const diff = Date.now() - new Date(iso).getTime();
    const m = Math.floor(diff / 60000);
    if (m < 1)  return "just now";
    if (m < 60) return `${m}m ago`;
    const h = Math.floor(m / 60);
    if (h < 24) return `${h}h ago`;
    const d = Math.floor(h / 24);
    return `${d}d ago`;
  }

  function fullDate(iso?: string): string {
    if (!iso) return "";
    return new Date(iso).toLocaleString("en", {
      day: "numeric", month: "short", year: "numeric",
      hour: "2-digit", minute: "2-digit", hour12: false,
    });
  }

  function closeMenu(e: MouseEvent) {
    if (menuOpenId && !(e.target as Element).closest(".wf-menu")) {
      menuOpenId = null;
    }
  }
</script>

<svelte:window onclick={closeMenu} />

<div class="flex flex-col h-full text-black-800 dark:text-white-100 select-none">

  <!-- Header — matches Channels page layout -->
  <header class="px-6 pt-14 pb-2 md:pt-6">
    <div class="flex items-start justify-between gap-4 mb-4">
      <div>
        <h1 class="text-xl font-semibold text-black-800 dark:text-white-100">Workflows</h1>
        <p class="text-sm text-black-700 dark:text-black-600 mt-0.5">DAG-based automations — drag-drop editor, AI-buildable, run-tracked.</p>
      </div>
      <button
        class="flex items-center gap-1.5 px-4 py-2 rounded-lg bg-green-500 hover:bg-green-600 text-white-100 text-sm font-medium transition-colors flex-shrink-0"
        onclick={openCreateModal}
      >
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><path d="M12 5v14M5 12h14"/></svg>
        New workflow
      </button>
    </div>
    <!-- Search inline, no separator border -->
    <div class="flex items-center gap-3 pb-4">
      <div class="relative flex-1 max-w-xs">
        <svg class="absolute left-2.5 top-1/2 -translate-y-1/2 text-black-700 dark:text-black-600" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="11" cy="11" r="7"/><path d="m21 21-4.3-4.3"/></svg>
        <input
          type="text"
          bind:value={search}
          placeholder="Search workflows…"
          class="w-full bg-white-200 dark:bg-navy-700 border border-white-300 dark:border-navy-600 rounded-lg pl-8 pr-3 py-1.5 text-sm text-black-800 dark:text-white-100 placeholder-black-700 dark:placeholder-black-600 focus:outline-none focus:border-green-500 focus:ring-2 focus:ring-green-500/20"
        />
      </div>
      {#if !loading}
        <span class="flex-shrink-0 whitespace-nowrap text-sm text-black-700 dark:text-black-600">{filtered.length} workflow{filtered.length !== 1 ? "s" : ""}</span>
      {/if}
      {#if actionMsg}
        <span class="text-xs ml-auto transition-opacity"
              class:text-green-500={!flashError}
              class:text-red-500={flashError}
        >{actionMsg}</span>
      {/if}
    </div>
  </header>

  <!-- Create modal -->
  {#if creating}
    <!-- svelte-ignore a11y_no_static_element_interactions -->
    <!-- svelte-ignore a11y_click_events_have_key_events -->
    <div class="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-sm"
         onclick={(e) => { if (e.target === e.currentTarget) creating = false; }}>
      <div class="w-full max-w-md mx-4 rounded-2xl bg-white-100 dark:bg-navy-700 border border-white-300 dark:border-navy-600 shadow-lg overflow-hidden">
        <div class="flex items-center justify-between px-5 py-4 border-b border-white-300 dark:border-navy-600">
          <h2 class="font-semibold text-black-800 dark:text-white-100">New workflow</h2>
          <button class="text-black-600 dark:text-black-600 hover:text-black-800 dark:hover:text-white-100 transition-colors" onclick={() => creating = false}>
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><path d="M18 6 6 18M6 6l12 12"/></svg>
          </button>
        </div>

        <div class="px-5 pt-4">
          <div class="flex gap-1 p-1 rounded-lg bg-white-200 dark:bg-navy-800">
            <button
              type="button"
              class={`flex-1 px-3 py-1.5 rounded-md text-xs font-medium transition-colors ${
                createMode === "template"
                  ? "bg-white-100 dark:bg-navy-600 text-black-800 dark:text-white-100 shadow-sm"
                  : "text-black-700 dark:text-black-500 hover:text-black-800 dark:hover:text-white-100"
              }`}
              onclick={() => createMode = "template"}
            >From template</button>
            <button
              type="button"
              class={`flex-1 px-3 py-1.5 rounded-md text-xs font-medium transition-colors ${
                createMode === "import"
                  ? "bg-white-100 dark:bg-navy-600 text-black-800 dark:text-white-100 shadow-sm"
                  : "text-black-700 dark:text-black-500 hover:text-black-800 dark:hover:text-white-100"
              }`}
              onclick={() => createMode = "import"}
            >Import file</button>
          </div>
        </div>

        {#if createMode === "template"}
          <div class="px-5 pt-5 pb-3">
            <label class="block text-xs font-medium text-black-700 dark:text-black-500 mb-1.5">Name</label>
            <input
              type="text"
              bind:value={newName}
              class="w-full bg-white-200 dark:bg-navy-700 border border-white-300 dark:border-navy-600 rounded-lg px-3 py-2 text-sm text-black-800 dark:text-white-100 focus:outline-none focus:border-green-500"
              onkeydown={(e) => e.key === "Enter" && createWorkflow()}
            />
          </div>

          <div class="px-5 pb-5">
            <label class="block text-xs font-medium text-black-700 dark:text-black-500 mb-2">Template</label>
            <div class="grid grid-cols-2 gap-2">
              {#each templates as t}
                <button
                  type="button"
                  class={`text-left px-3 py-2.5 rounded-xl border transition-all ${
                    newTemplate === t.value
                      ? "border-green-500 bg-green-50 dark:bg-green-900/20"
                      : "border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-700 hover:border-white-400 dark:hover:border-navy-500"
                  }`}
                  onclick={() => newTemplate = t.value}
                >
                  <div class="text-xs font-medium text-black-800 dark:text-white-100">{t.label}</div>
                  <div class="text-[10px] text-black-700 dark:text-black-600 mt-0.5 leading-tight">{t.desc}</div>
                </button>
              {/each}
            </div>
          </div>
        {:else}
          <div class="px-5 pt-5 pb-5">
            <label class="block text-xs font-medium text-black-700 dark:text-black-500 mb-1.5">Workflow file</label>
            <input
              type="file"
              accept="application/json,.json"
              bind:this={fileInput}
              onchange={pickImportFile}
              class="hidden"
            />
            <button
              type="button"
              class="w-full flex flex-col items-center justify-center gap-2 px-4 py-8 rounded-xl border border-dashed border-white-400 dark:border-navy-500 bg-white-200 dark:bg-navy-700 hover:border-green-500 transition-colors"
              onclick={() => fileInput.click()}
            >
              <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" class="text-black-700 dark:text-black-600"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="17 8 12 3 7 8"/><line x1="12" y1="3" x2="12" y2="15"/></svg>
              {#if importFile}
                <span class="text-sm text-black-800 dark:text-white-100">{importFile.name}</span>
                <span class="text-[11px] text-black-600 dark:text-black-600">Choose a different file</span>
              {:else}
                <span class="text-sm text-black-800 dark:text-white-100">Choose a .workflow.json file</span>
                <span class="text-[11px] text-black-600 dark:text-black-600">Exported via a workflow's Download JSON</span>
              {/if}
            </button>
          </div>
        {/if}

        <div class="flex items-center justify-end gap-2 px-5 py-4 border-t border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800/80">
          <button class="px-4 py-1.5 rounded-lg text-sm text-black-700 dark:text-black-500 hover:text-black-800 dark:hover:text-white-100 hover:bg-white-200 dark:hover:bg-navy-700 transition-colors"
                  onclick={() => creating = false}>Cancel</button>
          {#if createMode === "template"}
            <button
              class="px-5 py-1.5 rounded-lg bg-green-500 hover:bg-green-400 text-white-100 text-sm font-medium disabled:opacity-50 transition-colors"
              onclick={createWorkflow}
              disabled={saving || !newName.trim()}
            >{saving ? "Creating…" : "Create workflow"}</button>
          {:else}
            <button
              class="px-5 py-1.5 rounded-lg bg-green-500 hover:bg-green-400 text-white-100 text-sm font-medium disabled:opacity-50 transition-colors"
              onclick={importWorkflow}
              disabled={importing || !importFile}
            >{importing ? "Importing…" : "Import"}</button>
          {/if}
        </div>
      </div>
    </div>
  {/if}

  <!-- List -->
  <div class="flex-1 overflow-y-auto px-6 py-4">
    {#if loading}
      <div class="flex items-center gap-2 text-black-600 dark:text-black-600 text-sm mt-8">
        <svg class="animate-spin h-4 w-4" fill="none" viewBox="0 0 24 24">
          <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"/>
          <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8v8z"/>
        </svg>
        Loading…
      </div>
    {:else if error}
      <p class="text-sm text-red-500 dark:text-red-400 mt-4">{error}</p>
    {:else if filtered.length === 0}
      <div class="text-center mt-16">
        {#if search}
          <p class="text-black-700 dark:text-black-600 text-sm">No workflows match "<span class="text-black-800 dark:text-black-400">{search}</span>"</p>
        {:else}
          <div class="inline-flex flex-col items-center gap-3">
            <svg class="text-black-500 dark:text-black-700" width="40" height="40" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5"><rect x="3" y="3" width="18" height="18" rx="3"/><path d="M9 12h6M12 9v6"/></svg>
            <p class="text-black-700 dark:text-black-600 text-sm">No workflows yet</p>
            <button class="text-green-600 dark:text-green-400 hover:text-green-500 text-sm underline underline-offset-2 transition-colors"
                    onclick={openCreateModal}>Create your first workflow</button>
          </div>
        {/if}
      </div>
    {:else}
      <ul class="flex flex-col gap-3">
        {#each filtered as wf (wf.id)}
          <!-- svelte-ignore a11y_no_noninteractive_element_to_interactive_role -->
          <li class="group relative flex items-center gap-3 px-4 py-2.5 rounded-xl bg-white-100 dark:bg-navy-700 border border-white-300 dark:border-navy-600 shadow-sm
                     hover:border-green-500 hover:shadow-md transition-all cursor-pointer"
              role="button"
              tabindex="0"
              onclick={() => open(wf.id)}
              onkeydown={(e) => (e.key === "Enter" || e.key === " ") && open(wf.id)}
          >
            <!-- Status dot -->
            <div class={`flex-shrink-0 w-2 h-2 rounded-full mt-0.5 ${wf.enabled ? "bg-green-500" : "bg-white-400 dark:bg-navy-500"}`}
                 title={wf.enabled ? "Enabled" : "Disabled"}
            ></div>

            <!-- Name + timestamps -->
            <div class="flex-1 min-w-0">
              <div class="flex items-center gap-2 flex-wrap">
                <span class="font-medium text-sm text-black-800 dark:text-white-100 truncate">{wf.name}</span>
                {#if wf.has_draft}
                  <span class="px-1.5 py-0.5 rounded-md bg-yellow-100 dark:bg-yellow-900/30 text-yellow-700 dark:text-yellow-400 text-[10px] font-medium border border-yellow-200 dark:border-yellow-800/40">draft</span>
                {/if}
              </div>
              {#if wf.updated_at || wf.created_at}
                <div class="flex flex-wrap items-center gap-x-2 gap-y-0.5 mt-0.5 text-[11px] text-black-600 dark:text-black-600">
                  {#if wf.updated_at}
                    <span title={fullDate(wf.updated_at)}>Updated {timeAgo(wf.updated_at)}</span>
                  {/if}
                  {#if wf.created_at}
                    <span class="hidden sm:inline text-black-600 dark:text-black-700">Created {fullDate(wf.created_at)}</span>
                  {/if}
                </div>
              {/if}
            </div>

            <!-- Version badge -->
            <span class="hidden sm:inline text-[10px] text-black-600 dark:text-black-700 font-mono flex-shrink-0">v{wf.version ?? 1}</span>

            <!-- Status text -->
            <span class="text-[11px] flex-shrink-0 w-16 text-right"
                  class:text-green-600={wf.enabled}
                  class:text-green-400={wf.enabled}
                  class:text-black-600={!wf.enabled}
                  class:text-black-700={!wf.enabled}
            >{wf.enabled ? "enabled" : "disabled"}</span>

            <!-- 3-dot menu -->
            <div class="wf-menu flex-shrink-0 relative" onclick={(e) => e.stopPropagation()}>
              <button
                class="w-7 h-7 flex items-center justify-center rounded-lg text-black-600 dark:text-black-600 hover:text-black-800 dark:hover:text-white-100 hover:bg-white-200 dark:hover:bg-navy-600 opacity-0 group-hover:opacity-100 transition-all"
                onclick={() => menuOpenId = menuOpenId === wf.id ? null : wf.id}
                title="More options"
              >
                <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><circle cx="12" cy="5" r="1.5"/><circle cx="12" cy="12" r="1.5"/><circle cx="12" cy="19" r="1.5"/></svg>
              </button>

              {#if menuOpenId === wf.id}
                <div class="absolute right-0 top-8 z-50 w-44 rounded-xl bg-white-100 dark:bg-navy-700 border border-white-300 dark:border-navy-600 shadow-md overflow-hidden py-1">
                  <button class="w-full text-left px-4 py-2 text-sm text-black-800 dark:text-black-400 hover:bg-white-200 dark:hover:bg-navy-700 hover:text-black-800 dark:hover:text-white-100 transition-colors flex items-center gap-2"
                          onclick={() => open(wf.id)}>
                    <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6"/><polyline points="15 3 21 3 21 9"/><line x1="10" y1="14" x2="21" y2="3"/></svg>
                    Open editor
                  </button>
                  <button class="w-full text-left px-4 py-2 text-sm text-black-800 dark:text-black-400 hover:bg-white-200 dark:hover:bg-navy-700 hover:text-black-800 dark:hover:text-white-100 transition-colors flex items-center gap-2"
                          onclick={() => duplicate(wf.id)}>
                    <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>
                    Duplicate
                  </button>
                  <button class="w-full text-left px-4 py-2 text-sm text-black-800 dark:text-black-400 hover:bg-white-200 dark:hover:bg-navy-700 hover:text-black-800 dark:hover:text-white-100 transition-colors flex items-center gap-2"
                          onclick={() => toggleEnabled(wf)}>
                    <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M18.36 6.64A9 9 0 1 1 5.64 5.64"/><line x1="12" y1="2" x2="12" y2="12"/></svg>
                    {wf.enabled ? "Disable" : "Enable"}
                  </button>
                  <div class="border-t border-white-300 dark:border-navy-600 my-1"></div>
                  <button class="w-full text-left px-4 py-2 text-sm text-red-500 dark:text-red-400 hover:bg-white-200 dark:hover:bg-navy-700 hover:text-red-600 dark:hover:text-red-300 transition-colors flex items-center gap-2"
                          onclick={() => deleteWorkflow(wf.id, wf.name)}>
                    <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="3 6 5 6 21 6"/><path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/><path d="M10 11v6M14 11v6"/><path d="M9 6V4a1 1 0 0 1 1-1h4a1 1 0 0 1 1 1v2"/></svg>
                    Delete
                  </button>
                </div>
              {/if}
            </div>
          </li>
        {/each}
      </ul>
    {/if}
  </div>
</div>
