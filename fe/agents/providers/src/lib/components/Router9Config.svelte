<script lang="ts">
  import { toastError, toastOk } from "@wick-fe/common-stores";
  import {
    apiRouter9Status,
    apiRouter9Start,
    apiRouter9Models,
    apiRouter9Slots,
    type Router9Status,
    type Router9Slot,
    type Router9Model,
  } from "$lib/api.js";

  type Props = {
    base: string;
    type: string;
    // Only claude/codex support 9router; the parent gates visibility.
    supported: boolean;
    use9router: boolean;
    // models maps slot key → chosen model id (bindable).
    models: Record<string, string>;
    apiKey: string;
    // apiKeyMasked: a key is already stored (detail page) — placeholder hints
    // that leaving the field blank keeps it.
    apiKeyMasked?: boolean;
  };
  let {
    base,
    type,
    supported,
    use9router = $bindable(),
    models = $bindable(),
    apiKey = $bindable(),
    apiKeyMasked = false,
  }: Props = $props();

  let status = $state<Router9Status | null>(null);
  let loadingStatus = $state(false);
  let starting = $state(false);
  let slots = $state<Router9Slot[]>([]);
  let modelList = $state<Router9Model[]>([]);
  let loadingModels = $state(false);

  // Popup picker state.
  let pickerSlot = $state<string | null>(null);
  let pickerQuery = $state("");

  const installUrl = $derived(`${base}/9router`);
  const canEnable = $derived(status?.installed === true && status?.running === true);

  // Human labels for the owned_by group keys 9router emits. Unknown keys
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
  const groupedModels = $derived.by(() => {
    const groups: Array<{ key: string; label: string; models: Router9Model[] }> = [];
    const index = new Map<string, number>();
    for (const m of filteredModels) {
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
    loadingStatus = true;
    try {
      status = await apiRouter9Status(base);
      if (status.running) await refreshModels();
    } catch (e) {
      status = null;
      toastError(e instanceof Error ? e.message : "9router status failed");
    } finally {
      loadingStatus = false;
    }
  }

  async function refreshModels(): Promise<void> {
    loadingModels = true;
    try {
      // /v1/models already includes user-defined combos as owned_by=combo
      // rows (e.g. "auto"), so a single fetch covers every group. Combos
      // are kept — the user owns that choice.
      modelList = await apiRouter9Models();
    } finally {
      loadingModels = false;
    }
  }

  async function loadSlots(): Promise<void> {
    if (!supported) return;
    try {
      slots = await apiRouter9Slots(base, type);
    } catch {
      slots = [];
    }
  }

  async function onToggle(next: boolean): Promise<void> {
    if (next && !status) await refreshStatus();
    use9router = next;
    if (next && status?.running && modelList.length === 0) await refreshModels();
  }

  async function startRouter9(): Promise<void> {
    starting = true;
    try {
      await apiRouter9Start(base);
      toastOk("9router started");
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

  // Load status once on mount.
  $effect(() => {
    if (supported && status === null && !loadingStatus) {
      void refreshStatus();
    }
  });

  // Reload slots whenever the provider type flips or support resolves.
  // Create form lets the user switch claude↔codex (different slot sets);
  // detail page sets `supported` true only once `data` loads. Reading both
  // here makes the effect re-run on either change, not just at mount.
  $effect(() => {
    void type; // dependency
    void supported; // dependency
    void loadSlots();
  });
</script>

{#if supported}
  <div class="rounded-lg border border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800 p-4 space-y-3">
    <div class="flex items-center justify-between">
      <div>
        <p class="text-sm font-medium text-black-900 dark:text-white-100">Use 9router</p>
        <p class="text-[11px] text-black-700 dark:text-black-600">Route this provider through the embedded 9router proxy.</p>
      </div>
      <button
        type="button"
        role="switch"
        aria-checked={use9router}
        aria-label="Use 9router"
        disabled={!canEnable && !use9router}
        onclick={() => onToggle(!use9router)}
        class="relative inline-flex h-6 w-11 shrink-0 items-center rounded-full transition-colors disabled:opacity-50 {use9router ? 'bg-green-500' : 'bg-white-400 dark:bg-navy-600'}"
      >
        <span class="inline-block h-4 w-4 rounded-full bg-white-100 transition-transform {use9router ? 'translate-x-6' : 'translate-x-1'}"></span>
      </button>
    </div>

    {#if loadingStatus}
      <p class="text-[11px] text-black-700 dark:text-black-600">Checking 9router…</p>
    {:else if status && !status.installed}
      <div class="rounded-lg border border-cau-400/40 bg-cau-400/10 px-3 py-2 text-[11px] text-cau-400">
        9router is not installed.
        <a href={installUrl} class="font-medium text-link-400 hover:underline">Install it here</a>
        then reopen this form.
      </div>
    {:else if status && status.installed && !status.running}
      <div class="flex items-center justify-between rounded-lg border border-cau-400/40 bg-cau-400/10 px-3 py-2">
        <span class="text-[11px] text-cau-400">9router is installed but not running.</span>
        <button
          type="button"
          onclick={startRouter9}
          disabled={starting}
          class="rounded-lg bg-green-500 px-3 py-1 text-[11px] font-medium text-white-100 hover:bg-green-600 disabled:opacity-50"
        >{starting ? "Starting…" : "Start"}</button>
      </div>
    {/if}

    {#if use9router && canEnable}
      <div class="space-y-3">
        {#each slots as slot (slot.key)}
          <div>
            <label for={`r9-slot-${slot.key}`} class="block text-xs font-medium text-black-800 dark:text-black-600 mb-1">
              {slot.label}
              <span class="text-black-600 dark:text-black-700 font-normal">(optional)</span>
            </label>
            <div class="flex gap-2">
              <div class="relative w-full">
                <input
                  id={`r9-slot-${slot.key}`}
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
          <label for="router9-key" class="block text-xs font-medium text-black-800 dark:text-black-600 mb-1">API Key (optional)</label>
          <input
            id="router9-key"
            type="password"
            bind:value={apiKey}
            placeholder={apiKeyMasked ? "•••••••• (leave empty to keep)" : "leave empty = default"}
            class="w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-sm font-mono text-black-900 dark:text-white-100"
          />
        </div>
      </div>
    {/if}
  </div>

  {#if pickerSlot !== null}
    <div
      class="fixed inset-0 z-[60] flex items-center justify-center bg-black/50"
      role="presentation"
      onclick={(e) => { if (e.target === e.currentTarget) pickerSlot = null; }}
      onkeydown={(e) => { if (e.key === "Escape") pickerSlot = null; }}
    >
      <div class="w-full max-w-md rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-5 shadow-xl mx-4" role="dialog" aria-modal="true">
        <h3 class="mb-3 text-sm font-semibold text-black-900 dark:text-white-100">
          Select model{#if pickerSlot} for {slots.find((s) => s.key === pickerSlot)?.label ?? pickerSlot}{/if}
        </h3>
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
        <div class="mt-3 flex justify-end">
          <button type="button" onclick={() => (pickerSlot = null)} class="rounded-lg border border-white-400 dark:border-navy-600 px-4 py-2 text-sm text-black-800 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800">Close</button>
        </div>
      </div>
    </div>
  {/if}
{/if}
