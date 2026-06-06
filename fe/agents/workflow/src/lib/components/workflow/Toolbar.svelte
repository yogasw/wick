<script lang="ts">
  import {
    draftWorkflow,
    dirty,
    saveDraft,
    publish,
    saveStatus,
    lastSavedAt,
    validationErrorCount,
    validationWarningCount,
    workflowState,
    canActivate,
  } from "$lib/stores/editor";

  // Tick a local timestamp once per second so "Saved Xs ago" updates
  // without subscribing the lastSavedAt store on every render.
  let now = $state(Date.now());
  $effect(() => {
    const id = setInterval(() => (now = Date.now()), 1000);
    return () => clearInterval(id);
  });
  function fmtAgo(ms: number): string {
    const s = Math.floor(ms / 1000);
    if (s < 5) return "just now";
    if (s < 60) return `${s}s ago`;
    const m = Math.floor(s / 60);
    if (m < 60) return `${m}m ago`;
    const h = Math.floor(m / 60);
    return `${h}h ago`;
  }
  const savedAgo = $derived(
    $lastSavedAt ? fmtAgo(now - $lastSavedAt) : "",
  );
  import { workflowAPI } from "$lib/api/workflow";
  import ConfirmDialog from "$lib/components/shared/ConfirmDialog.svelte";
  import type { Writable } from "svelte/store";

  // EditorShell hoists the top-level Editor / Executions toggle and
  // passes the store down. Toolbar owns the visual tab pills.
  type Props = { topTab: Writable<"editor" | "executions"> };
  let { topTab }: Props = $props();

  let saving = $state(false);
  let publishing = $state(false);
  let publishMenuOpen = $state(false);
  let moreMenuOpen = $state(false);

  let confirmDelete = $state(false);
  let confirmDiscard = $state(false);
  let confirmUnpublish = $state(false);

  // Inline rename — clicking the workflow name opens a contenteditable
  // input bound to a local draft string. Commit on Enter / blur,
  // cancel on Escape. The URL stays put (workflowID is folder uuid),
  // so the rename is purely a Name attribute update.
  let editingName = $state(false);
  let nameDraft = $state("");
  function startRename() {
    nameDraft = $draftWorkflow?.name ?? "";
    editingName = true;
  }
  async function commitRename() {
    const wf = $draftWorkflow;
    if (!wf || !editingName) return;
    editingName = false;
    const next = nameDraft.trim();
    if (!next || next === wf.name) return;
    draftWorkflow.update((w) => (w ? { ...w, name: next } : w));
    try {
      await saveDraft();
    } catch (e) {
      console.error("rename save failed:", e);
    }
  }
  function cancelRename() {
    editingName = false;
  }

  async function onDelete() {
    const wf = $draftWorkflow;
    if (!wf) return;
    try {
      await workflowAPI.remove(wf.id);
    } finally {
      confirmDelete = false;
      // Navigate back to the list — full reload to drop stale state.
      window.location.href = "/tools/agents/workflows";
    }
  }

  async function onDiscard() {
    const wf = $draftWorkflow;
    if (!wf) return;
    try {
      await workflowAPI.discardDraft(wf.id);
      window.location.reload();
    } finally {
      confirmDiscard = false;
    }
  }

  async function onUnpublish() {
    const wf = $draftWorkflow;
    if (!wf) return;
    try {
      // Unpublish = toggle off active. We do not call a separate
      // endpoint — the legacy editor flips Enabled=false to deactivate
      // the live router registration.
      await workflowAPI.toggle(wf.id, false);
      draftWorkflow.update((w) => (w ? { ...w, enabled: false } : w));
    } finally {
      confirmUnpublish = false;
    }
  }

  async function onSave() {
    saving = true;
    try {
      await saveDraft();
    } finally {
      saving = false;
    }
  }

  async function onPublish() {
    publishing = true;
    try {
      await publish();
    } finally {
      publishing = false;
      publishMenuOpen = false;
    }
  }

  async function onToggle() {
    const wf = $draftWorkflow;
    if (!wf) return;
    await workflowAPI.toggle(wf.id, !wf.enabled);
    draftWorkflow.update((w) => (w ? { ...w, enabled: !w.enabled } : w));
  }
</script>

<header
  class="flex items-center gap-1.5 md:gap-3 pl-11 pr-2 md:px-4 py-2 w-full min-w-0 border-b border-white-300 dark:border-navy-600
         bg-white-100 dark:bg-navy-800 text-black-800 dark:text-white-100"
