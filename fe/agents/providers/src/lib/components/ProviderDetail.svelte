<script lang="ts">
  import { onMount } from "svelte";
  import { ConfirmDialog, KvList, Breadcrumb, Modal, Select, Button, TextInput, type BreadcrumbItem } from "@wick-fe/common-ui";
  import { toastOk, toastError } from "@wick-fe/common-stores";
  import {
    apiGetProviderDetail,
    apiSaveProviderDetail,
    apiSaveConfigKey,
    apiHookCheck,
    apiHookEnable,
    apiHookDisable,
    apiDeleteProvider,
    apiRenameProvider,
    apiProbeGate,
    apiGetProviderCatalog,
  } from "$lib/api.js";
  import type { CatalogEntry, ProviderCatalog } from "$lib/api.js";
  import type { ProviderDetailResponse, ConfigFieldDTO } from "$lib/types.js";

  type Props = {
    base: string;
    type: string;
    name: string;
    onBack: () => void;
  };
  let { base, type, name, onBack }: Props = $props();

  let crumbs = $derived<BreadcrumbItem[]>([
    { label: "Providers", onClick: onBack },
    { label: `${type}/${name}`, truncate: true },
  ]);

  type KVRow = { key: string; value: string };

  let data = $state<ProviderDetailResponse | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let saving = $state(false);
  let confirmDelete = $state(false);
  let showRename = $state(false);
  let renameValue = $state("");
  let renaming = $state(false);
  let togglingDisabled = $state(false);

  // The name becomes the second half of the "type/name" provider key.
  // Spaces auto-convert to '_' as the user types (matches the create
  // form); anything else outside [A-Za-z0-9_] is rejected inline. '_' is
  // the only allowed separator.
  function onRenameInput() {
    renameValue = renameValue.replace(/\s+/g, "_");
  }
  let renameError = $derived.by(() => {
    const v = renameValue.trim();
    if (v === "") return "Name is required";
    if (!/^[A-Za-z0-9_]+$/.test(v)) return "Use letters, digits or '_' only";
    if (v === name) return "That's already the current name";
    return "";
  });
  let busy = $state<Record<string, boolean>>({});

  let fieldValues = $state<Record<string, string>>({});
  let secretTouched = $state<Record<string, boolean>>({});
  /* editorRows holds parsed rows for each kvlist field, keyed by field Key */
  let editorRows = $state<Record<string, KVRow[]>>({});

  /* Curated env/args picker for this provider type (click-to-add known
     vars). Fetched once on mount; empty for unknown types. */
  let catalog = $state<ProviderCatalog>({ env: [], args: [] });

  /* Catalog picker modal state. `pickerField` is the editor field the
     modal adds rows to (env or extra_args); null = closed. */
  let pickerField = $state<ConfigFieldDTO | null>(null);
  let pickerSearch = $state("");
  let pickerSelected = $state<Set<string>>(new Set());

  function openPicker(f: ConfigFieldDTO) {
    pickerField = f;
    pickerSearch = "";
    pickerSelected = new Set();
  }
  function closePicker() {
    pickerField = null;
  }
  function togglePick(key: string) {
    const next = new Set(pickerSelected);
    if (next.has(key)) next.delete(key);
    else next.add(key);
    pickerSelected = next;
  }
  // Entries shown in the modal: the field's catalog filtered by search
  // (matches key or description, case-insensitive).
  let pickerEntries = $derived.by(() => {
    if (!pickerField) return [] as CatalogEntry[];
    const all = catalogFor(pickerField);
    const q = pickerSearch.trim().toLowerCase();
    if (!q) return all;
    return all.filter(
      (e) => e.key.toLowerCase().includes(q) || e.description.toLowerCase().includes(q),
    );
  });
  function addSelectedFromPicker() {
    if (!pickerField) return;
    const f = pickerField;
    const entries = catalogFor(f).filter((e) => pickerSelected.has(e.key));
    for (const e of entries) addFromCatalog(f, e);
    closePicker();
  }
  // Default value preview shown next to an entry in the modal.
  function entryDefault(e: CatalogEntry): string {
    if (e.options && e.options.length > 0) return e.options[0];
    return e.placeholder ?? "";
  }

  // entryAlreadyAdded reports whether an entry is already present in the
  // open picker field's editor — so the modal can mark it and disable its
  // checkbox (no point adding a duplicate env var; for args we match the
  // flag/`-c key=` prefix so the same knob isn't added twice).
  function entryAlreadyAdded(e: CatalogEntry): boolean {
    if (!pickerField) return false;
    const rows = editorRows[pickerField.Key] ?? [];
    if (isKeyValueEditor(pickerField)) {
      return rows.some((r) => r.key === e.key);
    }
    // value-list (args): each row's value is one token. "-c foo=" style
    // matches by prefix; bare flags match exactly.
    return rows.some((r) => {
      const v = r.value ?? "";
      return e.key.endsWith("=") ? v.startsWith(e.key) : v === e.key;
    });
  }

  function setBusy(key: string, val: boolean) {
    busy = { ...busy, [key]: val };
  }

  function kvCols(f: ConfigFieldDTO): string[] {
    if (f.Options === "") {
      return ["value"];
    }
    return f.Options.split("|");
  }

  function isKvlist(f: ConfigFieldDTO): boolean {
    return f.Type === "kvlist";
  }

  function isKeyValueEditor(f: ConfigFieldDTO): boolean {
    return isKvlist(f) && kvCols(f).length >= 2;
  }

  function isValueListEditor(f: ConfigFieldDTO): boolean {
    return isKvlist(f) && kvCols(f).length < 2;
  }

  function isSimpleField(f: ConfigFieldDTO): boolean {
    return !isKvlist(f);
  }

  function parseRows(f: ConfigFieldDTO): KVRow[] {
    if (f.Value === "") {
      return [];
    }
    try {
      const parsed = JSON.parse(f.Value) as Record<string, string>[];
      if (!Array.isArray(parsed)) {
        return [];
      }
      return parsed.map((r) => ({ key: r.key ?? "", value: r.value ?? "" }));
    } catch {
      return [];
    }
  }

  function serializeRows(f: ConfigFieldDTO, rows: KVRow[]): string {
    const kv = isKeyValueEditor(f);
    const cleaned = rows.filter((r) => (kv ? r.key.trim() !== "" || r.value.trim() !== "" : r.value.trim() !== ""));
    const out = cleaned.map((r) => (kv ? { key: r.key, value: r.value } : { value: r.value }));
    return JSON.stringify(out);
  }

  let simpleFields = $derived(data ? data.ConfigFields.filter(isSimpleField) : []);
  let valueListFields = $derived(data ? data.ConfigFields.filter(isValueListEditor) : []);
  let keyValueFields = $derived(data ? data.ConfigFields.filter(isKeyValueEditor) : []);

  async function load(silent = false) {
    if (!silent) { loading = true; error = null; }
    try {
      data = await apiGetProviderDetail(base, type, name);
      const vals: Record<string, string> = {};
      const rows: Record<string, KVRow[]> = {};
      for (const f of data.ConfigFields) {
        if (isKvlist(f)) {
          rows[f.Key] = parseRows(f);
        } else {
          vals[f.Key] = f.IsSecret ? "" : f.Value;
        }
      }
      fieldValues = vals;
      editorRows = rows;
      secretTouched = {};
    } catch (e) {
      if (!silent) error = e instanceof Error ? e.message : "Failed to load provider detail";
    } finally {
      if (!silent) loading = false;
    }
  }

  function onSecretInput(key: string) {
    secretTouched = { ...secretTouched, [key]: true };
  }

  function buildSavePayload(): Record<string, string> {
    const payload: Record<string, string> = {};
    if (!data) {
      return payload;
    }
    for (const f of simpleFields) {
      if (f.IsSecret) {
        if (secretTouched[f.Key] && fieldValues[f.Key] !== "") {
          payload[f.Key] = fieldValues[f.Key];
        }
      } else {
        payload[f.Key] = fieldValues[f.Key] ?? "";
      }
    }
    return payload;
  }

  async function doSave() {
    saving = true;
    try {
      await apiSaveProviderDetail(base, type, name, buildSavePayload());
      toastOk("Settings saved");
      await load(true);
    } catch (e) {
      toastError(e instanceof Error ? e.message : "Save failed");
    } finally {
      saving = false;
    }
  }

  function setRows(f: ConfigFieldDTO, rows: Record<string, string>[]) {
    editorRows = {
      ...editorRows,
      [f.Key]: rows.map((r) => ({ key: r.key ?? "", value: r.value ?? "" })),
    };
  }

  async function saveEditor(f: ConfigFieldDTO) {
    const key = `editor-${f.Key}`;
    setBusy(key, true);
    try {
      const serialized = serializeRows(f, editorRows[f.Key] ?? []);
      await apiSaveConfigKey(base, type, name, f.Key, serialized);
      toastOk(`Saved ${f.Key}`);
      await load(true);
    } catch (e) {
      toastError(e instanceof Error ? e.message : "Save failed");
    } finally {
      setBusy(key, false);
    }
  }

  // catalogFor maps an editor field to its picker entries: the `env`
  // field uses the env catalog, anything else (extra_args) uses args.
  function catalogFor(f: ConfigFieldDTO): CatalogEntry[] {
    return f.Key === "env" ? catalog.env : catalog.args;
  }

  // envEntryByKey maps an env var name to its catalog entry, so the
  // value cell can offer a dropdown of known options for that var.
  // Only meaningful for the `env` field. Returns undefined for unknown
  // vars (the cell falls back to a plain text input).
  function envEntryByKey(key: string): CatalogEntry | undefined {
    return catalog.env.find((e) => e.key === key);
  }

  // CUSTOM_OPTION is the sentinel a value dropdown selects to switch the
  // cell back to a free-text input (when the desired value isn't one of
  // the known options).
  const CUSTOM_OPTION = "__custom__";
  // Tracks which env rows the user forced into custom (free-text) mode,
  // keyed by row index, so picking "Custom…" sticks even if the value
  // momentarily matches an option again.
  let envCustomRows = $state<Set<number>>(new Set());
  function setEnvCustom(index: number, on: boolean) {
    const next = new Set(envCustomRows);
    if (on) next.add(index);
    else next.delete(index);
    envCustomRows = next;
  }

  // addFromCatalog appends row(s) to the editor for the picked entry,
  // pre-filling the default value (first option for bool/enum), then
  // saves.
  //
  //  - env (key-value editor): one row, entry.key in key, default in value.
  //  - extra_args (value-list editor): each row is one whitespace-split
  //    arg token, so:
  //      * "-c foo=" style (ends with "=")  -> one row "-c foo=<default>"
  //      * "--flag" with a value            -> two rows: "--flag", "<default>"
  //      * bare flag with no value          -> one row "--flag"
  function addFromCatalog(f: ConfigFieldDTO, entry: CatalogEntry) {
    const defaultVal = entry.options && entry.options.length > 0 ? entry.options[0] : (entry.placeholder ?? "");
    const existing = editorRows[f.Key] ?? [];

    if (isKeyValueEditor(f)) {
      if (existing.some((r) => r.key === entry.key)) {
        toastError(`${entry.key} is already set`);
        return;
      }
      editorRows = { ...editorRows, [f.Key]: [...existing, { key: entry.key, value: defaultVal }] };
      saveEditor(f);
      return;
    }

    // value-list editor (args)
    const rows: KVRow[] = [];
    if (entry.key.endsWith("=")) {
      rows.push({ key: "", value: entry.key + defaultVal });
    } else if (defaultVal !== "") {
      rows.push({ key: "", value: entry.key });
      rows.push({ key: "", value: defaultVal });
    } else {
      rows.push({ key: "", value: entry.key });
    }
    editorRows = { ...editorRows, [f.Key]: [...existing, ...rows] };
    saveEditor(f);
  }

  async function toggleDisabled() {
    if (!data) {
      return;
    }
    togglingDisabled = true;
    try {
      await apiSaveConfigKey(base, type, name, "disabled", data.Instance.Disabled ? "false" : "true");
      toastOk(data.Instance.Disabled ? "Provider enabled" : "Provider disabled");
      await load(true);
    } catch (e) {
      toastError(e instanceof Error ? e.message : "Toggle failed");
    } finally {
      togglingDisabled = false;
    }
  }

  async function doHookCheck(event: string) {
    const key = `hook-check-${event}`;
    setBusy(key, true);
    try {
      await apiHookCheck(base, type, name, event);
      toastOk(`Checked ${event} hook`);
      await load(true);
    } catch (e) {
      toastError(e instanceof Error ? e.message : "Hook check failed");
    } finally {
      setBusy(key, false);
    }
  }

  async function doHookEnable(event: string) {
    const key = `hook-enable-${event}`;
    setBusy(key, true);
    try {
      await apiHookEnable(base, type, name, event);
      toastOk(`Enabled ${event} hook`);
      await load(true);
    } catch (e) {
      toastError(e instanceof Error ? e.message : "Enable failed");
    } finally {
      setBusy(key, false);
    }
  }

  async function doHookDisable(event: string) {
    const key = `hook-disable-${event}`;
    setBusy(key, true);
    try {
      await apiHookDisable(base, type, name, event);
      toastOk(`Disabled ${event} hook`);
      await load(true);
    } catch (e) {
      toastError(e instanceof Error ? e.message : "Disable failed");
    } finally {
      setBusy(key, false);
    }
  }

  async function doProbeGate() {
    setBusy("probe-gate", true);
    try {
      await apiProbeGate(type, name);
      toastOk("Gate probe triggered");
      await load(true);
    } catch (e) {
      toastError(e instanceof Error ? e.message : "Probe failed");
    } finally {
      setBusy("probe-gate", false);
    }
  }

  async function doDelete() {
    confirmDelete = false;
    setBusy("delete", true);
    try {
      await apiDeleteProvider(type, name);
      toastOk("Provider deleted");
      onBack();
    } catch (e) {
      toastError(e instanceof Error ? e.message : "Delete failed");
      setBusy("delete", false);
    }
  }

  function openRename() {
    renameValue = name;
    showRename = true;
  }

  async function doRename() {
    if (renameError) return;
    const newName = renameValue.trim();
    renaming = true;
    try {
      const res = await apiRenameProvider(type, name, newName);
      const migrated = res.projects_migrated;
      toastOk(
        migrated > 0
          ? `Renamed to ${type}/${res.name} · ${migrated} project default${migrated === 1 ? "" : "s"} updated`
          : `Renamed to ${type}/${res.name}`,
      );
      showRename = false;
      // The route key (type/name) changed — return to the list so the
      // detail page reloads under the new name instead of 404ing on the
      // stale one.
      onBack();
    } catch (e) {
      toastError(e instanceof Error ? e.message : "Rename failed");
    } finally {
      renaming = false;
    }
  }

  onMount(() => {
    load();
    apiGetProviderCatalog(base, type)
      .then((c) => { catalog = c; })
      .catch(() => { /* picker is optional — manual KvList still works */ });
  });

  let hookEvents = $derived(data ? Object.keys(data.Hooks) : []);
