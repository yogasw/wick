<script lang="ts">
  /* One page, open: its body, and — for an admin — the ways to change it.

     The thing this screen has to get right is nerve. People are correctly
     wary of editing what an agent will later recall as fact, so the line
     that says every write is committed and restorable sits WITH the editor,
     not in a help page. The server sends that sentence (it is a claim about
     the backend's behaviour, not a reassurance the UI invented) and the
     restore control is right here, not two clicks away. */
  import { Button, ConfirmDialog, Select, TextArea, TextInput } from "@wick-fe/common-ui";
  import { checkpointLabel, checkpointsForPath, hasUnsavedWork, PAGE_KINDS } from "./projectview.js";
  import { MANAGE_ADMIN_ONLY } from "./format.js";
  import type { Checkpoint, Page } from "./types.js";

  type Props = {
    path: string;
    page: Page | null;
    loading: boolean;
    error: string;
    busy: boolean;
    canManage: boolean;
    /* The draft is owned by the parent so switching pages cannot silently
       drop an edit — and so "unsaved" survives a re-render. */
    draft: string;
    title: string;
    kind: string;
    /* committedNote is the server's sentence about writes being restorable;
       saveMsg is the outcome of the last save/delete/restore. */
    committedNote: string;
    saveMsg: string;
    saveFailed: boolean;
    checkpoints: Checkpoint[] | null;
    checkpointsLoading: boolean;
    onDraft: (v: string) => void;
    onTitle: (v: string) => void;
    onKind: (v: string) => void;
    onSave: () => void;
    onDelete: () => void;
    onClose: () => void;
    onLoadCheckpoints: () => void;
    onRestore: (oid: string) => void;
  };
  let {
    path,
    page,
    loading,
    error,
    busy,
    canManage,
    draft,
    title,
    kind,
    committedNote,
    saveMsg,
    saveFailed,
    checkpoints,
    checkpointsLoading,
    onDraft,
    onTitle,
    onKind,
    onSave,
    onDelete,
    onClose,
    onLoadCheckpoints,
    onRestore,
  }: Props = $props();

  let confirmDelete = $state(false);
  let restoreOpen = $state(false);
  let chosen = $state("");

  const dirty = $derived(hasUnsavedWork(page?.body ?? "", draft));
  const split = $derived(checkpointsForPath(checkpoints, path));
  const restorable = $derived([...split.matching, ...split.others]);

  function doRestore(): void {
    if (!chosen) return;
    restoreOpen = false;
    onRestore(chosen);
  }

  function openRestore(): void {
    restoreOpen = true;
    chosen = "";
    if (!checkpoints) onLoadCheckpoints();
  }
</script>

