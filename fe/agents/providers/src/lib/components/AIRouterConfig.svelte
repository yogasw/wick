<script lang="ts">
  import { toastError, toastOk } from "@wick-fe/common-stores";
  import { Modal } from "@wick-fe/common-ui";
  import {
    apiAIRouterStatus,
    apiAIRouterStart,
    apiAIRouterModels,
    apiAIRouterSlots,
    type AIRouterStatus,
    type AIRouterSlot,
    type AIRouterModel,
  } from "$lib/api.js";

  type Props = {
    base: string;
    type: string;
    // Only claude/codex support an AI router; the parent gates visibility.
    supported: boolean;
    useAirouter: boolean;
    // provider is the selected router id (bindable).
    provider: string;
    // routers is the list of available routers to pick from.
    routers: { ID: string; Name: string }[];
    // models maps slot key → chosen model id (bindable).
    models: Record<string, string>;
    apiKey: string;
    // apiKeyMasked: a key is already stored (detail page) — placeholder hints
    // that leaving the field blank keeps it.
    apiKeyMasked?: boolean;
    // rawConfig is free-form extra config appended to the router spawn
    // (bindable). One entry per line.
    rawConfig: string;
    // configPreview is the effective config wick sets on spawn (read-only,
    // secrets masked), rendered by the BE from the saved settings. Empty on
    // the create form (no saved instance yet).
    configPreview?: string;
  };
  let {
    base,
    type,
    supported,
    useAirouter = $bindable(),
    provider = $bindable(),
    routers,
    models = $bindable(),
    apiKey = $bindable(),
    apiKeyMasked = false,
    rawConfig = $bindable(),
    configPreview = "",
  }: Props = $props();

  // Advanced ("show raw config") section is collapsed by default.
  let showAdvanced = $state(false);

  // Opening the advanced section seeds the editable config box with the
  // effective config wick generates (when the user hasn't already set an
  // override), so they edit the real config directly instead of from a blank
  // box. Editing it becomes the override; clearing it (or Reset) falls back to
  // the generated config.
  function toggleAdvanced(): void {
    showAdvanced = !showAdvanced;
    if (showAdvanced && rawConfig.trim() === "" && configPreview) {
      rawConfig = configPreview;
    }
  }
  function resetRawConfig(): void {
    rawConfig = configPreview;
  }


  // Raw-config hint depends on the agent type: codex takes `-c` TOML
  // overrides, claude/gemini take env vars.
  const rawHint = $derived(
    type === "codex"
      ? 'One codex -c override per line, e.g. model_reasoning_effort="high"'
      : "One env var per line, e.g. ANTHROPIC_SMALL_FAST_MODEL=...",
  );

  let status = $state<AIRouterStatus | null>(null);
  let loadingStatus = $state(false);
  let starting = $state(false);
  let slots = $state<AIRouterSlot[]>([]);
  let modelList = $state<AIRouterModel[]>([]);
  let loadingModels = $state(false);

  // Popup picker state.
  let pickerSlot = $state<string | null>(null);
  let pickerQuery = $state("");

  const installUrl = $derived(`${base}/airouter`);
  const canEnable = $derived(status?.installed === true && status?.running === true);
  // Human label for the selected router (falls back to a generic name).
  const routerName = $derived(routers.find((r) => r.ID === provider)?.Name ?? "AI Router");

  // Human labels for the owned_by group keys a router emits. Unknown keys
  // fall back to the raw key (uppercased) so new groups still render.
  const GROUP_LABELS: Record<string, string> = {
    combo: "Combos",
    kc: "Kilo Code",
    gc: "Gemini CLI",
    cc: "Claude Code",
    oc: "OpenAI Codex",
  };
  function groupLabel(key: string): string {
    return GROUP_LABELS[key] ?? (key ? key.toUpperCase() : "Other");
  }

  const filteredModels = $derived(
    pickerQuery.trim() === ""
      ? modelList
      : modelList.filter((m) => m.id.toLowerCase().includes(pickerQuery.trim().toLowerCase())),
  );

  // Group the filtered models by owned_by, preserving first-seen order.
  // Dedupe by id GLOBALLY: a router that aggregates many upstreams (OmniRoute)
  // can list the same model id more than once — a duplicate {#each} key crashes
  // Svelte (each_key_duplicate), so keep only the first occurrence of each id.
  const groupedModels = $derived.by(() => {
    const groups: Array<{ key: string; label: string; models: AIRouterModel[] }> = [];
    const index = new Map<string, number>();
    const seenIds = new Set<string>();
    for (const m of filteredModels) {
      if (seenIds.has(m.id)) continue;
      seenIds.add(m.id);
      let i = index.get(m.ownedBy);
      if (i === undefined) {
        i = groups.length;
        index.set(m.ownedBy, i);
        groups.push({ key: m.ownedBy, label: groupLabel(m.ownedBy), models: [] });
      }
      groups[i].models.push(m);
    }
    return groups;
  });

  function clearSlot(slotKey: string): void {
    models[slotKey] = "";
  }

  async function refreshStatus(): Promise<void> {
    if (provider === "") return;
    loadingStatus = true;
    try {
      status = await apiAIRouterStatus(base, provider);
      if (status.running) await refreshModels();
    } catch (e) {
      status = null;
      toastError(e instanceof Error ? e.message : "AI Router status failed");
    } finally {
      loadingStatus = false;
    }
  }

  async function refreshModels(): Promise<void> {
    if (provider === "") return;
    loadingModels = true;
    try {
      // /v1/models already includes user-defined combos as owned_by=combo
      // rows (e.g. "auto"), so a single fetch covers every group. Combos
      // are kept — the user owns that choice.
      modelList = await apiAIRouterModels(provider);
    } finally {
      loadingModels = false;
    }
  }

  async function loadSlots(): Promise<void> {
    if (!supported || provider === "") return;
    try {
      slots = await apiAIRouterSlots(base, type, provider);
    } catch {
      slots = [];
    }
  }

  async function onToggle(next: boolean): Promise<void> {
    if (next && !status) await refreshStatus();
    useAirouter = next;
    if (next && status?.running && modelList.length === 0) await refreshModels();
  }

  async function startRouter(): Promise<void> {
    starting = true;
    try {
      await apiAIRouterStart(base, provider);
      toastOk(`${routerName} started`);
      await refreshStatus();
    } catch (e) {
      toastError(e instanceof Error ? e.message : "Start failed");
    } finally {
      starting = false;
    }
  }

  function openPicker(slotKey: string): void {
    pickerQuery = "";
    pickerSlot = slotKey;
    if (modelList.length === 0) void refreshModels();
  }

  function pickModel(m: string): void {
    if (pickerSlot) models[pickerSlot] = m;
    pickerSlot = null;
  }

  // Default the selected router to the first available one so every call
  // has a concrete router id to target.
  $effect(() => {
    if (provider === "" && routers.length > 0) {
      provider = routers[0].ID;
    }
  });

  // Load slots + status + models whenever the provider type flips, support
  // resolves, or the selected router changes. Switching router must refetch
  // everything, so clear the stale status/models first.
  $effect(() => {
    void type; // dependency
    void supported; // dependency
    const rid = provider; // dependency
    if (!supported || rid === "") return;
    status = null;
    modelList = [];
    void loadSlots();
    void refreshStatus();
  });
