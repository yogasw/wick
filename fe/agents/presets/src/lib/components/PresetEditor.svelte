<script lang="ts">
  import { toastOk, toastError } from "@wick-fe/common-stores";
  import { Breadcrumb, type BreadcrumbItem } from "@wick-fe/common-ui";
  import { getPreset, updatePreset } from "$lib/api.js";

  type Props = { name: string; onBack: () => void };
  let { name, onBack }: Props = $props();

  let crumbs = $derived<BreadcrumbItem[]>([
    { label: "Presets", onClick: onBack },
    { label: name, truncate: true },
  ]);

  let body = $state("");
  let loading = $state(true);
  let saving = $state(false);
  let error = $state("");

  async function load() {
    loading = true;
    error = "";
    try {
      const p = await getPreset(name);
      body = p.body;
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      loading = false;
    }
  }

  async function handleSave(e: SubmitEvent) {
    e.preventDefault();
    saving = true;
    try {
      await updatePreset(name, body);
      toastOk("Saved");
    } catch (err) {
      toastError("Save failed", err instanceof Error ? err.message : String(err));
    } finally {
      saving = false;
    }
  }

  $effect(() => { load(); });
</script>

<div class="space-y-4">
  <Breadcrumb items={crumbs} />
  <h1 class="text-lg font-semibold text-black-900 dark:text-white-100">{name}</h1>

  {#if loading}
    <div class="text-sm text-black-700 dark:text-black-600">Loading…</div>
  {:else if error}
    <div class="rounded-lg border border-red-300 dark:border-red-800 bg-red-50 dark:bg-red-900/20 px-4 py-3 text-sm text-red-700 dark:text-red-400">{error}</div>
  {:else}
    <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-sm p-5">
      <form onsubmit={handleSave} class="space-y-4">
        <div>
          <label for="editor-body" class="block text-xs font-medium text-black-800 dark:text-black-600 mb-1">System prompt</label>
          <textarea
            id="editor-body"
            bind:value={body}
            rows={20}
            maxlength={10000}
            class="w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-sm text-black-900 dark:text-white-100 font-mono focus:border-green-500 focus:ring-2 focus:ring-green-200 dark:focus:ring-green-800 focus:outline-none resize-y"
          ></textarea>
        </div>
        <div class="flex justify-end">
          <button
            type="submit"
            disabled={saving}
            class="rounded-lg bg-green-500 px-6 py-2 text-sm font-medium text-white-100 hover:bg-green-600 active:bg-green-700 transition-colors disabled:opacity-50"
          >{saving ? "Saving…" : "Save"}</button>
        </div>
      </form>
    </div>
  {/if}
</div>