>
  <!-- Breadcrumb + inline-renamable name. -->
  <div class="flex items-center gap-2 text-sm font-medium min-w-0">
    <a href="/tools/agents/workflows" class="hidden md:inline-block text-black-700 dark:text-black-600 hover:text-black-800 dark:hover:text-black-800 dark:text-white-100">
      Workflows
    </a>
    <span class="hidden md:inline text-black-700 dark:text-black-500">›</span>
    {#if editingName}
      <input
        class="rounded border border-slate-300 dark:border-navy-500 bg-white dark:bg-navy-700 px-2 py-0.5 text-sm"
        bind:value={nameDraft}
        onblur={commitRename}
        onkeydown={(e) => { if (e.key === "Enter") commitRename(); if (e.key === "Escape") cancelRename(); }}
        autofocus
      />
    {:else}
      <button
        class="truncate min-w-0 hover:text-emerald-500"
        title="Click to rename (URL stays the same)"
        onclick={startRename}
      >{$draftWorkflow?.name ?? "—"}</button>
    {/if}
  </div>

  <!-- Editor / Executions tab toggle. -->
  <div class="shrink-0 flex items-center bg-white-300 dark:bg-navy-700 rounded-lg p-0.5 text-xs gap-0.5">
    {#each ["editor", "executions"] as t}
      <button
        class="px-2 md:px-3 py-1 rounded font-medium transition-colors"
        class:bg-white-100={$topTab === t}
        class:dark:bg-navy-600={$topTab === t}
        class:shadow-sm={$topTab === t}
        class:text-black-800={$topTab === t}
        class:dark:text-white-100={$topTab === t}
        class:text-black-700={$topTab !== t}
        class:dark:text-black-500={$topTab !== t}
        onclick={() => topTab.set(t as "editor" | "executions")}
      >
        <!-- Short labels on mobile to keep the toolbar on one row. -->
        <span class="md:hidden">{t === "editor" ? "Edit" : "Runs"}</span>
        <span class="hidden md:inline capitalize">{t}</span>
      </button>
    {/each}
  </div>

  <div class="flex-1"></div>

  <!-- Save status text — mirrors v1's #wf-save-status. Hidden in idle
       state to avoid noise; shows up the moment a save runs. The
       "Saved Xs ago" suffix ticks every second off lastSavedAt. -->
  {#if $saveStatus !== "idle"}
    <span
      class="hidden md:inline text-[11px] italic"
      class:text-black-700={$saveStatus === "saving" || $saveStatus === "saved"}
      class:text-black-600={$saveStatus === "saving" || $saveStatus === "saved"}
      class:text-amber-600={$saveStatus === "pending"}
      class:text-amber-400={$saveStatus === "pending"}
      class:text-rose-600={$saveStatus === "failed"}
      class:text-rose-400={$saveStatus === "failed"}
    >
      {#if $saveStatus === "pending"}Pending…
      {:else if $saveStatus === "saving"}⟳ Saving…
      {:else if $saveStatus === "saved"}✓ Saved{#if savedAgo} {savedAgo}{/if}
      {:else if $saveStatus === "failed"}✕ Save failed
      {/if}
    </span>
  {/if}

  <!-- Validation count chip — separate from save status so it stays
       visible after the status text fades, surfacing the gate state at
       all times. Hidden when validation is clean. -->
  {#if $validationErrorCount > 0}
    <span
      class="hidden md:inline-flex items-center gap-1.5 px-2 py-0.5 rounded-full text-[11px] font-medium bg-rose-100 text-rose-800 dark:bg-rose-900/30 dark:text-rose-300"
      title="Validation errors block publish"
    >
      <span class="h-1.5 w-1.5 rounded-full bg-rose-500"></span>
      {$validationErrorCount} validation {$validationErrorCount === 1 ? "issue" : "issues"}
    </span>
  {:else if $validationWarningCount > 0}
    <span
      class="hidden md:inline-flex items-center gap-1.5 px-2 py-0.5 rounded-full text-[11px] font-medium bg-amber-100 text-amber-800 dark:bg-amber-900/30 dark:text-amber-300"
      title="Warnings do not block publish"
    >
      <span class="h-1.5 w-1.5 rounded-full bg-amber-500"></span>
      {$validationWarningCount} {$validationWarningCount === 1 ? "warning" : "warnings"}
    </span>
  {/if}

  <!-- Approved badge — shown when governance has signed off on a
       published version. v1 surfaces this as "approved vN" so the
       operator knows the live version is the one auditors green-lit. -->
  {#if $workflowState?.approved}
    <span
      class="hidden md:inline-flex items-center gap-1.5 px-2 py-0.5 rounded-full text-[11px] font-medium bg-sky-100 text-sky-800 dark:bg-sky-900/30 dark:text-sky-300"
      title={$workflowState.approved_by
        ? `approved by ${$workflowState.approved_by}` + ($workflowState.approved_at ? ` · ${new Date($workflowState.approved_at).toLocaleString()}` : "")
        : "approved"}
    >
      <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round"><polyline points="20 6 9 17 4 12" /></svg>
      approved{$workflowState.approved_version ? ` v${$workflowState.approved_version}` : ""}
    </span>
  {/if}

  <!-- Draft pill — only when there's an unpublished draft. -->
  {#if $dirty}
    <span class="hidden md:inline-flex items-center gap-1.5 px-2 py-0.5 rounded-full text-[11px] font-medium bg-amber-100 text-amber-800 dark:bg-amber-900/30 dark:text-amber-300">
      <span class="h-1.5 w-1.5 rounded-full bg-amber-500"></span>
      draft
    </span>
  {/if}

  <!-- Active / Inactive toggle button. Single pill that flips colour
       + label — matches n8n's "Active / Inactive" button + a leading
       status dot so the state reads at a glance. -->
  {#if $draftWorkflow}
    {@const blocked = !$draftWorkflow.enabled && !$canActivate}
    <button
      class="shrink-0 inline-flex items-center gap-2 px-2.5 md:px-3 py-1.5 rounded-full text-xs font-semibold border transition-colors disabled:cursor-not-allowed disabled:opacity-60"
      class:bg-emerald-500={$draftWorkflow.enabled}
      class:border-emerald-500={$draftWorkflow.enabled}
      class:text-white-100={$draftWorkflow.enabled}
      class:hover:bg-emerald-600={$draftWorkflow.enabled}
      class:bg-white-200={!$draftWorkflow.enabled}
      class:border-white-400={!$draftWorkflow.enabled}
      class:text-black-700={!$draftWorkflow.enabled}
      onclick={onToggle}
      disabled={blocked}
      title={blocked
        ? "Publish a version before activating — the runtime only schedules the published copy"
        : $draftWorkflow.enabled
          ? "Click to deactivate"
          : "Click to activate"}
    >
      <span class="h-1.5 w-1.5 rounded-full"
            class:bg-white={$draftWorkflow.enabled}
            class:bg-white-200={!$draftWorkflow.enabled}></span>
      {$draftWorkflow.enabled ? "Active" : "Inactive"}
    </button>
  {/if}

  <!-- Save draft. Hidden on mobile (folds into the ⋮ menu) — auto-save
       already covers most cases; the manual button stays on md+. -->
  <button
    class="hidden md:inline-block shrink-0 px-3 py-1.5 rounded text-xs font-medium bg-slate-100 dark:bg-navy-600 hover:bg-slate-200 dark:hover:bg-white-400 dark:bg-navy-500 disabled:opacity-50"
    onclick={onSave}
    disabled={saving || !$dirty}
  >{saving ? "Saving…" : "Save"}</button>

  <!-- Publish split button. -->
  <div class="relative flex shrink-0">
    <button
      class="px-2.5 md:px-3 py-1.5 rounded md:rounded-r-none text-xs font-semibold bg-emerald-500 hover:bg-emerald-600 text-white-100 disabled:opacity-50 disabled:cursor-not-allowed"
      onclick={onPublish}
      disabled={publishing || !$draftWorkflow || $validationErrorCount > 0}
      title={$validationErrorCount > 0
        ? `Fix ${$validationErrorCount} validation error${$validationErrorCount === 1 ? "" : "s"} before publishing`
        : "Publish the draft as the live version"}
    >{publishing ? "…" : "Publish"}</button>
    <button
      class="hidden md:block px-2 py-1.5 rounded-r text-xs bg-emerald-600 hover:bg-emerald-700 text-white-100 border-l border-emerald-700"
      onclick={() => (publishMenuOpen = !publishMenuOpen)}
      aria-haspopup="menu"
      aria-expanded={publishMenuOpen}
      aria-label="Publish menu"
    >▾</button>
    {#if publishMenuOpen}
      <div class="absolute right-0 top-full mt-1 min-w-[180px] rounded shadow-lg bg-white dark:bg-navy-700 border border-slate-200 dark:border-navy-600 text-xs z-20">
        <button
          class="w-full px-3 py-2 text-left hover:bg-white-200 dark:hover:bg-white-300 dark:bg-navy-600 disabled:opacity-50"
          disabled={!$dirty}
          onclick={() => { publishMenuOpen = false; confirmDiscard = true; }}
        >Discard draft</button>
        <button
          class="w-full px-3 py-2 text-left hover:bg-white-200 dark:hover:bg-white-300 dark:bg-navy-600 text-amber-700 dark:text-amber-300"
          onclick={() => { publishMenuOpen = false; confirmUnpublish = true; }}
        >Unpublish (deactivate)</button>
      </div>
    {/if}
  </div>

  <!-- History icon. -->
  <button
    class="hidden md:flex h-7 w-7 rounded items-center justify-center text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-white-300 dark:bg-navy-600"
    title="Show execution history"
    aria-label="History"
  >
    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><path d="M3 12a9 9 0 1 0 3-6.7L3 8"/><path d="M3 3v5h5"/><path d="M12 7v5l3 3"/></svg>
  </button>

  <!-- More menu (kebab). -->
  <div class="relative shrink-0">
    <button
      class="h-7 w-7 rounded flex items-center justify-center text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-white-300 dark:bg-navy-600"
      onclick={() => (moreMenuOpen = !moreMenuOpen)}
      title="More actions"
      aria-haspopup="menu"
      aria-expanded={moreMenuOpen}
      aria-label="More"
    >
      <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><circle cx="12" cy="5" r="1.5"/><circle cx="12" cy="12" r="1.5"/><circle cx="12" cy="19" r="1.5"/></svg>
    </button>
    {#if moreMenuOpen}
      <div class="absolute right-0 top-full mt-1 min-w-[180px] rounded shadow-lg bg-white dark:bg-navy-700 border border-slate-200 dark:border-navy-600 text-xs z-20">
        <!-- Mobile-only: these are dedicated buttons on md+, folded here
             on phones so the toolbar fits one row without scrolling. -->
        <button
          class="md:hidden w-full px-3 py-2 text-left hover:bg-white-200 dark:hover:bg-white-300 dark:bg-navy-600 disabled:opacity-50"
          disabled={saving || !$dirty}
          onclick={() => { moreMenuOpen = false; void onSave(); }}
        >{saving ? "Saving…" : "Save draft"}</button>
        <button
          class="md:hidden w-full px-3 py-2 text-left hover:bg-white-200 dark:hover:bg-white-300 dark:bg-navy-600 disabled:opacity-50"
          disabled={!$dirty}
          onclick={() => { moreMenuOpen = false; confirmDiscard = true; }}
        >Discard draft</button>
        <button
          class="md:hidden w-full px-3 py-2 text-left text-amber-700 dark:text-amber-300 hover:bg-white-200 dark:hover:bg-white-300 dark:bg-navy-600"
          onclick={() => { moreMenuOpen = false; confirmUnpublish = true; }}
        >Unpublish (deactivate)</button>
        <div class="md:hidden border-t border-slate-200 dark:border-navy-600"></div>
        <a
          class="block w-full px-3 py-2 text-left hover:bg-white-200 dark:hover:bg-white-300 dark:bg-navy-600"
          href={`/tools/agents/workflows/edit/${$draftWorkflow?.id ?? ""}/download`}
        >Download JSON</a>
        <button
          class="w-full px-3 py-2 text-left text-rose-600 hover:bg-white-200 dark:hover:bg-white-300 dark:bg-navy-600"
          onclick={() => { moreMenuOpen = false; confirmDelete = true; }}
        >Delete workflow</button>
      </div>
    {/if}
  </div>
</header>

<ConfirmDialog
  open={confirmDelete}
  title="Delete workflow?"
  body={`This will permanently remove "${$draftWorkflow?.name ?? ""}" and every run history attached to it. This cannot be undone.`}
  confirmLabel="Delete"
  destructive
  onConfirm={onDelete}
  onCancel={() => (confirmDelete = false)}
/>

<ConfirmDialog
  open={confirmDiscard}
  title="Discard draft?"
  body="Unsaved changes since the last publish will be lost."
  confirmLabel="Discard"
  destructive
  onConfirm={onDiscard}
  onCancel={() => (confirmDiscard = false)}
/>

<ConfirmDialog
  open={confirmUnpublish}
  title="Unpublish workflow?"
  body="The workflow stays in storage but stops responding to triggers."
  confirmLabel="Unpublish"
  destructive
  onConfirm={onUnpublish}
  onCancel={() => (confirmUnpublish = false)}
/>
