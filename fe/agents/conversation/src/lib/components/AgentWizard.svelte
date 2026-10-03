<script lang="ts">
  /* + Agent: a centred modal in two steps (mockup create()).
     1 Persona — an optional one-line brief whose ✨ Generate fills the
       fields below (queued aigen job), then avatar, name, tagline, handle,
       description, system prompt, and the project behind it under
       "Advanced" (default: a new one made from these fields).
     2 Access — the same connector checklist as Settings › Access, starting
       empty (off until added). A new agent runs as the caller with
       include-new off; both are changed later in Settings › Access.
     The provider lives in Settings › Lanjutan; the mockup's third "Connect"
     step waits for Slack/A2A (phase 1b). */
  import { onMount } from "svelte";
  import { AIGenerateButton } from "@wick-fe/common-ui";
  import { AgentAvatar, AVATAR_SHAPES, AVATAR_COLORS, defaultAvatarFor } from "@wick-fe/common-avatar";
  import ConnectorChecklist from "./ConnectorChecklist.svelte";
  import { getProjectOptions } from "../api/options.js";
  import { createAgent, getProjectPersona, listAgentConnectors, runApi, type AgentItem, type AgentConnector, type ConnectorGrant } from "../api/team.js";
  import { HANDLE_RE, slugHandle, uniqueHandle, parseGrantErrors, projectOptionLabel, type GrantErrors, type PickerProject } from "../agentForm.js";
  import { PERSONA_KIND, personaInput, suggestedConnectors, type PersonaDraft } from "../personaGen.js";
  import { setOverride } from "../accessTiers.js";

  type Props = {
    base: string;
    /** Handles already in use, so the suggestion never collides. */
    taken: string[];
    onClose: () => void;
    onCreated: (a: AgentItem) => void;
  };
  let { base, taken, onClose, onCreated }: Props = $props();

  const STEPS = ["Persona", "Access"];
  let step = $state(1);

  let brief = $state("");
  let suggested = $state<string[]>([]);
  let name = $state("");
  let tagline = $state("");
  let description = $state("");
  let handle = $state("");
  let handleTouched = $state(false);
  let systemPrompt = $state("");
  let shape = $state("circle");
  let color = $state(AVATAR_COLORS[1]);
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

  let saving = $state(false);
  let error = $state("");
  let handleError = $state("");
  let grantErrors = $state<GrantErrors | null>(null);

  onMount(() => {
    runApi(getProjectOptions(base, { hideTeam: true })).then((p) => { projects = p ?? []; }).catch(() => {});
    runApi(listAgentConnectors(base))
      .then((c) => { catalog = c ?? []; })
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
      if (d.avatar_shape) shape = d.avatar_shape;
      if (d.avatar_color) color = d.avatar_color;
      avatarTouched = true;
    }
    suggested = d.connectors ?? [];
  }
  const suggestions = $derived(suggestedConnectors(suggested, catalog, grants.map((g) => g.connector_id)));

  const handleOk = $derived(HANDLE_RE.test(handle));
  const handleTaken = $derived(taken.includes(handle));
  const personaOk = $derived(name.trim() !== "" && handleOk && !handleTaken);

  function next() {
    if (step === 1 && personaOk) step = 2;
  }

  async function submit() {
    if (!personaOk || saving) return;
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
          avatar: { shape, color },
          ...(projectId ? { project_id: projectId } : {}),
          allowed_connectors: $state.snapshot(grants) as ConnectorGrant[],
          include_new_connectors: false,
          run_as: "caller",
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
  <h2 class="flex-1 text-[17px] font-semibold text-black-900 dark:text-white-100">New agent</h2>
  <button
    type="button"
    class="flex h-8 w-8 items-center justify-center rounded-lg text-black-800 hover:bg-white-200 dark:text-black-600 dark:hover:bg-navy-600"
    aria-label="Close"
    onclick={onClose}
  >
    <svg class="h-4 w-4" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M4 4l8 8M12 4l-8 8"></path></svg>
  </button>
</div>

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
      <AgentAvatar {shape} {color} size={64} />
      <div class="space-y-2">
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
      </div>
    </div>
    <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
      <div>
        <label class={label} for="aw-name">Name</label>
        <!-- svelte-ignore a11y_autofocus -->
        <input id="aw-name" class={input} bind:value={name} placeholder="Log Hunter" autofocus />
        <label class="{label} mt-3" for="aw-tagline">Tagline</label>
        <input id="aw-tagline" class={input} bind:value={tagline} maxlength="32" placeholder="e.g. The Critic" />
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
  {:else}
    <p class="text-sm text-black-800 dark:text-black-600">Connectors are off until you add them. Platform tools are on for every agent; System tools are for the Captain.</p>
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
      errors={grantErrors}
      showRunAs={false}
      showIncludeNew={false}
    />
  {/if}
  {#if error}<p class="text-sm text-neg-400">{error}</p>{/if}
</div>

<div class="flex items-center justify-between gap-2 px-6 py-4">
  {#if step > 1}
    <button type="button" class={ghost} onclick={() => (step = 1)}>← Back</button>
  {:else}
    <span></span>
  {/if}
  {#if step < STEPS.length}
    <button type="button" class={primary} disabled={!personaOk} onclick={next}>Next →</button>
  {:else}
    <button type="button" class={primary} disabled={!personaOk || saving} onclick={submit}>{saving ? "Creating…" : "Create agent"}</button>
  {/if}
</div>
