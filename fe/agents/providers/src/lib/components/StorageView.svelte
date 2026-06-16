<script lang="ts">
  import { onMount } from "svelte";
  import { ConfirmDialog, Breadcrumb, type BreadcrumbItem } from "@wick-fe/common-ui";
  import { toastOk, toastError } from "@wick-fe/common-stores";
  import {
    apiGetStorage,
    apiStorageRetention,
    apiStoragePreview,
    apiStorageRestore,
    apiStorageDelete,
    apiStorageSync,
    apiStorageUpload,
  } from "$lib/api.js";
  import type { StorageFileDTO } from "$lib/api.js";
  import type { StorageResponse } from "$lib/types.js";

  type Props = {
    onBack: () => void;
  };
  let { onBack }: Props = $props();

  const crumbs: BreadcrumbItem[] = [
    { label: "Providers", onClick: () => onBack() },
    { label: "Provider Storage" },
  ];

  let data = $state<StorageResponse | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let busy = $state<Record<string, boolean>>({});

  let filterProvider = $state("");
  let filterInstance = $state("");
  let selected = $state<Set<number>>(new Set());
  let confirmDeleteFile = $state<StorageFileDTO | null>(null);
  let previewFile = $state<StorageFileDTO | null>(null);
  let previewContent = $state<string | null>(null);
  let previewLoading = $state(false);

  let uploadOpen = $state(false);
  let uploadType = $state("");
  let uploadInstance = $state("");
  let uploadRelPath = $state("");
  let uploadFile = $state<File | null>(null);
  let uploadInputEl = $state<HTMLInputElement | null>(null);

  function setBusy(key: string, val: boolean) {
    busy = { ...busy, [key]: val };
  }

  async function load(silent = false) {
    if (!silent) { loading = true; error = null; }
    try {
      data = await apiGetStorage(filterProvider, filterInstance);
    } catch (e) {
      if (!silent) error = e instanceof Error ? e.message : "Failed to load storage";
    } finally {
      if (!silent) loading = false;
    }
  }

  async function applyFilter() {
    loading = true;
    error = null;
    selected = new Set();
    await load();
  }

  function formatSize(bytes: number): string {
    if (bytes === 0) return "0 B";
    if (bytes < 1024) return `${bytes} B`;
    if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
    return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
  }

  function formatDate(iso: string): string {
    if (!iso) return "—";
    try {
      return new Date(iso).toLocaleString();
    } catch {
      return iso;
    }
  }

  function toggleSelect(id: number) {
    const s = new Set(selected);
    if (s.has(id)) s.delete(id);
    else s.add(id);
    selected = s;
  }

  function toggleSelectAll() {
    if (!data) return;
    const files = data.files.filter((f) => !f.is_dir);
    if (selected.size === files.length) {
      selected = new Set();
    } else {
      selected = new Set(files.map((f) => f.id));
    }
  }

  async function doRetention(file: StorageFileDTO, days: number) {
    const key = `ret-${file.id}`;
    setBusy(key, true);
    try {
      await apiStorageRetention(file.id, days);
      toastOk(`Retention updated to ${days} days`);
      await load(true);
    } catch (e) {
      toastError(e instanceof Error ? e.message : "Retention update failed");
    } finally {
      setBusy(key, false);
    }
  }

  async function doPreview(file: StorageFileDTO) {
    previewFile = file;
    previewContent = null;
    previewLoading = true;
    try {
      const r = await apiStoragePreview(file.id);
      if (r.too_large) {
        previewContent = `[File too large to preview — ${formatSize(r.size as number)}]`;
      } else if (r.is_binary) {
        previewContent = "[Binary file — cannot display]";
      } else {
        previewContent = (r.content as string) ?? "";
      }
    } catch (e) {
      previewContent = `Error: ${e instanceof Error ? e.message : "unknown"}`;
    } finally {
      previewLoading = false;
    }
  }

  async function doRestore() {
    if (selected.size === 0) return;
    setBusy("restore", true);
    try {
      const ids = [...selected];
      const r = await apiStorageRestore(ids);
      toastOk(`Restored ${r.restored} file(s)`);
      selected = new Set();
      await load(true);
    } catch (e) {
      toastError(e instanceof Error ? e.message : "Restore failed");
    } finally {
      setBusy("restore", false);
    }
  }

  async function doDelete(file: StorageFileDTO) {
    confirmDeleteFile = null;
    const key = `del-${file.id}`;
    setBusy(key, true);
    try {
      await apiStorageDelete(file.id);
      toastOk(`Deleted ${file.name}`);
      selected = new Set([...selected].filter((id) => id !== file.id));
      await load(true);
    } catch (e) {
      toastError(e instanceof Error ? e.message : "Delete failed");
    } finally {
      setBusy(key, false);
    }
  }

  async function doSync(type: string, name: string) {
    const key = `sync-${type}-${name}`;
    setBusy(key, true);
    try {
      await apiStorageSync(type, name);
      toastOk(`Sync triggered for ${name}`);
      await load(true);
    } catch (e) {
      toastError(e instanceof Error ? e.message : "Sync failed");
    } finally {
      setBusy(key, false);
    }
  }

  async function doUpload() {
    if (!uploadFile || !uploadType || !uploadInstance || !uploadRelPath) {
      toastError("All upload fields are required");
      return;
    }
    setBusy("upload", true);
    try {
      await apiStorageUpload(uploadType, uploadInstance, uploadRelPath, uploadFile);
      toastOk("File uploaded");
      uploadOpen = false;
      uploadType = "";
      uploadInstance = "";
      uploadRelPath = "";
      uploadFile = null;
      if (uploadInputEl) uploadInputEl.value = "";
      await load(true);
    } catch (e) {
      toastError(e instanceof Error ? e.message : "Upload failed");
    } finally {
      setBusy("upload", false);
    }
  }

  onMount(() => {
    load();
  });
