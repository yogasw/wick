<script lang="ts">
  /* Settings drawer for one agent. Four pill tabs share one draft and one
     Save, so switching tabs never loses an edit. Only the fields that
     differ from the loaded agent are PATCHed: the persona half lands in the
     project's meta.json server-side, and a no-op save must not rewrite it. */
  import { onMount, untrack } from "svelte";
  import { ProviderPicker, Toggle, buildProviderOptions } from "@wick-fe/common-ui";
  import { toastOk } from "@wick-fe/common-stores";
  import DrawerHeader from "./DrawerHeader.svelte";
  import AgentAvatar from "./AgentAvatar.svelte";
  import { getProviderOptions } from "../api/options.js";
  import {
    updateAgent, deleteAgent, listAgentConnectors, runApi,
    type AgentItem, type AgentWrite, type ConnectorGrant, type AgentConnector,
  } from "../api/team.js";
  import { FEATURE_TABS, type AgentFeatures } from "../agentMode.js";
  import { AVATAR_SHAPES, AVATAR_COLORS } from "../avatarShape.js";
  import ConnectorChecklist from "./ConnectorChecklist.svelte";
  import { HANDLE_RE, splitPick, joinPick, pruneGrants, parseGrantErrors, type GrantErrors } from "../agentForm.js";
  import type { SettingsTab } from "../agentsRouter.js";

  type Props = {
    base: string;
    agent: AgentItem;
    agents: AgentItem[];
    tab: SettingsTab;
    onTab: (t: SettingsTab) => void;
    onClose: () => void;
    onSaved: (a: AgentItem) => void;
    onDeleted: () => void;
  };
  let { base, agent, agents, tab, onTab, onClose, onSaved, onDeleted }: Props = $props();

  type Draft = {
    handle: string; name: string; description: string; system_prompt: string;
    pick: string; features: AgentFeatures; avatar: { shape: string; color: string };
    grants: ConnectorGrant[]; include_new_connectors: boolean; run_as: "caller" | "owner";
    disabled: boolean;
  };
  function draftOf(a: AgentItem): Draft {
    return {
      handle: a.handle, name: a.name, description: a.description,
      system_prompt: a.system_prompt, pick: joinPick(a.provider, a.model),
      features: { ...a.features }, avatar: { ...a.avatar },
      grants: $state.snapshot(a.allowed_connectors ?? []) as ConnectorGrant[],
      include_new_connectors: a.include_new_connectors, run_as: a.run_as ?? "caller",
      disabled: a.disabled,
    };
  }
  let draft = $state<Draft>(untrack(() => draftOf(agent)));
  // Reset only when a different agent is opened, not on every roster poll.
  let loadedId = untrack(() => agent.id);
  $effect(() => {
    if (agent.id !== loadedId) {
      loadedId = agent.id;
      draft = draftOf(agent);
    }
  });

  let providers = $state<{ type: string; name: string; models?: { id: string; label: string; default: boolean }[] }[]>([]);
  let catalog = $state<AgentConnector[]>([]);
  let catalogError = $state("");
  let catalogLoading = $state(true);
  let pruned = $state(0);
  let grantErrors = $state<GrantErrors | null>(null);
  let saving = $state(false);
  let error = $state("");
  let confirmDelete = $state(false);

  onMount(() => {
    runApi(getProviderOptions(base)).then((p) => { providers = p; }).catch(() => {});
    runApi(listAgentConnectors(base))
      .then((c) => {
        catalog = c ?? [];
        // The server rejects a list naming access the owner lost; drop it
        // from the draft up front and say so, rather than failing the save.
        const p = pruneGrants(draft.grants, catalog);
        if (p.dropped > 0) {
          draft.grants = p.grants;
          pruned = p.dropped;
        }
      })
      .catch((e) => { catalogError = e instanceof Error ? e.message : String(e); })
      .finally(() => { catalogLoading = false; });
  });

  const sharedWith = $derived(
    agent.project_id ? agents.filter((a) => a.id !== agent.id && a.project_id === agent.project_id).length : 0,
  );

  /* ── save ──────────────────────────────────────────────────────── */
  const patch = $derived.by((): AgentWrite => {
    const p: AgentWrite = {};
    const d = draft;
    if (d.handle !== agent.handle) p.handle = d.handle;
    if (d.name !== agent.name) p.name = d.name;
    if (d.description !== agent.description) p.description = d.description;
    if (d.system_prompt !== agent.system_prompt) p.system_prompt = d.system_prompt;
    if (d.pick !== joinPick(agent.provider, agent.model)) {
      const { provider, model } = splitPick(d.pick);
      p.provider = provider;
      p.model = model;
    }
    if (JSON.stringify(d.features) !== JSON.stringify(agent.features)) p.features = { ...d.features };
    if (JSON.stringify(d.avatar) !== JSON.stringify(agent.avatar)) p.avatar = { ...d.avatar };
    if (JSON.stringify(d.grants) !== JSON.stringify(agent.allowed_connectors ?? [])) {
      p.allowed_connectors = $state.snapshot(d.grants) as ConnectorGrant[];
    }
    if (d.include_new_connectors !== agent.include_new_connectors) p.include_new_connectors = d.include_new_connectors;
    if (d.run_as !== (agent.run_as ?? "caller")) p.run_as = d.run_as;
    if (d.disabled !== agent.disabled) p.disabled = d.disabled;
    return p;
  });
  const dirty = $derived(Object.keys(patch).length > 0);
  const handleOk = $derived(HANDLE_RE.test(draft.handle));

  async function save() {
    if (!dirty || !handleOk || saving) return;
    saving = true;
    error = "";
    try {
      const next = await runApi(updateAgent(base, agent.id, patch));
      onSaved(next);
      draft = draftOf(next);
      pruned = 0;
      grantErrors = null;
      toastOk("Agent disimpan");
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      const ge = parseGrantErrors(msg, catalog);
      if (ge.general) {
        grantErrors = null;
        error = msg;
      } else {
        // Item-level rejections are drawn on their rows in Akses.
        grantErrors = ge;
        error = tab === "access" ? "" : "Ada akses yang ditolak server — lihat tab Akses.";
      }
    } finally {
      saving = false;
    }
  }

  async function remove() {
    saving = true;
    error = "";
    try {
      await runApi(deleteAgent(base, agent.id));
      onDeleted();
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
      confirmDelete = false;
    } finally {
      saving = false;
    }
  }

  const TABS: { id: SettingsTab; label: string }[] = [
    { id: "persona", label: "Persona" },
    { id: "access", label: "Akses" },
    { id: "features", label: "Fitur" },
    { id: "avatar", label: "Avatar" },
  ];
  const input =
    "w-full rounded-lg border border-white-300 bg-white-100 px-3 py-2 text-sm text-black-900 focus:border-green-500 focus:outline-none dark:border-navy-600 dark:bg-navy-800 dark:text-white-100";
  const label = "mb-1 block text-xs font-medium text-black-800 dark:text-black-600";
