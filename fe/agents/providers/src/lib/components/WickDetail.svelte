<script lang="ts">
  import { onMount } from "svelte";
  import { ConfirmDialog, Breadcrumb, Modal, Select, Button, KebabMenu, type BreadcrumbItem } from "@wick-fe/common-ui";
  import { toastOk, toastError } from "@wick-fe/common-stores";
  import {
    apiGetWickConfig,
    apiSaveWickModel,
    apiDeleteWickModel,
    apiSetWickModelDefault,
    apiSetWickModelDisabled,
    apiDuplicateWickModel,
    apiTestWickModel,
    apiSaveWickSettings,
    apiDiscoverWickModels,
    apiGetWickLiveSetModels,
    apiSetWickDefaultVendorModel,
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
  let maxConsecErrors = $state("");
  let maxTurnMinutes = $state("");
  let maxModelRetries = $state("");
  let modelCallTimeout = $state("");
  let defTemp = $state("");
  let defThinking = $state("");
  let settingsRaw = $state("");
  let savingSettings = $state(false);
  let showAdvanced = $state(false);

  function seedSettings(s: WickSettingsDTO) {
    shellMode = s.ShellToolDisabled ? "disabled" : "enabled";
    maxContext = s.MaxContextTokens ? String(s.MaxContextTokens) : "";
    maxTurns = s.MaxTurns ? String(s.MaxTurns) : "";
    maxConsecErrors = s.MaxConsecErrors ? String(s.MaxConsecErrors) : "";
    maxTurnMinutes = s.MaxTurnMinutes ? String(s.MaxTurnMinutes) : "";
    maxModelRetries = s.MaxModelRetries ? String(s.MaxModelRetries) : "";
    modelCallTimeout = s.ModelCallTimeoutSec ? String(s.ModelCallTimeoutSec) : "";
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
        max_consec_errors: intOrUndef(maxConsecErrors),
        max_turn_minutes: intOrUndef(maxTurnMinutes),
        max_model_retries: intOrUndef(maxModelRetries),
        model_call_timeout_sec: intOrUndef(modelCallTimeout),
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
  // keyReplacing: an existing key is stored AND the user has typed something —
  // the new value will overwrite the stored one on save. Drives the API-key
  // "Saved" → "Replacing…" indicator.
  let keyReplacing = $derived(mKeyTouched && mKey.trim() !== "");
  let modelPlaceholder = $derived(`e.g. ${kindDefault.modelExample}`);

  // discovery
  let modelSearch = $state("");
  let discovered = $state<WickDiscoverModel[]>([]);
  let discovering = $state(false);
  let discoverErr = $state("");
  let discoverTimer: ReturnType<typeof setTimeout> | null = null;
  let searchEl = $state<HTMLInputElement | undefined>();

  // Multi-select has two selection modes:
  //   "live"   — selection tracks the filter: every model matching the search
  //              is auto-selected and re-tracks as you type. Row clicks add a
  //              per-id override (drop a matched one / force-add an unmatched).
  //   "manual" — you tick models yourself (+ a "Select all matching" snapshot);
  //              the filter only narrows the list, it doesn't select.
  // Single mode keeps the "click fills Model ID" behaviour.
  let multiSelect = $state(false);
  let selectMode = $state<"live" | "manual">("live");
  // live-mode overrides on top of the filter match:
  let excludedIDs = $state<Set<string>>(new Set()); // matched but dropped
  let forcedIDs = $state<Set<string>>(new Set());    // unmatched but added
  // manual-mode explicit picks:
  let selectedIDs = $state<Set<string>>(new Set());
  // live-mode default vendor: in LIVE mode there is no multi-select — the set
  // is defined by the filter alone, and the list is a preview. The user may
  // pin ONE model as the sticky default (saved as DefaultVendorModel). ""
  // means "top of the filtered list" (no pin).
  let liveDefaultVendor = $state("");

  function modelById(id: string): WickDiscoverModel {
    return discovered.find((m) => m.id === id) ?? { id, label: "" };
  }
  // isSelected: in live mode, filter-match ± overrides; in manual, the tick set.
  function isSelected(id: string): boolean {
    if (selectMode === "manual") return selectedIDs.has(id);
    if (forcedIDs.has(id)) return true;
    if (excludedIDs.has(id)) return false;
    return matchesTerms(modelById(id));
  }
  // The effective set of models to add: everything currently selected.
  let selectedModels = $derived.by(() => discovered.filter((m) => isSelected(m.id)));

  // Search grammar (intentionally tiny): space-separated terms; a term is
  // "include" by default, or "exclude" when prefixed with `-` or `!`. A model
  // matches when it contains every include term AND no exclude term. Matching
  // is over both the id and the human label.
  type Term = { text: string; exclude: boolean };
  let searchTerms = $derived.by<Term[]>(() =>
    modelSearch
      .toLowerCase()
      .split(/\s+/)
      .map((t) => t.trim())
      .filter((t) => t !== "" && t !== "-" && t !== "!")
      .map((t) =>
        t.startsWith("-") || t.startsWith("!")
          ? { text: t.slice(1), exclude: true }
          : { text: t, exclude: false },
      ),
  );

  function matchesTerms(m: WickDiscoverModel): boolean {
    const hay = `${m.id} ${m.label}`.toLowerCase();
    for (const t of searchTerms) {
      const hit = hay.includes(t.text);
      if (t.exclude ? hit : !hit) return false;
    }
    return true;
  }

  let filteredModels = $derived.by(() => {
    if (searchTerms.length === 0) return discovered;
    return discovered.filter(matchesTerms);
  });

  // Auto-focus the model filter the moment the modal opens for a known
  // provider, so you can start typing straight away without a click. Guarded
  // on modalOpen + a visible search box; the input only renders for non-other.
  $effect(() => {
    if (modalOpen && mKind !== "other" && searchEl) searchEl.focus();
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
  // isAutoBaseURL reports whether the current Base URL is still an
  // auto-filled vendor default (any known provider's default, or empty) —
  // i.e. NOT something the user typed themselves. Used to decide whether a
  // provider switch may re-fill it: a custom proxy URL (e.g. a gateway) is
  // preserved, an untouched default is swapped for the new vendor's.
  function isAutoBaseURL(url: string): boolean {
    const u = url.trim();
    if (u === "") return true;
    return Object.values(KIND_DEFAULTS).some((d) => d.baseURL !== "" && d.baseURL === u);
  }

  function onKindChange(v: string) {
    mKind = v;
    // Pre-fill the concrete Base URL for known providers (it's well-known
    // for OpenAI/Anthropic/etc, so show the real value, not a grey hint).
    // "other" clears it — required, user-supplied. But NEVER clobber a Base
    // URL the user typed themselves (a custom proxy / gateway): only re-fill
    // when the current value is still an auto default. This is what lets an
    // edited model keep its custom endpoint when the provider is re-picked.
    if (isAutoBaseURL(mBaseURL)) mBaseURL = defaultBaseURL(v);
    // Reload the model list for the new vendor. Clear the previously
    // discovered list so stale ids don't show.
    discovered = [];
    discoverErr = "";
    scheduleDiscover();
  }
  function pickModel(m: WickDiscoverModel) {
    if (multiSelect) { toggleSelected(m.id); return; }
    mModel = m.id;
    if (mLabel.trim() === "") mLabel = m.label;
  }

  // Toggle a single row. Manual mode: flip membership in the tick set. Live
  // mode: layer an override on the filter match (drop a matched one / force an
  // unmatched one), keeping the two override sets mutually exclusive.
  function toggleSelected(id: string) {
    if (selectMode === "manual") {
      const next = new Set(selectedIDs);
      if (next.has(id)) next.delete(id); else next.add(id);
      selectedIDs = next;
      return;
    }
    const nextEx = new Set(excludedIDs);
    const nextFor = new Set(forcedIDs);
    const matched = matchesTerms(modelById(id));
    if (isSelected(id)) { nextFor.delete(id); if (matched) nextEx.add(id); }
    else { nextEx.delete(id); if (!matched) nextFor.add(id); }
    excludedIDs = nextEx;
    forcedIDs = nextFor;
  }
  // Manual mode: snapshot the currently-filtered rows into the tick set.
  function selectAllFiltered() {
    const next = new Set(selectedIDs);
    for (const m of filteredModels) next.add(m.id);
    selectedIDs = next;
  }
  function clearOverrides() { excludedIDs = new Set(); forcedIDs = new Set(); selectedIDs = new Set(); }
  // Leaving multi mode drops all selection so nothing silently applies later.
  function setMulti(on: boolean) {
    multiSelect = on;
    if (!on) clearOverrides();
  }
  // Switching sub-mode: clear the other mode's state so the count is honest.
  function setSelectMode(m: "live" | "manual") {
    selectMode = m;
    clearOverrides();
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
    multiSelect = false; clearOverrides();
    liveDefaultVendor = "";
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
    clearOverrides();
    // A live set opens in Multiple + Live with its filter prefilled so editing
    // tweaks the query; a plain model opens in Single.
    if (m.LiveSet) {
      multiSelect = true; selectMode = "live"; modelSearch = m.DiscoveryFilter;
      liveDefaultVendor = m.DefaultVendorModel || "";
    } else {
      multiSelect = false; selectMode = "live"; modelSearch = "";
      liveDefaultVendor = "";
    }
    discovered = []; discoverErr = ""; discovering = false;
    modalOpen = true;
    // Reuse the stored key to repopulate the picker without re-entry.
    scheduleDiscover();
  }

  // Shared payload for a single model id — everything except the id/label,
  // which differ between the single form and each batch entry.
  function baseModelInput() {
    return {
      kind: mKind,
      api_key: (mKeyTouched && mKey.trim() !== "") ? mKey.trim() : undefined,
      base_url: mBaseURL.trim() || undefined,
      api_format: mFormat || undefined,
      max_output_tokens: intOrUndef(mMaxOut),
      temperature: floatOrUndef(mTemp),
      thinking_budget: intOrUndef(mThinking),
      raw_config: mRaw.trim() || undefined,
    };
  }

  async function saveModel() {
    if (mKind === "other" && mBaseURL.trim() === "") { toastError("Base URL is required for Other provider"); return; }

    // Multiple + LIVE: save ONE entry that stores the filter query. At
    // picker time it expands to the vendor's models matching the filter, so a
    // single entry stands in for many models (no per-model registration).
    if (multiSelect && selectMode === "live") {
      savingModel = true;
      try {
        // Editing ANY existing entry updates it in place (carry its id) —
        // including a plain model being converted to a live set. Only a
        // brand-new live set (no editing target) gets a fresh id. Previously
        // this dropped the id when converting plain→live, which left the old
        // plain entry behind and created a duplicate.
        const liveID = editing?.ID;
        // Filter is OPTIONAL: empty = match ALL of the vendor's models. The
        // entry is flagged live_set explicitly, so no sentinel filter needed.
        const filter = modelSearch.trim();
        await apiSaveWickModel(base, {
          ...baseModelInput(),
          id: liveID,
          model: "", // live set — no single pinned model
          label: mLabel.trim() || (filter ? `Live: ${filter}` : "Live: all models"),
          live_set: true,
          discovery_filter: filter,
          default_vendor_model: liveDefaultVendor, // "" clears the pin (top-of-list)
        });
        toastOk(liveID ? "Live model set updated" : "Live model set added");
        modalOpen = false;
        await loadConfig(true);
      } catch (e) {
        toastError(e instanceof Error ? e.message : "Save failed");
      } finally {
        savingModel = false;
      }
      return;
    }

    // Multiple + MANUAL: one new provider entry per ticked model, label
    // defaults to the discovered label, sharing the key + advanced settings.
    if (multiSelect) {
      const chosen = selectedModels;
      if (chosen.length === 0) { toastError("Select at least one model"); return; }
      savingModel = true;
      let ok = 0;
      try {
        for (const m of chosen) {
          await apiSaveWickModel(base, {
            ...baseModelInput(),
            model: m.id,
            label: m.label && m.label !== m.id ? m.label : undefined,
          });
          ok++;
        }
        toastOk(`${ok} model${ok === 1 ? "" : "s"} added`);
        modalOpen = false;
        await loadConfig(true);
      } catch (e) {
        // Partial success is possible; report what landed then reload.
        toastError(`${e instanceof Error ? e.message : "Save failed"}${ok > 0 ? ` (${ok} saved before the error)` : ""}`);
        if (ok > 0) { modalOpen = false; await loadConfig(true); }
      } finally {
        savingModel = false;
      }
      return;
    }

    if (mModel.trim() === "") { toastError("Pick or enter a model id"); return; }
    savingModel = true;
    try {
      await apiSaveWickModel(base, {
        ...baseModelInput(),
        id: editing?.ID,
        label: mLabel.trim() || undefined,
        model: mModel.trim(),
        default: editing?.Default ? true : undefined,
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

  // ── Live-set default vendor model picker ────────────────────────────
  // A live set expands to many vendor models; this modal pins the sticky
  // default (used when the set is picked without choosing a specific model).
  let vendorPickerFor = $state<WickModelDTO | null>(null);
  let vendorModels = $state<{ id: string; label: string; default: boolean }[]>([]);
  let vendorLoading = $state(false);
  let vendorErr = $state("");
  let vendorSearch = $state("");

  async function openVendorPicker(m: WickModelDTO) {
    vendorPickerFor = m;
    vendorModels = [];
    vendorErr = "";
    vendorSearch = "";
    vendorLoading = true;
    try {
      vendorModels = await apiGetWickLiveSetModels(base, m.ID);
      if (vendorModels.length === 0) vendorErr = "No models returned — check the API key and filter.";
    } catch (e) {
      vendorErr = e instanceof Error ? e.message : "Failed to load models";
    } finally {
      vendorLoading = false;
    }
  }
  let filteredVendorModels = $derived.by(() => {
    const q = vendorSearch.trim().toLowerCase();
    if (!q) return vendorModels;
    return vendorModels.filter((m) => `${m.id} ${m.label}`.toLowerCase().includes(q));
  });
  async function pinVendorModel(vendorId: string) {
    const m = vendorPickerFor;
    if (!m) return;
    setBusy(`vendor-${m.ID}`, true);
    try {
      await apiSetWickDefaultVendorModel(base, m.ID, vendorId);
      toastOk(vendorId ? `Default model: ${vendorId}` : "Default cleared — top of list");
      vendorPickerFor = null;
      await loadConfig(true);
    } catch (e) {
      toastError(e instanceof Error ? e.message : "Failed to set default model");
    } finally {
      setBusy(`vendor-${m.ID}`, false);
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

  async function testModel(m: WickModelDTO) {
    setBusy(`test-${m.ID}`, true);
    try {
      const r = await apiTestWickModel(base, m.ID);
      if (r.ok) toastOk(`${m.Label || m.Model}: OK (${r.latencyMs}ms)`);
      else toastError(r.error || "Test failed");
    } catch (e) {
      toastError(e instanceof Error ? e.message : "Test failed");
    } finally {
      setBusy(`test-${m.ID}`, false);
    }
  }

  async function duplicateModel(m: WickModelDTO) {
    setBusy(`dup-${m.ID}`, true);
    try {
      await apiDuplicateWickModel(base, m.ID);
      toastOk(`Duplicated ${m.Label || m.Model}`);
      await loadConfig(true);
    } catch (e) {
      toastError(e instanceof Error ? e.message : "Duplicate failed");
    } finally {
      setBusy(`dup-${m.ID}`, false);
    }
  }

  async function toggleDisabled(m: WickModelDTO) {
    setBusy(`disable-${m.ID}`, true);
    try {
      await apiSetWickModelDisabled(base, m.ID, !m.Disabled);
      toastOk(m.Disabled ? `Enabled ${m.Label || m.Model}` : `Disabled ${m.Label || m.Model}`);
      await loadConfig(true);
    } catch (e) {
      toastError(e instanceof Error ? e.message : "Failed to update model");
    } finally {
      setBusy(`disable-${m.ID}`, false);
    }
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

<!-- Live-set default vendor model picker -->
<Modal open={vendorPickerFor !== null} title="Set default model" size="md" onClose={() => { vendorPickerFor = null; }}>
  <div class="space-y-3">
    <p class="text-sm text-black-700 dark:text-black-600">
      Pick the model used when <span class="font-medium text-black-900 dark:text-white-100">{vendorPickerFor?.Label || vendorPickerFor?.DiscoveryFilter || "this live set"}</span>
      is chosen without drilling into a specific model. If the pinned model later disappears from the list, the top of the list is used automatically.
    </p>
    {#if vendorLoading}
      <div class="px-4 py-6 text-center text-sm text-black-700 dark:text-black-600">Loading models…</div>
    {:else if vendorErr}
      <div class="rounded-lg border border-red-300 dark:border-red-700 bg-red-50 dark:bg-red-900/20 px-4 py-3 text-sm text-red-700 dark:text-red-400">{vendorErr}</div>
    {:else}
      <input
        type="text"
        bind:value={vendorSearch}
        placeholder="Filter models…"
        class="w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-sm font-mono text-black-900 dark:text-white-100 placeholder:text-black-700 outline-none focus:border-green-500 focus:ring-2 focus:ring-green-200 dark:focus:ring-green-800 transition-colors"
      />
      <div class="max-h-64 overflow-y-auto rounded-lg border border-white-400 dark:border-navy-600">
        <button
          type="button"
          onclick={() => pinVendorModel("")}
          class="flex w-full items-center justify-between gap-2 px-4 py-2 text-left text-sm hover:bg-white-200 dark:hover:bg-navy-800 {!vendorPickerFor?.DefaultVendorModel ? 'text-green-600 dark:text-green-400 font-medium' : 'text-black-900 dark:text-white-100'}"
        >
          <span>Top of list <span class="text-[11px] font-normal text-black-700 dark:text-black-600">(no pin — always the first match)</span></span>
          {#if !vendorPickerFor?.DefaultVendorModel}<span class="text-[11px]">current</span>{/if}
        </button>
        {#each filteredVendorModels as vm (vm.id)}
          {@const isPin = vm.id === vendorPickerFor?.DefaultVendorModel}
          <button
            type="button"
            onclick={() => pinVendorModel(vm.id)}
            class="flex w-full items-center justify-between gap-2 border-t border-white-300 dark:border-navy-700 px-4 py-2 text-left text-sm hover:bg-white-200 dark:hover:bg-navy-800 {isPin ? 'text-green-600 dark:text-green-400 font-medium' : 'text-black-900 dark:text-white-100'}"
          >
            <span class="min-w-0 truncate">
              {vm.id}
              {#if vm.label && vm.label !== vm.id}<span class="ml-1 text-[11px] font-normal text-black-700 dark:text-black-600">· {vm.label}</span>{/if}
            </span>
            <span class="flex shrink-0 items-center gap-1.5">
              {#if vm.default && !isPin}<span class="rounded-full bg-white-300 dark:bg-navy-700 px-1.5 py-0.5 text-[10px] text-black-700 dark:text-black-600">top</span>{/if}
              {#if isPin}<span class="text-[11px]">current</span>{/if}
            </span>
          </button>
        {/each}
        {#if filteredVendorModels.length === 0}
          <div class="px-4 py-3 text-xs text-black-700 dark:text-black-600">No models match “{vendorSearch}”.</div>
        {/if}
      </div>
    {/if}
  </div>
</Modal>

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
        <!-- ── Advanced settings (limits + raw config) ─────────────── -->
        <div class="rounded-lg border border-white-300 dark:border-navy-600">
          <button
            type="button"
            onclick={() => { showAdvanced = !showAdvanced; }}
            class="w-full flex items-center justify-between px-4 py-2.5 text-xs font-medium text-black-800 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800 transition-colors"
          >
            <span>Advanced settings <span class="font-normal text-black-700 dark:text-black-600">— limits, retries, raw config</span></span>
            <span class="text-black-700">{showAdvanced ? "▴" : "▾"}</span>
          </button>
          {#if showAdvanced}
          <div class="px-4 pb-4 pt-1 space-y-5 border-t border-white-300 dark:border-navy-600">
          <div class="grid grid-cols-1 sm:grid-cols-2 gap-x-6 gap-y-5">
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
              placeholder="0 = unlimited"
              class="w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-sm font-mono text-black-900 dark:text-white-100 placeholder:text-black-700 outline-none focus:border-green-500 focus:ring-2 focus:ring-green-200 dark:focus:ring-green-800 transition-colors"
            />
            <p class="mt-1.5 text-[11px] text-black-700 dark:text-black-600">Max tool-call rounds in one reply. 0 = unlimited — the error/time guards below are the safety net.</p>
          </div>
          <div>
            <label for="wick-maxconsecerr" class="block text-xs font-medium text-black-800 dark:text-black-600 mb-1.5">Max consecutive tool errors</label>
            <input
              id="wick-maxconsecerr"
              type="text"
              bind:value={maxConsecErrors}
              placeholder="0 = default (20)"
              class="w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-sm font-mono text-black-900 dark:text-white-100 placeholder:text-black-700 outline-none focus:border-green-500 focus:ring-2 focus:ring-green-200 dark:focus:ring-green-800 transition-colors"
            />
            <p class="mt-1.5 text-[11px] text-black-700 dark:text-black-600">Stops a turn after N all-error tool rounds in a row. Any success resets the counter.</p>
          </div>
          <div>
            <label for="wick-maxturnmin" class="block text-xs font-medium text-black-800 dark:text-black-600 mb-1.5">Max turn duration (minutes)</label>
            <input
              id="wick-maxturnmin"
              type="text"
              bind:value={maxTurnMinutes}
              placeholder="0 = default (60)"
              class="w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-sm font-mono text-black-900 dark:text-white-100 placeholder:text-black-700 outline-none focus:border-green-500 focus:ring-2 focus:ring-green-200 dark:focus:ring-green-800 transition-colors"
            />
            <p class="mt-1.5 text-[11px] text-black-700 dark:text-black-600">Wall-clock ceiling for one reply — the backstop when max turns is unlimited.</p>
          </div>
          <div>
            <label for="wick-maxretries" class="block text-xs font-medium text-black-800 dark:text-black-600 mb-1.5">Max model retries</label>
            <input
              id="wick-maxretries"
              type="text"
              bind:value={maxModelRetries}
              placeholder="0 = default (3)"
              class="w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-sm font-mono text-black-900 dark:text-white-100 placeholder:text-black-700 outline-none focus:border-green-500 focus:ring-2 focus:ring-green-200 dark:focus:ring-green-800 transition-colors"
            />
            <p class="mt-1.5 text-[11px] text-black-700 dark:text-black-600">Total attempts per failing model call (incl. the first). 1 disables retries.</p>
          </div>
          <div>
            <label for="wick-calltimeout" class="block text-xs font-medium text-black-800 dark:text-black-600 mb-1.5">Model call timeout (seconds)</label>
            <input
              id="wick-calltimeout"
              type="text"
              bind:value={modelCallTimeout}
              placeholder="0 = default (120)"
              class="w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-sm font-mono text-black-900 dark:text-white-100 placeholder:text-black-700 outline-none focus:border-green-500 focus:ring-2 focus:ring-green-200 dark:focus:ring-green-800 transition-colors"
            />
            <p class="mt-1.5 text-[11px] text-black-700 dark:text-black-600">Ceiling for one model-call attempt before it counts as failed and retries.</p>
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
          {/if}
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
                <tr class="border-b border-white-300 dark:border-navy-600 last:border-0 hover:bg-white-200 dark:hover:bg-navy-800 {m.Disabled ? 'opacity-55' : ''}">
                  <td class="px-4 py-3">
                    <button
                      type="button"
                      role="radio"
                      aria-checked={m.Default}
                      aria-label={m.Default ? "Default model" : "Set as default"}
                      title={m.Disabled ? "Enable it first" : m.Default ? "Default model" : "Set as default"}
                      disabled={busy[`default-${m.ID}`] || m.Disabled}
                      onclick={() => setDefault(m)}
                      class="flex h-4 w-4 items-center justify-center rounded-full border-2 transition-colors disabled:opacity-50 {m.Default ? 'border-green-500 bg-green-500' : 'border-white-400 dark:border-navy-500 hover:border-green-500'}"
                    >
                      {#if m.Default}
                        <span class="h-1.5 w-1.5 rounded-full bg-white-100"></span>
                      {/if}
                    </button>
                  </td>
                  <td class="px-4 py-3">
                    <div class="flex items-center gap-2">
                      <span class="text-black-900 dark:text-white-100">{m.Label || m.Model || (m.LiveSet ? "Live model set" : "")}</span>
                      {#if m.LiveSet}
                        <span class="inline-flex items-center rounded-full bg-green-500/10 px-2 py-0.5 text-[11px] font-medium text-green-600 dark:text-green-400">Live set</span>
                      {/if}
                      {#if m.Disabled}
                        <span class="inline-flex items-center rounded-full bg-cau-100 px-2 py-0.5 text-[11px] font-medium text-cau-400 dark:bg-cau-400/15">Disabled</span>
                      {/if}
                    </div>
                    <div class="font-mono text-xs text-black-700 dark:text-black-600">
                      {#if m.LiveSet}
                        {m.DiscoveryFilter ? `filter: ${m.DiscoveryFilter}` : "all models"}
                        <span class="ml-1 text-black-600 dark:text-black-700">· default: {m.DefaultVendorModel || "top of list"}</span>
                      {:else}
                        {m.Model}
                      {/if}
                    </div>
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
                    <div class="flex justify-end">
                      <KebabMenu
                        ariaLabel={`Actions for ${m.Label || m.Model}`}
                        items={[
                          { label: "Edit", onclick: () => openEdit(m) },
                          { label: "Set default", onclick: () => setDefault(m), disabled: m.Disabled || m.Default },
                          ...(m.LiveSet
                            ? [{ label: "Set default model…", onclick: () => openVendorPicker(m), disabled: m.Disabled }]
                            : []),
                          { label: "Test", onclick: () => testModel(m), disabled: m.Disabled },
                          { label: "Duplicate", onclick: () => duplicateModel(m) },
                          { label: m.Disabled ? "Enable" : "Disable", onclick: () => toggleDisabled(m) },
                          { label: "Delete", onclick: () => { confirmDelete = m; }, danger: true },
                        ]}
                      />
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
      <label for="m-key" class="flex items-center gap-2 text-sm font-medium text-black-900 dark:text-white-100 mb-1">
        API key
        <!-- Saved indicator: this model already has a stored (encrypted) key
             and the user hasn't typed a replacement yet. Turns to "Replacing…"
             the moment they start typing, so it's clear the new value wins. -->
        {#if editing && editing.HasKey}
          {#if keyReplacing}
            <span class="inline-flex items-center gap-1 rounded-full bg-amber-500/10 px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-amber-600 dark:text-amber-400">Replacing…</span>
          {:else}
            <span class="inline-flex items-center gap-1 rounded-full bg-green-500/10 px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-green-600 dark:text-green-400">
              <svg viewBox="0 0 16 16" fill="none" class="h-3 w-3"><path d="M3.5 8.5l3 3 6-7" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>
              Saved
            </span>
          {/if}
        {/if}
      </label>
      <input
        id="m-key"
        type="password"
        autocomplete="new-password"
        value={mKey}
        oninput={(e) => onKeyInput((e.target as HTMLInputElement).value)}
        placeholder={keyPlaceholder}
        class="w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-sm font-mono text-black-900 dark:text-white-100 placeholder:text-black-700 outline-none focus:border-green-500 focus:ring-2 focus:ring-green-200 dark:focus:ring-green-800 transition-colors"
      />
      <p class="mt-1 text-[11px] text-black-700 dark:text-black-600">
        {#if editing && editing.HasKey && !keyReplacing}A key is already stored for this model — leave blank to keep it. {/if}Encrypted at rest, used server-side only — never sent back to the browser.
      </p>
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
        disabled={multiSelect}
        placeholder={multiSelect ? "Using checked models below" : modelPlaceholder}
        class="w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-sm font-mono text-black-900 dark:text-white-100 placeholder:text-black-700 outline-none focus:border-green-500 focus:ring-2 focus:ring-green-200 dark:focus:ring-green-800 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
      />
      <p class="mt-1 text-[11px] text-black-700 dark:text-black-600">{multiSelect ? "This field is ignored in Multiple mode — the models come from the list / filter below." : "The model identifier sent to the provider's API. Type it directly, or pick from the list below."}</p>

      {#if mKind !== "other"}
        <!-- Optional discovery helper: search the vendor's model list and
             click to fill the Model ID above (single) or check several to add
             at once (multiple). Never required. -->

        <!-- Single vs multiple: single fills the Model ID above; multiple lets
             you tick several models and batch-ADD one provider entry per model,
             sharing the key + advanced settings. Available while editing too —
             switching an edit to Multiple just adds new entries (the model
             being edited is left untouched). -->
        <div class="mt-3 flex items-center justify-between gap-3">
          <div class="inline-flex rounded-lg border border-white-400 dark:border-navy-600 p-0.5 text-xs">
            <button
              type="button"
              onclick={() => setMulti(false)}
              class="rounded-md px-2.5 py-1 font-medium transition-colors {!multiSelect ? 'bg-green-500/10 text-green-600 dark:text-green-400' : 'text-black-700 dark:text-black-600 hover:text-black-900 dark:hover:text-white-100'}"
            >Single</button>
            <button
              type="button"
              onclick={() => setMulti(true)}
              class="rounded-md px-2.5 py-1 font-medium transition-colors {multiSelect ? 'bg-green-500/10 text-green-600 dark:text-green-400' : 'text-black-700 dark:text-black-600 hover:text-black-900 dark:hover:text-white-100'}"
            >Multiple</button>
          </div>
          {#if multiSelect && selectMode === "live"}
            <span class="text-[11px] font-medium text-black-700 dark:text-black-600">{filteredModels.length} match now{liveDefaultVendor ? ` · default: ${liveDefaultVendor}` : ""}</span>
          {:else if multiSelect}
            <span class="text-[11px] font-medium {selectedModels.length > 0 ? 'text-green-600 dark:text-green-400' : 'text-black-700 dark:text-black-600'}">{selectedModels.length} selected</span>
          {/if}
        </div>

        {#if multiSelect}
          <!-- Sub-mode: Live (selection follows the filter) vs Manual (tick +
               Select all snapshot). -->
          <div class="mt-2 flex items-center justify-between gap-3">
            <div class="inline-flex rounded-lg border border-white-400 dark:border-navy-600 p-0.5 text-[11px]">
              <button
                type="button"
                onclick={() => setSelectMode("live")}
                class="rounded-md px-2 py-0.5 font-medium transition-colors {selectMode === 'live' ? 'bg-green-500/10 text-green-600 dark:text-green-400' : 'text-black-700 dark:text-black-600 hover:text-black-900 dark:hover:text-white-100'}"
              >Live (follows filter)</button>
              <button
                type="button"
                onclick={() => setSelectMode("manual")}
                class="rounded-md px-2 py-0.5 font-medium transition-colors {selectMode === 'manual' ? 'bg-green-500/10 text-green-600 dark:text-green-400' : 'text-black-700 dark:text-black-600 hover:text-black-900 dark:hover:text-white-100'}"
              >Manual</button>
            </div>
            <div class="flex items-center gap-3 text-[11px]">
              {#if selectMode === "manual"}
                {#if selectedIDs.size > 0}
                  <button type="button" onclick={clearOverrides} class="text-black-700 dark:text-black-600 hover:text-black-900 dark:hover:text-white-100">Clear</button>
                {/if}
                <button type="button" onclick={selectAllFiltered} disabled={filteredModels.length === 0} class="font-medium text-green-600 dark:text-green-400 hover:underline disabled:opacity-40 disabled:no-underline disabled:cursor-not-allowed">Select all{searchTerms.length > 0 ? " matching" : ""}</button>
              {:else if excludedIDs.size > 0 || forcedIDs.size > 0}
                <button type="button" onclick={clearOverrides} class="text-black-700 dark:text-black-600 hover:text-black-900 dark:hover:text-white-100">Reset overrides</button>
              {/if}
            </div>
          </div>
        {/if}

        <p class="mt-1.5 text-[11px] text-black-700 dark:text-black-600">
          {#if multiSelect && selectMode === "live"}
            Saves <span class="font-medium">one live entry</span> that stores this filter. In the model picker it re-fetches the vendor list and shows everything matching — no per-model registration. The list below is a preview of what matches now; click one to pin it as the <span class="font-medium">default</span> (used when the set is picked without choosing a model). No pin = top of the list, which auto-re-selects if that model disappears.
          {:else if multiSelect}
            Tick models to add each as its own entry{editing ? " — the model you're editing stays as is" : ""}.
          {:else}
            Pick one model to fill the Model ID above, or switch to Multiple to add several at once.
          {/if}
          Filter grammar: <code class="rounded bg-white-200 dark:bg-navy-700 px-1">term</code> contains, <code class="rounded bg-white-200 dark:bg-navy-700 px-1">-term</code> / <code class="rounded bg-white-200 dark:bg-navy-700 px-1">!term</code> excludes.
        </p>

        <div class="relative mt-2">
          <svg class="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-black-700 dark:text-black-600" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="11" cy="11" r="8"/><path d="m21 21-4.3-4.3"/></svg>
          <input
            id="m-model-search"
            type="text"
            bind:this={searchEl}
            bind:value={modelSearch}
            placeholder="Filter models — space-separated, prefix -term or !term to exclude…"
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
                {@const isLive = multiSelect && selectMode === "live"}
                {@const isPin = liveDefaultVendor === m.id}
                {@const selected = isLive ? isPin : multiSelect ? isSelected(m.id) : mModel === m.id}
                <button
                  type="button"
                  onclick={() => (isLive ? (liveDefaultVendor = isPin ? "" : m.id) : pickModel(m))}
                  title={isLive ? (isPin ? "Default model — click to unpin (top of list)" : "Set as default model") : undefined}
                  class="flex w-full items-center justify-between gap-2 px-4 py-2 text-left text-sm hover:bg-white-200 dark:hover:bg-navy-800 {selected ? 'text-green-600 dark:text-green-400 font-medium' : 'text-black-900 dark:text-white-100'}"
                >
                  <span class="flex min-w-0 items-center gap-2">
                    {#if isLive}
                      <!-- Live: radio to pin the sticky DEFAULT (one), not a multi-select. -->
                      <span class="flex h-4 w-4 shrink-0 items-center justify-center rounded-full border-2 {isPin ? 'border-green-500 bg-green-500' : 'border-white-400 dark:border-navy-500'}">
                        {#if isPin}<span class="h-1.5 w-1.5 rounded-full bg-white-100"></span>{/if}
                      </span>
                    {:else if multiSelect}
                      <span class="flex h-4 w-4 shrink-0 items-center justify-center rounded border {selected ? 'border-green-500 bg-green-500 text-white-100' : 'border-white-400 dark:border-navy-600'}">
                        {#if selected}<svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round"><path d="M20 6 9 17l-5-5"/></svg>{/if}
                      </span>
                    {/if}
                    <span class="min-w-0 truncate">
                      {m.id}
                      {#if m.label && m.label !== m.id}
                        <span class="ml-1 text-[11px] font-normal text-black-700 dark:text-black-600">· {m.label}</span>
                      {/if}
                    </span>
                  </span>
                  {#if isLive && isPin}
                    <span class="shrink-0 rounded-full bg-green-500/10 px-1.5 py-0.5 text-[10px] font-medium text-green-600 dark:text-green-400">default</span>
                  {:else if selected && !multiSelect}
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
    <Button variant="primary" disabled={savingModel} onclick={saveModel}>
      {#if savingModel}Saving…{:else if multiSelect && selectMode === "live"}{editing ? "Save live set" : "Add live set"}{:else if multiSelect}Add {selectedModels.length || ""} model{selectedModels.length === 1 ? "" : "s"}{:else}{editing ? "Save model" : "Add model"}{/if}
    </Button>
  {/snippet}
</Modal>
