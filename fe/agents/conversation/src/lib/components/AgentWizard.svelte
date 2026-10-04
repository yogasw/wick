<script lang="ts">
  /* + Agent: a centred modal in two steps (mockup create()).
     1 Persona — an optional one-line brief whose ✨ Generate fills the
       fields below (queued aigen job), then avatar, name, tagline, handle,
       description, system prompt, and the project behind it under
       "Advanced" (default: a new one made from these fields).
     2 Access — the same connector checklist as Settings › Access, starting
       empty (off until added). A new agent runs as the caller with
       include-new off; both are changed later in Settings › Access.
     3 Model — provider and model (or sub model) through the same picker
       as Project Settings, picked by hand: Create stays off until both are
       chosen. Changed later in Settings › Advanced.
     The mockup's "Connect" step waits for Slack/A2A (phase 1b). */
  import { onMount } from "svelte";
  import { AIGenerateButton, ProviderPicker, buildProviderOptions } from "@wick-fe/common-ui";
  import { AgentAvatar, BlobAvatarPicker, AVATAR_SHAPES, AVATAR_COLORS, defaultAvatarFor, isBlobKind, switchAvatarKind } from "@wick-fe/common-avatar";
  import ConnectorChecklist from "./ConnectorChecklist.svelte";
  import { accessPayload, type AccessMode } from "../accessList.js";
  import { getProjectOptions, getProviderOptions, getProviderOptionModels } from "../api/options.js";
  import { createAgent, getProjectPersona, listAgentConnectors, runApi, type AgentItem, type AgentConnector, type ConnectorGrant } from "../api/team.js";
  import { HANDLE_RE, splitPick, slugHandle, uniqueHandle, parseGrantErrors, projectOptionLabel, type GrantErrors, type PickerProject, TAGLINE_MAX } from "../agentForm.js";
  import { PERSONA_KIND, personaInput, suggestedConnectors, type PersonaDraft } from "../personaGen.js";
  import { setOverride } from "../accessTiers.js";
  import { convertGrants, convertSummary } from "../convertProject.js";
  import { CAPTAIN_STARTER, CAPTAIN_FACTS } from "../captainSettings.js";

  type Props = {
    base: string;
    /** Handles already in use, so the suggestion never collides. */
    taken: string[];
    /** "Make this an agent…": the project to convert. Its persona fills
        the form and the new agent takes the project over. */
    convertProject?: string;
    onClose: () => void;
    onCreated: (a: AgentItem) => void;
    /** Switches + Agent to a remote agent's wizard (not when converting). */
    onType?: (t: "local" | "remote" | "slack" | "plugin") => void;
    /** The Team has no Captain yet: the agent made here becomes it, so
        the Persona step says so and offers a starter persona. */
    firstAgent?: boolean;
  };
  let { base, taken, convertProject, onClose, onCreated, onType, firstAgent = false }: Props = $props();

  const STEPS = ["Persona", "Access", "Model"];
  let step = $state(1);

  let brief = $state("");
  let suggested = $state<string[]>([]);
  let name = $state("");
  let tagline = $state("");
  let description = $state("");
  let handle = $state("");
  let handleTouched = $state(false);
  let systemPrompt = $state("");
  let kind = $state("");
  let shape = $state("circle");
  let color = $state(AVATAR_COLORS[1]);
  let expression = $state("neutral");
  function setKind(k: string) {
    const a = switchAvatarKind({ kind, shape, color, expression }, k);
    kind = a.kind ?? "";
    shape = a.shape;
    expression = a.expression ?? "neutral";
    avatarTouched = true;
  }
  // Until a shape or swatch is picked the avatar follows the handle's hash
  // (same function as the server's default), so it is stable per handle.
  let avatarTouched = $state(false);
  let projectId = $state("");
  // What was typed before an existing project replaced it, so going back
  // to "Project baru" restores it.
  let typed: { name: string; description: string; systemPrompt: string } | null = null;
  let projectLoading = $state(false);
  let projects = $state<PickerProject[]>([]);

  let catalog = $state<AgentConnector[]>([]);
  let catalogLoading = $state(true);
  let catalogError = $state("");
  let grants = $state<ConnectorGrant[]>([]);

  // Convert keeps what the project's chats can do today: every connector
  // at Write plus new ones, run as the caller, the global system prompt.
  // The user can narrow it on the Access step.
  let includeNew = $state(false);
  let accessMode = $state<AccessMode>("choose");
  let useGlobalPrompt = $state(true);
  // On by default: the agent's project stays in Agents → Projects as well.
  let showInProjects = $state(true);
  // Step 3: "type/name::modelID", empty until picked by hand.
  let providers = $state<{ type: string; name: string; models?: { id: string; label: string; default: boolean }[] }[]>([]);
  let pick = $state("");
  // The picker only sets a value from the model level: a model (or sub
  // model), or its "Use default model" row for an instance that lists none.
  const modelOk = $derived(splitPick(pick).provider !== "");
  function loadProviderModels(optionValue: string, opts?: { entry?: string; refresh?: boolean }) {
    const slash = optionValue.indexOf("/");
    const type = slash < 0 ? optionValue : optionValue.slice(0, slash);
    const name = slash < 0 ? optionValue : optionValue.slice(slash + 1);
    return runApi(getProviderOptionModels(base, type, name, opts));
  }
  type ConvertPreview = { name?: string; chats: number; channels: string[] | null; schedules: number; workflows?: string[] | null };
  let convertInfo = $state<ConvertPreview | null>(null);

  const convertProjectName = $derived(
    convertInfo?.name || projects.find((p) => p.id === convertProject)?.name || convertProject || "",
  );

  let saving = $state(false);
  let error = $state("");
  let handleError = $state("");
  let grantErrors = $state<GrantErrors | null>(null);

  onMount(() => {
    runApi(getProjectOptions(base, { hideTeam: true })).then((p) => { projects = p ?? []; }).catch(() => {});
    runApi(getProviderOptions(base)).then((p) => { providers = p ?? []; }).catch(() => {});
    if (convertProject) {
      void pickProject(convertProject);
      includeNew = true;
      fetch(`${base}/projects/${encodeURIComponent(convertProject)}/delete-preview`, { credentials: "same-origin" })
        .then(async (r) => { if (r.ok) convertInfo = (await r.json()) as ConvertPreview; })
        .catch(() => {});
    }
    runApi(listAgentConnectors(base))
      .then((c) => {
        catalog = c ?? [];
        if (convertProject) grants = convertGrants(catalog);
      })
      .catch((e) => { catalogError = e instanceof Error ? e.message : String(e); })
      .finally(() => { catalogLoading = false; });
  });

  // The handle follows the name (made free with -2, -3…) until edited.
  $effect(() => {
    if (!handleTouched) handle = uniqueHandle(slugHandle(name), taken);
  });

  $effect(() => {
    if (avatarTouched) return;
    const d = defaultAvatarFor(handle || "agent");
    shape = d.shape;
    color = d.color;
  });

  /* "Use an existing project": the form shows that project's persona right
     away. Whatever is in the fields at submit is sent and written to the
     project, so a name typed after the pick is the one the agent gets. */
  async function pickProject(id: string) {
    if (id && !typed) typed = { name, description, systemPrompt };
    projectId = id;
    if (!id) {
      if (typed) {
        name = typed.name;
        description = typed.description;
        systemPrompt = typed.systemPrompt;
      }
      typed = null;
      return;
    }
    projectLoading = true;
    try {
      const p = await runApi(getProjectPersona(base, id));
      if (projectId !== id) return;
      name = p.name;
      description = p.description;
      systemPrompt = p.system_prompt;
    } catch {
      // Unreadable project: keep what is there; the create will say why.
    } finally {
      projectLoading = false;
    }
  }

  /* The brief was asked for in so many words, so its draft fills the form
     at once; every field stays editable before Next. */
  function useDraft(d: PersonaDraft) {
    if (d.name) name = d.name;
    if (d.handle) {
      handle = uniqueHandle(d.handle, taken);
      handleTouched = true;
    }
    tagline = d.tagline;
    description = d.description;
    systemPrompt = d.system_prompt;
    if (d.avatar_shape || d.avatar_color) {
      // The generator suggests a classic shape: take it as classic.
      if (d.avatar_shape) {
        kind = "";
        shape = d.avatar_shape;
      }
      if (d.avatar_color) color = d.avatar_color;
      avatarTouched = true;
    }
    suggested = d.connectors ?? [];
  }
  const suggestions = $derived(suggestedConnectors(suggested, catalog, grants.map((g) => g.connector_id)));

  /* "Use Captain starter persona": an optional example the owner edits;
     the Captain role's own prompt is added server-side whatever is here. */
  function useCaptainStarter() {
    name = CAPTAIN_STARTER.name;
    handle = uniqueHandle(CAPTAIN_STARTER.handle, taken);
    handleTouched = true;
    description = CAPTAIN_STARTER.description;
    systemPrompt = CAPTAIN_STARTER.system_prompt;
  }

  const handleOk = $derived(HANDLE_RE.test(handle));
  const handleTaken = $derived(taken.includes(handle));
  const personaOk = $derived(name.trim() !== "" && handleOk && !handleTaken);

  function next() {
    if (step === 1 && personaOk) step = 2;
    else if (step === 2) step = 3;
  }

  async function submit() {
    if (!personaOk || !modelOk || saving) return;
    saving = true;
    error = "";
    handleError = "";
    grantErrors = null;
    try {
      const a = await runApi(
        createAgent(base, {
          handle,
          name: name.trim(),
          tagline: tagline.trim(),
          description: description.trim(),
          system_prompt: systemPrompt,
          avatar: isBlobKind(kind) ? { kind, shape, color, expression } : { shape, color },
          ...(projectId ? { project_id: projectId } : {}),
          ...(convertProject && projectId === convertProject ? { convert: true } : {}),
          ...accessPayload(accessMode, $state.snapshot(grants) as ConnectorGrant[], includeNew),
          run_as: "caller",
          ...(convertProject && projectId === convertProject ? { use_global_prompt: useGlobalPrompt } : {}),
          // Only the agent's own project (new or converted) can be hidden.
          ...(!projectId || projectId === convertProject ? { show_in_projects: showInProjects } : {}),
          provider: splitPick(pick).provider,
          model: splitPick(pick).model,
        }),
      );
      onCreated(a);
    } catch (err) {
      // Same mapping as Settings: each rejection lands on its own field.
      const msg = err instanceof Error ? err.message : String(err);
      if (msg.startsWith("handle")) {
        handleError = msg;
        step = 1;
        return;
      }
      const ge = parseGrantErrors(msg, catalog);
      if (ge.general) error = msg;
      else {
        grantErrors = ge;
        step = 2;
      }
    } finally {
      saving = false;
    }
  }

  const input =
    "w-full rounded-lg border border-white-300 bg-white-100 px-3 py-2 text-sm text-black-900 focus:border-green-500 focus:outline-none dark:border-navy-600 dark:bg-navy-800 dark:text-white-100";
  const label = "mb-1 block text-xs font-medium text-black-800 dark:text-black-600";
  const ghost =
    "rounded-lg px-4 py-2 text-sm text-black-800 hover:bg-white-200 dark:text-black-600 dark:hover:bg-navy-600";
  const primary =
    "rounded-lg bg-green-500 px-4 py-2 text-sm font-medium text-white-100 hover:bg-green-600 disabled:opacity-50";
