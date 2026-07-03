<script lang="ts">
  import { ConfirmDialog } from "@wick-fe/common-ui";
  import { toastOk, toastError } from "@wick-fe/common-stores";
  import {
    getProjectSettings,
    updateProject,
    createProject,
    deleteProject,
    unpinSession,
  } from "$lib/api.js";
  import type { ProjectSettingsData } from "$lib/types.js";

  type Props = { projectID: string; base: string };
  let { projectID, base }: Props = $props();

  let data = $state<ProjectSettingsData | null>(null);
  let loading = $state(true);
  let error = $state("");
  let saving = $state(false);
  let showDeleteConfirm = $state(false);

  let name = $state("");
  let icon = $state("");
  let description = $state("");
  let folderMode = $state<"managed" | "custom">("managed");
  let customPath = $state("");
  let preset = $state("default");
  let provider = $state("");
  let systemAddon = $state("");

  // Promote a bare provider type ("claude") to its canonical default
  // instance key ("claude/claude"). Mirrors normalizeProviderKey on the
  // backend so the dropdown value round-trips to the spawn path. Empty
  // stays empty.
  function normalizeProviderKey(key: string): string {
    if (!key) return "";
    return key.includes("/") ? key : `${key}/${key}`;
  }

  // Build the provider dropdown from the healthy instances the backend
  // reports. Each option's value is the "type/name" key stored in
  // Defaults.Provider; the label drops the redundant name for the
  // canonical default (claude/claude → Claude). If the currently-saved
  // provider isn't in the list (instance deleted/renamed), surface it as
  // a trailing "(unavailable)" option so the form doesn't silently
  // change the saved value to something else.
  let providerOptions = $derived.by(() => {
    const list = data?.provider_list ?? [];
    const opts = list.map((p) => {
      const value = `${p.type}/${p.name}`;
      const label = p.name === p.type
        ? p.type.charAt(0).toUpperCase() + p.type.slice(1)
        : value;
      return { value, label };
    });
    if (provider && !opts.some((o) => o.value === provider)) {
      opts.push({ value: provider, label: `${provider} (unavailable)` });
    }
    return opts;
  });

  async function load() {
    loading = true;
    error = "";
    try {
      const d = await getProjectSettings(projectID);
      data = d;
      name = d.name;
      icon = d.icon;
      description = d.description;
      folderMode = d.managed ? "managed" : "custom";
      customPath = d.custom_path;
      preset = d.default_preset;
      // Normalize to the "type/name" key the spawn path expects. Older
      // projects stored a bare type (e.g. "claude"); promote it to the
      // canonical default instance "claude/claude". Empty stays empty so
      // the dropdown falls back to the first available instance.
      provider = normalizeProviderKey(d.default_provider);
      systemAddon = d.system_addon;
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      loading = false;
    }
  }

  async function handleSubmit(e: SubmitEvent) {
    e.preventDefault();
    if (!data) return;
    saving = true;
    try {
      if (data.is_new) {
        const redirectURL = await createProject({
          name: name.trim(),
          icon: icon.trim(),
          description,
          folder_mode: folderMode,
          custom_path: folderMode === "managed" ? "" : customPath.trim(),
          preset,
          provider,
          system_addon: systemAddon,
        });
        window.location.href = redirectURL;
        return;
      }
      await updateProject(projectID, {
        name: name.trim(),
        icon: icon.trim(),
        description,
        folder_mode: folderMode,
        custom_path: folderMode === "managed" ? "" : customPath.trim(),
        preset,
        provider,
        system_addon: systemAddon,
      });
      toastOk("Project saved");
      await load();
    } catch (err) {
      toastError("Save failed", err instanceof Error ? err.message : String(err));
    } finally {
      saving = false;
    }
  }

  async function handleDelete() {
    showDeleteConfirm = false;
    try {
      await deleteProject(projectID);
      toastOk("Project deleted");
      window.location.href = `${base}/sessions`;
    } catch (err) {
      toastError("Delete failed", err instanceof Error ? err.message : String(err));
    }
  }

  async function handleUnpin(sessionID: string) {
    if (!data) return;
    try {
      await unpinSession(projectID, sessionID);
      toastOk("Unpinned");
      await load();
    } catch (err) {
      toastError("Unpin failed", err instanceof Error ? err.message : String(err));
    }
  }

  function backHref(): string {
    if (data && !data.is_new && data.id) {
      return `${base}/sessions?project=${data.id}`;
    }
    return `${base}/sessions`;
  }

  $effect(() => { load(); });
</script>

<ConfirmDialog
  open={showDeleteConfirm}
  title="Delete project?"
  body="All sessions in this project will be moved to the default project. This cannot be undone."
  confirmLabel="Delete"
  destructive={true}
  onConfirm={handleDelete}
  onCancel={() => { showDeleteConfirm = false; }}
/>

