<script lang="ts">
  import { onMount } from "svelte";
  import { ConfirmDialog, Breadcrumb, Modal, Select, Button, type BreadcrumbItem } from "@wick-fe/common-ui";
  import { toastOk, toastError } from "@wick-fe/common-stores";
  import {
    apiGetWickConfig,
    apiSaveWickModel,
    apiDeleteWickModel,
    apiSetWickModelDefault,
    apiSaveWickSettings,
    apiDiscoverWickModels,
  } from "$lib/api.js";
  import type { WickModelDTO, WickSettingsDTO, WickDiscoverModel } from "$lib/api.js";
  import RecentSpawns from "$lib/components/RecentSpawns.svelte";

  type Props = {
    base: string;
    onBack: () => void;
    onOpenSession: (sessionID: string) => void;
  };
  let { base, onBack, onOpenSession }: Props = $props();

  let crumbs = $derived<BreadcrumbItem[]>([
    { label: "Providers", onClick: onBack },
    { label: "wick", truncate: true },
  ]);

  // Provider select values map 1:1 to the BE `kind` field.
  const KIND_OPTIONS = [
    { label: "Google Gemini", value: "google" },
    { label: "OpenAI", value: "openai" },
    { label: "Anthropic", value: "anthropic" },
    { label: "OpenRouter", value: "openrouter" },
    { label: "Other provider", value: "other" },
  ];
  const FORMAT_OPTIONS = [
    { label: "Gemini generateContent", value: "gemini" },
    { label: "OpenAI Chat Completions", value: "openai_chat" },
    { label: "OpenAI Responses", value: "openai_responses" },
    { label: "Anthropic Messages", value: "anthropic_messages" },
  ];
  const SHELL_OPTIONS = [
    { label: "Enabled — gate enforced natively", value: "enabled" },
    { label: "Disabled", value: "disabled" },
  ];

  // Per-provider defaults so the modal's Base URL + API format follow the
  // selected provider (shown as placeholders / hints). Mirrors the
  // backend's defaultBaseURL / defaultFormatForKind — keep in sync.
  type KindDefault = {
    baseURL: string;
    format: string;
    formatLabel: string;
    keyExample: string;
    modelExample: string;
  };
  const KIND_DEFAULTS: Record<string, KindDefault> = {
    google:     { baseURL: "https://generativelanguage.googleapis.com", format: "gemini",             formatLabel: "Gemini generateContent", keyExample: "AIza…",   modelExample: "gemini-flash-latest" },
    openai:     { baseURL: "https://api.openai.com/v1",                  format: "openai_chat",        formatLabel: "OpenAI Chat Completions", keyExample: "sk-…",    modelExample: "gpt-5.2" },
    anthropic:  { baseURL: "https://api.anthropic.com/v1",               format: "anthropic_messages", formatLabel: "Anthropic Messages",      keyExample: "sk-ant-…", modelExample: "claude-sonnet-4-5" },
    openrouter: { baseURL: "https://openrouter.ai/api/v1",               format: "openai_chat",        formatLabel: "OpenAI Chat Completions", keyExample: "sk-or-…", modelExample: "qwen/qwen3-coder" },
    other:      { baseURL: "",                                           format: "openai_chat",        formatLabel: "OpenAI Chat Completions", keyExample: "your API key", modelExample: "provider/model-id" },
  };

  function kindChip(kind: string): string {
    switch (kind) {
      case "google": return "Google";
      case "openai": return "OpenAI";
      case "anthropic": return "Anthropic";
      case "openrouter": return "OpenRouter";
      default: return "Other";
    }
  }

  function intOrUndef(s: string): number | undefined {
    const t = s.trim();
    if (t === "") return undefined;
    const n = Number(t);
    return Number.isFinite(n) ? Math.trunc(n) : undefined;
  }
  function floatOrUndef(s: string): number | undefined {
    const t = s.trim();
    if (t === "") return undefined;
    const n = Number(t);
    return Number.isFinite(n) ? n : undefined;
  }

  // ── Config state ────────────────────────────────────────────────────
  let models = $state<WickModelDTO[]>([]);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let busy = $state<Record<string, boolean>>({});
  function setBusy(k: string, v: boolean) { busy = { ...busy, [k]: v }; }

  // ── Provider settings form ──────────────────────────────────────────
  let shellMode = $state("enabled"); // enabled | disabled
  let maxContext = $state("");
  let maxTurns = $state("");
  let defTemp = $state("");
  let defThinking = $state("");
  let settingsRaw = $state("");
  let savingSettings = $state(false);

  function seedSettings(s: WickSettingsDTO) {
    shellMode = s.ShellToolDisabled ? "disabled" : "enabled";
    maxContext = s.MaxContextTokens ? String(s.MaxContextTokens) : "";
    maxTurns = s.MaxTurns ? String(s.MaxTurns) : "";
    defTemp = s.Temperature != null ? String(s.Temperature) : "";
    defThinking = s.ThinkingBudget != null ? String(s.ThinkingBudget) : "";
    settingsRaw = s.RawConfig;
  }

  async function loadConfig(silent = false) {
    if (!silent) { loading = true; error = null; }
    try {
      const cfg = await apiGetWickConfig(base);
      models = cfg.models;
      seedSettings(cfg.settings);
    } catch (e) {
      if (!silent) error = e instanceof Error ? e.message : "Failed to load wick config";
    } finally {
      if (!silent) loading = false;
    }
  }

  async function saveSettings() {
    savingSettings = true;
    try {
      await apiSaveWickSettings(base, {
        shell_tool_disabled: shellMode === "disabled",
        max_context_tokens: intOrUndef(maxContext),
        max_turns: intOrUndef(maxTurns),
        temperature: floatOrUndef(defTemp),
        thinking_budget: intOrUndef(defThinking),
        raw_config: settingsRaw,
      });
      toastOk("Provider settings saved");
      await loadConfig(true);
    } catch (e) {
      toastError(e instanceof Error ? e.message : "Save failed");
    } finally {
      savingSettings = false;
    }
  }

  // ── Add / Edit model modal ──────────────────────────────────────────
  let modalOpen = $state(false);
  let editing = $state<WickModelDTO | null>(null);
  let mKind = $state("openai");
  let mKey = $state("");
  let mKeyTouched = $state(false);
  let mModel = $state("");
  let mLabel = $state("");
  let mBaseURL = $state("");
  let mFormat = $state("");
  let mMaxOut = $state("");
  let mTemp = $state("");
  let mThinking = $state("");
  let mRaw = $state("");
  let advOpen = $state(false);
  let savingModel = $state(false);

  // Placeholders/hints that track the selected provider (example key,
  // model id, base URL, and default API format) so the modal shows a
  // template for each vendor instead of generic text.
  let kindDefault = $derived(KIND_DEFAULTS[mKind] ?? KIND_DEFAULTS.other);
  let baseURLPlaceholder = $derived(
    mKind === "other" ? "https://your-endpoint/v1 (required)" : kindDefault.baseURL,
  );
  let formatPlaceholder = $derived(`Default: ${kindDefault.formatLabel}`);
  let keyPlaceholder = $derived(
    editing && editing.HasKey ? "Stored — type to replace" : `Enter API key (e.g. ${kindDefault.keyExample})`,
  );
  let modelPlaceholder = $derived(`e.g. ${kindDefault.modelExample}`);

  // discovery
  let modelSearch = $state("");
  let discovered = $state<WickDiscoverModel[]>([]);
  let discovering = $state(false);
  let discoverErr = $state("");
  let discoverTimer: ReturnType<typeof setTimeout> | null = null;

  let filteredModels = $derived.by(() => {
    const q = modelSearch.trim().toLowerCase();
    if (!q) return discovered;
    return discovered.filter((m) => m.id.toLowerCase().includes(q) || m.label.toLowerCase().includes(q));
  });

  function scheduleDiscover() {
    if (discoverTimer) clearTimeout(discoverTimer);
    discoverTimer = setTimeout(() => { void runDiscover(); }, 350);
  }

  async function runDiscover() {
    if (mKind === "other") { discovered = []; discoverErr = ""; discovering = false; return; }
    const hasKey = mKeyTouched && mKey.trim() !== "";
    const ref = editing?.ID ?? "";
    // Need either a freshly typed key or an existing model to reuse the
    // stored key from. Otherwise there is nothing to authenticate with.
    if (!hasKey && ref === "") { discovered = []; discoverErr = ""; discovering = false; return; }
    discovering = true;
    discoverErr = "";
    try {
      const r = await apiDiscoverWickModels(base, {
        kind: mKind,
        api_key: hasKey ? mKey.trim() : undefined,
        base_url: mBaseURL.trim() || undefined,
        model_ref: !hasKey && ref !== "" ? ref : undefined,
      });
      if (r.error) { discoverErr = r.error; discovered = []; }
      else { discovered = r.models; }
    } catch (e) {
      discoverErr = e instanceof Error ? e.message : "Discovery failed";
      discovered = [];
    } finally {
      discovering = false;
    }
  }

  function onKeyInput(v: string) {
    mKey = v;
    mKeyTouched = true;
    scheduleDiscover();
  }
  // Base URL feeds the discovery call, so changing it must re-run the
  // search (a custom/self-hosted endpoint lists different models).
  function onBaseURLInput(v: string) {
    mBaseURL = v;
    scheduleDiscover();
  }
  // defaultBaseURL returns the concrete endpoint for a known provider
  // (empty for "other" — the user must supply it).
  function defaultBaseURL(kind: string): string {
    return (KIND_DEFAULTS[kind] ?? KIND_DEFAULTS.other).baseURL;
  }

  function onKindChange(v: string) {
    mKind = v;
    // Pre-fill the concrete Base URL for known providers (it's well-known
    // for OpenAI/Anthropic/etc, so show the real value, not a grey hint).
    // "other" clears it — required, user-supplied.
    mBaseURL = defaultBaseURL(v);
    // Reload the model list for the new vendor. Clear the previously
    // discovered list so stale ids don't show.
    discovered = [];
    discoverErr = "";
    scheduleDiscover();
  }
  function pickModel(m: WickDiscoverModel) {
    mModel = m.id;
    if (mLabel.trim() === "") mLabel = m.label;
  }

  function openAdd() {
    editing = null;
    mKind = "openai";
    mKey = ""; mKeyTouched = false;
    mModel = ""; mLabel = "";
    mBaseURL = defaultBaseURL("openai"); mFormat = "";
    mMaxOut = ""; mTemp = ""; mThinking = ""; mRaw = "";
    advOpen = false;
    modelSearch = "";
    discovered = []; discoverErr = ""; discovering = false;
    modalOpen = true;
  }

  function openEdit(m: WickModelDTO) {
    editing = m;
    mKind = m.Kind || "openai";
    mKey = ""; mKeyTouched = false;
    mModel = m.Model; mLabel = m.Label;
    // Show the stored URL, or fall back to the provider default so the
    // field is never blank for a known provider.
    mBaseURL = m.BaseURL || defaultBaseURL(m.Kind || "openai"); mFormat = m.APIFormat;
    mMaxOut = m.MaxOutputTokens ? String(m.MaxOutputTokens) : "";
    mTemp = m.Temperature != null ? String(m.Temperature) : "";
    mThinking = m.ThinkingBudget != null ? String(m.ThinkingBudget) : "";
    mRaw = m.RawConfig;
    advOpen = !!(m.BaseURL || m.APIFormat || m.MaxOutputTokens || m.Temperature != null || m.ThinkingBudget != null || m.RawConfig);
    modelSearch = "";
    discovered = []; discoverErr = ""; discovering = false;
    modalOpen = true;
    // Reuse the stored key to repopulate the picker without re-entry.
    scheduleDiscover();
  }

  async function saveModel() {
    if (mModel.trim() === "") { toastError("Pick or enter a model id"); return; }
    if (mKind === "other" && mBaseURL.trim() === "") { toastError("Base URL is required for Other provider"); return; }
    savingModel = true;
    try {
      await apiSaveWickModel(base, {
        id: editing?.ID,
        kind: mKind,
        label: mLabel.trim() || undefined,
        model: mModel.trim(),
        api_key: (mKeyTouched && mKey.trim() !== "") ? mKey.trim() : undefined,
        base_url: mBaseURL.trim() || undefined,
        api_format: mFormat || undefined,
        max_output_tokens: intOrUndef(mMaxOut),
        default: editing?.Default ? true : undefined,
        temperature: floatOrUndef(mTemp),
        thinking_budget: intOrUndef(mThinking),
        raw_config: mRaw.trim() || undefined,
      });
      toastOk(editing ? "Model updated" : "Model added");
      modalOpen = false;
      await loadConfig(true);
    } catch (e) {
      toastError(e instanceof Error ? e.message : "Save failed");
    } finally {
      savingModel = false;
    }
  }

  async function setDefault(m: WickModelDTO) {
    if (m.Default) return;
    setBusy(`default-${m.ID}`, true);
    try {
      await apiSetWickModelDefault(base, m.ID);
      toastOk(`Default model: ${m.Label || m.Model}`);
      await loadConfig(true);
    } catch (e) {
      toastError(e instanceof Error ? e.message : "Failed to set default");
    } finally {
      setBusy(`default-${m.ID}`, false);
    }
  }

  let confirmDelete = $state<WickModelDTO | null>(null);
  async function doDelete(m: WickModelDTO) {
    confirmDelete = null;
    setBusy(`del-${m.ID}`, true);
    try {
      await apiDeleteWickModel(base, m.ID);
      toastOk(`Deleted ${m.Label || m.Model}`);
      await loadConfig(true);
    } catch (e) {
      toastError(e instanceof Error ? e.message : "Delete failed");
    } finally {
      setBusy(`del-${m.ID}`, false);
    }
  }

  function testModel(m: WickModelDTO) {
    // Per-model 1-token ping is a later phase (needs a BE test endpoint).
    // Placeholder keeps the row layout stable and signals intent.
    toastOk(`Test for ${m.Label || m.Model} is not wired yet`);
  }

  onMount(() => { void loadConfig(); });
