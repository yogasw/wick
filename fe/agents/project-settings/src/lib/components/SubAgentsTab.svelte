<script lang="ts">
  /* Project scope of sub-agent roles.
     Two groups from one fetch: what this project inherited from the global
     scope (read-only here) and what it owns (editable). A role the project
     defines under a global key shadows it — for sessions in this project
     only; the global role is untouched everywhere else. */
  import { onMount } from "svelte";
  import {
    deleteAgentProfile,
    emptyAgentProfile,
    listAgentProfiles,
    saveAgentProfile,
    shadowedKeys,
    type AgentProfile,
    type ProviderListItem,
    type TagOption,
  } from "@wick-fe/common-api";
  import {
    AgentModelQuickChange,
    AgentProfileEditor,
    AgentProfileRow,
    Button,
    ConfirmDialog,
  } from "@wick-fe/common-ui";
  import { toastError, toastOk } from "@wick-fe/common-stores";
  import { getProviderOptionModels } from "$lib/api.js";

  type Props = { projectID: string; base: string };
  let { projectID, base }: Props = $props();

  // Same lazy loader the project's provider picker uses, so a role can be
  // pinned to a wick live-set leaf here too rather than only to an instance.
  function loadProviderModels(optionValue: string, opts?: { entry?: string }) {
    const slash = optionValue.indexOf("/");
    const type = slash < 0 ? optionValue : optionValue.slice(0, slash);
    const name = slash < 0 ? optionValue : optionValue.slice(slash + 1);
    return getProviderOptionModels(type, name, opts);
  }

  let owned = $state<AgentProfile[]>([]);
  let inherited = $state<AgentProfile[]>([]);
  let tags = $state<TagOption[]>([]);
  let providerList = $state<ProviderListItem[]>([]);

  let editing = $state<AgentProfile | null>(null);
  let loading = $state(true);
  let saving = $state(false);
  let loadError = $state("");
  let formError = $state("");
  let pendingDelete = $state<AgentProfile | null>(null);
  let quickChange = $state<AgentProfile | null>(null);

  const shadowed = $derived(
    shadowedKeys({
      profiles: [],
      owned,
      inherited,
      tags,
      provider_list: providerList,
      is_admin: false,
    }),
  );
  // A global role this project has already overridden is not offered for
  // override again — its replacement is listed below, under this project.
  const availableToOverride = $derived(inherited.filter((p) => !shadowed.has(p.key)));

  async function load() {
    loading = true;
    loadError = "";
    try {
      const r = await listAgentProfiles(base, projectID);
      owned = r.owned;
      inherited = r.inherited;
      tags = r.tags;
      providerList = r.provider_list;
    } catch (e) {
      loadError = e instanceof Error ? e.message : String(e);
    } finally {
      loading = false;
    }
  }

  onMount(() => {
    void load();
  });

  async function save(p: AgentProfile) {
    saving = true;
    formError = "";
    try {
      await saveAgentProfile(base, { ...p, project_id: projectID });
      toastOk(`Saved ${p.key}`);
      editing = null;
      await load();
    } catch (e) {
      formError = e instanceof Error ? e.message : String(e);
    } finally {
      saving = false;
    }
  }

  async function confirmDelete() {
    const p = pendingDelete;
    pendingDelete = null;
    if (!p) return;
    try {
      await deleteAgentProfile(base, p.id);
      toastOk(`Deleted ${p.key}`);
      editing = null;
      await load();
    } catch (e) {
      toastError(e instanceof Error ? e.message : String(e));
    }
  }

  /* Override copies a global role into this project under the same key.
     It is the only way to create a shadow, so nobody produces one by
     reusing a key without meaning to. id is cleared so the save creates a
     new row rather than editing the global one. */
  function override(p: AgentProfile) {
    formError = "";
    editing = { ...p, id: "", project_id: projectID };
  }

  function startNew() {
    formError = "";
    editing = emptyAgentProfile(projectID);
  }

  /* Save straight from a row action (quick change, disable toggle) without
     opening the editor. The whole role is sent because the API replaces the
     row; only the field the action names is altered. */
  async function saveInline(p: AgentProfile, what: string) {
    saving = true;
    try {
      await saveAgentProfile(base, { ...p, project_id: projectID });
      toastOk(`${what} — ${p.name || p.key}`);
      quickChange = null;
      await load();
    } catch (e) {
      toastError(e instanceof Error ? e.message : String(e));
    } finally {
      saving = false;
    }
  }

  function toggleDisabled(p: AgentProfile) {
    void saveInline({ ...p, disabled: !p.disabled }, p.disabled ? "Enabled" : "Disabled");
  }
</script>

