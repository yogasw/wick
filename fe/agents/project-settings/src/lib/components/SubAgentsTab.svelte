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
    type TagOption,
  } from "@wick-fe/common-api";
  import { AgentProfileEditor, Button, ConfirmDialog } from "@wick-fe/common-ui";
  import { toastError, toastOk } from "@wick-fe/common-stores";

  type Props = { projectID: string; base: string };
  let { projectID, base }: Props = $props();

  let owned = $state<AgentProfile[]>([]);
  let inherited = $state<AgentProfile[]>([]);
  let tags = $state<TagOption[]>([]);
  let providers = $state<string[]>([]);

  let editing = $state<AgentProfile | null>(null);
  let loading = $state(true);
  let saving = $state(false);
  let loadError = $state("");
  let formError = $state("");
  let pendingDelete = $state<AgentProfile | null>(null);

  const shadowed = $derived(
    shadowedKeys({ profiles: [], owned, inherited, tags, providers, is_admin: false }),
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
      providers = r.providers;
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
</script>

{#if loading}
  <p class="text-sm text-black-800 dark:text-black-600">Loading…</p>
{:else if loadError}
  <p class="text-sm text-rose-600 dark:text-rose-400">{loadError}</p>
{:else if editing}
  <div class="flex flex-col gap-4">
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
      {providers}
      {saving}
      error={formError}
      onsave={save}
      oncancel={() => (editing = null)}
      ondelete={(p) => (pendingDelete = p)}
    />
  </div>
{:else}
  <div class="flex flex-col gap-8">
    <section class="flex flex-col gap-2">
      <h2 class="text-xs font-semibold uppercase tracking-wider text-black-700 dark:text-black-600">
        Inherited from global
      </h2>
      {#if availableToOverride.length === 0}
        <p class="text-xs text-black-700 dark:text-black-600">
          {inherited.length === 0
            ? "No global roles defined yet."
            : "Every global role is overridden below."}
        </p>
      {:else}
        <ul class="flex flex-col gap-1">
          {#each availableToOverride as p (p.id)}
            <li
              class="flex items-center gap-3 rounded-lg border border-white-300 px-3 py-2 dark:border-navy-600"
            >
              <span class="min-w-0 flex-1">
                <span class="block truncate text-sm text-black-900 dark:text-white-100">
                  {p.name || p.key}
                </span>
                <span class="block truncate text-[11px] text-black-700 dark:text-black-600">
                  {p.provider}{p.description ? ` · ${p.description}` : ""}
                </span>
              </span>
              <Button variant="secondary" size="sm" onclick={() => override(p)}>Override</Button>
            </li>
          {/each}
        </ul>
      {/if}
    </section>

    <section class="flex flex-col gap-2">
      <div class="flex items-center justify-between">
        <h2
          class="text-xs font-semibold uppercase tracking-wider text-black-700 dark:text-black-600"
        >
          This project
        </h2>
        <button
          type="button"
          class="text-[11px] font-semibold text-green-600 transition-colors hover:text-green-500 dark:text-green-400"
          onclick={startNew}
        >
          + New agent
        </button>
      </div>
      {#if owned.length === 0}
        <p class="text-xs text-black-700 dark:text-black-600">
          No roles of its own yet. Roles added here are visible only inside this
          project.
        </p>
      {:else}
        <ul class="flex flex-col gap-1">
          {#each owned as p (p.id)}
            <li
              class="flex items-center gap-3 rounded-lg border border-white-300 px-3 py-2 dark:border-navy-600"
            >
              <span class="min-w-0 flex-1">
                <span class="flex items-center gap-2">
                  <span class="truncate text-sm text-black-900 dark:text-white-100">
                    {p.name || p.key}
                  </span>
                  {#if shadowed.has(p.key)}
                    <span
                      class="shrink-0 rounded px-1.5 py-0.5 text-[10px] font-medium text-cau-400 ring-1 ring-cau-400/40"
                    >
                      shadows global
                    </span>
                  {/if}
                  {#if p.disabled}
                    <span
                      class="shrink-0 rounded px-1.5 py-0.5 text-[10px] font-medium text-black-700 ring-1 ring-white-400 dark:text-black-600 dark:ring-navy-600"
                    >
                      Disabled
                    </span>
                  {/if}
                </span>
                <span class="block truncate text-[11px] text-black-700 dark:text-black-600">
                  {p.provider}{p.description ? ` · ${p.description}` : ""}
                </span>
              </span>
              <Button
                variant="secondary"
                size="sm"
                onclick={() => {
                  formError = "";
                  editing = p;
                }}
              >
                Edit
              </Button>
              <Button variant="danger" size="sm" onclick={() => (pendingDelete = p)}>Delete</Button>
            </li>
          {/each}
        </ul>
      {/if}
    </section>
  </div>
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