</script>

<div class="flex items-center gap-2 px-6 pt-5">
  <h2 class="flex-1 text-[17px] font-semibold text-black-900 dark:text-white-100">{convertProject ? "Make this an agent" : "New agent"}</h2>
  <button
    type="button"
    class="flex h-8 w-8 items-center justify-center rounded-lg text-black-800 hover:bg-white-200 dark:text-black-600 dark:hover:bg-navy-600"
    aria-label="Close"
    onclick={onClose}
  >
    <svg class="h-4 w-4" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M4 4l8 8M12 4l-8 8"></path></svg>
  </button>
</div>

{#if onType && !convertProject}
  <div class="px-6 pt-3">
    <div class="inline-flex rounded-lg border border-white-300 p-0.5 dark:border-navy-600" role="group" aria-label="Agent type">
      <button type="button" class="rounded-md bg-green-500 px-3 py-1 text-xs text-white-100" aria-pressed="true">Wick agent</button>
      <button type="button" class="rounded-md px-3 py-1 text-xs text-black-800 dark:text-black-600" aria-pressed="false" data-testid="aw-type-remote" onclick={() => onType?.("remote")}>Remote agent</button>
    </div>
  </div>
{/if}

<ol class="flex items-center gap-2 px-6 pt-3 pb-4 text-xs" aria-label="Steps">
  {#each STEPS as s, i (s)}
    {@const n = i + 1}
    <li class="flex items-center gap-1.5 {n === step ? 'font-semibold text-black-900 dark:text-white-100' : 'text-black-800 dark:text-black-600'}" aria-current={n === step ? "step" : undefined}>
      <span class="flex h-5 w-5 items-center justify-center rounded-full text-[11px] font-bold {n <= step ? 'bg-green-500 text-white-100' : 'bg-white-300 text-black-800 dark:bg-navy-600 dark:text-black-600'}">{n < step ? "✓" : n}</span>
      {s}
    </li>
    {#if i < STEPS.length - 1}<li class="h-px w-8 bg-white-300 dark:bg-navy-600" aria-hidden="true"></li>{/if}
  {/each}
</ol>

<div class="min-h-0 flex-1 space-y-4 overflow-y-auto px-6 pb-2">
  {#if step === 1}
    {#if convertProject}
      <p class="rounded-xl bg-white-200 px-3 py-2 text-xs text-black-800 dark:bg-navy-800 dark:text-black-600" data-testid="aw-convert-note">
        This project becomes the agent's own: its chats and files stay, it leaves the Projects list and lives on in Team.
        {#if convertInfo}
          <br /><span data-testid="aw-convert-summary">{convertSummary(convertInfo.chats, convertInfo.channels ?? [], convertInfo.schedules, convertInfo.workflows ?? [])}</span>
        {/if}
        <br />What they do now is kept: every connector at Write (plus new ones), run as the caller, and the global system prompt — both shown, and changeable, on the next step.
      </p>
    {/if}
    {#if firstAgent && !convertProject}
      <div class="flex gap-2.5 rounded-xl border border-green-500/40 bg-green-500/10 px-3 py-2.5 text-sm text-black-900 dark:text-white-100" data-testid="aw-captain-note">
        <span aria-hidden="true">🧭</span>
        <div class="min-w-0">
          <b>This agent will be your Captain.</b> Name it anything and write its own persona below.
          <details class="mt-1.5 text-xs text-black-800 dark:text-black-600" data-testid="aw-captain-what">
            <summary class="cursor-pointer select-none">What is a Captain?</summary>
            <ul class="mt-1.5 list-disc space-y-0.5 pl-4">
              {#each CAPTAIN_FACTS as f (f)}<li>{f}</li>{/each}
            </ul>
          </details>
          <button
            type="button"
            class="mt-2 rounded-full border border-white-300 bg-white-100 px-3 py-1 text-xs text-black-900 hover:bg-white-200 dark:border-navy-600 dark:bg-navy-800 dark:text-white-100 dark:hover:bg-navy-600"
            data-testid="aw-captain-starter"
            onclick={useCaptainStarter}
          >Use Captain starter persona</button>
        </div>
      </div>
    {/if}
    <div class="rounded-xl border border-white-300 p-3 dark:border-navy-600">
      <label class={label} for="aw-brief">What should this agent do?</label>
      <textarea id="aw-brief" class="{input} min-h-16" rows="2" bind:value={brief} placeholder="e.g. Review pull requests critically and point out risky changes"></textarea>
      <div class="mt-2">
        <AIGenerateButton
          kind={PERSONA_KIND}
          autoUse
          testid="aw-generate"
          validate={() => (brief.trim() ? "" : "Describe what the agent should do first.")}
          input={() => personaInput("all", brief, {}, catalog)}
          onUse={(d: PersonaDraft) => useDraft(d)}
        />
      </div>
    </div>
    <div class="flex items-center gap-4">
      <AgentAvatar {kind} {shape} {expression} {color} size={64} live />
      <div class="space-y-2">
        <div class="inline-flex rounded-lg border border-white-300 p-0.5 dark:border-navy-600" role="group" aria-label="Avatar kind">
          {#each [["", "Classic"], ["blob", "Blob"]] as [k, lbl] (k)}
            <button
              type="button"
              class="rounded-md px-3 py-1 text-xs {kind === k ? 'bg-green-500 text-white-100' : 'text-black-800 dark:text-black-600'}"
              aria-pressed={kind === k}
              data-testid="avatar-kind-{k || 'classic'}"
              onclick={() => setKind(k)}
            >{lbl}</button>
          {/each}
        </div>
        {#if isBlobKind(kind)}
          <BlobAvatarPicker {shape} {expression} {color} compact onChange={(l) => { shape = l.shape; expression = l.expression; color = l.color; avatarTouched = true; }} />
        {:else}
        <div class="flex gap-2">
          {#each AVATAR_SHAPES as s (s)}
            <button
              type="button"
              class="rounded-xl border-2 p-1 {shape === s ? 'border-green-500' : 'border-white-300 dark:border-navy-600'}"
              aria-label={s}
              aria-pressed={shape === s}
              onclick={() => { shape = s; avatarTouched = true; }}
            ><AgentAvatar shape={s} {color} size={28} /></button>
          {/each}
        </div>
        <div class="flex flex-wrap gap-1.5">
          {#each AVATAR_COLORS as col (col)}
            <button
              type="button"
              class="h-6 w-6 rounded-full border-2 {color === col ? 'border-green-500' : 'border-transparent'}"
              style:background-color={col}
              aria-label={col}
              aria-pressed={color === col}
              onclick={() => { color = col; avatarTouched = true; }}
            ></button>
          {/each}
        </div>
        {/if}
      </div>
    </div>
    <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
      <div>
        <label class={label} for="aw-name">Name</label>
        <!-- svelte-ignore a11y_autofocus -->
        <input id="aw-name" class={input} bind:value={name} placeholder="Log Hunter" autofocus />
        <label class="{label} mt-3" for="aw-tagline">Tagline</label>
        <input id="aw-tagline" class={input} bind:value={tagline} maxlength={TAGLINE_MAX} placeholder="e.g. The Critic" />
      </div>
      <div>
        <label class={label} for="aw-handle">Handle</label>
        <input
          id="aw-handle"
          class="{input} font-mono"
          value={handle}
          oninput={(e) => { handleTouched = true; handleError = ""; handle = (e.currentTarget as HTMLInputElement).value.toLowerCase().replace(/^@/, ""); }}
          placeholder="log-hunter"
        />
        {#if handleError}
          <p class="mt-1 text-xs text-neg-400">{handleError}</p>
        {:else if handleTaken}
          <p class="mt-1 text-xs text-neg-400">@{handle} is already taken by another agent.</p>
        {:else if handle && !handleOk}
          <p class="mt-1 text-xs text-neg-400">Lowercase letters, digits and "-", 2–31 characters, starting with a letter or digit.</p>
        {/if}
      </div>
    </div>
    <div>
      <label class={label} for="aw-desc">Short description</label>
      <input id="aw-desc" class={input} bind:value={description} placeholder="One line on what this agent is for" />
    </div>
    <div>
      <label class={label} for="aw-sys">System prompt (persona)</label>
      <textarea id="aw-sys" class="{input} min-h-24" rows="4" bind:value={systemPrompt} placeholder="What this agent focuses on, e.g. Investigate production errors from Loki and summarise the cause."></textarea>
    </div>
    {#if convertProject}
      <!-- Converting: the project already exists and IS the agent's home, so
           there is nothing to choose — say which one, read-only. -->
      <p class="text-sm text-black-800 dark:text-black-600" data-testid="aw-convert-project">
        Project: <span class="font-medium text-black-900 dark:text-white-100">{convertProjectName}</span> (this project)
      </p>
    {:else}
    <details class="text-sm text-black-800 dark:text-black-600">
      <summary class="cursor-pointer select-none">Advanced — project (default: created automatically)</summary>
      <div class="mt-2">
        <select class={input} value={projectId} onchange={(e) => pickProject((e.currentTarget as HTMLSelectElement).value)} aria-label="Project">
          <option value="">New project (automatic)</option>
          {#each projects as p (p.id)}
            <option value={p.id}>{projectOptionLabel(p)}</option>
          {/each}
        </select>
        {#if projectId}
          <p class="mt-1 text-xs">{projectLoading ? "Loading project persona…" : "The persona above is loaded from this project; the name and system prompt you save apply to that project."}</p>
        {/if}
      </div>
    </details>
    {/if}
    {#if !projectId || projectId === convertProject}
      <label class="flex items-start gap-2 text-sm text-black-900 dark:text-white-100" data-testid="aw-show-in-projects">
        <input type="checkbox" class="mt-1" bind:checked={showInProjects} />
        <span>Keep the project in Agents → Projects<br /><span class="text-xs text-black-800 dark:text-black-600">It is always in Team. Turn off to list it only there.</span></span>
      </label>
    {/if}
  {:else if step === 2}
    {#if convertProject}
      <p class="rounded-xl bg-white-200 px-3 py-2 text-xs text-black-800 dark:bg-navy-800 dark:text-black-600" data-testid="aw-convert-access">
        <span class="font-medium text-black-900 dark:text-white-100">Default for a converted project:</span> every connector you have at Write, and connectors added later open read-only — so its chats keep doing what they do today. Narrow it below or later in Settings.
      </p>
      <label class="flex items-start gap-2 text-sm text-black-900 dark:text-white-100" data-testid="aw-global-prompt">
        <input type="checkbox" class="mt-1" bind:checked={useGlobalPrompt} />
        <span>Use the global system prompt<br /><span class="text-xs text-black-800 dark:text-black-600">On by default: the project's chats already run with it. Turn off to run on the persona alone.</span></span>
      </label>
    {:else}
    <p class="text-sm text-black-800 dark:text-black-600">Connectors are off until you add them. Platform tools are on for every agent; System tools are for the Captain.</p>
    {/if}
    {#if suggestions.length > 0}
      <div class="flex flex-wrap items-center gap-2 rounded-xl border border-dashed border-green-500 px-3 py-2 text-xs text-black-800 dark:text-black-600" data-testid="aw-suggested">
        <span class="font-medium">✨ Suggested:</span>
        {#each suggestions as c (c.id)}
          <button
            type="button"
            class="rounded-full border border-white-300 px-2.5 py-1 font-medium text-black-900 hover:border-green-500 dark:border-navy-600 dark:text-white-100"
            title="Add {c.label} read-only"
            onclick={() => (grants = setOverride(grants, c.id, "read"))}
          >+ {c.label}</button>
        {/each}
      </div>
    {/if}
    <ConnectorChecklist
      {catalog}
      loading={catalogLoading}
      loadError={catalogError}
      bind:grants
      bind:includeNew
      bind:accessMode
      errors={grantErrors}
      showRunAs={false}
      showIncludeNew={!!convertProject}
    />
  {:else}
    <!-- Step 3: the same picker as Project Settings (provider → model / sub
         model). No default is filled in: the user picks one on purpose. -->
    <div data-testid="aw-model-step">
      <label class={label} for="aw-provider">Provider &amp; model</label>
      <ProviderPicker
        id="aw-provider"
        options={buildProviderOptions(providers, pick)}
        value={pick}
        onChange={(v) => (pick = v)}
        loadModels={loadProviderModels}
        placeholder="Choose provider & model…"
      />
      <p class="mt-1 text-xs text-black-800 dark:text-black-600">
        {modelOk ? "Saved as the agent's project default; change it later in Settings → Advanced." : "Pick a provider, then a model, to create the agent."}
      </p>
    </div>
  {/if}
  {#if error}<p class="text-sm text-neg-400">{error}</p>{/if}
</div>

<div class="flex items-center justify-between gap-2 px-6 py-4">
  {#if step > 1}
    <button type="button" class={ghost} onclick={() => (step -= 1)}>← Back</button>
  {:else}
    <span></span>
  {/if}
  {#if step < STEPS.length}
    <button type="button" class={primary} disabled={!personaOk} onclick={next}>Next →</button>
  {:else}
    <button type="button" class={primary} disabled={!personaOk || !modelOk || saving} onclick={submit}>{saving ? "Creating…" : "Create agent"}</button>
  {/if}
</div>