{#if loading}
  <p class="text-sm text-black-800 dark:text-black-600">Loading…</p>
{:else if loadError}
  <p class="text-sm text-rose-600 dark:text-rose-400">{loadError}</p>
{:else if editing}
  <div
    class="flex flex-col gap-4 rounded-xl border border-white-300 bg-white-100 p-6 shadow-sm dark:border-navy-600 dark:bg-navy-700"
  >
    <div class="flex items-center gap-2">
      <h2 class="text-sm font-semibold text-black-900 dark:text-white-100">
        {editing.id ? `Edit ${editing.key}` : "New agent"}
      </h2>
      {#if !editing.id && inherited.some((p) => p.key === editing?.key)}
        <span
          class="rounded px-1.5 py-0.5 text-[10px] font-medium text-cau-400 ring-1 ring-cau-400/40"
        >
          shadows global
        </span>
      {/if}
    </div>
    <AgentProfileEditor
      profile={editing}
      {tags}
      {providerList}
      {saving}
      error={formError}
      onsave={save}
      oncancel={() => (editing = null)}
      ondelete={(p) => (pendingDelete = p)}
    />
  </div>
{:else}
  <div class="flex flex-col gap-6">
    <!-- Panelled to match the General tab: these sections used to float on the
         page background, which read as unfinished next to the settings form. -->
    <section
      class="rounded-xl border border-white-300 bg-white-100 p-6 shadow-sm dark:border-navy-600 dark:bg-navy-700"
    >
      <div class="mb-1 flex items-center justify-between gap-3">
        <h2 class="text-sm font-semibold text-black-900 dark:text-white-100">This project</h2>
        <Button variant="secondary" size="sm" onclick={startNew}>+ New agent</Button>
      </div>
      <p class="mb-4 text-xs text-black-700 dark:text-black-600">
        Roles only sessions in this project can delegate to. Click a role to edit it; use
        ⋮ for its other actions.
      </p>
      {#if owned.length === 0}
        <p class="text-xs text-black-700 dark:text-black-600">
          No roles of its own yet.
        </p>
      {:else}
        <ul class="flex flex-col gap-2">
          {#each owned as p (p.id)}
            <AgentProfileRow
              profile={p}
              {providerList}
              loadModels={loadProviderModels}
              shadowsGlobal={shadowed.has(p.key)}
              onedit={(x) => {
                formError = "";
                editing = x;
              }}
              onchangeModel={(x) => (quickChange = x)}
              ontoggle={toggleDisabled}
              ondelete={(x) => (pendingDelete = x)}
            />
          {/each}
        </ul>
      {/if}
    </section>

    <section
      class="rounded-xl border border-white-300 bg-white-100 p-6 shadow-sm dark:border-navy-600 dark:bg-navy-700"
    >
      <h2 class="mb-1 text-sm font-semibold text-black-900 dark:text-white-100">
        Inherited from global
      </h2>
      <p class="mb-4 text-xs text-black-700 dark:text-black-600">
        Available here but owned globally. Override one to keep a project-specific copy —
        the global role stays untouched everywhere else.
      </p>
      {#if availableToOverride.length === 0}
        <p class="text-xs text-black-700 dark:text-black-600">
          {inherited.length === 0
            ? "No global roles defined yet."
            : "Every global role is overridden above."}
        </p>
      {:else}
        <ul class="flex flex-col gap-2">
          {#each availableToOverride as p (p.id)}
            <li>
              <div
                class="flex items-center gap-3 rounded-lg border border-white-300 bg-white-100 px-4 py-3 dark:border-navy-600 dark:bg-navy-800"
              >
                <span class="min-w-0 flex-1">
                  <span class="block truncate text-sm font-medium text-black-900 dark:text-white-100">
                    {p.name || p.key}
                  </span>
                  <span class="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1">
                    <span
                      class="shrink-0 rounded bg-white-200 px-1.5 py-0.5 font-mono text-[10px] text-black-800 dark:bg-navy-700 dark:text-black-600"
                    >
                      {p.provider.split("::")[0] || "no provider"}
                    </span>
                    {#if p.description}
                      <span class="min-w-0 truncate text-[11px] text-black-700 dark:text-black-600">
                        {p.description}
                      </span>
                    {/if}
                  </span>
                </span>
                <Button variant="secondary" size="sm" onclick={() => override(p)}>Override</Button>
              </div>
            </li>
          {/each}
        </ul>
      {/if}
    </section>
  </div>
{/if}

{#if quickChange}
  <AgentModelQuickChange
    profile={quickChange}
    {providerList}
    {saving}
    loadModels={loadProviderModels}
    onsave={(p) => saveInline(p, "Provider / model updated")}
    onclose={() => (quickChange = null)}
  />
{/if}

{#if pendingDelete}
  <ConfirmDialog
    open
    title="Delete role"
    body={`Delete "${pendingDelete.key}" from this project? If it shadows a global role, the global one takes effect again here.`}
    confirmLabel="Delete"
    destructive
    onConfirm={confirmDelete}
    onCancel={() => (pendingDelete = null)}
  />
{/if}