</script>

{#if supported}
  <div class="rounded-lg border border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800 p-4 space-y-3">
    <!-- Toggle first: express the intent to route through an AI router, THEN
         pick which one + start it. -->
    <div class="flex items-center justify-between">
      <div>
        <p class="text-sm font-medium text-black-900 dark:text-white-100">Use AI Router</p>
        <p class="text-[11px] text-black-700 dark:text-black-600">Route this provider's model calls through an embedded AI router proxy.</p>
      </div>
      <button
        type="button"
        role="switch"
        aria-checked={useAirouter}
        aria-label="Use AI Router"
        onclick={() => onToggle(!useAirouter)}
        class="relative inline-flex h-6 w-11 shrink-0 items-center rounded-full transition-colors {useAirouter ? 'bg-green-500' : 'bg-white-400 dark:bg-navy-600'}"
      >
        <span class="inline-block h-4 w-4 rounded-full bg-white-100 transition-transform {useAirouter ? 'translate-x-6' : 'translate-x-1'}"></span>
      </button>
    </div>

    {#if useAirouter}
      {#if routers.length > 0}
        <div>
          <label for="airouter-provider" class="block text-xs font-medium text-black-800 dark:text-black-600 mb-1">Router</label>
          <select
            id="airouter-provider"
            bind:value={provider}
            class="w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-sm text-black-900 dark:text-white-100"
          >
            {#each routers as r (r.ID)}
              <option value={r.ID}>{r.Name}</option>
            {/each}
          </select>
        </div>
      {/if}

      {#if loadingStatus}
        <p class="text-[11px] text-black-700 dark:text-black-600">Checking {routerName}…</p>
      {:else if status && !status.installed}
        <div class="rounded-lg border border-cau-400/40 bg-cau-400/10 px-3 py-2 text-[11px] text-cau-400">
          {routerName} is not installed.
          <a href={installUrl} class="font-medium text-link-400 hover:underline">Install it here</a>
          then reopen this form.
        </div>
      {:else if status && status.installed && !status.running}
        <div class="flex items-center justify-between rounded-lg border border-cau-400/40 bg-cau-400/10 px-3 py-2">
          <span class="text-[11px] text-cau-400">{routerName} is installed but not running.</span>
          <button
            type="button"
            onclick={startRouter}
            disabled={starting}
            class="rounded-lg bg-green-500 px-3 py-1 text-[11px] font-medium text-white-100 hover:bg-green-600 disabled:opacity-50"
          >{starting ? "Starting…" : "Start"}</button>
        </div>
      {/if}
    {/if}

    {#if useAirouter && canEnable}
      <div class="space-y-3">
        {#each slots as slot (slot.key)}
          <div>
            <label for={`air-slot-${slot.key}`} class="block text-xs font-medium text-black-800 dark:text-black-600 mb-1">
              {slot.label}
              <span class="text-black-600 dark:text-black-700 font-normal">(optional)</span>
            </label>
            <div class="flex gap-2">
              <div class="relative w-full">
                <input
                  id={`air-slot-${slot.key}`}
                  type="text"
                  bind:value={models[slot.key]}
                  placeholder={`e.g. ${slot.placeholder}`}
                  class="w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 pr-8 text-sm font-mono text-black-900 dark:text-white-100"
                />
                {#if (models[slot.key] ?? "") !== ""}
                  <button
                    type="button"
                    onclick={() => clearSlot(slot.key)}
                    title="Clear"
                    aria-label={`Clear ${slot.label}`}
                    class="absolute right-2 top-1/2 -translate-y-1/2 text-black-700 dark:text-black-600 hover:text-black-900 dark:hover:text-white-100"
                  >✕</button>
                {/if}
              </div>
              <button
                type="button"
                onclick={() => openPicker(slot.key)}
                title="Pick a model"
                aria-label={`Pick a model for ${slot.label}`}
                class="shrink-0 rounded-lg border border-white-400 dark:border-navy-600 px-3 py-2 text-sm text-black-800 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-700"
              >⋯</button>
            </div>
          </div>
        {/each}

        <div>
          <label for="airouter-key" class="block text-xs font-medium text-black-800 dark:text-black-600 mb-1">API Key (optional)</label>
          <input
            id="airouter-key"
            type="password"
            bind:value={apiKey}
            placeholder={apiKeyMasked ? "•••••••• (leave empty to keep)" : "leave empty = default"}
            class="w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-sm font-mono text-black-900 dark:text-white-100"
          />
        </div>
      </div>
    {/if}

    {#if useAirouter}
      <!-- Advanced: the effective config wick passes to the CLI — EDITABLE.
           Collapsed by default; editing it fully overrides the generated
           config (the API key stays in the field above, shown as <API_KEY>). -->
      <div class="border-t border-white-300 dark:border-navy-600 pt-3">
        <button
          type="button"
          onclick={toggleAdvanced}
          class="flex items-center gap-1.5 text-[11px] font-medium text-black-700 dark:text-black-600 hover:text-black-900 dark:hover:text-white-100"
        >
          <svg viewBox="0 0 16 16" class="h-3 w-3 transition-transform {showAdvanced ? 'rotate-90' : ''}" fill="none" stroke="currentColor" stroke-width="2">
            <path d="M6 4l4 4-4 4" stroke-linecap="round" stroke-linejoin="round"></path>
          </svg>
          Show raw config
        </button>

        {#if showAdvanced}
          <div class="mt-2">
            <div class="mb-1 flex items-center justify-between gap-2">
              <label for="airouter-raw" class="text-xs font-medium text-black-800 dark:text-black-600">Config passed to the CLI (editable)</label>
              <button type="button" onclick={resetRawConfig} class="text-[11px] font-medium text-link-400 hover:underline">Reset to generated</button>
            </div>
            <textarea
              id="airouter-raw"
              bind:value={rawConfig}
              rows="8"
              spellcheck="false"
              placeholder={configPreview || rawHint}
              class="w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-[12px] leading-relaxed font-mono text-black-900 dark:text-white-100"
            ></textarea>
            <p class="mt-1 text-[11px] text-black-700 dark:text-black-600">
              Fully overrides what wick sets — edit or remove any line. Keep <span class="font-mono">&lt;API_KEY&gt;</span> as-is; it's filled from the API Key field. Clear the box (or Reset) to use the generated config.
            </p>
          </div>
        {/if}
      </div>
    {/if}
  </div>

  <Modal
    open={pickerSlot !== null}
    title={`Select model${pickerSlot ? " for " + (slots.find((s) => s.key === pickerSlot)?.label ?? pickerSlot) : ""}`}
    size="md"
    onClose={() => (pickerSlot = null)}
  >
    <input
      type="text"
      bind:value={pickerQuery}
      placeholder="Search models…"
      class="mb-3 w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-sm text-black-900 dark:text-white-100"
    />
    <div class="max-h-80 overflow-y-auto space-y-3">
      {#if loadingModels}
        <p class="text-[11px] text-black-700 dark:text-black-600">Loading models…</p>
      {:else if filteredModels.length === 0}
        <p class="text-[11px] text-black-700 dark:text-black-600">No models found. Type the id manually in the field.</p>
      {:else}
        {#each groupedModels as g (g.key)}
          <div>
            <p class="mb-1 flex items-center gap-1.5 text-[11px] font-semibold uppercase tracking-wide text-green-600 dark:text-green-400">
              {g.label}
              <span class="rounded-full bg-white-300 dark:bg-navy-600 px-1.5 py-0.5 text-[10px] font-medium text-black-700 dark:text-black-600">{g.models.length}</span>
            </p>
            <div class="space-y-1">
              {#each g.models as m (m.id)}
                <button
                  type="button"
                  onclick={() => pickModel(m.id)}
                  class="flex w-full items-center justify-between gap-2 rounded-lg px-3 py-2 text-left text-sm font-mono text-black-900 dark:text-white-100 hover:bg-white-200 dark:hover:bg-navy-800 {models[pickerSlot ?? ''] === m.id ? 'bg-green-100 dark:bg-green-900' : ''}"
                >
                  <span class="truncate">{m.id}</span>
                  {#if models[pickerSlot ?? ''] === m.id}<span class="shrink-0 text-green-600 dark:text-green-400">✓</span>{/if}
                </button>
              {/each}
            </div>
          </div>
        {/each}
      {/if}
    </div>
  </Modal>
{/if}
