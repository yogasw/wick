<script lang="ts">
  import { onMount } from "svelte";
  import { ConfirmDialog } from "@wick-fe/common-ui";
  import { toastOk, toastError } from "@wick-fe/common-stores";
  import { listSkills, postMutation } from "$lib/api.js";
  import type { SkillListResponse, SkillListItem } from "$lib/types.js";

  type Props = {
    onNavigate: (name: string) => void;
  };
  let { onNavigate }: Props = $props();

  let data = $state<SkillListResponse | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let confirmDelete = $state<SkillListItem | null>(null);
  let uploading = $state(false);
  let showUpload = $state(false);
  let fileInput = $state<HTMLInputElement | null>(null);

  async function load() {
    loading = true;
    error = null;
    try {
      data = await listSkills();
    } catch (e) {
      error = e instanceof Error ? e.message : "Failed to load skills";
    } finally {
      loading = false;
    }
  }

  async function syncAll() {
    try {
      await postMutation("/skills/sync");
      toastOk("Synced all skills");
      await load();
    } catch (e) {
      toastError(e instanceof Error ? e.message : "Sync failed");
    }
  }

  async function doDelete(skill: SkillListItem) {
    confirmDelete = null;
    try {
      await postMutation(`/skills/${skill.name}/delete`);
      toastOk(`Deleted ${skill.name}`);
      await load();
    } catch (e) {
      toastError(e instanceof Error ? e.message : "Delete failed");
    }
  }

  async function handleUpload(e: Event) {
    const form = e.currentTarget as HTMLFormElement;
    const fd = new FormData(form);
    uploading = true;
    try {
      const resp = await fetch("/skills/upload", { method: "POST", body: fd, redirect: "manual" });
      if (resp.type === "opaqueredirect" || resp.status === 303 || resp.ok) {
        toastOk("Uploaded successfully");
        showUpload = false;
        await load();
      } else {
        toastError("Upload failed");
      }
    } catch (e) {
      toastError(e instanceof Error ? e.message : "Upload failed");
    } finally {
      uploading = false;
    }
  }

  function dirLabel(dir: string): string {
    const clean = dir.replace(/\\/g, "/").replace(/\/$/, "");
    const parts = clean.split("/");
    for (let i = parts.length - 1; i >= 0; i--) {
      const seg = parts[i].replace(/^\./, "");
      if (seg && seg !== "skills") return seg;
    }
    return dir;
  }

  onMount(load);
</script>

