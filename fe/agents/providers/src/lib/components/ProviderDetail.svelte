<script lang="ts">
  import { onMount } from "svelte";
  import { ConfirmDialog } from "@wick-fe/common-ui";
  import { toastOk, toastError } from "@wick-fe/common-stores";
  import {
    apiGetProviderDetail,
    apiSaveProviderDetail,
    apiSaveConfigKey,
    apiHookCheck,
    apiHookEnable,
    apiHookDisable,
    apiDeleteProvider,
    apiProbeGate,
  } from "$lib/api.js";
  import type { ProviderDetailResponse, ConfigFieldDTO } from "$lib/types.js";

  type Props = {
    base: string;
    type: string;
    name: string;
    onBack: () => void;
  };
  let { base, type, name, onBack }: Props = $props();

  type KVRow = { key: string; value: string };

  let data = $state<ProviderDetailResponse | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let saving = $state(false);
  let confirmDelete = $state(false);
  let togglingDisabled = $state(false);
  let busy = $state<Record<string, boolean>>({});

  let fieldValues = $state<Record<string, string>>({});
  let secretTouched = $state<Record<string, boolean>>({});
  /* editorRows holds parsed rows for each kvlist field, keyed by field Key */
  let editorRows = $state<Record<string, KVRow[]>>({});

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

  function addRow(f: ConfigFieldDTO) {
    const current = editorRows[f.Key] ?? [];
    editorRows = { ...editorRows, [f.Key]: [...current, { key: "", value: "" }] };
  }

  function removeRow(f: ConfigFieldDTO, idx: number) {
    const current = editorRows[f.Key] ?? [];
    const next = current.filter((_, i) => i !== idx);
    editorRows = { ...editorRows, [f.Key]: next };
    void saveEditor(f);
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

  onMount(() => { load(); });

  let hookEvents = $derived(data ? Object.keys(data.Hooks) : []);
</script>

{#if confirmDelete}
  <ConfirmDialog
    message={`Delete provider ${type}/${name}? This cannot be undone.`}
    onConfirm={doDelete}
    onCancel={() => { confirmDelete = false; }}
  />
{/if}

<div class="space-y-4">
  <!-- Header -->
  <div class="flex items-center justify-between gap-3 flex-wrap">
    <div class="flex items-center gap-3 flex-wrap">
      <button
        onclick={onBack}
        class="text-xs text-black-700 dark:text-black-600 hover:underline"
      >← Providers</button>
      <span class="text-black-400 dark:text-black-600">/</span>
      <h1 class="text-lg font-semibold text-black-900 dark:text-white-100">{type}/{name}</h1>
      {#if data}
        {#if !data.PathFound}
          <span class="rounded bg-red-100 dark:bg-red-900 px-2 py-0.5 text-xs font-medium text-red-700 dark:text-red-300">not found</span>
        {:else}
          <span class="rounded bg-green-100 dark:bg-green-900 px-2 py-0.5 text-xs font-medium text-green-700 dark:text-green-300">{data.Version}</span>
        {/if}
        {#if data.ActiveCount > 0}
          <span class="rounded bg-blue-100 dark:bg-blue-900 px-2 py-0.5 text-xs font-medium text-blue-700 dark:text-blue-300">{data.ActiveCount} active</span>
        {/if}
      {/if}
    </div>
    {#if data}
      <div class="flex items-center gap-2">
        {#if data.Instance.Disabled}
          <button
            onclick={toggleDisabled}
            disabled={togglingDisabled}
            class="flex items-center gap-2 rounded-lg border border-amber-400 dark:border-amber-700 px-3 py-1.5 text-xs font-medium text-amber-700 dark:text-amber-400 hover:bg-amber-50 dark:hover:bg-amber-900/20 disabled:opacity-50"
          >
            <span class="inline-block w-2 h-2 rounded-full bg-amber-500"></span>
            Disabled — click to enable
          </button>
        {:else}
          <button
            onclick={toggleDisabled}
            disabled={togglingDisabled}
            class="flex items-center gap-2 rounded-lg border border-white-400 dark:border-navy-600 px-3 py-1.5 text-xs text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800 disabled:opacity-50"
          >
            <span class="inline-block w-2 h-2 rounded-full bg-green-500"></span>
            Enabled — click to disable
          </button>
        {/if}
        <button
          onclick={() => { confirmDelete = true; }}
          disabled={busy["delete"]}
          class="rounded-lg border border-red-300 dark:border-red-700 px-3 py-1.5 text-xs font-medium text-red-700 dark:text-red-400 hover:bg-red-50 dark:hover:bg-red-900/20 disabled:opacity-50"
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
      <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-sm overflow-hidden">
        <div class="px-5 py-3 border-b border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800">
          <div class="flex items-center gap-2">
            <span class="font-mono text-sm font-semibold text-black-900 dark:text-white-100">{f.Key}</span>
          </div>
          {#if f.Description}
            <p class="mt-0.5 text-xs text-black-700 dark:text-black-600 whitespace-pre-line">{f.Description}</p>
          {/if}
        </div>
        <div class="p-5">
          <div class="rounded-lg border border-white-300 dark:border-navy-600 overflow-hidden mb-3">
            <div class="flex border-b border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800">
              <div class="flex-1 min-w-0 px-3 py-2 text-[11px] font-semibold uppercase tracking-wide text-black-700 dark:text-black-600">Value</div>
              <div class="w-10 shrink-0"></div>
            </div>
            {#if (editorRows[f.Key] ?? []).length === 0}
              <div class="px-4 py-5 text-center text-xs text-black-700 dark:text-black-600">No rows yet — click <strong>+ Add Row</strong> to start</div>
            {:else}
              {#each editorRows[f.Key] as row, idx (idx)}
                <div class="flex border-b border-white-300 dark:border-navy-600 last:border-b-0 hover:bg-white-200 dark:hover:bg-navy-800 transition-colors">
                  <div class="flex-1 min-w-0 px-3 py-2">
                    <input
                      type="text"
                      bind:value={row.value}
                      onblur={() => saveEditor(f)}
                      placeholder="value"
                      class="w-full bg-transparent border-none outline-none text-xs font-mono text-black-900 dark:text-white-100 placeholder:text-black-700 dark:placeholder:text-black-600"
                    />
                  </div>
                  <div class="w-10 shrink-0 flex items-center justify-center">
                    <button
                      type="button"
                      onclick={() => removeRow(f, idx)}
                      aria-label="Remove row"
                      class="text-black-700 dark:text-black-600 hover:text-red-500 text-base leading-none transition-colors"
                    >✕</button>
                  </div>
                </div>
              {/each}
            {/if}
          </div>
          <button
            type="button"
            onclick={() => addRow(f)}
            class="w-full rounded-lg border border-dashed border-white-400 dark:border-navy-600 px-3 py-2 text-xs font-medium text-black-700 dark:text-black-600 hover:border-green-500 hover:text-green-500 transition-colors"
          >+ Add Row</button>
        </div>
      </div>
    {/each}

    <!-- Key-value editors (multi-column kvlist, e.g. env) -->
    {#each keyValueFields as f (f.Key)}
      {@const cols = kvCols(f)}
      <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-sm overflow-hidden">
        <div class="px-5 py-3 border-b border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800">
          <div class="flex items-center gap-2">
            <span class="font-mono text-sm font-semibold text-black-900 dark:text-white-100">{f.Key}</span>
            <span class="text-[11px] text-black-700 dark:text-black-600 font-normal">{cols.join(" · ")}</span>
          </div>
          {#if f.Description}
            <p class="mt-0.5 text-xs text-black-700 dark:text-black-600 whitespace-pre-line">{f.Description}</p>
          {/if}
        </div>
        <div class="p-5">
          <div class="rounded-lg border border-white-300 dark:border-navy-600 overflow-hidden mb-3">
            <div class="flex border-b border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800">
              {#each cols as col (col)}
                <div class="flex-1 min-w-0 px-3 py-2 text-[11px] font-semibold uppercase tracking-wide text-black-700 dark:text-black-600 border-r border-white-300 dark:border-navy-600 last:border-r-0 capitalize">{col}</div>
              {/each}
              <div class="w-10 shrink-0"></div>
            </div>
            {#if (editorRows[f.Key] ?? []).length === 0}
              <div class="px-4 py-5 text-center text-xs text-black-700 dark:text-black-600">No rows yet — click <strong>+ Add Row</strong> to start</div>
            {:else}
              {#each editorRows[f.Key] as row, idx (idx)}
                <div class="flex border-b border-white-300 dark:border-navy-600 last:border-b-0 hover:bg-white-200 dark:hover:bg-navy-800 transition-colors">
                  <div class="flex-1 min-w-0 px-3 py-2 border-r border-white-300 dark:border-navy-600">
                    <input
                      type="text"
                      bind:value={row.key}
                      onblur={() => saveEditor(f)}
                      placeholder="key"
                      class="w-full bg-transparent border-none outline-none text-xs font-mono text-black-900 dark:text-white-100 placeholder:text-black-700 dark:placeholder:text-black-600"
                    />
                  </div>
                  <div class="flex-1 min-w-0 px-3 py-2">
                    <input
                      type="text"
                      bind:value={row.value}
                      onblur={() => saveEditor(f)}
                      placeholder="value"
                      class="w-full bg-transparent border-none outline-none text-xs font-mono text-black-900 dark:text-white-100 placeholder:text-black-700 dark:placeholder:text-black-600"
                    />
                  </div>
                  <div class="w-10 shrink-0 flex items-center justify-center">
                    <button
                      type="button"
                      onclick={() => removeRow(f, idx)}
                      aria-label="Remove row"
                      class="text-black-700 dark:text-black-600 hover:text-red-500 text-base leading-none transition-colors"
                    >✕</button>
                  </div>
                </div>
              {/each}
            {/if}
          </div>
          <button
            type="button"
            onclick={() => addRow(f)}
            class="w-full rounded-lg border border-dashed border-white-400 dark:border-navy-600 px-3 py-2 text-xs font-medium text-black-700 dark:text-black-600 hover:border-green-500 hover:text-green-500 transition-colors"
          >+ Add Row</button>
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