</script>

{#if confirmDelete}
  <ConfirmDialog
    message={`Delete provider ${type}/${name}? This cannot be undone.`}
    onConfirm={doDelete}
    onCancel={() => { confirmDelete = false; }}
  />
{/if}

<Modal open={showRename} title={`Rename ${type}/${name}`} size="md" onClose={() => { showRename = false; }}>
  <div class="space-y-3">
    <div>
      <label for="rename-input" class="block text-xs text-black-700 dark:text-black-600 mb-1">New name</label>
      <div class="flex items-center gap-1.5">
        <span class="font-mono text-sm text-black-700 dark:text-black-600 shrink-0">{type}/</span>
        <input
          id="rename-input"
          type="text"
          bind:value={renameValue}
          oninput={onRenameInput}
          onkeydown={(e) => { if (e.key === "Enter" && !renameError && !renaming) doRename(); }}
          placeholder="abc_a"
          class="w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-sm font-mono text-black-900 dark:text-white-100 outline-none focus:border-green-500 focus:ring-2 focus:ring-green-200 dark:focus:ring-green-800"
        />
      </div>
      {#if renameValue.trim() !== "" && renameError}
        <p class="mt-1.5 text-[11px] text-red-600 dark:text-red-400">{renameError}</p>
      {:else}
        <p class="mt-1.5 text-[11px] text-black-700 dark:text-black-600">Letters, digits and '_' only. Spaces auto-convert to '_' (e.g. abc_a).</p>
      {/if}
    </div>
    <div class="rounded-lg border border-amber-300 dark:border-amber-700 bg-amber-50 dark:bg-amber-900/20 px-3 py-2.5 text-[11px] leading-relaxed text-amber-800 dark:text-amber-300">
      <p class="font-semibold mb-0.5">Heads up</p>
      <p>Projects whose default provider is this instance are re-pointed automatically.</p>
      <p class="mt-1">Existing sessions keep the old provider and may error on next message — open the session and pick the provider again to fix it.</p>
    </div>
  </div>
  {#snippet footer()}
    <Button variant="secondary" disabled={renaming} onclick={() => { showRename = false; }}>Cancel</Button>
    <Button variant="primary" disabled={renaming || !!renameError} onclick={doRename}>{renaming ? "Renaming…" : "Rename"}</Button>
  {/snippet}
</Modal>

<Modal
  open={pickerField !== null}
  title={pickerField ? `Add to ${pickerField.Key}` : ""}
  size="lg"
  onClose={closePicker}
>
  <div class="space-y-3">
    <TextInput
      value={pickerSearch}
      onChange={(v) => { pickerSearch = v; }}
      type="search"
      placeholder="Search by name or description…"
    />
    <div class="max-h-[55vh] overflow-y-auto space-y-1">
      {#if pickerEntries.length === 0}
        <p class="py-6 text-center text-xs text-black-700 dark:text-black-600">No matches.</p>
      {/if}
      {#each pickerEntries as entry (entry.key)}
        {@const def = entryDefault(entry)}
        {@const added = entryAlreadyAdded(entry)}
        <label
          class="flex items-start gap-3 rounded-lg px-2 py-2 {added ? 'opacity-60 cursor-not-allowed' : 'cursor-pointer hover:bg-white-200 dark:hover:bg-navy-800'}"
        >
          <input
            type="checkbox"
            checked={added || pickerSelected.has(entry.key)}
            disabled={added}
            onchange={() => togglePick(entry.key)}
            class="mt-0.5 h-4 w-4 shrink-0 accent-green-600 disabled:cursor-not-allowed"
          />
          <span class="min-w-0 flex-1">
            <span class="flex items-baseline gap-2 flex-wrap">
              <span class="font-mono text-xs font-semibold text-black-900 dark:text-white-100 break-all">{entry.key}</span>
              {#if added}
                <span class="rounded bg-green-50 dark:bg-green-900 px-1.5 py-0.5 text-[10px] font-semibold text-green-700 dark:text-green-300">already added</span>
              {/if}
              {#if def !== ""}
                <span class="rounded bg-white-300 dark:bg-navy-600 px-1.5 py-0.5 text-[10px] font-mono text-black-700 dark:text-black-600">default: {def}</span>
              {/if}
              {#if entry.options && entry.options.length > 0}
                <span class="text-[10px] text-black-600 dark:text-black-700">{entry.options.join(" / ")}</span>
              {/if}
            </span>
            <span class="mt-0.5 block text-[11px] text-black-700 dark:text-black-600 leading-relaxed">{entry.description}</span>
          </span>
        </label>
      {/each}
    </div>
  </div>
  {#snippet footer()}
    <span class="mr-auto text-xs text-black-700 dark:text-black-600">{pickerSelected.size} selected</span>
    <Button variant="secondary" onclick={closePicker}>Cancel</Button>
    <Button variant="primary" disabled={pickerSelected.size === 0} onclick={addSelectedFromPicker}>
      Add {pickerSelected.size} selected
    </Button>
  {/snippet}
</Modal>

<div class="space-y-4">
  <!-- Header -->
  <Breadcrumb items={crumbs} />
  <div class="flex items-center justify-between gap-3 flex-wrap">
    <div class="flex items-center gap-2 flex-wrap">
      <button
        type="button"
        onclick={openRename}
        ondblclick={openRename}
        title="Rename this provider"
        class="group flex items-center gap-1.5 rounded px-1 -mx-1 hover:bg-white-200 dark:hover:bg-navy-800 transition-colors"
      >
        <span class="text-lg font-semibold text-black-900 dark:text-white-100">{type}/{name}</span>
        <svg
          class="w-4 h-4 text-black-700 dark:text-black-600 opacity-60 group-hover:opacity-100 transition-opacity shrink-0"
          fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24" aria-hidden="true"
        >
          <path stroke-linecap="round" stroke-linejoin="round" d="M11 5H6a2 2 0 00-2 2v11a2 2 0 002 2h11a2 2 0 002-2v-5m-1.414-9.414a2 2 0 112.828 2.828L11.828 15H9v-2.828l8.586-8.586z"/>
        </svg>
      </button>
      {#if data}
        {#if !data.PathFound}
          <span class="rounded bg-red-50 dark:bg-red-900 px-2 py-0.5 text-xs font-medium text-red-700 dark:text-red-300">not found</span>
        {:else}
          <span class="rounded bg-green-50 dark:bg-green-900 px-2 py-0.5 text-xs font-medium text-green-700 dark:text-green-300">{data.Version}</span>
        {/if}
        {#if data.ActiveCount > 0}
          <span class="rounded bg-blue-500/10 dark:bg-blue-500/20 px-2 py-0.5 text-xs font-medium text-blue-600 dark:text-blue-400">{data.ActiveCount} active</span>
        {/if}
      {/if}
    </div>
    {#if data}
      <div class="flex items-center gap-2">
        {#if data.Instance.Disabled}
          <button
            onclick={toggleDisabled}
            disabled={togglingDisabled}
            title="Click to enable this provider"
            class="flex items-center gap-2 rounded-lg border border-amber-300 dark:border-amber-700 bg-amber-50 dark:bg-amber-900 px-4 py-2 text-xs font-semibold text-amber-800 dark:text-amber-300 hover:bg-amber-300/40 dark:hover:bg-amber-700 disabled:opacity-50 transition-colors"
          >
            <span class="inline-block w-2 h-2 rounded-full bg-amber-500"></span>
            Disabled — click to enable
          </button>
        {:else}
          <button
            onclick={toggleDisabled}
            disabled={togglingDisabled}
            title="Click to disable this provider"
            class="flex items-center gap-2 rounded-lg border border-green-300 dark:border-green-700 bg-green-50 dark:bg-green-900 px-4 py-2 text-xs font-semibold text-green-700 dark:text-green-300 hover:bg-green-200 dark:hover:bg-green-800 disabled:opacity-50 transition-colors"
          >
            <span class="inline-block w-2 h-2 rounded-full bg-green-500"></span>
            Enabled — click to disable
          </button>
        {/if}
        <button
          onclick={() => { confirmDelete = true; }}
          disabled={busy["delete"]}
          class="rounded-lg border border-red-300 dark:border-red-700 bg-red-50 dark:bg-red-900 px-4 py-2 text-xs font-semibold text-red-700 dark:text-red-300 hover:bg-red-300/40 dark:hover:bg-red-800 disabled:opacity-50 transition-colors"
        >Delete</button>
      </div>
    {/if}
  </div>

  {#if loading}
    <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-6 py-10 text-center text-sm text-black-700 dark:text-black-600">Loading…</div>
  {:else if error}
    <div class="rounded-xl border border-red-300 dark:border-red-700 bg-red-50 dark:bg-red-900/20 px-4 py-3 text-sm text-red-700 dark:text-red-400">{error}</div>
  {:else if data}
    <!-- Binary info -->
    <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-5 space-y-1 text-xs">
      <div class="flex gap-2">
        <span class="w-20 shrink-0 text-black-700 dark:text-black-600">resolved</span>
        {#if data.Path}
          <span class="font-mono text-black-900 dark:text-white-100 break-all">{data.Path}</span>
        {:else}
          <span class="text-black-600 dark:text-black-700">—</span>
        {/if}
      </div>
      {#if data.VersionErr}
        <div class="flex gap-2">
          <span class="w-20 shrink-0 text-black-700 dark:text-black-600">error</span>
          <span class="font-mono text-red-600 dark:text-red-400 break-all">{data.VersionErr}</span>
        </div>
      {/if}
    </div>

    <!-- Configuration (simple fields, 2-column grid) -->
    {#if simpleFields.length > 0}
      <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-sm overflow-hidden">
        <div class="px-5 py-3 border-b border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800">
          <h3 class="text-sm font-semibold text-black-900 dark:text-white-100">Configuration</h3>
        </div>
        <div class="p-5">
          <div class="grid grid-cols-1 sm:grid-cols-2 gap-x-6 gap-y-5">
            {#each simpleFields as f (f.Key)}
              <div>
                <div class="flex items-center gap-2 mb-1.5">
                  <span class="font-mono text-xs font-semibold text-black-900 dark:text-white-100">{f.Key}</span>
                  {#if f.Required && f.Value === ""}
                    <span class="rounded bg-red-100 dark:bg-red-900 px-1.5 py-0.5 text-[10px] font-semibold text-red-700 dark:text-red-300">missing</span>
                  {:else if f.Required}
                    <span class="text-[10px] font-semibold text-red-500" title="required">*</span>
                  {/if}
                  {#if f.IsSecret && f.Value !== ""}
                    <span class="rounded-full bg-green-100 dark:bg-green-900 px-1.5 py-0.5 text-[10px] font-semibold text-green-700 dark:text-green-300">stored</span>
                  {/if}
                </div>
                {#if (f.Type === "dropdown" || f.Type === "select") && f.Options}
                  <select
                    bind:value={fieldValues[f.Key]}
                    class="w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2.5 text-sm font-mono text-black-900 dark:text-white-100 outline-none focus:border-green-500 focus:ring-2 focus:ring-green-200 dark:focus:ring-green-800 transition-colors cursor-pointer"
                  >
                    {#each f.Options.split(f.Type === "dropdown" ? "|" : ",").map((o) => o.trim()).filter(Boolean) as opt (opt)}
                      <option value={opt}>{opt}</option>
                    {/each}
                  </select>
                {:else if f.IsSecret}
                  <input
                    type="password"
                    placeholder={f.Value !== "" ? "Type new value to replace" : "Enter secret"}
                    autocomplete="new-password"
                    bind:value={fieldValues[f.Key]}
                    oninput={() => onSecretInput(f.Key)}
                    class="w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2.5 text-sm font-mono text-black-900 dark:text-white-100 placeholder:text-black-700 outline-none focus:border-green-500 focus:ring-2 focus:ring-green-200 dark:focus:ring-green-800 transition-colors"
                  />
                {:else if f.Type === "number"}
                  <input
                    type="number"
                    bind:value={fieldValues[f.Key]}
                    class="w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2.5 text-sm font-mono text-black-900 dark:text-white-100 placeholder:text-black-700 outline-none focus:border-green-500 focus:ring-2 focus:ring-green-200 dark:focus:ring-green-800 transition-colors"
                  />
                {:else}
                  <input
                    type="text"
                    bind:value={fieldValues[f.Key]}
                    class="w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2.5 text-sm font-mono text-black-900 dark:text-white-100 placeholder:text-black-700 outline-none focus:border-green-500 focus:ring-2 focus:ring-green-200 dark:focus:ring-green-800 transition-colors"
                  />
                {/if}
                {#if f.Description}
                  <p class="mt-1.5 text-[11px] text-black-700 dark:text-black-600 leading-relaxed whitespace-pre-line">{f.Description}</p>
                {/if}
              </div>
            {/each}
          </div>
        </div>
        <div class="px-5 py-3 border-t border-white-300 dark:border-navy-600 flex justify-end">
          <button
            onclick={doSave}
            disabled={saving}
            class="rounded-lg bg-green-600 hover:bg-green-700 px-4 py-1.5 text-xs font-medium text-white-100 disabled:opacity-50"
          >{saving ? "Saving…" : "Save All"}</button>
        </div>
      </div>
    {/if}

    <!-- Value-list editors (single-column kvlist, e.g. extra_args) -->
    {#each valueListFields as f (f.Key)}
      {@const entries = catalogFor(f)}
      <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-sm overflow-hidden">
        <div class="px-5 py-3 border-b border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800">
          <div class="flex items-center justify-between gap-2 flex-wrap">
            <span class="font-mono text-sm font-semibold text-black-900 dark:text-white-100">{f.Key}</span>
            {#if entries.length > 0}
              <button
                type="button"
                onclick={() => openPicker(f)}
                class="rounded-lg border border-green-400 dark:border-green-700 bg-green-50 dark:bg-green-900 px-3 py-1 text-xs font-medium text-green-700 dark:text-green-300 hover:bg-green-200 dark:hover:bg-green-800 transition-colors"
              >+ Add from catalog</button>
            {/if}
          </div>
          {#if f.Description}
            <p class="mt-0.5 text-xs text-black-700 dark:text-black-600 whitespace-pre-line">{f.Description}</p>
          {/if}
        </div>
        <div class="p-5">
          <KvList
            columns={kvCols(f)}
            rows={editorRows[f.Key] ?? []}
            onChange={(rows: Record<string, string>[]) => setRows(f, rows)}
            onCommit={() => saveEditor(f)}
            placeholders={{ value: "value" }}
            addLabel="+ Add Row"
            emptyText="No rows yet — click + Add Row to start"
          />
        </div>
      </div>
    {/each}

    <!-- Key-value editors (multi-column kvlist, e.g. env) -->
    {#each keyValueFields as f (f.Key)}
      {@const cols = kvCols(f)}
      {@const entries = catalogFor(f)}
      <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-sm overflow-hidden">
        <div class="px-5 py-3 border-b border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800">
          <div class="flex items-center justify-between gap-2 flex-wrap">
            <div class="flex items-center gap-2">
              <span class="font-mono text-sm font-semibold text-black-900 dark:text-white-100">{f.Key}</span>
              <span class="text-[11px] text-black-700 dark:text-black-600 font-normal">{cols.join(" · ")}</span>
            </div>
            {#if entries.length > 0}
              <button
                type="button"
                onclick={() => openPicker(f)}
                class="rounded-lg border border-green-400 dark:border-green-700 bg-green-50 dark:bg-green-900 px-3 py-1 text-xs font-medium text-green-700 dark:text-green-300 hover:bg-green-200 dark:hover:bg-green-800 transition-colors"
              >+ Add from catalog</button>
            {/if}
          </div>
          {#if f.Description}
            <p class="mt-0.5 text-xs text-black-700 dark:text-black-600 whitespace-pre-line">{f.Description}</p>
          {/if}
        </div>
        <div class="p-5">
          <KvList
            columns={cols}
            rows={editorRows[f.Key] ?? []}
            onChange={(rows: Record<string, string>[]) => setRows(f, rows)}
            onCommit={() => saveEditor(f)}
            placeholders={{ key: "key", value: "value" }}
            addLabel="+ Add Row"
            emptyText="No rows yet — click + Add Row to start"
          >
            {#snippet cell({ row: r, index, col, value, set }: { row: Record<string, string>; index: number; col: string; value: string; set: (v: string) => void })}
              {@const entry = col === "value" && f.Key === "env" ? envEntryByKey(r.key ?? "") : undefined}
              {#if entry && entry.options && entry.options.length > 0 && !envCustomRows.has(index)}
                <Select
                  size="sm"
                  value={entry.options.includes(value) ? value : (value === "" ? entry.options[0] : CUSTOM_OPTION)}
                  options={[...entry.options, { label: "Custom…", value: CUSTOM_OPTION }]}
                  onChange={(v) => {
                    if (v === CUSTOM_OPTION) { setEnvCustom(index, true); return; }
                    set(v);
                    saveEditor(f);
                  }}
                />
              {:else}
                <input
                  type="text"
                  class="w-full rounded-md border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-2.5 py-1.5 text-xs font-mono text-black-900 dark:text-white-100 outline-none focus:border-green-500 focus:ring-1 focus:ring-green-200 dark:focus:ring-green-800"
                  aria-label={col}
                  placeholder={col === "value" && entry ? (entry.placeholder ?? "value") : col}
                  {value}
                  oninput={(e) => set((e.target as HTMLInputElement).value)}
                  onblur={() => saveEditor(f)}
                />
              {/if}
            {/snippet}
          </KvList>
        </div>
      </div>
    {/each}

    <!-- Hooks -->
    {#if hookEvents.length > 0}
      <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-sm overflow-hidden">
        <div class="px-5 py-3 border-b border-white-300 dark:border-navy-600">
          <h2 class="text-sm font-semibold text-black-900 dark:text-white-100">Hooks</h2>
        </div>
        <div class="divide-y divide-white-300 dark:divide-navy-600">
          {#each hookEvents as event (event)}
            {@const cap = data.Hooks[event]}
            {@const enabled = data.HookEnabled[event] ?? false}
            <div class="px-5 py-4 flex flex-col gap-2 sm:flex-row sm:items-center sm:gap-4">
              <div class="sm:w-40 shrink-0">
                <p class="text-xs font-medium font-mono text-black-900 dark:text-white-100">{event}</p>
                {#if cap.Scope}
                  <p class="text-xs text-black-600 dark:text-black-700">{cap.Scope}</p>
                {/if}
              </div>
              <div class="flex flex-wrap items-center gap-2 flex-1">
                {#if cap.Verified}
                  <span class="rounded bg-green-100 dark:bg-green-900 px-2 py-0.5 text-xs font-medium text-green-700 dark:text-green-300">verified</span>
                {:else if cap.Supported}
                  <span class="rounded bg-amber-100 dark:bg-amber-900 px-2 py-0.5 text-xs font-medium text-amber-700 dark:text-amber-300">supported</span>
                {:else}
                  <span class="rounded bg-white-300 dark:bg-navy-600 px-2 py-0.5 text-xs font-medium text-black-700 dark:text-black-600">unknown</span>
                {/if}
                {#if enabled}
                  <span class="rounded bg-blue-100 dark:bg-blue-900 px-2 py-0.5 text-xs font-medium text-blue-700 dark:text-blue-300">enabled</span>
                {/if}
                {#if cap.Error}
                  <span class="text-xs text-red-600 dark:text-red-400 font-mono truncate max-w-xs" title={cap.Error}>{cap.Error}</span>
                {/if}
                {#if cap.ProbedAt}
                  <span class="text-xs text-black-600 dark:text-black-700">probed {new Date(cap.ProbedAt).toLocaleString()}</span>
                {/if}
              </div>
              <div class="flex gap-2 shrink-0">
                <button
                  onclick={() => doHookCheck(event)}
                  disabled={busy[`hook-check-${event}`]}
                  class="rounded-lg border border-white-400 dark:border-navy-500 px-3 py-1.5 text-xs font-medium text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800 disabled:opacity-50"
                >Check</button>
                {#if enabled}
                  <button
                    onclick={() => doHookDisable(event)}
                    disabled={busy[`hook-disable-${event}`]}
                    class="rounded-lg border border-amber-400 dark:border-amber-700 px-3 py-1.5 text-xs font-medium text-amber-700 dark:text-amber-400 hover:bg-amber-50 dark:hover:bg-amber-900/20 disabled:opacity-50"
                  >Disable</button>
                {:else}
                  <button
                    onclick={() => doHookEnable(event)}
                    disabled={busy[`hook-enable-${event}`]}
                    class="rounded-lg border border-green-400 dark:border-green-700 px-3 py-1.5 text-xs font-medium text-green-700 dark:text-green-400 hover:bg-green-50 dark:hover:bg-green-900/20 disabled:opacity-50"
                  >Enable</button>
                {/if}
              </div>
            </div>
          {/each}
        </div>
      </div>
    {/if}

    <!-- Command Gate -->
    <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-sm p-5 space-y-3">
      <div class="flex items-center justify-between">
        <h2 class="text-sm font-semibold text-black-900 dark:text-white-100">Command Gate</h2>
        <button
          onclick={doProbeGate}
          disabled={busy["probe-gate"]}
          class="rounded-lg border border-white-400 dark:border-navy-500 px-3 py-1.5 text-xs font-medium text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800 disabled:opacity-50"
        >{busy["probe-gate"] ? "Probing…" : "Probe Gate"}</button>
      </div>
      <div class="text-xs space-y-1">
        <div class="flex gap-2">
          <span class="w-24 shrink-0 text-black-700 dark:text-black-600">enabled</span>
          <span class="font-mono text-black-900 dark:text-white-100">{data.Gate.Enabled ? "yes" : "no"}</span>
        </div>
        {#if data.Gate.Binary}
          <div class="flex gap-2">
            <span class="w-24 shrink-0 text-black-700 dark:text-black-600">binary</span>
            <span class="font-mono text-black-900 dark:text-white-100 break-all">{data.Gate.Binary}</span>
          </div>
        {/if}
        {#if data.Gate.Source}
          <div class="flex gap-2">
            <span class="w-24 shrink-0 text-black-700 dark:text-black-600">source</span>
            <span class="font-mono text-black-900 dark:text-white-100">{data.Gate.Source}</span>
          </div>
        {/if}
        {#if data.Gate.PermissionMode}
          <div class="flex gap-2">
            <span class="w-24 shrink-0 text-black-700 dark:text-black-600">mode</span>
            <span class="font-mono text-black-900 dark:text-white-100">{data.Gate.PermissionMode}</span>
          </div>
        {/if}
        {#if data.Gate.Note}
          <div class="flex gap-2">
            <span class="w-24 shrink-0 text-black-700 dark:text-black-600">note</span>
            <span class="text-black-900 dark:text-white-100">{data.Gate.Note}</span>
          </div>
        {/if}
      </div>
    </div>

    <!-- Active processes -->
    {#if data.ActivePIDs.length > 0}
      <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-sm overflow-hidden">
        <div class="px-5 py-3 border-b border-white-300 dark:border-navy-600 flex items-center justify-between">
          <h2 class="text-sm font-semibold text-black-900 dark:text-white-100">Active Processes</h2>
          <span class="rounded bg-blue-100 dark:bg-blue-900 px-2 py-0.5 text-xs font-medium text-blue-700 dark:text-blue-300">{data.ActivePIDs.length}</span>
        </div>
        <table class="w-full text-xs">
          <thead>
            <tr class="border-b border-white-300 dark:border-navy-600 text-black-700 dark:text-black-600">
              <th class="px-5 py-2 text-left font-medium">Session</th>
              <th class="px-5 py-2 text-left font-medium">PID</th>
              <th class="px-5 py-2 text-left font-medium">State</th>
            </tr>
          </thead>
          <tbody>
            {#each data.ActivePIDs as p (p.SessionID)}
              <tr class="border-b border-white-300 dark:border-navy-600 last:border-0 hover:bg-white-200 dark:hover:bg-navy-800">
                <td class="px-5 py-2 font-mono text-black-900 dark:text-white-100">{p.SessionID.slice(0, 8)}</td>
                <td class="px-5 py-2 font-mono text-black-700 dark:text-black-600">{p.PID > 0 ? p.PID : "—"}</td>
                <td class="px-5 py-2 text-black-700 dark:text-black-600">{p.Lifecycle}{p.Substate ? `/${p.Substate}` : ""}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}

    <!-- Recent spawns -->
    <details class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-sm overflow-hidden group">
      <summary class="px-5 py-3 flex items-center gap-2 cursor-pointer list-none select-none hover:bg-white-200 dark:hover:bg-navy-800 transition-colors">
        <svg class="w-3.5 h-3.5 text-black-700 dark:text-black-600 transition-transform group-open:rotate-90 shrink-0" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" d="M9 5l7 7-7 7"/></svg>
        <h2 class="text-sm font-semibold text-black-900 dark:text-white-100">Recent Spawns</h2>
        {#if data.Spawns.length > 0}
          <span class="rounded bg-white-300 dark:bg-navy-600 px-2 py-0.5 text-xs font-medium text-black-700 dark:text-black-600">{data.Spawns.length}</span>
        {/if}
      </summary>
      {#if data.Spawns.length === 0}
        <div class="px-5 py-8 text-center text-sm text-black-700 dark:text-black-600">No spawns recorded yet.</div>
      {:else}
        <table class="w-full text-xs">
          <thead>
            <tr class="border-b border-white-300 dark:border-navy-600 text-black-700 dark:text-black-600">
              <th class="px-5 py-2.5 text-left">Started</th>
              <th class="px-5 py-2.5 text-left">Session</th>
              <th class="px-5 py-2.5 text-left">PID</th>
              <th class="px-5 py-2.5 text-left">Status</th>
              <th class="px-5 py-2.5 text-left">First Message</th>
            </tr>
          </thead>
          <tbody>
            {#each data.Spawns as s (s.Path)}
              <tr class="border-b border-white-300 dark:border-navy-600 last:border-0 hover:bg-white-200 dark:hover:bg-navy-800">
                <td class="px-5 py-2 font-mono text-black-700 dark:text-black-600 whitespace-nowrap">{new Date(s.StartedAt).toLocaleString()}</td>
                <td class="px-5 py-2 font-mono text-black-900 dark:text-white-100">{s.SessionID.slice(0, 8)}</td>
                <td class="px-5 py-2 font-mono text-black-700 dark:text-black-600">{s.PID > 0 ? s.PID : "—"}</td>
                <td class="px-5 py-2">
                  {#if s.ExitReason === ""}
                    <span class="rounded bg-green-100 dark:bg-green-900 px-1.5 py-0.5 text-xs text-green-700 dark:text-green-300">running</span>
                  {:else if s.ExitReason === "unclean"}
                    <span class="rounded bg-red-100 dark:bg-red-900 px-1.5 py-0.5 text-xs text-red-700 dark:text-red-300">unclean exit</span>
                  {:else}
                    <span class="rounded bg-white-300 dark:bg-navy-600 px-1.5 py-0.5 text-xs text-black-700 dark:text-black-600">{s.ExitReason}</span>
                  {/if}
                </td>
                <td class="px-5 py-2 text-black-700 dark:text-black-600 max-w-xs truncate">{s.FirstUserMessage}</td>
              </tr>
            {/each}
          </tbody>
        </table>
        {#if data.Page > 1 || data.HasNext}
          <div class="flex items-center justify-between border-t border-white-300 dark:border-navy-600 px-5 py-3">
            {#if data.Page > 1}
              <button onclick={() => load()} class="text-sm text-green-600 dark:text-green-400 hover:underline">← Prev</button>
            {:else}
              <span></span>
            {/if}
            <span class="text-xs text-black-700 dark:text-black-600">Page {data.Page}</span>
            {#if data.HasNext}
              <button onclick={() => load()} class="text-sm text-green-600 dark:text-green-400 hover:underline">Next →</button>
            {:else}
              <span></span>
            {/if}
          </div>
        {/if}
      {/if}
    </details>
  {/if}
</div>