<div class="space-y-6">
  <div class="flex items-center justify-between gap-3 flex-wrap">
    <div>
      <h1 class="text-lg font-semibold text-black-900 dark:text-white-100">Skills</h1>
      <p class="text-xs text-black-700 dark:text-black-600 mt-0.5">Skill files are synced across all agent dirs. Upload once → available everywhere.</p>
    </div>
    <div class="flex items-center gap-2">
      <button
        onclick={syncAll}
        class="rounded-lg border border-white-400 dark:border-navy-600 px-3 py-2 text-xs font-medium text-black-800 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800"
      >Sync All</button>
      <button
        onclick={() => { showUpload = true; }}
        class="rounded-lg bg-green-500 px-4 py-2 text-sm font-medium text-white-100 hover:bg-green-600 active:bg-green-700 transition-colors"
      >+ Upload Skill</button>
    </div>
  </div>

  {#if loading}
    <div class="text-sm text-black-600 dark:text-black-500">Loading…</div>
  {:else if error}
    <div class="rounded-lg border border-red-300 dark:border-red-700 bg-red-50 dark:bg-red-900/20 px-4 py-3 text-sm text-red-700 dark:text-red-400">{error}</div>
  {:else if data}
    {#if data.dirs.length > 0}
      <div class="flex flex-wrap gap-2 items-center">
        <span class="text-xs text-black-700 dark:text-black-600">Skill dirs:</span>
        {#each data.dirs as dir}
          <span class="rounded-full border border-white-400 dark:border-navy-600 px-2.5 py-0.5 font-mono text-xs text-black-800 dark:text-black-600" title={dir}>{dirLabel(dir)}</span>
        {/each}
      </div>
    {:else}
      <div class="rounded-lg border border-amber-300 dark:border-amber-700 bg-amber-50 dark:bg-amber-900/20 px-4 py-3 text-sm text-amber-700 dark:text-amber-400">
        No skill directories found. Create at least one of: <code class="font-mono">~/.agents/skills</code>, <code class="font-mono">~/.claude/skills</code>.
      </div>
    {/if}

    {#if data.skills.length === 0 && data.dirs.length > 0}
      <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-6 py-12 text-center text-sm text-black-700 dark:text-black-600">
        No skill files yet. Upload one above.
      </div>
    {:else if data.skills.length > 0}
      <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-sm overflow-hidden">
        <table class="w-full text-sm">
          <thead>
            <tr class="border-b border-white-300 dark:border-navy-600 text-xs font-medium text-black-700 dark:text-black-600 uppercase tracking-wide">
              <th class="px-5 py-3 text-left">Name</th>
              <th class="px-5 py-3 text-left">Present In</th>
              <th class="px-5 py-3 text-left">Status</th>
              <th class="px-5 py-3 text-right">Actions</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-white-300 dark:divide-navy-600">
            {#each data.skills as skill}
              <tr
                class="cursor-pointer hover:bg-white-200 dark:hover:bg-navy-800 transition-colors"
                onclick={() => onNavigate(skill.name)}
              >
                <td class="px-5 py-3">
                  <div class="flex items-center gap-2">
                    {#if skill.is_dir}
                      <svg class="w-4 h-4 text-amber-500 shrink-0" fill="currentColor" viewBox="0 0 20 20"><path d="M2 6a2 2 0 012-2h5l2 2h5a2 2 0 012 2v6a2 2 0 01-2 2H4a2 2 0 01-2-2V6z"/></svg>
                      <span class="font-mono text-xs text-black-900 dark:text-white-100">{skill.name}/</span>
                    {:else}
                      <svg class="w-4 h-4 text-black-500 dark:text-black-600 shrink-0" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" d="M9 12h6m-6 4h6m2 5H7a2 2 0 01-2-2V5a2 2 0 012-2h5.586a1 1 0 01.707.293l5.414 5.414a1 1 0 01.293.707V19a2 2 0 01-2 2z"/></svg>
                      <span class="font-mono text-xs text-black-900 dark:text-white-100">{skill.name}</span>
                    {/if}
                  </div>
                </td>
                <td class="px-5 py-3">
                  <div class="flex flex-wrap gap-1">
                    {#each skill.in_dirs as dir}
                      <span class="rounded-full bg-green-100 dark:bg-green-900/30 border border-green-300 dark:border-green-700 px-2 py-0.5 text-xs font-medium text-green-700 dark:text-green-400" title={dir}>{dirLabel(dir)}</span>
                    {/each}
                    {#each skill.missing_dirs as dir}
                      <span class="rounded-full bg-white-300 dark:bg-navy-600 border border-white-400 dark:border-navy-500 px-2 py-0.5 text-xs text-black-600 dark:text-black-500 line-through" title="{dir} (missing)">{dirLabel(dir)}</span>
                    {/each}
                  </div>
                </td>
                <td class="px-5 py-3">
                  {#if skill.missing_dirs.length === 0}
                    <span class="inline-flex items-center gap-1 text-xs text-green-600 dark:text-green-400">
                      <svg class="w-3 h-3" fill="currentColor" viewBox="0 0 20 20"><path fill-rule="evenodd" d="M16.707 5.293a1 1 0 010 1.414l-8 8a1 1 0 01-1.414 0l-4-4a1 1 0 011.414-1.414L8 12.586l7.293-7.293a1 1 0 011.414 0z" clip-rule="evenodd"/></svg>
                      synced
                    </span>
                  {:else}
                    <span class="inline-flex items-center gap-1 text-xs text-amber-600 dark:text-amber-400">
                      <svg class="w-3 h-3" fill="currentColor" viewBox="0 0 20 20"><path fill-rule="evenodd" d="M8.257 3.099c.765-1.36 2.722-1.36 3.486 0l5.58 9.92c.75 1.334-.213 2.98-1.742 2.98H4.42c-1.53 0-2.493-1.646-1.743-2.98l5.58-9.92zM11 13a1 1 0 11-2 0 1 1 0 012 0zm-1-8a1 1 0 00-1 1v3a1 1 0 002 0V6a1 1 0 00-1-1z" clip-rule="evenodd"/></svg>
                      missing {skill.missing_dirs.length} dir(s)
                    </span>
                  {/if}
                </td>
                <td class="px-5 py-3 text-right" onclick={(e) => e.stopPropagation()}>
                  <button
                    class="text-xs text-red-600 dark:text-red-400 hover:underline"
                    onclick={() => { confirmDelete = skill; }}
                  >Delete</button>
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  {/if}
</div>

{#if showUpload}
  <div class="fixed inset-0 z-50 flex items-center justify-center bg-black/50">
    <div class="w-full max-w-lg rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-xl p-6">
      <div class="flex items-center justify-between mb-4">
        <h2 class="text-base font-semibold text-black-900 dark:text-white-100">Upload Skill File</h2>
        <button aria-label="Close" onclick={() => { showUpload = false; }} class="text-black-600 dark:text-black-500 hover:text-black-900 dark:hover:text-white-100">
          <svg class="w-5 h-5" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12"/></svg>
        </button>
      </div>
      <form onsubmit={handleUpload} class="space-y-4">
        <div>
          <label for="upload-skill-file" class="block text-xs font-medium text-black-800 dark:text-black-600 mb-1">File</label>
          <input id="upload-skill-file" bind:this={fileInput} type="file" name="file" accept=".md,.txt,.zip,.skills" required class="block w-full text-sm text-black-800 dark:text-black-600 file:mr-3 file:py-1.5 file:px-3 file:rounded file:border file:border-white-400 dark:file:border-navy-600 file:text-xs file:bg-white-200 dark:file:bg-navy-800 file:text-black-800 dark:file:text-black-600"/>
        </div>
        <div class="flex justify-end gap-2">
          <button type="button" onclick={() => { showUpload = false; }} class="rounded-lg border border-white-400 dark:border-navy-600 px-3 py-2 text-sm text-black-800 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800">Cancel</button>
          <button type="submit" disabled={uploading} class="rounded-lg bg-green-500 px-4 py-2 text-sm font-medium text-white-100 hover:bg-green-600 disabled:opacity-50">{uploading ? "Uploading…" : "Upload & Sync"}</button>
        </div>
      </form>
    </div>
  </div>
{/if}

<ConfirmDialog
  open={confirmDelete !== null}
  title={`Delete ${confirmDelete?.name ?? ""}?`}
  body="This will remove the skill from all directories."
  confirmLabel="Delete"
  destructive={true}
  onConfirm={() => { if (confirmDelete) doDelete(confirmDelete); }}
  onCancel={() => { confirmDelete = null; }}
/>