</script>

<DrawerHeader
  title="Settings"
  subtitle={`${agent.name} · @${agent.handle} — chat tetap di belakang`}
  avatar={draft.avatar}
  bordered={false}
  {onClose}
/>

<div class="flex shrink-0 gap-1 overflow-x-auto border-b border-white-300 px-6 pb-3 dark:border-navy-600" role="tablist">
  {#each TABS as t (t.id)}
    <button
      type="button"
      role="tab"
      aria-selected={tab === t.id}
      class="whitespace-nowrap rounded-full px-3 py-1.5 text-xs font-medium {tab === t.id
        ? 'bg-black-900 text-white-100 dark:bg-white-200 dark:text-navy-700'
        : 'text-black-800 hover:bg-white-200 dark:text-black-600 dark:hover:bg-navy-600'}"
      onclick={() => onTab(t.id)}
    >{t.label}</button>
  {/each}
</div>

<div class="min-h-0 flex-1 space-y-4 overflow-y-auto px-6 py-4">
  {#if tab === "persona"}
    <div>
      <label class={label} for="as-name">Nama</label>
      <input id="as-name" class={input} bind:value={draft.name} />
    </div>
    <div>
      <label class={label} for="as-handle">Handle</label>
      <input id="as-handle" class={input} bind:value={draft.handle} />
      {#if !handleOk}
        <p class="mt-1 text-xs text-neg-400">Huruf kecil, angka, dan "-", 2–31 karakter.</p>
      {/if}
    </div>
    <div>
      <label class={label} for="as-desc">Deskripsi</label>
      <input id="as-desc" class={input} bind:value={draft.description} />
    </div>
    <div>
      <label class={label} for="as-sys">System prompt (persona)</label>
      <textarea id="as-sys" class="{input} min-h-32" rows="8" bind:value={draft.system_prompt}></textarea>
    </div>
    <div>
      <span class={label}>Provider / model</span>
      <ProviderPicker
        options={buildProviderOptions(providers, draft.pick)}
        value={draft.pick}
        onChange={(v) => (draft.pick = v)}
        placeholder="Default project"
      />
    </div>
    <div class="flex flex-wrap items-center gap-2 text-xs text-black-800 dark:text-black-600">
      <span>Project</span>
      <code class="rounded bg-white-200 px-2 py-1 font-mono dark:bg-navy-800">{agent.project_id || "—"}</code>
      {#if sharedWith > 0}
        <span class="rounded-full bg-cau-100 px-2 py-1 font-medium text-cau-700 dark:bg-cau-900/30 dark:text-cau-300">
          dipakai juga oleh {sharedWith} agent
        </span>
      {/if}
    </div>
    <Toggle checked={draft.disabled} onChange={(v) => (draft.disabled = v)} label="Nonaktifkan agent" />

    <div class="rounded-xl border border-neg-300 p-4 dark:border-neg-700">
      <p class="text-sm font-medium text-black-900 dark:text-white-100">Hapus agent</p>
      <p class="mt-1 text-xs text-black-800 dark:text-black-600">
        Menghapus agent saja; project dan percakapannya tetap ada.
        {#if agent.is_captain}Captain tidak bisa dihapus selama masih ada agent lain.{/if}
      </p>
      {#if confirmDelete}
        <div class="mt-2 flex gap-2">
          <button type="button" class="rounded-lg bg-neg-400 px-3 py-1 text-sm font-medium text-white-100 disabled:opacity-50" disabled={saving} onclick={remove}>Ya, hapus</button>
          <button type="button" class="rounded-lg px-3 py-1 text-sm text-black-800 hover:bg-white-200 dark:text-black-600 dark:hover:bg-navy-600" onclick={() => (confirmDelete = false)}>Batal</button>
        </div>
      {:else}
        <button type="button" class="mt-2 rounded-lg border border-neg-300 px-3 py-1 text-sm text-neg-400 hover:bg-neg-50 dark:border-neg-700 dark:hover:bg-neg-900/20" onclick={() => (confirmDelete = true)}>Hapus…</button>
      {/if}
    </div>
  {:else if tab === "access"}
    <p class="text-xs text-black-800 dark:text-black-600">
      Daftar connector yang bisa <b>kamu</b> pakai. Centang yang boleh dipakai agent ini; agent tidak pernah
      mendapat lebih dari akses kamu sendiri.
    </p>
    {#if pruned > 0}
      <p class="text-xs text-black-800 dark:text-black-600">
        {pruned} akses lama tidak lagi bisa kamu pakai dan dibuang dari daftar — simpan untuk menerapkan.
      </p>
    {/if}
    <ConnectorChecklist
      {catalog}
      loading={catalogLoading}
      loadError={catalogError}
      bind:grants={draft.grants}
      bind:includeNew={draft.include_new_connectors}
      bind:runAs={draft.run_as}
      errors={grantErrors}
    />
  {:else if tab === "features"}
    <p class="text-xs text-black-800 dark:text-black-600">Fitur yang dimatikan menyembunyikan tab-nya di rail chat agent.</p>
    <div class="space-y-3">
      {#each FEATURE_TABS as f (f.feature)}
        <Toggle checked={draft.features[f.feature]} onChange={(v) => (draft.features[f.feature] = v)} label={f.label} />
      {/each}
    </div>
  {:else if tab === "avatar"}
    <div class="flex items-center gap-4">
      <AgentAvatar shape={draft.avatar.shape} color={draft.avatar.color} size={72} />
      <AgentAvatar shape={draft.avatar.shape} color={draft.avatar.color} size={72} working />
      <span class="text-xs text-black-800 dark:text-black-600">diam · bekerja</span>
    </div>
    <div>
      <span class={label}>Bentuk</span>
      <div class="flex gap-2">
        {#each AVATAR_SHAPES as s (s)}
          <button
            type="button"
            class="rounded-xl border-2 p-1 {draft.avatar.shape === s ? 'border-green-500' : 'border-white-300 dark:border-navy-600'}"
            aria-label={s}
            aria-pressed={draft.avatar.shape === s}
            onclick={() => (draft.avatar.shape = s)}
          ><AgentAvatar shape={s} color={draft.avatar.color} size={40} /></button>
        {/each}
      </div>
    </div>
    <div>
      <span class={label}>Warna</span>
      <div class="flex flex-wrap gap-2">
        {#each AVATAR_COLORS as col (col)}
          <button
            type="button"
            class="h-8 w-8 rounded-full border-2 {draft.avatar.color.toLowerCase() === col ? 'border-black-900 dark:border-white-100' : 'border-transparent'}"
            style:background-color={col}
            aria-label={col}
            aria-pressed={draft.avatar.color.toLowerCase() === col}
            onclick={() => (draft.avatar.color = col)}
          ></button>
        {/each}
      </div>
    </div>
  {/if}
  {#if error}<p class="text-sm text-neg-400">{error}</p>{/if}
</div>

<div class="flex items-center justify-end gap-2 border-t border-white-300 px-6 py-4 dark:border-navy-600">
  {#if dirty}<span class="mr-auto text-xs text-black-800 dark:text-black-600">Ada perubahan belum disimpan</span>{/if}
  <button type="button" class="rounded-lg px-4 py-2 text-sm text-black-800 hover:bg-white-200 dark:text-black-600 dark:hover:bg-navy-600" onclick={onClose}>Tutup</button>
  <button
    type="button"
    class="rounded-lg bg-green-500 px-4 py-2 text-sm font-medium text-white-100 hover:bg-green-600 disabled:opacity-50"
    disabled={!dirty || !handleOk || saving}
    onclick={save}
  >{saving ? "Menyimpan…" : "Simpan"}</button>
</div>