</script>

<ConfirmDialog
  open={confirmDelete !== null}
  title={`Delete ${confirmDelete?.Label || confirmDelete?.Model || "model"}?`}
  body="This removes the model from the wick provider. Sessions using it will fall back to the default model."
  confirmLabel="Delete"
  destructive={true}
  onConfirm={() => { if (confirmDelete) doDelete(confirmDelete); }}
  onCancel={() => { confirmDelete = null; }}
/>

<div class="space-y-4">
  <Breadcrumb items={crumbs} />
  <div class="flex items-center gap-2 flex-wrap">
    <span class="text-lg font-semibold text-black-900 dark:text-white-100">wick</span>
    <span class="rounded-full bg-green-100 dark:bg-green-900 px-2 py-0.5 text-xs font-medium text-green-700 dark:text-green-300">Built-in</span>
    <span class="rounded-full border border-white-400 dark:border-navy-500 px-2 py-0.5 text-xs font-medium text-black-800 dark:text-black-600">Single instance — no duplicate / rename</span>
  </div>

  {#if loading}
    <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-6 py-10 text-center text-sm text-black-700 dark:text-black-600">Loading…</div>
  {:else if error}
    <div class="rounded-xl border border-red-300 dark:border-red-700 bg-red-50 dark:bg-red-900/20 px-4 py-3 text-sm text-red-700 dark:text-red-400">{error}</div>
  {:else}
    <!-- ── Provider settings ─────────────────────────────────────────── -->
    <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-sm overflow-hidden">
      <div class="px-5 py-3 border-b border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800">
        <h2 class="text-sm font-semibold text-black-900 dark:text-white-100">Provider settings</h2>
        <p class="mt-0.5 text-xs text-black-700 dark:text-black-600">Instance-level config. Common knobs below; anything else goes in raw config.</p>
      </div>
      <div class="p-5 space-y-5">
        <div class="grid grid-cols-1 sm:grid-cols-2 gap-x-6 gap-y-5">
          <div>
            <label for="wick-shell" class="block text-xs font-medium text-black-800 dark:text-black-600 mb-1.5">Shell tool (bash / cmd)</label>
            <Select value={shellMode} options={SHELL_OPTIONS} onChange={(v) => { shellMode = v; }} />
            <p class="mt-1.5 text-[11px] text-black-700 dark:text-black-600">Gate rules run inside the tool callback before exec.</p>
          </div>
          <div>
            <label for="wick-connectors" class="block text-xs font-medium text-black-800 dark:text-black-600 mb-1.5">Connectors</label>
            <Select value="all" options={[{ label: "All ready connectors", value: "all" }]} disabled onChange={() => {}} />
            <p class="mt-1.5 text-[11px] text-black-700 dark:text-black-600">Wired directly as function tools — no MCP. Per-connector selection is coming later.</p>
          </div>
          <div>
            <label for="wick-maxctx" class="block text-xs font-medium text-black-800 dark:text-black-600 mb-1.5">Max context tokens</label>
            <input
              id="wick-maxctx"
              type="text"
              bind:value={maxContext}
              placeholder="0 = unlimited (no compaction)"
              class="w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-sm font-mono text-black-900 dark:text-white-100 placeholder:text-black-700 outline-none focus:border-green-500 focus:ring-2 focus:ring-green-200 dark:focus:ring-green-800 transition-colors"
            />
            <p class="mt-1.5 text-[11px] text-black-700 dark:text-black-600">History-replay budget. Above ~80% of this, the model summarizes the oldest turns (compaction). Default 0 = off.</p>
          </div>
          <div>
            <label for="wick-maxturns" class="block text-xs font-medium text-black-800 dark:text-black-600 mb-1.5">Max turns per message</label>
            <input
              id="wick-maxturns"
              type="text"
              bind:value={maxTurns}
              placeholder="0 = default (safety cap 50)"
              class="w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-sm font-mono text-black-900 dark:text-white-100 placeholder:text-black-700 outline-none focus:border-green-500 focus:ring-2 focus:ring-green-200 dark:focus:ring-green-800 transition-colors"
            />
            <p class="mt-1.5 text-[11px] text-black-700 dark:text-black-600">Max tool-call rounds in one reply before wick stops.</p>
          </div>
          <div>
            <label for="wick-temp" class="block text-xs font-medium text-black-800 dark:text-black-600 mb-1.5">Default temperature</label>
            <input
              id="wick-temp"
              type="text"
              bind:value={defTemp}
              placeholder="model default"
              class="w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-sm font-mono text-black-900 dark:text-white-100 placeholder:text-black-700 outline-none focus:border-green-500 focus:ring-2 focus:ring-green-200 dark:focus:ring-green-800 transition-colors"
            />
          </div>
          <div>
            <label for="wick-think" class="block text-xs font-medium text-black-800 dark:text-black-600 mb-1.5">Default thinking budget</label>
            <input
              id="wick-think"
              type="text"
              bind:value={defThinking}
              placeholder="model default · 0 = off"
              class="w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-sm font-mono text-black-900 dark:text-white-100 placeholder:text-black-700 outline-none focus:border-green-500 focus:ring-2 focus:ring-green-200 dark:focus:ring-green-800 transition-colors"
            />
          </div>
        </div>
        <div>
          <label for="wick-raw" class="block text-xs font-medium text-black-800 dark:text-black-600 mb-1.5">Raw model config <span class="font-normal text-black-700 dark:text-black-600">(JSON, merged last)</span></label>
          <textarea
            id="wick-raw"
            bind:value={settingsRaw}
            rows="3"
            placeholder={'{"safetySettings": [...], "topK": 40}'}
            class="w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-sm font-mono text-black-900 dark:text-white-100 placeholder:text-black-700 outline-none focus:border-green-500 focus:ring-2 focus:ring-green-200 dark:focus:ring-green-800 transition-colors"
          ></textarea>
          <p class="mt-1.5 text-[11px] text-black-700 dark:text-black-600">Escape hatch: extra generation-config fields (e.g. safetySettings, topK) as JSON, merged over the fields above. For options that don't have a control yet.</p>
        </div>
      </div>
      <div class="px-5 py-3 border-t border-white-300 dark:border-navy-600 flex justify-end">
        <button
          onclick={saveSettings}
          disabled={savingSettings}
          class="rounded-lg bg-green-600 hover:bg-green-700 px-4 py-1.5 text-xs font-medium text-white-100 disabled:opacity-50"
        >{savingSettings ? "Saving…" : "Save settings"}</button>
      </div>
    </div>

    <!-- ── Models ─────────────────────────────────────────────────────── -->
    <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-sm overflow-hidden">
      <div class="px-5 py-3 border-b border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800 flex items-center justify-between gap-4 flex-wrap">
        <div>
          <h2 class="text-sm font-semibold text-black-900 dark:text-white-100">Models</h2>
          <p class="mt-0.5 text-xs text-black-700 dark:text-black-600">Custom models this instance can run. One is the default; sessions use it unless overridden.</p>
        </div>
        <button
          onclick={openAdd}
          class="inline-flex items-center gap-2 rounded-lg bg-green-500 hover:bg-green-600 px-4 py-2 text-sm font-medium text-white-100"
        >
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 5v14M5 12h14"/></svg>
          Add model
        </button>
      </div>
      {#if models.length === 0}
        <div class="px-6 py-12 text-center text-sm text-black-700 dark:text-black-600">
          No models yet. Click <span class="font-medium text-black-900 dark:text-white-100">Add model</span> to register one.
        </div>
      {:else}
        <div class="overflow-x-auto">
          <table class="w-full text-sm min-w-[640px]">
            <thead>
              <tr class="border-b border-white-300 dark:border-navy-600 text-black-700 dark:text-black-600">
                <th class="w-16 px-4 py-3 text-left text-xs font-medium">Default</th>
                <th class="px-4 py-3 text-left text-xs font-medium">Model</th>
                <th class="px-4 py-3 text-left text-xs font-medium">Provider</th>
                <th class="px-4 py-3 text-left text-xs font-medium">API key</th>
                <th class="w-40 px-4 py-3"></th>
              </tr>
            </thead>
            <tbody>
              {#each models as m (m.ID)}
                <tr class="border-b border-white-300 dark:border-navy-600 last:border-0 hover:bg-white-200 dark:hover:bg-navy-800">
                  <td class="px-4 py-3">
                    <button
                      type="button"
                      role="radio"
                      aria-checked={m.Default}
                      aria-label={m.Default ? "Default model" : "Set as default"}
                      title={m.Default ? "Default model" : "Set as default"}
                      disabled={busy[`default-${m.ID}`]}
                      onclick={() => setDefault(m)}
                      class="flex h-4 w-4 items-center justify-center rounded-full border-2 transition-colors disabled:opacity-50 {m.Default ? 'border-green-500 bg-green-500' : 'border-white-400 dark:border-navy-500 hover:border-green-500'}"
                    >
                      {#if m.Default}
                        <span class="h-1.5 w-1.5 rounded-full bg-white-100"></span>
                      {/if}
                    </button>
                  </td>
                  <td class="px-4 py-3">
                    <div class="text-black-900 dark:text-white-100">{m.Label || m.Model}</div>
                    <div class="font-mono text-xs text-black-700 dark:text-black-600">{m.Model}</div>
                  </td>
                  <td class="px-4 py-3">
                    <span class="inline-flex items-center rounded-full border border-white-400 dark:border-navy-500 px-2.5 py-0.5 text-xs font-medium text-black-800 dark:text-black-600">{kindChip(m.Kind)}</span>
                  </td>
                  <td class="px-4 py-3">
                    {#if m.HasKey}
                      <span class="font-mono text-xs text-black-700 dark:text-black-600">{m.KeyMasked || "••••••••"}</span>
                    {:else}
                      <span class="text-xs text-black-700 dark:text-black-600">not set</span>
                    {/if}
                  </td>
                  <td class="px-4 py-3">
                    <div class="flex items-center justify-end gap-1">
                      <button
                        type="button"
                        onclick={() => testModel(m)}
                        title="1-token ping: validates key + model id"
                        class="rounded-lg border border-white-400 dark:border-navy-600 px-3 py-1 text-xs font-medium text-black-800 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800"
                      >Test</button>
                      <button
                        type="button"
                        onclick={() => openEdit(m)}
                        title="Edit"
                        aria-label="Edit model"
                        class="rounded p-1.5 text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800 hover:text-black-900 dark:hover:text-white-100"
                      >
                        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M17 3a2.8 2.8 0 1 1 4 4L7.5 20.5 2 22l1.5-5.5Z"/></svg>
                      </button>
                      <button
                        type="button"
                        disabled={busy[`del-${m.ID}`]}
                        onclick={() => { confirmDelete = m; }}
                        title="Delete"
                        aria-label="Delete model"
                        class="rounded p-1.5 text-black-700 dark:text-black-600 hover:bg-red-50 dark:hover:bg-red-900/20 hover:text-red-600 dark:hover:text-red-400 disabled:opacity-50"
                      >
                        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M3 6h18M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2m3 0v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6"/></svg>
                      </button>
                    </div>
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      {/if}
    </div>

    <!-- Native-tools notice -->
    <div class="flex items-start gap-3 rounded-lg border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-4 py-3 text-xs text-black-800 dark:text-black-600">
      <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="mt-0.5 shrink-0 text-amber-500"><circle cx="12" cy="12" r="10"/><path d="M12 8v4M12 16h.01"/></svg>
      <span>Wick runs in-process with native tools: <span class="font-semibold text-black-900 dark:text-white-100">shell</span> (bash/cmd — gate rules enforced inside the tool itself) and <span class="font-semibold text-black-900 dark:text-white-100">wick connectors</span> (direct service call, no MCP round-trip).</span>
    </div>

    <RecentSpawns {base} type="wick" name="wick" {onOpenSession} />
  {/if}
</div>

<!-- ── Add / Edit Custom Model modal ────────────────────────────────── -->
<Modal open={modalOpen} title={editing ? "Edit custom model" : "Add custom model"} size="lg" onClose={() => { modalOpen = false; }}>
  <div class="space-y-4">
    <div>
      <label for="m-kind" class="block text-sm font-medium text-black-900 dark:text-white-100 mb-1">Provider</label>
      <Select value={mKind} options={KIND_OPTIONS} onChange={onKindChange} />
    </div>

    <div>
      <label for="m-baseurl" class="block text-sm font-medium text-black-900 dark:text-white-100 mb-1">Base URL {#if mKind === "other"}<span class="text-red-500">*</span>{/if}</label>
      <input
        id="m-baseurl"
        type="text"
        value={mBaseURL}
        oninput={(e) => onBaseURLInput((e.target as HTMLInputElement).value)}
        placeholder={baseURLPlaceholder}
        class="w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-sm font-mono text-black-900 dark:text-white-100 placeholder:text-black-700 outline-none focus:border-green-500 focus:ring-2 focus:ring-green-200 dark:focus:ring-green-800 transition-colors"
      />
      <p class="mt-1 text-[11px] text-black-700 dark:text-black-600">
        {#if mKind === "other"}Required — the OpenAI-compatible endpoint (self-hosted / custom).{:else}Defaults to the vendor endpoint. Change only for a proxy / custom gateway — the model search uses this.{/if}
      </p>
    </div>

    <div>
      <label for="m-key" class="block text-sm font-medium text-black-900 dark:text-white-100 mb-1">API key</label>
      <input
        id="m-key"
        type="password"
        autocomplete="new-password"
        value={mKey}
        oninput={(e) => onKeyInput((e.target as HTMLInputElement).value)}
        placeholder={keyPlaceholder}
        class="w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-sm font-mono text-black-900 dark:text-white-100 placeholder:text-black-700 outline-none focus:border-green-500 focus:ring-2 focus:ring-green-200 dark:focus:ring-green-800 transition-colors"
      />
      <p class="mt-1 text-[11px] text-black-700 dark:text-black-600">Encrypted at rest, used server-side only — never sent back to the browser.</p>
    </div>

    <div>
      <label for="m-model" class="block text-sm font-medium text-black-900 dark:text-white-100 mb-1">Model ID</label>
      <!-- Manual model id is ALWAYS the source of truth — discovery/search
           can miss (custom deploys, out-of-sync vendor list), so the user
           can always type the id directly. The picker below just fills this. -->
      <input
        id="m-model"
        type="text"
        bind:value={mModel}
        placeholder={modelPlaceholder}
        class="w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-sm font-mono text-black-900 dark:text-white-100 placeholder:text-black-700 outline-none focus:border-green-500 focus:ring-2 focus:ring-green-200 dark:focus:ring-green-800 transition-colors"
      />
      <p class="mt-1 text-[11px] text-black-700 dark:text-black-600">The model identifier sent to the provider's API. Type it directly, or pick from the list below.</p>

      {#if mKind !== "other"}
        <!-- Optional discovery helper: search the vendor's model list and
             click to fill the Model ID above. Never required. -->
        <div class="relative mt-3">
          <svg class="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-black-700 dark:text-black-600" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="11" cy="11" r="8"/><path d="m21 21-4.3-4.3"/></svg>
          <input
            id="m-model-search"
            type="text"
            bind:value={modelSearch}
            placeholder="Search available models…"
            class="w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 pl-10 pr-3 py-2 text-sm text-black-900 dark:text-white-100 placeholder:text-black-700 outline-none focus:border-green-500 focus:ring-2 focus:ring-green-200 dark:focus:ring-green-800 transition-colors"
          />
        </div>
        {#if discoverErr}
          <p class="mt-1 text-[11px] text-amber-600 dark:text-amber-400">Discovery unavailable ({discoverErr}) — type the model id above manually.</p>
        {:else}
          <div class="mt-2 max-h-48 overflow-y-auto rounded-lg border border-white-400 dark:border-navy-600">
            {#if discovering}
              <div class="px-4 py-3 text-xs text-black-700 dark:text-black-600">Discovering models…</div>
            {:else if !(mKeyTouched && mKey.trim() !== "") && !editing}
              <div class="px-4 py-3 text-xs text-black-700 dark:text-black-600">Enter an API key above to load available models — or just type the id.</div>
            {:else if filteredModels.length === 0}
              <div class="px-4 py-3 text-xs text-black-700 dark:text-black-600">No models found — type the id above manually.</div>
            {:else}
              {#each filteredModels as m (m.id)}
                {@const selected = mModel === m.id}
                <button
                  type="button"
                  onclick={() => pickModel(m)}
                  class="flex w-full items-center justify-between px-4 py-2 text-left text-sm hover:bg-white-200 dark:hover:bg-navy-800 {selected ? 'text-green-600 dark:text-green-400 font-medium' : 'text-black-900 dark:text-white-100'}"
                >
                  <span class="min-w-0 truncate">
                    {m.id}
                    {#if m.label && m.label !== m.id}
                      <span class="ml-1 text-[11px] font-normal text-black-700 dark:text-black-600">· {m.label}</span>
                    {/if}
                  </span>
                  {#if selected}
                    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="shrink-0"><path d="M20 6 9 17l-5-5"/></svg>
                  {/if}
                </button>
              {/each}
            {/if}
          </div>
        {/if}
      {/if}
    </div>

    <div>
      <label for="m-label" class="block text-sm font-medium text-black-900 dark:text-white-100 mb-1">Display name <span class="font-normal text-black-700 dark:text-black-600">(optional)</span></label>
      <input
        id="m-label"
        type="text"
        bind:value={mLabel}
        placeholder="falls back to the model id"
        class="w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-sm text-black-900 dark:text-white-100 placeholder:text-black-700 outline-none focus:border-green-500 focus:ring-2 focus:ring-green-200 dark:focus:ring-green-800 transition-colors"
      />
    </div>

    <button
      type="button"
      onclick={() => { advOpen = !advOpen; }}
      class="flex items-center gap-2 text-sm font-medium text-black-800 dark:text-black-600 hover:text-black-900 dark:hover:text-white-100"
    >
      <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="transition-transform {advOpen ? 'rotate-90' : ''}"><path d="m9 18 6-6-6-6"/></svg>
      Advanced options
    </button>

    {#if advOpen}
      <div class="flex flex-col gap-4 border-l-2 border-white-300 dark:border-navy-600 pl-4">
        <div class="grid grid-cols-1 sm:grid-cols-2 gap-4">
          <div>
            <label for="m-format" class="block text-sm font-medium text-black-900 dark:text-white-100 mb-1">API format</label>
            <Select value={mFormat} options={FORMAT_OPTIONS} placeholder={formatPlaceholder} onChange={(v: string) => { mFormat = v; }} />
            <p class="mt-1 text-[11px] text-black-700 dark:text-black-600">Defaults from the provider — set only for “Other”.</p>
          </div>
          <div>
            <label for="m-maxout" class="block text-sm font-medium text-black-900 dark:text-white-100 mb-1">Max output tokens <span class="font-normal text-black-700 dark:text-black-600">(optional)</span></label>
            <input
              id="m-maxout"
              type="text"
              bind:value={mMaxOut}
              placeholder="vendor default"
              class="w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-sm font-mono text-black-900 dark:text-white-100 placeholder:text-black-700 outline-none focus:border-green-500 focus:ring-2 focus:ring-green-200 dark:focus:ring-green-800 transition-colors"
            />
          </div>
        </div>
        <div class="grid grid-cols-1 sm:grid-cols-2 gap-4">
          <div>
            <label for="m-temp" class="block text-sm font-medium text-black-900 dark:text-white-100 mb-1">Temperature <span class="font-normal text-black-700 dark:text-black-600">(optional)</span></label>
            <input
              id="m-temp"
              type="text"
              bind:value={mTemp}
              placeholder="instance default"
              class="w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-sm font-mono text-black-900 dark:text-white-100 placeholder:text-black-700 outline-none focus:border-green-500 focus:ring-2 focus:ring-green-200 dark:focus:ring-green-800 transition-colors"
            />
          </div>
          <div>
            <label for="m-think" class="block text-sm font-medium text-black-900 dark:text-white-100 mb-1">Thinking budget <span class="font-normal text-black-700 dark:text-black-600">(optional)</span></label>
            <input
              id="m-think"
              type="text"
              bind:value={mThinking}
              placeholder="instance default · 0 = off"
              class="w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-sm font-mono text-black-900 dark:text-white-100 placeholder:text-black-700 outline-none focus:border-green-500 focus:ring-2 focus:ring-green-200 dark:focus:ring-green-800 transition-colors"
            />
          </div>
        </div>
        <div>
          <label for="m-raw" class="block text-sm font-medium text-black-900 dark:text-white-100 mb-1">Raw model config <span class="font-normal text-black-700 dark:text-black-600">(JSON, per-model, merged last)</span></label>
          <textarea
            id="m-raw"
            bind:value={mRaw}
            rows="2"
            placeholder={'{"safetySettings": [...], "topK": 40}'}
            class="w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-sm font-mono text-black-900 dark:text-white-100 placeholder:text-black-700 outline-none focus:border-green-500 focus:ring-2 focus:ring-green-200 dark:focus:ring-green-800 transition-colors"
          ></textarea>
          <p class="mt-1 text-[11px] text-black-700 dark:text-black-600">Per-model override, merged over the instance settings. Same JSON shape.</p>
        </div>
      </div>
    {/if}
  </div>
  {#snippet footer()}
    <Button variant="secondary" disabled={savingModel} onclick={() => { modalOpen = false; }}>Cancel</Button>
    <Button variant="primary" disabled={savingModel} onclick={saveModel}>{savingModel ? "Saving…" : "Save model"}</Button>
  {/snippet}
</Modal>