</script>

<div class="space-y-6">
  <!-- header -->
  <Breadcrumb items={crumbs} />
  <div class="flex items-center justify-between gap-3 flex-wrap">
    <div>
      <h1 class="text-lg font-semibold text-black-900 dark:text-white-100">Provider Storage</h1>
      <p class="text-xs text-black-700 dark:text-black-600 mt-0.5">Synced file snapshots — preview, restore, manage retention.</p>
    </div>
    <button
      onclick={() => { uploadOpen = !uploadOpen; }}
      class="rounded-lg border border-white-400 dark:border-navy-600 px-3 py-2 text-xs font-medium text-black-800 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800"
    >Upload Backup</button>
  </div>

  <!-- upload panel -->
  {#if uploadOpen}
    <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-5 space-y-4">
      <div class="text-sm font-semibold text-black-900 dark:text-white-100">Upload Backup File</div>
      <div class="grid grid-cols-1 sm:grid-cols-3 gap-3">
        <div>
          <label for="upload-type" class="block text-xs text-black-700 dark:text-black-600 mb-1">Provider type</label>
          <input
            id="upload-type"
            type="text"
            bind:value={uploadType}
            placeholder="e.g. claude"
            class="w-full rounded border border-white-400 dark:border-navy-600 bg-white-50 dark:bg-navy-800 px-3 py-1.5 text-sm text-black-900 dark:text-white-100 placeholder-black-500 dark:placeholder-black-600 focus:outline-none focus:ring-1 focus:ring-green-500"
          />
        </div>
        <div>
          <label for="upload-instance" class="block text-xs text-black-700 dark:text-black-600 mb-1">Instance name</label>
          <input
            id="upload-instance"
            type="text"
            bind:value={uploadInstance}
            placeholder="e.g. default"
            class="w-full rounded border border-white-400 dark:border-navy-600 bg-white-50 dark:bg-navy-800 px-3 py-1.5 text-sm text-black-900 dark:text-white-100 placeholder-black-500 dark:placeholder-black-600 focus:outline-none focus:ring-1 focus:ring-green-500"
          />
        </div>
        <div>
          <label for="upload-relpath" class="block text-xs text-black-700 dark:text-black-600 mb-1">Relative path</label>
          <input
            id="upload-relpath"
            type="text"
            bind:value={uploadRelPath}
            placeholder="e.g. config.json"
            class="w-full rounded border border-white-400 dark:border-navy-600 bg-white-50 dark:bg-navy-800 px-3 py-1.5 text-sm text-black-900 dark:text-white-100 placeholder-black-500 dark:placeholder-black-600 focus:outline-none focus:ring-1 focus:ring-green-500"
          />
        </div>
      </div>
      <div>
        <label for="upload-file" class="block text-xs text-black-700 dark:text-black-600 mb-1">File</label>
        <input
          id="upload-file"
          type="file"
          bind:this={uploadInputEl}
          onchange={(e) => {
            const t = e.currentTarget as HTMLInputElement;
            uploadFile = t.files?.[0] ?? null;
          }}
          class="text-sm text-black-800 dark:text-black-600"
        />
      </div>
      <div class="flex items-center gap-2">
        <button
          onclick={doUpload}
          disabled={busy["upload"]}
          class="rounded-lg px-4 py-2 text-xs font-medium bg-green-500 text-white-100 hover:bg-green-600 disabled:opacity-50"
        >{busy["upload"] ? "Uploading…" : "Upload"}</button>
        <button
          onclick={() => { uploadOpen = false; }}
          class="rounded-lg px-4 py-2 text-xs font-medium border border-white-400 dark:border-navy-600 text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800"
        >Cancel</button>
      </div>
    </div>
  {/if}

  <!-- filters -->
  <div class="flex items-center gap-3 flex-wrap">
    <select
      bind:value={filterProvider}
      class="rounded border border-white-400 dark:border-navy-600 bg-white-50 dark:bg-navy-800 px-3 py-1.5 text-xs text-black-800 dark:text-black-600 focus:outline-none focus:ring-1 focus:ring-green-500"
    >
      <option value="">All providers</option>
      {#if data}
        {#each data.provider_types as pt}
          <option value={pt}>{pt}</option>
        {/each}
      {/if}
    </select>
    <input
      type="text"
      bind:value={filterInstance}
      placeholder="Filter by instance…"
      class="rounded border border-white-400 dark:border-navy-600 bg-white-50 dark:bg-navy-800 px-3 py-1.5 text-xs text-black-800 dark:text-black-600 placeholder-black-500 dark:placeholder-black-600 focus:outline-none focus:ring-1 focus:ring-green-500"
    />
    <button
      onclick={applyFilter}
      class="rounded-lg border border-white-400 dark:border-navy-600 px-3 py-1.5 text-xs font-medium text-black-800 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800"
    >Apply</button>
    {#if data && [...new Set(data.files.map((f) => `${f.provider_type}/${f.instance_name}`))].length > 0}
      {#each [...new Set(data.files.map((f) => `${f.provider_type}/${f.instance_name}`))] as key}
        {@const [stype, sname] = key.split("/")}
        {@const syncKey = `sync-${stype}-${sname}`}
        <button
          onclick={() => doSync(stype, sname)}
          disabled={busy[syncKey]}
          class="rounded-lg border border-white-400 dark:border-navy-600 px-3 py-1.5 text-xs font-medium text-black-800 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800 disabled:opacity-50"
        >{busy[syncKey] ? "Syncing…" : `Sync ${sname}`}</button>
      {/each}
    {/if}
  </div>

  {#if loading}
    <div class="text-sm text-black-600 dark:text-black-500">Loading…</div>
  {:else if error}
    <div class="rounded-lg border border-red-300 dark:border-red-700 bg-red-50 dark:bg-red-900/20 px-4 py-3 text-sm text-red-700 dark:text-red-400">{error}</div>
  {:else if data}

    <!-- restore toolbar -->
    {#if selected.size > 0}
      <div class="flex items-center gap-3 rounded-lg border border-green-300 dark:border-green-700 bg-green-50 dark:bg-green-900/20 px-4 py-2.5">
        <span class="text-xs text-green-700 dark:text-green-400">{selected.size} file(s) selected</span>
        <button
          onclick={doRestore}
          disabled={busy["restore"]}
          class="rounded-lg px-3 py-1 text-xs font-medium bg-green-500 text-white-100 hover:bg-green-600 disabled:opacity-50"
        >{busy["restore"] ? "Restoring…" : "Restore Selected"}</button>
        <button
          onclick={() => { selected = new Set(); }}
          class="text-xs text-green-700 dark:text-green-400 hover:underline"
        >Clear</button>
      </div>
    {/if}

    <!-- table -->
    {#if data.files.length === 0}
      <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-6 py-12 text-center text-sm text-black-700 dark:text-black-600">
        No storage files found. Sync a provider to populate snapshots.
      </div>
    {:else}
      <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 overflow-hidden">
        <div class="overflow-x-auto">
          <table class="w-full text-xs">
            <thead>
              <tr class="border-b border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800">
                <th class="px-3 py-2 text-left">
                  <input
                    type="checkbox"
                    checked={selected.size === data.files.filter((f) => !f.is_dir).length && data.files.filter((f) => !f.is_dir).length > 0}
                    onchange={toggleSelectAll}
                    class="rounded"
                  />
                </th>
                <th class="px-3 py-2 text-left font-semibold text-black-800 dark:text-black-500">Provider / Instance</th>
                <th class="px-3 py-2 text-left font-semibold text-black-800 dark:text-black-500">Path</th>
                <th class="px-3 py-2 text-right font-semibold text-black-800 dark:text-black-500">Size</th>
                <th class="px-3 py-2 text-left font-semibold text-black-800 dark:text-black-500">Synced</th>
                <th class="px-3 py-2 text-left font-semibold text-black-800 dark:text-black-500">Retention</th>
                <th class="px-3 py-2 text-right font-semibold text-black-800 dark:text-black-500">Actions</th>
              </tr>
            </thead>
            <tbody class="divide-y divide-white-300 dark:divide-navy-600">
              {#each data.files as file}
                {@const retKey = `ret-${file.id}`}
                {@const delKey = `del-${file.id}`}
                <tr class="hover:bg-white-200 dark:hover:bg-navy-800 transition-colors">
                  <td class="px-3 py-2">
                    {#if !file.is_dir}
                      <input
                        type="checkbox"
                        checked={selected.has(file.id)}
                        onchange={() => toggleSelect(file.id)}
                        class="rounded"
                      />
                    {/if}
                  </td>
                  <td class="px-3 py-2">
                    <span class="font-mono text-black-900 dark:text-white-100">{file.provider_type}</span>
                    <span class="text-black-600 dark:text-black-500">/{file.instance_name}</span>
                    {#if file.is_dir}
                      <span class="ml-1 inline-flex rounded-full bg-black-600/10 dark:bg-navy-600 px-1.5 py-0.5 text-xs text-black-600 dark:text-black-500">dir</span>
                    {/if}
                  </td>
                  <td class="px-3 py-2 font-mono text-black-700 dark:text-black-600 max-w-xs truncate" title={file.rel_path}>{file.rel_path}</td>
                  <td class="px-3 py-2 text-right text-black-700 dark:text-black-600 whitespace-nowrap">{formatSize(file.size)}</td>
                  <td class="px-3 py-2 text-black-700 dark:text-black-600 whitespace-nowrap">{formatDate(file.synced_at)}</td>
                  <td class="px-3 py-2">
                    <select
                      disabled={busy[retKey]}
                      value={file.retention_days}
                      onchange={(e) => {
                        const v = parseInt((e.currentTarget as HTMLSelectElement).value, 10);
                        doRetention(file, v);
                      }}
                      class="rounded border border-white-400 dark:border-navy-600 bg-white-50 dark:bg-navy-800 px-2 py-0.5 text-xs text-black-800 dark:text-black-600 focus:outline-none focus:ring-1 focus:ring-green-500 disabled:opacity-50"
                    >
                      <option value={0}>Forever</option>
                      <option value={1}>1 day</option>
                      <option value={7}>7 days</option>
                      <option value={14}>14 days</option>
                      <option value={30}>30 days</option>
                      <option value={90}>90 days</option>
                    </select>
                  </td>
                  <td class="px-3 py-2">
                    <div class="flex items-center justify-end gap-2">
                      {#if !file.is_dir}
                        <button
                          onclick={() => doPreview(file)}
                          class="rounded px-2 py-0.5 text-xs font-medium border border-white-400 dark:border-navy-600 text-black-800 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800"
                        >Preview</button>
                      {/if}
                      <button
                        onclick={() => { confirmDeleteFile = file; }}
                        disabled={busy[delKey]}
                        class="rounded px-2 py-0.5 text-xs font-medium text-red-600 dark:text-red-400 hover:underline disabled:opacity-50"
                      >Delete</button>
                    </div>
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      </div>
    {/if}
  {/if}
</div>

<!-- preview modal -->
{#if previewFile}
  <div
    role="dialog"
    aria-modal="true"
    aria-label="File preview"
    tabindex="-1"
    class="fixed inset-0 z-50 flex items-center justify-center bg-black-900/50 p-4"
    onclick={(e) => { if (e.target === e.currentTarget) { previewFile = null; previewContent = null; } }}
    onkeydown={(e) => { if (e.key === "Escape") { previewFile = null; previewContent = null; } }}
  >
    <div class="w-full max-w-2xl rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-xl overflow-hidden">
      <div class="flex items-center justify-between px-5 py-4 border-b border-white-300 dark:border-navy-600">
        <div>
          <div class="text-sm font-semibold text-black-900 dark:text-white-100">{previewFile.name}</div>
          <div class="text-xs text-black-600 dark:text-black-500 font-mono mt-0.5">{previewFile.rel_path}</div>
        </div>
        <button
          onclick={() => { previewFile = null; previewContent = null; }}
          class="text-black-600 dark:text-black-500 hover:text-black-900 dark:hover:text-white-100"
          aria-label="Close preview"
        >
          <svg class="w-5 h-5" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12"/>
          </svg>
        </button>
      </div>
      <div class="p-5 max-h-96 overflow-y-auto">
        {#if previewLoading}
          <div class="text-sm text-black-600 dark:text-black-500">Loading…</div>
        {:else}
          <pre class="font-mono text-xs text-black-800 dark:text-black-400 whitespace-pre-wrap break-all">{previewContent}</pre>
        {/if}
      </div>
    </div>
  </div>
{/if}

<!-- delete confirm -->
{#if confirmDeleteFile}
  <ConfirmDialog
    title="Delete file snapshot?"
    message={`Delete "${confirmDeleteFile.name}" from storage? This cannot be undone.`}
    confirmLabel="Delete"
    onConfirm={() => { if (confirmDeleteFile) doDelete(confirmDeleteFile); }}
    onCancel={() => { confirmDeleteFile = null; }}
  />
{/if}
