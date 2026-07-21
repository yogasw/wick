<script lang="ts">
  import { toastOk, toastError } from "@wick-fe/common-stores";
  import {
    listBrowserExtensions,
    uploadBrowserExtension,
    addBrowserExtensionFromStore,
    removeBrowserExtension,
    type BrowserExtension,
  } from "../api.js";

  type Props = { connectorId: string };
  let { connectorId }: Props = $props();

  let extensions = $state<BrowserExtension[]>([]);
  let loading = $state(true);
  let loaded = $state(false);
  let busy = $state(false);
  let dragOver = $state(false);
  let storeId = $state("");
  let fileEl = $state<HTMLInputElement | undefined>();

  async function refresh() {
    loading = true;
    try {
      extensions = await listBrowserExtensions(connectorId);
      loaded = true;
    } catch (e) {
      toastError("Load extensions failed", e instanceof Error ? e.message : String(e));
    } finally {
      loading = false;
    }
  }

  async function upload(file: File) {
    if (busy) return;
    busy = true;
    try {
      const ext = await uploadBrowserExtension(connectorId, file);
      toastOk(`Installed ${ext.name || ext.id}`);
      await refresh();
    } catch (e) {
      toastError("Install failed", e instanceof Error ? e.message : String(e));
    } finally {
      busy = false;
    }
  }

  function onDrop(e: DragEvent) {
    e.preventDefault();
    dragOver = false;
    const f = e.dataTransfer?.files?.[0];
    if (f) upload(f);
  }

  function onPick(e: Event) {
    const f = (e.target as HTMLInputElement).files?.[0];
    if (f) upload(f);
    if (fileEl) fileEl.value = ""; // allow re-picking the same file
  }

  async function addFromStore() {
    const sid = storeId.trim();
    if (!sid || busy) return;
    busy = true;
    try {
      const ext = await addBrowserExtensionFromStore(connectorId, sid);
      toastOk(`Added ${ext.name || ext.id}`);
      storeId = "";
      await refresh();
    } catch (e) {
      toastError("Add failed", e instanceof Error ? e.message : String(e));
    } finally {
      busy = false;
    }
  }

  async function remove(extId: string) {
    if (busy) return;
    busy = true;
    try {
      await removeBrowserExtension(connectorId, extId);
      toastOk("Removed");
      await refresh();
    } catch (e) {
      toastError("Remove failed", e instanceof Error ? e.message : String(e));
    } finally {
      busy = false;
    }
  }

  function fmtSize(n: number): string {
    if (n < 1024) return `${n} B`;
    if (n < 1024 * 1024) return `${(n / 1024).toFixed(0)} KB`;
    return `${(n / 1024 / 1024).toFixed(1)} MB`;
  }

  $effect(() => {
    refresh();
  });
</script>

<section class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-sm">
  <div class="flex items-start justify-between gap-4 px-5 py-4">
    <div>
      <h2 class="text-base font-semibold text-black-900 dark:text-white-100">Extensions</h2>
      <p class="mt-1 text-sm text-black-800 dark:text-black-600">
        Chrome extensions loaded into this connector's live sessions. Applies to <strong>new</strong> sessions; any installed extension forces sessions to run headed.
      </p>
    </div>
    <button
      type="button"
      class="shrink-0 rounded-lg border border-white-400 dark:border-navy-600 px-3 py-1.5 text-xs font-medium text-black-700 dark:text-white-200 hover:border-green-400 transition-colors disabled:opacity-50"
      disabled={loading}
      onclick={refresh}
    >{loading ? "Loading…" : "Refresh"}</button>
  </div>

  <div class="border-t border-white-300 dark:border-navy-600 px-5 py-4 space-y-3">
    <!-- Upload: drag-drop + file picker -->
    <div
      role="button"
      tabindex="0"
      class={"rounded-lg border-2 border-dashed px-4 py-6 text-center transition-colors cursor-pointer " +
        (dragOver ? "border-green-500 bg-green-50 dark:bg-green-900/20" : "border-white-400 dark:border-navy-600 hover:border-green-400")}
      ondragover={(e) => { e.preventDefault(); dragOver = true; }}
      ondragleave={() => (dragOver = false)}
      ondrop={onDrop}
      onclick={() => fileEl?.click()}
      onkeydown={(e) => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); fileEl?.click(); } }}
    >
      <p class="text-sm text-black-800 dark:text-white-200">Drop a <span class="font-mono">.zip</span> or <span class="font-mono">.crx</span> here, or click to choose</p>
      <p class="mt-1 text-[11px] text-black-600 dark:text-black-500">{busy ? "Working…" : "Unpacked extension archive"}</p>
      <input bind:this={fileEl} type="file" accept=".zip,.crx" class="hidden" onchange={onPick} />
    </div>

    <!-- Add from Chrome Web Store by id -->
    <div class="flex items-center gap-2">
      <input
        class="flex-1 rounded-lg border border-white-400 dark:border-navy-600 bg-white-50 dark:bg-navy-800 px-3 py-2 text-sm text-black-900 dark:text-white-100 focus:border-green-500 focus:outline-none"
        bind:value={storeId}
        onkeydown={(e) => { if (e.key === "Enter") addFromStore(); }}
        placeholder="Chrome Web Store extension id (32 letters)"
      />
      <button
        type="button"
        class="rounded-lg bg-green-500 px-3 py-2 text-sm font-medium text-white-100 hover:bg-green-600 active:bg-green-700 transition-colors disabled:opacity-50"
        disabled={busy || !storeId.trim()}
        onclick={addFromStore}
      >Add</button>
    </div>

    <!-- List -->
    {#if !loaded && loading}
      <p class="text-sm text-black-700 dark:text-black-600">Loading…</p>
    {:else if extensions.length === 0}
      <p class="text-sm text-black-700 dark:text-black-600">No extensions installed.</p>
    {:else}
      <div class="space-y-2">
        {#each extensions as x (x.id)}
          <div class="flex items-center gap-3 rounded-lg border border-white-300 dark:border-navy-600 bg-white-50 dark:bg-navy-800 px-4 py-2.5">
            <div class="min-w-0">
              <p class="truncate text-sm font-medium text-black-900 dark:text-white-100">{x.name || x.id}</p>
              <p class="truncate font-mono text-[11px] text-black-600 dark:text-black-500">{x.id}{x.version ? ` · v${x.version}` : ""} · {fmtSize(x.size)}</p>
            </div>
            <button
              type="button"
              class="ml-auto shrink-0 text-[11px] font-medium text-neg-400 hover:underline disabled:opacity-50"
              disabled={busy}
              onclick={() => remove(x.id)}
            >Remove</button>
          </div>
        {/each}
      </div>
    {/if}
  </div>
</section>
