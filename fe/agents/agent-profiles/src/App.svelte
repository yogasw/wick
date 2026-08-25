<script lang="ts">
  /* Sub-agents — the GLOBAL scope of agent roles.
     Project-scoped roles are edited from Project settings › Sub-agents;
     this page deliberately shows only what every project inherits. */
  import {
    deleteAgentProfile,
    emptyAgentProfile,
    listAgentProfiles,
    saveAgentProfile,
    type AgentProfile,
    type ProviderListItem,
    type TagOption,
  } from "@wick-fe/common-api";
  import {
    AgentModelQuickChange,
    AgentProfileEditor,
    ConfirmDialog,
    ToastHost,
  } from "@wick-fe/common-ui";
  import { toastError, toastOk } from "@wick-fe/common-stores";
  import { onMount } from "svelte";
  import ProfileList from "$lib/components/ProfileList.svelte";

  const base = document.getElementById("app")?.dataset.base ?? "";

  // Lazy model loader so a role can be pinned to a wick live-set leaf, not
  // just to an instance. Failure yields an empty list — the picker then shows
  // no extra models rather than breaking the page.
  async function loadProviderModels(optionValue: string, opts?: { entry?: string }) {
    const slash = optionValue.indexOf("/");
    const type = slash < 0 ? optionValue : optionValue.slice(0, slash);
    const name = slash < 0 ? optionValue : optionValue.slice(slash + 1);
    const q = opts?.entry ? `?entry=${encodeURIComponent(opts.entry)}` : "";
    try {
      const resp = await fetch(
        `${base}/providers/options/${encodeURIComponent(type)}/${encodeURIComponent(name)}/models${q}`,
        { credentials: "same-origin", headers: { Accept: "application/json" } },
      );
      if (!resp.ok) return [];
      const body = (await resp.json()) as { models?: unknown[] | null };
      return (body.models ?? []) as never[];
    } catch {
      return [];
    }
  }

  let profiles = $state<AgentProfile[]>([]);
  let tags = $state<TagOption[]>([]);
  let providerList = $state<ProviderListItem[]>([]);
  let isAdmin = $state(false);

  let selected = $state<AgentProfile | null>(null);
  let loading = $state(true);
  let saving = $state(false);
  let loadError = $state("");
  let formError = $state("");
  let pendingDelete = $state<AgentProfile | null>(null);
  let quickChange = $state<AgentProfile | null>(null);

  async function load(selectKey = "") {
    loading = true;
    loadError = "";
    try {
      const r = await listAgentProfiles(base);
      profiles = r.profiles;
      tags = r.tags;
      providerList = r.provider_list;
      isAdmin = r.is_admin;
      // Keep the editor pointed at the same role across a reload, so
      // saving does not bounce the user back to an empty pane.
      const want = selectKey || selected?.key || "";
      selected = profiles.find((p) => p.key === want) ?? profiles[0] ?? null;
    } catch (e) {
      loadError = e instanceof Error ? e.message : String(e);
    } finally {
      loading = false;
    }
  }

  // onMount, not $effect: an effect tracks whatever it reads, and load()
  // both reads and writes this component's state — one stray sync read
  // would turn it into a fetch loop.
  onMount(() => {
    void load();
  });

  async function save(p: AgentProfile) {
    saving = true;
    formError = "";
    try {
      await saveAgentProfile(base, p);
      toastOk(`Saved ${p.key}`);
      await load(p.key);
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
      selected = null;
      await load();
    } catch (e) {
      toastError(e instanceof Error ? e.message : String(e));
    }
  }

  function startNew() {
    formError = "";
    selected = emptyAgentProfile();
  }

  /* Save from a row action without opening the editor pane. Keeps the
     selection where it was: a disable toggle should not yank the user to a
     different role. */
  async function saveInline(p: AgentProfile, what: string) {
    saving = true;
    try {
      await saveAgentProfile(base, p);
      toastOk(`${what} — ${p.name || p.key}`);
      quickChange = null;
      await load(p.key);
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

<ToastHost />

<div class="flex h-full flex-col">
  <header class="border-b border-white-300 px-6 py-4 dark:border-navy-600">
    <h1 class="text-base font-semibold text-black-900 dark:text-white-100">Sub-agents</h1>
    <p class="mt-1 text-xs text-black-800 dark:text-black-600">
      Reusable roles an agent can hand work to. These are global — every project
      inherits them. To add a role only one project can see, open that project's
      settings. Click a role to edit it; ⋮ holds its other actions. A role marked
      <span class="font-medium text-cau-400">Locked</span> can be read but not
      changed, disabled or deleted here.
    </p>
  </header>

  {#if loading}
    <p class="px-6 py-8 text-sm text-black-800 dark:text-black-600">Loading…</p>
  {:else if loadError}
    <p class="px-6 py-8 text-sm text-rose-600 dark:text-rose-400">{loadError}</p>
  {:else}
    <div class="flex min-h-0 flex-1 flex-col md:flex-row">
      <!-- Wider than the old 256px rail: the rows now carry a provider and
           model chip, which a narrow column truncated into uselessness. -->
      <aside
        class="shrink-0 border-b border-white-300 md:w-80 md:border-b-0 md:border-r dark:border-navy-600"
      >
        <ProfileList
          {profiles}
          {providerList}
          loadModels={loadProviderModels}
          selectedID={selected?.id ?? ""}
          canCreate={isAdmin}
          onselect={(p) => {
            formError = "";
            selected = p;
          }}
          oncreate={startNew}
          onchangeModel={isAdmin ? (p) => (quickChange = p) : undefined}
          ontoggle={isAdmin ? toggleDisabled : undefined}
          ondelete={isAdmin ? (p) => (pendingDelete = p) : undefined}
        />
      </aside>

      <section class="min-w-0 flex-1 overflow-y-auto px-6 py-6">
        {#if selected}
          <AgentProfileEditor
            profile={selected}
            {tags}
            {providerList}
            readonly={!isAdmin}
            {saving}
            error={formError}
            onsave={save}
            ondelete={(p) => (pendingDelete = p)}
          />
        {:else}
          <p class="text-sm text-black-800 dark:text-black-600">
            {isAdmin
              ? "Select a role, or create one with + New."
              : "No roles you can view yet."}
          </p>
        {/if}
      </section>
    </div>
  {/if}
</div>

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
    body={`Delete "${pendingDelete.key}"? Sessions that already delegated to it keep their history, but no new delegation can name it.`}
    confirmLabel="Delete"
    destructive
    onConfirm={confirmDelete}
    onCancel={() => (pendingDelete = null)}
  />
{/if}
