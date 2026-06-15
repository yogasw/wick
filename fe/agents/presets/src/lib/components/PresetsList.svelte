<script lang="ts">
  import { ConfirmDialog, Button, TextInput, TextArea } from "@wick-fe/common-ui";
  import { toastOk, toastError } from "@wick-fe/common-stores";
  import { listPresets, createPreset, deletePreset } from "$lib/api.js";
  import type { PresetItem } from "$lib/types.js";

  type Props = { onNavigate: (name: string) => void };
  let { onNavigate }: Props = $props();

  let presets = $state<PresetItem[]>([]);
  let loading = $state(true);
  let error = $state("");

  let showCreate = $state(false);
  let createName = $state("");
  let createBody = $state("");
  let creating = $state(false);

  let deleteTarget = $state<string | null>(null);

  async function load() {
    loading = true;
    error = "";
    try {
      const resp = await listPresets();
      presets = resp.presets;
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      loading = false;
    }
  }

  async function handleCreate(e: SubmitEvent) {
    e.preventDefault();
    const name = createName.trim();
    if (!name) return;
    creating = true;
    try {
      await createPreset(name, createBody);
      showCreate = false;
      createName = "";
      createBody = "";
      toastOk("Preset created");
      await load();
      onNavigate(name);
    } catch (err) {
      toastError("Create failed", err instanceof Error ? err.message : String(err));
    } finally {
      creating = false;
    }
  }

  async function handleDelete() {
    if (!deleteTarget) return;
    const name = deleteTarget;
    deleteTarget = null;
    try {
      await deletePreset(name);
      toastOk(`Deleted "${name}"`);
      await load();
    } catch (err) {
      toastError("Delete failed", err instanceof Error ? err.message : String(err));
    }
  }

  $effect(() => { load(); });
</script>

<ConfirmDialog
  open={deleteTarget !== null}
  title={`Delete preset "${deleteTarget}"?`}
  body="This cannot be undone."
  confirmLabel="Delete"
  destructive={true}
  onConfirm={handleDelete}
  onCancel={() => { deleteTarget = null; }}
/>

{#if showCreate}
  <div class="fixed inset-0 z-50 flex items-center justify-center bg-black/50">
    <div class="w-full max-w-lg rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-6 shadow-xl mx-4">
      <h2 class="mb-4 text-base font-semibold text-black-900 dark:text-white-100">New Preset</h2>
      <form onsubmit={handleCreate} class="space-y-4">
        <div>
          <label for="create-name" class="block text-xs font-medium text-black-800 dark:text-black-600 mb-1">Name <span class="text-red-500">*</span></label>
          <TextInput
            id="create-name"
            required
            value={createName}
            onChange={(v) => (createName = v)}
            placeholder="e.g. reviewer"
          />
        </div>
        <div>
          <label for="create-body" class="block text-xs font-medium text-black-800 dark:text-black-600 mb-1">System prompt</label>
          <TextArea
            id="create-body"
            rows={8}
            maxlength={10000}
            value={createBody}
            onChange={(v) => (createBody = v)}
            placeholder="You are a code reviewer. Focus on clarity, performance, and correctness…"
          />
        </div>
        <div class="flex justify-end gap-3 pt-2">
          <Button variant="secondary" size="lg" onclick={() => { showCreate = false; }}>Cancel</Button>
          <Button type="submit" size="lg" disabled={creating}>{creating ? "Creating…" : "Create"}</Button>
        </div>
      </form>
    </div>
  </div>
{/if}

<div class="space-y-6">
  <div class="flex items-center justify-between">
    <h1 class="text-lg font-semibold text-black-900 dark:text-white-100">Presets</h1>
    <Button size="lg" onclick={() => { showCreate = true; }}>+ New Preset</Button>
  </div>

  {#if loading}
    <div class="px-5 py-12 text-center text-sm text-black-700 dark:text-black-600">Loading…</div>
  {:else if error}
    <div class="rounded-lg border border-red-300 dark:border-red-800 bg-red-50 dark:bg-red-900/20 px-4 py-3 text-sm text-red-700 dark:text-red-400">{error}</div>
  {:else}
    <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-sm overflow-visible">
      {#if presets.length === 0}
        <div class="px-5 py-12 text-center text-sm text-black-700 dark:text-black-600">No presets yet.</div>
      {:else}
        <ul class="divide-y divide-white-300 dark:divide-navy-600">
          {#each presets as preset (preset.name)}
            <li class="flex items-center justify-between px-5 py-4 hover:bg-white-200 dark:hover:bg-navy-800 transition-colors">
              <button
                onclick={() => onNavigate(preset.name)}
                class="font-medium text-black-900 dark:text-white-100 text-sm text-left flex-1"
              >{preset.name}</button>
              {#if !preset.is_default}
                <button
                  onclick={(e) => { e.stopPropagation(); deleteTarget = preset.name; }}
                  class="ml-3 rounded px-2 py-1 text-xs text-red-600 dark:text-red-400 hover:bg-red-50 dark:hover:bg-red-900/20 transition-colors"
                  title="Delete preset"
                >Delete</button>
              {/if}
            </li>
          {/each}
        </ul>
      {/if}
    </div>
  {/if}
</div>
