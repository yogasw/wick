<script lang="ts">
  /* Settings drawer for one agent. Every change saves itself: toggles,
     selects and checklists at once, text fields ~800ms after the last
     keystroke or on blur. Only the fields that differ from the last saved
     state are PATCHed (the persona half lands in the project's meta.json
     server-side, and a no-op save must not rewrite it). One PATCH is in
     flight per agent; edits made meanwhile go out in the next one. A
     rejected PATCH keeps the local value and shows the error inline. */
  import { onMount, untrack } from "svelte";
  import { AIGenerateButton, Button, Modal, ProviderPicker, Toggle, buildProviderOptions } from "@wick-fe/common-ui";
  import { toastOk } from "@wick-fe/common-stores";
  import DrawerHeader from "./DrawerHeader.svelte";
  import { AgentAvatar, AVATAR_SHAPES, AVATAR_COLORS, AVATAR_STATES, AVATAR_STATE_LABELS, colorInputValue } from "@wick-fe/common-avatar";
  import { getProviderOptions, getProjectOptions } from "../api/options.js";
  import {
    updateAgent, deleteAgent, getProjectPersona, listAgentConnectors, runApi,
    type AgentItem, type AgentWrite, type ConnectorGrant, type AgentConnector,
  } from "../api/team.js";
  import { FEATURE_TABS, railShownNote, type AgentFeatures } from "../agentMode.js";
  import ConnectorChecklist from "./ConnectorChecklist.svelte";
  import { HANDLE_RE, splitPick, joinPick, pruneGrants, parseGrantErrors, projectOptionLabel, type GrantErrors, type PickerProject } from "../agentForm.js";
  import type { SettingsTab } from "../agentsRouter.js";
  import { PERSONA_KIND, personaInput, type PersonaDraft, type PersonaTarget } from "../personaGen.js";

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
    handle: string; name: string; tagline: string; description: string; system_prompt: string;
    pick: string; features: AgentFeatures; avatar: { shape: string; color: string };
    project_id: string; grants: ConnectorGrant[]; include_new_connectors: boolean; run_as: "caller" | "owner";
    disabled: boolean; allow_provider_switch: boolean; use_global_prompt: boolean;
  };
  function draftOf(a: AgentItem): Draft {
    return {
      handle: a.handle, name: a.name, tagline: a.tagline ?? "", description: a.description,
      system_prompt: a.system_prompt, pick: joinPick(a.provider, a.model),
      features: { ...a.features }, avatar: { ...a.avatar }, project_id: a.project_id,
      grants: $state.snapshot(a.allowed_connectors ?? []) as ConnectorGrant[],
      include_new_connectors: a.include_new_connectors, run_as: a.run_as ?? "caller",
      disabled: a.disabled, allow_provider_switch: !!a.allow_provider_switch, use_global_prompt: !!a.use_global_prompt,
    };
  }
  let draft = $state<Draft>(untrack(() => draftOf(agent)));
  // saved is what the server last confirmed; patch diffs the draft
  // against it, not against the roster's copy, so a slow roster poll
  // never re-sends an edit.
  let saved = $state<AgentItem>(untrack(() => agent));
  // Reset only when a different agent is opened, not on every roster poll.
  let loadedId = untrack(() => agent.id);
  $effect(() => {
    if (agent.id !== loadedId) {
      loadedId = agent.id;
      draft = draftOf(agent);
      saved = agent;
      failedKey = "";
    }
  });

  let providers = $state<{ type: string; name: string; models?: { id: string; label: string; default: boolean }[] }[]>([]);
  let catalog = $state<AgentConnector[]>([]);
  let catalogError = $state("");
  let catalogLoading = $state(true);
  let projects = $state<PickerProject[]>([]);
  // The agent's own project is often hidden from the picker list; keep it
  // selectable so the select never shows a blank value.
  const projectChoices = $derived.by(() => {
    const own = agent.project_id;
    if (!own || projects.some((p) => p.id === own)) return projects;
    return [{ id: own, name: `${agent.name} (this agent's project)` }, ...projects];
  });
  let pruned = $state(0);
  let grantErrors = $state<GrantErrors | null>(null);
  let saving = $state(false);
  let error = $state("");
  let status = $state<"" | "saving" | "saved" | "error">("");
  // failedKey is the patch the server last refused: it is not re-sent on
  // its own (that would loop), only after another edit or Retry.
  let failedKey = $state("");
  let confirmDelete = $state(false);
  let deleteChats = $state<"delete" | "keep">("delete");

  onMount(() => {
    runApi(getProviderOptions(base)).then((p) => { providers = p; }).catch(() => {});
    runApi(getProjectOptions(base, { hideTeam: true, include: [agent.project_id] })).then((p) => { projects = p ?? []; }).catch(() => {});
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

  /* Switching the project reloads the persona fields from it at once
     (before Save), so the form shows what the agent will become. Back to
     the agent's own project restores what it has now. Whatever is in the
     fields at Save is written to the newly picked project. */
  let projectLoading = $state(false);
  async function switchProject(id: string) {
    draft.project_id = id;
    if (!id || id === agent.project_id) {
      Object.assign(draft, {
        name: agent.name, description: agent.description, system_prompt: agent.system_prompt,
        pick: joinPick(agent.provider, agent.model),
      });
      return;
    }
    projectLoading = true;
    error = "";
    try {
      const p = await runApi(getProjectPersona(base, id));
      if (draft.project_id !== id) return;
      Object.assign(draft, {
        name: p.name || draft.handle, description: p.description, system_prompt: p.system_prompt,
        pick: joinPick(p.provider, p.model),
      });
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      projectLoading = false;
    }
  }
  const projectSwitched = $derived(!!draft.project_id && draft.project_id !== agent.project_id);

  /* The server counts every user of the project (other owners' agents,
     web/channel chats); the local roster is the fallback for an older one. */
  const sharedWith = $derived(
    agent.shared_with ??
      (agent.project_id ? agents.filter((a) => a.id !== agent.id && a.project_id === agent.project_id).length : 0),
  );

  /* ✨ Generate / Improve read the form as it is now; their draft only
     lands on Use (then autosaves like typing). */
  const genInput = (target: PersonaTarget) => () =>
    personaInput(target, "", {
      name: draft.name, tagline: draft.tagline, description: draft.description, system_prompt: draft.system_prompt,
    }, catalog);
  const genRefuse = () =>
    draft.name.trim() || draft.description.trim() || draft.system_prompt.trim() ? "" : "Write a name, description or system prompt first.";

  /* ── save ──────────────────────────────────────────────────────── */
  const patch = $derived.by((): AgentWrite => {
    const p: AgentWrite = {};
    const d = draft;
    if (d.handle !== saved.handle) p.handle = d.handle;
    // "" would be a 400: an agent always has a project.
    if (d.project_id && d.project_id !== saved.project_id) p.project_id = d.project_id;
    if (d.name !== saved.name) p.name = d.name;
    if (d.tagline !== (saved.tagline ?? "")) p.tagline = d.tagline;
    if (d.description !== saved.description) p.description = d.description;
    if (d.system_prompt !== saved.system_prompt) p.system_prompt = d.system_prompt;
    if (d.pick !== joinPick(saved.provider, saved.model)) {
      const { provider, model } = splitPick(d.pick);
      p.provider = provider;
      p.model = model;
    }
    if (JSON.stringify(d.features) !== JSON.stringify(saved.features)) p.features = { ...d.features };
    if (JSON.stringify(d.avatar) !== JSON.stringify(saved.avatar)) p.avatar = { ...d.avatar };
    if (JSON.stringify(d.grants) !== JSON.stringify(saved.allowed_connectors ?? [])) {
      p.allowed_connectors = $state.snapshot(d.grants) as ConnectorGrant[];
    }
    if (d.include_new_connectors !== saved.include_new_connectors) p.include_new_connectors = d.include_new_connectors;
    if (d.run_as !== (saved.run_as ?? "caller")) p.run_as = d.run_as;
    if (d.disabled !== saved.disabled) p.disabled = d.disabled;
    if (d.allow_provider_switch !== !!saved.allow_provider_switch) p.allow_provider_switch = d.allow_provider_switch;
    if (d.use_global_prompt !== !!saved.use_global_prompt) p.use_global_prompt = d.use_global_prompt;
    return p;
  });
  const dirty = $derived(Object.keys(patch).length > 0);
  const handleOk = $derived(HANDLE_RE.test(draft.handle));

  const TEXT_KEYS = ["handle", "name", "tagline", "description", "system_prompt"];
  let timer: ReturnType<typeof setTimeout> | undefined;
  $effect(() => {
    const key = JSON.stringify(patch);
    if (!dirty || !handleOk || key === failedKey) return;
    clearTimeout(timer);
    const typing = Object.keys(patch).some((k) => TEXT_KEYS.includes(k));
    timer = setTimeout(() => void save(), typing ? 800 : 0);
    return () => clearTimeout(timer);
  });
  /** flush sends a pending text edit now (field blur). */
  function flush() {
    if (!dirty || !handleOk) return;
    clearTimeout(timer);
    void save();
  }
  function retry() {
    failedKey = "";
    flush();
  }

  async function save() {
    if (!dirty || !handleOk) return;
    if (saving) return; // the effect re-fires once the in-flight PATCH settles
    const sent = $state.snapshot(patch) as AgentWrite;
    const key = JSON.stringify(sent);
    saving = true;
    status = "saving";
    error = "";
    try {
      const next = await runApi(updateAgent(base, agent.id, sent));
      onSaved(next);
      saved = next;
      failedKey = "";
      pruned = 0;
      grantErrors = null;
      status = "saved";
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      failedKey = key;
      status = "error";
      const ge = parseGrantErrors(msg, catalog);
      if (ge.general) {
        grantErrors = null;
        error = msg;
      } else {
        // Item-level rejections are drawn on their rows in Access.
        grantErrors = ge;
        error = tab === "access" ? "" : "The server rejected some access — see the Access tab.";
      }
    } finally {
      saving = false;
    }
  }

  async function remove() {
    saving = true;
    error = "";
    try {
      await runApi(deleteAgent(base, agent.id, deleteChats));
      onDeleted();
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
      confirmDelete = false;
    } finally {
      saving = false;
    }
  }

  // Features now governed by Access (Platform/System rows); the Tools tab
  // keeps panel-only switches.
  const ACCESS_FEATURES: (keyof AgentFeatures)[] = ["source", "schedule", "subagents", "notes", "tickets", "browser"];
  const TABS: { id: SettingsTab; label: string }[] = [
    { id: "persona", label: "Persona" },
    { id: "access", label: "Access" },
    { id: "tools", label: "Tools & features" },
    { id: "avatar", label: "Avatar" },
    { id: "advanced", label: "Advanced" },
  ];
  const input =
    "w-full rounded-lg border border-white-300 bg-white-100 px-3 py-2 text-sm text-black-900 focus:border-green-500 focus:outline-none dark:border-navy-600 dark:bg-navy-800 dark:text-white-100";
  const label = "mb-1 block text-xs font-medium text-black-800 dark:text-black-600";
</script>

<DrawerHeader
  title="Settings"
  subtitle={`${agent.name}${agent.tagline ? ` · ${agent.tagline}` : ""} · @${agent.handle} — chat stays behind`}
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

<div class="min-h-0 flex-1 space-y-4 overflow-y-auto px-6 py-4" onfocusout={flush}>
  {#if tab === "persona"}
    <div>
      <p class="text-sm font-semibold text-black-900 dark:text-white-100">Persona</p>
      <p class="mt-1 text-xs text-black-800 dark:text-black-600">
        Saved to the agent's project (hidden){#if sharedWith > 0} · also used by {sharedWith} agent{sharedWith === 1 ? "" : "s"}{/if}
      </p>
      {#if projectSwitched}
        <p class="mt-1 text-xs text-black-800 dark:text-black-600">
          {projectLoading ? "Loading persona from the new project…" : "Loaded from the new project (Advanced tab) — saved changes apply to that project."}
        </p>
      {/if}
    </div>
    <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
      <div>
        <label class={label} for="as-name">Name</label>
        <input id="as-name" class={input} bind:value={draft.name} />
        <label class="{label} mt-3" for="as-tagline">Tagline</label>
        <input id="as-tagline" class={input} bind:value={draft.tagline} maxlength="32" placeholder="e.g. The Critic" />
      </div>
      <div>
        <label class={label} for="as-handle">Handle</label>
        <div class="flex items-center rounded-lg border border-white-300 bg-white-100 focus-within:border-green-500 dark:border-navy-600 dark:bg-navy-800">
          <span class="pl-3 font-mono text-sm text-black-800 dark:text-black-600">@</span>
          <input
            id="as-handle"
            class="w-full min-w-0 bg-transparent py-2 pl-1 pr-3 font-mono text-sm text-black-900 focus:outline-none dark:text-white-100"
            bind:value={draft.handle}
          />
        </div>
        {#if handleOk}
          <p class="mt-1 text-xs text-black-800 dark:text-black-600">used for @mentions</p>
        {:else}
          <p class="mt-1 text-xs text-neg-400">Lowercase letters, digits and "-", 2–31 characters.</p>
        {/if}
      </div>
    </div>
    <div>
      <label class={label} for="as-desc">Short description</label>
      <input id="as-desc" class={input} bind:value={draft.description} />
      <div class="mt-2">
        <AIGenerateButton
          kind={PERSONA_KIND}
          testid="as-gen-desc"
          label={draft.description.trim() || draft.tagline.trim() ? "✨ Improve tagline & description" : "✨ Generate tagline & description"}
          validate={genRefuse}
          input={genInput("description")}
          current={[draft.tagline, draft.description].filter((s) => s.trim()).join(" — ")}
          onUse={(d: PersonaDraft) => { draft.tagline = d.tagline; draft.description = d.description; }}
        >
          {#snippet preview(d: PersonaDraft)}
            <p class="text-xs font-semibold text-black-900 dark:text-white-100">{d.tagline}</p>
            <p class="mt-0.5 text-xs text-black-900 dark:text-white-100">{d.description}</p>
          {/snippet}
        </AIGenerateButton>
      </div>
    </div>
    <div>
      <label class={label} for="as-sys">System prompt (persona)</label>
      <textarea id="as-sys" class={input} rows="8" bind:value={draft.system_prompt}></textarea>
      <div class="mt-1 flex flex-wrap items-start justify-between gap-2">
        <p class="text-xs text-black-800 dark:text-black-600">appended after the base preset</p>
        <AIGenerateButton
          kind={PERSONA_KIND}
          testid="as-gen-sys"
          label={draft.system_prompt.trim() ? "✨ Improve" : "✨ Generate"}
          validate={genRefuse}
          input={genInput("system_prompt")}
          current={draft.system_prompt}
          onUse={(d: PersonaDraft) => { draft.system_prompt = d.system_prompt; }}
        >
          {#snippet preview(d: PersonaDraft)}
            <pre class="max-h-64 overflow-auto whitespace-pre-wrap font-sans text-xs text-black-900 dark:text-white-100">{d.system_prompt}</pre>
          {/snippet}
        </AIGenerateButton>
      </div>
    </div>
  {:else if tab === "access"}
    <p class="text-xs text-black-800 dark:text-black-600">
      Connectors are off until you add them. Platform tools are on for every agent; System
      tools are for the Captain. An agent never gets more than your own access.
    </p>
    {#if pruned > 0}
      <p class="text-xs text-black-800 dark:text-black-600">
        {pruned} old grant{pruned === 1 ? "" : "s"} you can no longer use {pruned === 1 ? "was" : "were"} removed from the list.
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
      isCaptain={agent.is_captain}
    />
  {:else if tab === "tools"}
    <div>
      <p class="text-sm font-semibold text-black-900 dark:text-white-100">Tools &amp; features</p>
      <p class="mt-1 text-xs text-black-800 dark:text-black-600">Panels of the agent's chat. Notes, Tickets, Source, Schedule and Sub-agents follow Access › Platform, and the Browser tab follows the Playwright connector in Access › Connectors.</p>
    </div>
    <div class="rounded-xl border border-white-300 px-4 py-3 opacity-60 dark:border-navy-600" aria-disabled="true">
      <p class="text-sm font-medium text-black-900 dark:text-white-100">
        Native tools &amp; Bash
        <span class="ml-1 rounded-full bg-white-200 px-2 py-0.5 text-xs font-medium text-black-800 dark:bg-navy-600 dark:text-black-600">Coming soon (Phase 1c)</span>
      </p>
      <p class="mt-1 text-xs text-black-800 dark:text-black-600">Choosing built-in tools and Bash commands per agent is not available yet.</p>
    </div>
    <div>
      <span class={label}>wick features</span>
      <div class="space-y-3">
        {#each FEATURE_TABS.filter((f) => !ACCESS_FEATURES.includes(f.feature)) as f (f.feature)}
          <!-- Toggle draws only the switch; the name and hint sit beside it. -->
          <div class="flex items-start gap-3">
            <Toggle checked={draft.features[f.feature]} onChange={(v) => (draft.features[f.feature] = v)} label={f.label} describedBy={f.hint ? `as-ft-${f.feature}` : undefined} />
            <span class="min-w-0">
              <span class="block text-sm text-black-900 dark:text-white-100">{f.label}</span>
              {#if f.hint}<span id="as-ft-{f.feature}" class="block text-xs text-black-800 dark:text-black-600">{f.hint}</span>{/if}
            </span>
          </div>
        {/each}
      </div>
      <p class="mt-2 text-xs text-black-800 dark:text-black-600">{railShownNote(draft.features)}</p>
    </div>
  {:else if tab === "avatar"}
    <div class="flex items-center gap-4">
      <AgentAvatar shape={draft.avatar.shape} color={draft.avatar.color} size={72} />
      <AgentAvatar shape={draft.avatar.shape} color={draft.avatar.color} size={72} working />
      <span class="text-xs text-black-800 dark:text-black-600">idle · working</span>
    </div>
    <div>
      <span class={label}>Shape</span>
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
      <span class={label}>Color</span>
      <div class="flex flex-wrap gap-2">
        {#each AVATAR_COLORS as col (col)}
          <button
            type="button"
            class="h-8 w-8 rounded-full border-2 {draft.avatar.color.toLowerCase() === col ? 'border-green-500' : 'border-transparent'}"
            style:background-color={col}
            aria-label={col}
            aria-pressed={draft.avatar.color.toLowerCase() === col}
            onclick={() => (draft.avatar.color = col)}
          ></button>
        {/each}
        <!-- Any color, not only the swatches: the server takes free hex. -->
        <input
          type="color"
          class="h-8 w-10 cursor-pointer rounded-lg border border-white-300 dark:border-navy-600 bg-transparent p-0.5"
          aria-label="Other color"
          title="Other color"
          value={colorInputValue(draft.avatar.color)}
          oninput={(e) => (draft.avatar.color = e.currentTarget.value)}
        />
      </div>
    </div>
    <div>
      <span class={label}>State</span>
      <div class="grid grid-cols-3 gap-3 sm:grid-cols-7">
        {#each AVATAR_STATES as st (st)}
          <div class="flex flex-col items-center gap-1.5 text-center">
            <AgentAvatar shape={draft.avatar.shape} color={draft.avatar.color} size={40} pose={st} />
            <span class="text-[11px] text-black-800 dark:text-black-600">{AVATAR_STATE_LABELS[st]}</span>
          </div>
        {/each}
      </div>
    </div>
  {:else if tab === "advanced"}
    <div>
      <p class="text-sm font-semibold text-black-900 dark:text-white-100">Advanced</p>
      <p class="mt-1 text-xs text-black-800 dark:text-black-600">The project behind this agent — usually best left alone.</p>
    </div>
    <div>
      <label class={label} for="as-project">Project</label>
      <select id="as-project" class={input} value={draft.project_id} onchange={(e) => switchProject((e.currentTarget as HTMLSelectElement).value)}>
        {#each projectChoices as p (p.id)}
          <option value={p.id}>{projectOptionLabel(p)}</option>
        {/each}
      </select>
      <p class="mt-1 text-xs text-black-800 dark:text-black-600">
        Switching projects reloads the persona from that project at once, and saved persona changes apply to it; old chats stay in the old project.
      </p>
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
    <div>
      <Toggle checked={draft.use_global_prompt} onChange={(v) => (draft.use_global_prompt = v)} label="Use the global system prompt" />
      <p class="mt-1 text-xs text-black-800 dark:text-black-600">
        On: this agent's chats carry the global system prompt (channel rules such as Slack formatting live there) instead of the Team agents prompt. On by default for an agent made from a project.
      </p>
    </div>
    <div>
      <Toggle checked={draft.allow_provider_switch} onChange={(v) => (draft.allow_provider_switch = v)} label="Allow provider switch in chat" />
      <p class="mt-1 text-xs text-black-800 dark:text-black-600">
        Off: every chat uses the provider above. On: a chat can pick another provider before its first message, and another model of the same provider any time. Neither changes the agent's default.
      </p>
    </div>
    <div class="space-y-3 border-t border-white-300 pt-4 dark:border-navy-600">
      <p class="text-sm font-semibold text-neg-400">Danger zone</p>
      <Toggle checked={draft.disabled} onChange={(v) => (draft.disabled = v)} label="Disable agent" />
      <div>
        <button type="button" class="rounded-lg border border-neg-300 px-3 py-1 text-sm text-neg-400 hover:bg-neg-100 dark:hover:bg-navy-600" onclick={() => { deleteChats = "delete"; confirmDelete = true; }}>Delete agent…</button>
        <p class="mt-1 text-xs text-black-800 dark:text-black-600">
          You choose what happens to its chats and memory.
          {#if agent.is_captain}The Captain cannot be deleted while other agents exist.{/if}
        </p>
      </div>
    </div>
  {/if}
  {#if error}<p class="text-sm text-neg-400">{error}</p>{/if}
</div>

<div class="flex items-center justify-end gap-2 border-t border-white-300 px-6 py-4 dark:border-navy-600">
  <span class="mr-auto flex items-center gap-2 text-xs" aria-live="polite" data-testid="autosave-status">
    {#if status === "saving" || (dirty && handleOk && JSON.stringify(patch) !== failedKey)}
      <span class="text-black-800 dark:text-black-600">Saving…</span>
    {:else if status === "error"}
      <span class="text-neg-400">Not saved</span><span aria-hidden="true" class="text-black-700">·</span>
      <button type="button" class="font-medium text-green-600 hover:underline" onclick={retry}>Retry</button>
    {:else if !handleOk}
      <span class="text-neg-400">Not saved — fix the handle</span>
    {:else if status === "saved"}
      <span class="text-black-800 dark:text-black-600">Saved ✓</span>
    {:else}
      <span class="text-black-700 dark:text-black-700">All changes saved</span>
    {/if}
  </span>
  <button type="button" class="rounded-lg px-4 py-2 text-sm text-black-800 hover:bg-white-200 dark:text-black-600 dark:hover:bg-navy-600" onclick={onClose}>Close</button>
</div>

<Modal open={confirmDelete} title={`Delete agent ${agent.name || agent.handle}?`} onClose={() => (confirmDelete = false)} size="sm">
  <fieldset class="space-y-2" data-testid="agent-delete-modes">
    <label class="flex items-start gap-2 text-sm text-black-900 dark:text-white-100">
      <input type="radio" name="agent-delete-chats" value="delete" bind:group={deleteChats} class="mt-1" />
      <span>Delete its chats and memory too
        <span class="block text-xs text-black-700 dark:text-black-600">Removes the agent's project: its chats, files folder and the memory it built. Cannot be undone.</span>
      </span>
    </label>
    <label class="flex items-start gap-2 text-sm text-black-900 dark:text-white-100">
      <input type="radio" name="agent-delete-chats" value="keep" bind:group={deleteChats} class="mt-1" />
      <span>Keep its chats as a normal project
        <span class="block text-xs text-black-700 dark:text-black-600">The project stays and shows in the sidebar under Projects.</span>
      </span>
    </label>
  </fieldset>
  {#snippet footer()}
    <Button variant="secondary" onclick={() => (confirmDelete = false)}>Cancel</Button>
    <Button variant="danger" disabled={saving} onclick={remove}>{deleteChats === "delete" ? "Delete agent and chats" : "Delete agent"}</Button>
  {/snippet}
</Modal>