<section class="rounded-xl border border-white-300 bg-white-100 dark:border-navy-600 dark:bg-navy-700" data-testid="page-editor">
  <div class="flex flex-wrap items-start justify-between gap-3 border-b border-white-300 px-5 py-3 dark:border-navy-600">
    <div class="min-w-0">
      <p class="truncate font-mono text-sm text-black-900 dark:text-white-100" title={path}>{path}</p>
      {#if dirty}
        <p class="mt-0.5 text-[0.6875rem] text-cau-600 dark:text-cau-400">Unsaved changes</p>
      {/if}
    </div>
    <Button variant="ghost" size="sm" onclick={onClose}>Close</Button>
  </div>

  {#if loading}
    <p class="px-5 py-8 text-center text-xs text-black-700 dark:text-black-600">Reading the page…</p>
  {:else if error}
    <p class="px-5 py-4 text-xs leading-relaxed text-rose-700 dark:text-rose-300">{error}</p>
  {:else}
    {#if canManage}
      <div class="grid gap-3 border-b border-white-300 px-5 py-3 sm:grid-cols-2 dark:border-navy-600">
        <div>
          <p class="mb-1 text-[0.6875rem] uppercase tracking-wider text-black-700 dark:text-black-600">Title</p>
          <TextInput
            value={title}
            onChange={onTitle}
            placeholder="Derived from the first heading"
            ariaLabel="Page title"
            disabled={busy}
          />
        </div>
        <div>
          <p class="mb-1 text-[0.6875rem] uppercase tracking-wider text-black-700 dark:text-black-600">Kind</p>
          <Select value={kind} options={PAGE_KINDS} onChange={onKind} disabled={busy} />
        </div>
      </div>
    {/if}

    <div class="px-5 py-3">
      {#if canManage}
        <TextArea
          value={draft}
          onChange={onDraft}
          rows={16}
          ariaLabel="Page body"
          disabled={busy}
          class="font-mono"
        />
      {:else}
        <!-- A viewer reads the page as it is written, not as a form they
             cannot submit. -->
        <pre
          class="max-h-96 overflow-auto whitespace-pre-wrap break-words rounded-lg bg-white-200 p-3 font-mono text-xs leading-relaxed text-black-900 dark:bg-navy-800 dark:text-white-100">{page?.body ??
            ""}</pre>
      {/if}
    </div>

    {#if canManage}
      <div class="flex flex-wrap items-center justify-between gap-3 border-t border-white-300 px-5 py-3 dark:border-navy-600">
        <p class="max-w-md text-[0.6875rem] leading-relaxed text-black-700 dark:text-black-600">{committedNote}</p>
        <div class="flex flex-wrap items-center gap-2">
          <Button variant="secondary" size="sm" disabled={busy} onclick={openRestore}>Restore…</Button>
          <Button variant="danger" size="sm" disabled={busy} onclick={() => (confirmDelete = true)}>Delete page</Button>
          <Button variant="primary" size="sm" disabled={busy || !dirty} onclick={onSave}>
            {busy ? "Saving…" : "Save"}
          </Button>
        </div>
      </div>
    {:else}
      <p class="border-t border-white-300 px-5 py-3 text-xs leading-relaxed text-black-700 dark:border-navy-600 dark:text-black-600">
        {MANAGE_ADMIN_ONLY}
      </p>
    {/if}

    {#if saveMsg}
      <p
        class={`border-t border-white-300 px-5 py-3 text-xs leading-relaxed dark:border-navy-600 ${
          saveFailed ? "text-rose-700 dark:text-rose-300" : "text-black-800 dark:text-black-600"
        }`}
        data-testid="save-result"
      >
        {saveMsg}
      </p>
    {/if}

    {#if restoreOpen && canManage}
      <div class="border-t border-white-300 px-5 py-3 dark:border-navy-600" data-testid="restore-panel">
        <p class="text-[0.6875rem] uppercase tracking-wider text-black-700 dark:text-black-600">Restore from a checkpoint</p>
        {#if checkpointsLoading}
          <p class="mt-2 text-xs text-black-700 dark:text-black-600">Reading the wiki's history…</p>
        {:else if restorable.length === 0}
          <p class="mt-2 text-xs leading-relaxed text-black-700 dark:text-black-600">
            No checkpoints yet — the wiki's history starts at the first write.
          </p>
        {:else}
          <!-- The commits that name this page come first: the list is
               store-wide, so an unfiltered order would put another project's
               commit at the top of a restore for this one. -->
          <div class="mt-2">
            <Select
              value={chosen}
              onChange={(v) => (chosen = v)}
              options={[
                { label: "Pick a checkpoint…", value: "" },
                ...restorable.map((c) => ({ label: checkpointLabel(c), value: c.oid })),
              ]}
              disabled={busy}
            />
          </div>
          {#if split.matching.length === 0}
            <p class="mt-2 text-[0.6875rem] leading-relaxed text-black-700 dark:text-black-600">
              None of these checkpoints names this page. Restoring from one still works — a single commit can carry
              several pages — but check the summary before you do.
            </p>
          {/if}
          <div class="mt-3 flex flex-wrap items-center gap-2">
            <Button variant="secondary" size="sm" disabled={busy || !chosen} onclick={doRestore}>Restore this version</Button>
            <Button variant="ghost" size="sm" onclick={() => (restoreOpen = false)}>Cancel</Button>
          </div>
        {/if}
      </div>
    {/if}
  {/if}
</section>

<ConfirmDialog
  open={confirmDelete}
  title="Delete this page?"
  body={`${path} stops being recalled immediately. It stays in the wiki's git history, so it can be restored from a checkpoint.`}
  confirmLabel="Delete page"
  cancelLabel="Keep it"
  destructive={true}
  onConfirm={() => {
    confirmDelete = false;
    onDelete();
  }}
  onCancel={() => (confirmDelete = false)}
/>