{#if loading}
  <div class="px-5 py-12 text-center text-sm text-black-700 dark:text-black-600">Loading…</div>
{:else if error}
  <div class="rounded-lg border border-red-300 dark:border-red-800 bg-red-50 dark:bg-red-900/20 px-4 py-3 text-sm text-red-700 dark:text-red-400">{error}</div>
{:else if data}
  <div class="max-w-5xl mx-auto space-y-5">
    <a href={backHref()} class="text-xs text-black-600 dark:text-black-700 hover:text-black-900 dark:hover:text-white-100 transition-colors">← Back</a>

    <form onsubmit={handleSubmit} class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-sm p-6 space-y-6">
      <!-- Header: icon + name + meta + delete -->
      <div class="flex items-center gap-3 pb-4 border-b border-white-300 dark:border-navy-600">
        <input
          type="text"
          maxlength="2"
          bind:value={icon}
          class="w-12 h-12 rounded-xl bg-white-200 dark:bg-navy-800 text-center text-2xl outline-none border border-transparent focus:border-green-400"
        />
        <div class="min-w-0">
          <input
            type="text"
            required
            bind:value={name}
            placeholder="Project name"
            class="text-2xl font-bold bg-transparent text-black-900 dark:text-white-100 outline-none border-b border-transparent focus:border-green-400 w-full"
          />
          {#if !data.is_new}
            <p class="text-xs text-black-600 dark:text-black-700">{data.chat_count} chats · created {data.created_at}</p>
          {/if}
        </div>
        {#if !data.is_new && !data.is_default}
          <button
            type="button"
            onclick={() => { showDeleteConfirm = true; }}
            class="ml-auto text-xs px-3 py-1.5 bg-red-50 dark:bg-red-900/20 text-red-600 dark:text-red-400 rounded-md border border-red-200 dark:border-red-800 hover:bg-red-100 dark:hover:bg-red-900/40 transition-colors"
          >Delete project</button>
        {/if}
      </div>

      <div class="grid grid-cols-1 md:grid-cols-2 gap-6">
        <!-- Left: folder + defaults -->
        <div class="space-y-6">
          <div>
            <h4 class="font-bold text-sm mb-2 text-black-900 dark:text-white-100">Folder</h4>
            <div class="border border-white-300 dark:border-navy-600 rounded-lg p-3 space-y-2">
              <label class="flex items-center gap-2 text-sm cursor-pointer">
                <input
                  type="radio"
                  name="folder_mode"
                  value="custom"
                  checked={folderMode === "custom"}
                  onchange={() => { folderMode = "custom"; }}
                  class="text-green-500 focus:ring-green-500"
                />
                <span class="font-semibold text-black-900 dark:text-white-100">Custom path</span>
                <span class="rounded bg-amber-100 dark:bg-amber-900/30 text-amber-800 dark:text-amber-300 px-1.5 py-0.5 text-[10px] font-bold uppercase">custom</span>
              </label>
              {#if folderMode === "custom"}
                <div>
                  <div class="flex gap-2">
                    <input
                      type="text"
                      bind:value={customPath}
                      placeholder="D:/code/work/wick"
                      class="flex-1 rounded-md border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-2 py-1.5 text-xs font-mono text-black-900 dark:text-white-100 focus:border-green-500 focus:outline-none"
                    />
                    <label class="rounded-md border border-white-400 dark:border-navy-600 px-3 py-1.5 text-xs font-medium text-black-800 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800 cursor-pointer transition-colors whitespace-nowrap">
                      Choose…
                      <input
                        type="file"
                        class="hidden"
                        onchange={(e) => {
                          const f = (e.currentTarget as HTMLInputElement).files?.[0];
                          if (f) customPath = f.name;
                        }}
                      />
                    </label>
                  </div>
                  <p class="text-xs text-black-600 dark:text-black-700 mt-1">Absolute path to an existing folder. Wick uses this as the agent cwd. Browsers hide absolute paths — the picker fills only the folder name, prefix the parent manually.</p>
                </div>
              {/if}
              <label class="flex items-center gap-2 text-sm cursor-pointer mt-3 pt-3 border-t border-white-300 dark:border-navy-600">
                <input
                  type="radio"
                  name="folder_mode"
                  value="managed"
                  checked={folderMode === "managed"}
                  onchange={() => { folderMode = "managed"; }}
                  class="text-green-500 focus:ring-green-500"
                />
                <span class="font-semibold text-black-900 dark:text-white-100">Managed</span>
                <span class="rounded bg-indigo-100 dark:bg-indigo-900/30 text-indigo-700 dark:text-indigo-300 px-1.5 py-0.5 text-[10px] font-bold uppercase">managed</span>
              </label>
              <p class="text-xs text-black-600 dark:text-black-700">Wick creates and owns the folder at <code class="font-mono">projects/&lt;id&gt;/files/</code>. Useful for scratch sessions.</p>
            </div>
          </div>

          <div>
            <h4 class="font-bold text-sm mb-2 text-black-900 dark:text-white-100">Defaults</h4>
            <div class="space-y-3">
              <div>
                <label for="ps-provider" class="block text-black-600 dark:text-black-700 text-xs mb-0.5">Provider</label>
                <select
                  id="ps-provider"
                  bind:value={provider}
                  class="w-full rounded-md border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-2 py-1.5 text-sm text-black-900 dark:text-white-100 focus:border-green-500 focus:outline-none"
                >
                  {#each providerOptions as opt (opt.value)}
                    <option value={opt.value}>{opt.label}</option>
                  {/each}
                </select>
              </div>
              <div>
                <label for="ps-preset" class="block text-black-600 dark:text-black-700 text-xs mb-0.5">Preset</label>
                <select
                  id="ps-preset"
                  bind:value={preset}
                  class="w-full rounded-md border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-2 py-1.5 text-sm text-black-900 dark:text-white-100 focus:border-green-500 focus:outline-none"
                >
                  <option value="default">default</option>
                  {#each (data.preset_list ?? []).filter(p => p !== "default") as p (p)}
                    <option value={p}>{p}</option>
                  {/each}
                </select>
              </div>
              <div>
                <label for="ps-system-addon" class="block text-black-600 dark:text-black-700 text-xs mb-0.5">System prompt addon</label>
                <textarea
                  id="ps-system-addon"
                  bind:value={systemAddon}
                  rows={3}
                  placeholder="Appended to preset system prompt for every session..."
                  class="w-full rounded-md border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 p-2 text-xs text-black-900 dark:text-white-100 focus:border-green-500 focus:outline-none resize-none"
                ></textarea>
              </div>
              <div>
                <label for="ps-description" class="block text-black-600 dark:text-black-700 text-xs mb-0.5">Description</label>
                <input
                  id="ps-description"
                  type="text"
                  bind:value={description}
                  placeholder="Short description"
                  class="w-full rounded-md border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-2 py-1.5 text-sm text-black-900 dark:text-white-100 focus:border-green-500 focus:outline-none"
                />
              </div>
            </div>
          </div>
        </div>

        <!-- Right: pinned + meta preview + folder semantics -->
        <div class="space-y-6">
          {#if !data.is_new}
            <div>
              <h4 class="font-bold text-sm mb-2 text-black-900 dark:text-white-100">📌 Pinned sessions</h4>
              <div class="space-y-1">
                {#if data.pinned.length === 0}
                  <p class="text-xs text-black-600 dark:text-black-700 italic px-2 py-1">No pinned sessions. Pin one from a chat's menu.</p>
                {:else}
                  {#each data.pinned as pin (pin.id)}
                    <div class="flex items-center gap-2 rounded-md border border-white-300 dark:border-navy-600 px-3 py-2 text-sm text-black-900 dark:text-white-100">
                      <span class="flex-1 min-w-0 truncate">{pin.label}</span>
                      <button
                        type="button"
                        onclick={() => handleUnpin(pin.id)}
                        class="text-black-500 dark:text-black-600 hover:text-red-500 transition-colors"
                        title="Unpin"
                      >✕</button>
                    </div>
                  {/each}
                {/if}
              </div>
            </div>

            <div>
              <h4 class="font-bold text-sm mb-2 text-black-900 dark:text-white-100">Project meta preview</h4>
              <pre class="text-xs bg-white-200 dark:bg-navy-800 border border-white-300 dark:border-navy-600 rounded-md p-3 overflow-x-auto text-black-800 dark:text-black-600">{data.meta_json}</pre>
            </div>
          {/if}

          <div>
            <h4 class="font-bold text-sm mb-2 text-black-900 dark:text-white-100">Folder change semantics</h4>
            <ul class="list-disc pl-5 text-xs text-black-600 dark:text-black-700 space-y-1">
              <li>Managed → custom: managed <code class="font-mono">files/</code> kept on disk (orphaned backup; delete manually)</li>
              <li>Custom → managed: new managed dir created; custom path untouched</li>
              <li>Live sessions: cwd shifts at next spawn; running subprocess unaffected until restart</li>
            </ul>
          </div>
        </div>
      </div>

      <div class="flex justify-end gap-3 pt-2 border-t border-white-300 dark:border-navy-600">
        <a
          href={backHref()}
          class="rounded-lg border border-white-400 dark:border-navy-600 px-4 py-2 text-sm text-black-800 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800 transition-colors"
        >Cancel</a>
        {#if data.is_new}
          <button
            type="submit"
            disabled={saving}
            class="rounded-lg bg-green-500 px-5 py-2 text-sm font-medium text-white-100 hover:bg-green-600 transition-colors disabled:opacity-50"
          >{saving ? "Creating…" : "Create project"}</button>
        {:else}
          <button
            type="submit"
            disabled={saving}
            class="rounded-lg bg-green-500 px-5 py-2 text-sm font-medium text-white-100 hover:bg-green-600 transition-colors disabled:opacity-50"
          >{saving ? "Saving…" : "Save"}</button>
        {/if}
      </div>
    </form>
  </div>
{/if}
