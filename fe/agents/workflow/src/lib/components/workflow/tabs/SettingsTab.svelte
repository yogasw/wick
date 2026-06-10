<script lang="ts">
  import { workflowAPI, type EnvField } from "$lib/api/workflow";
  import Select from "$lib/components/shared/Select.svelte";

  type Props = { workflowID: string };
  let { workflowID }: Props = $props();

  // key: env var name (A-Z 0-9 _ only)
  // value: plaintext to save. Empty + stored=true = keep existing encrypted value.
  // secret: encrypt on save
  // stored: backend already has an encrypted value (shows "Type to replace" hint)
  type KVRow = { key: string; value: string; secret: boolean; stored: boolean };

  let rows = $state<KVRow[]>([]);
  let loading = $state(true);
  let saving = $state(false);
  let error = $state("");
  let saved = $state(false);
  let saveTimer: ReturnType<typeof setTimeout> | null = null;

  let schema = $state<EnvField[]>([]);
  let schemaValues = $state<Record<string, string>>({});

  const inputClass = "w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2.5 text-sm font-mono text-black-900 dark:text-white-100 placeholder:text-black-700 dark:placeholder:text-black-600 outline-none focus:border-green-500 focus:ring-2 focus:ring-green-200 dark:focus:ring-green-800 transition-colors";

  $effect(() => { if (workflowID) void load(); });

  async function load() {
    loading = true; error = "";
    try {
      const res = await workflowAPI.envGet(workflowID);
      schema = res.schema ?? [];
      const all = res.values ?? {};
      const schemaKeys = new Set(schema.map((f) => f.name));
      const sv: Record<string, string> = {};
      for (const f of schema) sv[f.name] = all[f.name] ?? f.default ?? "";
      schemaValues = sv;
      const secretSet = new Set(res.secret_keys ?? []);
      const free: KVRow[] = [];
      for (const [k, v] of Object.entries(all)) {
        if (schemaKeys.has(k)) continue;
        const isSecret = secretSet.has(k);
        free.push({ key: k, value: isSecret ? "" : v, secret: isSecret, stored: isSecret });
      }
      rows = free;
    } catch (e: any) {
      error = e?.message ?? "Failed to load";
    } finally {
      loading = false;
    }
  }

  function scheduleSave() {
    saved = false;
    if (saveTimer) clearTimeout(saveTimer);
    saveTimer = setTimeout(() => void save(), 800);
  }

  function setRow(i: number, patch: Partial<KVRow>) {
    rows = rows.map((r, idx) => idx === i ? { ...r, ...patch } : r);
    scheduleSave();
  }

  function normaliseKey(raw: string): string {
    // uppercase + strip invalid chars (only A-Z 0-9 _ allowed)
    return raw.toUpperCase().replace(/[^A-Z0-9_]/g, "");
  }

  function isDuplicateKey(key: string, selfIdx: number): boolean {
    if (!key) return false;
    return rows.some((r, idx) => idx !== selfIdx && r.key === key);
  }

  function addRow() {
    rows = [...rows, { key: "", value: "", secret: false, stored: false }];
  }

  function removeRow(i: number) {
    rows = rows.filter((_, idx) => idx !== i);
    scheduleSave();
  }

  function onTabKey(e: KeyboardEvent, i: number) {
    if (e.key === "Tab" && !e.shiftKey && i === rows.length - 1) {
      e.preventDefault();
      addRow();
      setTimeout(() => {
        const inputs = document.querySelectorAll<HTMLInputElement>(".kv-key");
        inputs[inputs.length - 1]?.focus();
      }, 0);
    }
  }

  async function save() {
    // Block save on duplicate keys.
    const hasDupe = rows.some((r, i) => r.key && isDuplicateKey(r.key, i));
    if (hasDupe) { error = "Duplicate keys — fix before saving."; return; }
    saving = true; error = "";
    try {
      const merged: Record<string, string> = { ...schemaValues };
      const secretKeys: string[] = [];
      for (const row of rows) {
        if (!row.key.trim()) continue;
        // stored=true + no new value typed → keep existing encrypted value in DB (omit from payload).
        if (row.stored && row.value === "") continue;
        merged[row.key.trim()] = row.value;
        if (row.secret) secretKeys.push(row.key.trim());
      }
      await workflowAPI.envSave(workflowID, merged, secretKeys);
      saved = true;
      setTimeout(() => (saved = false), 2000);
    } catch (e: any) {
      error = e?.message ?? "Save failed";
    } finally {
      saving = false;
    }
  }
</script>

<div class="px-5 py-5 flex flex-col gap-4">

    {#if loading}
      <p class="text-xs text-black-700 dark:text-black-600">Loading…</p>

    {:else}

      {#if error}
        <div class="px-4 py-3 rounded-lg bg-neg-100 dark:bg-neg-900/20 text-neg-600 dark:text-neg-400 text-xs">{error}</div>
      {/if}

      <!-- Schema fields (from env: block) -->
      {#if schema.length > 0}
        <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-sm overflow-hidden">
          <div class="px-5 py-3 border-b border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800 flex items-center justify-between">
            <h3 class="text-sm font-semibold text-black-900 dark:text-white-100">Configuration</h3>
            <span class="text-[11px] font-medium transition-colors"
              class:text-green-500={saved}
              class:text-black-700={saving}
              class:dark:text-black-600={!saved}
            >{#if saving}Saving…{:else if saved}✓ Saved{:else}&nbsp;{/if}</span>
          </div>
          <div class="p-5">
            <div class="grid grid-cols-1 sm:grid-cols-2 gap-x-6 gap-y-5">
              {#each schema as f (f.name)}
                {@const w = f.widget ?? "text"}
                {@const val = schemaValues[f.name] ?? ""}
                {@const isSecret = w === "secret"}
                {@const isStored = isSecret && val === "••••••••"}
                {@const colSpan = w === "textarea" ? "sm:col-span-2" : ""}

                <div class={colSpan}>
                  <div class="flex items-center gap-2 mb-1.5">
                    <span class="font-mono text-xs font-semibold text-black-900 dark:text-white-100">{f.name}</span>
                    {#if f.required && !val}
                      <span class="rounded bg-neg-100 px-1.5 py-0.5 text-[10px] font-semibold text-neg-400">missing</span>
                    {:else if f.required}
                      <span class="text-[10px] font-semibold text-neg-400">*</span>
                    {/if}
                    {#if isStored}
                      <span class="rounded-full bg-pos-100 px-1.5 py-0.5 text-[10px] font-semibold text-pos-400">stored</span>
                    {/if}
                  </div>

                  {#if w === "textarea"}
                    <textarea rows="4" class={inputClass}
                      value={val}
                      oninput={(e) => { schemaValues = { ...schemaValues, [f.name]: (e.target as HTMLTextAreaElement).value }; scheduleSave(); }}
                    ></textarea>
                  {:else if w === "checkbox" || w === "bool" || w === "boolean"}
                    <label class="inline-flex items-center gap-3 cursor-pointer mt-1 select-none">
                      <input type="checkbox" class="sr-only cfg-toggle-input"
                        checked={val === "true" || val === "1"}
                        onchange={(e) => { schemaValues = { ...schemaValues, [f.name]: (e.target as HTMLInputElement).checked ? "true" : "false" }; scheduleSave(); }}
                      />
                      <span class="toggle-track {val === 'true' || val === '1' ? 'is-on' : ''}">
                        <span class="toggle-knob"></span>
                      </span>
                      <span class="text-xs font-medium text-black-800 dark:text-black-600 min-w-7">
                        {val === "true" || val === "1" ? "On" : "Off"}
                      </span>
                    </label>
                  {:else if w === "dropdown"}
                    <Select
                      value={val}
                      options={(f.options ?? []).map(o => ({ label: o.name || o.id, value: o.id }))}
                      placeholder="— select —"
                      onChange={(v) => { schemaValues = { ...schemaValues, [f.name]: v }; scheduleSave(); }}
                    />
                  {:else if w === "secret"}
                    <input type="password" autocomplete="new-password"
                      class={inputClass}
                      placeholder={isStored ? "Type new value to replace" : "Enter secret"}
                      value=""
                      oninput={(e) => { schemaValues = { ...schemaValues, [f.name]: (e.target as HTMLInputElement).value }; scheduleSave(); }}
                    />
                  {:else}
                    <input
                      type={w === "number" ? "number" : w === "email" ? "email" : w === "url" ? "url" : "text"}
                      class={inputClass}
                      value={val}
                      oninput={(e) => { schemaValues = { ...schemaValues, [f.name]: (e.target as HTMLInputElement).value }; scheduleSave(); }}
                    />
                  {/if}

                  {#if f.desc && w !== "checkbox" && w !== "bool" && w !== "boolean"}
                    <p class="mt-1.5 text-[11px] text-black-700 dark:text-black-600 leading-relaxed">{f.desc}</p>
                  {/if}
                </div>
              {/each}
            </div>
          </div>
        </div>
      {/if}

      <!-- Free-form env vars -->
      <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-sm overflow-hidden">
        <div class="px-5 py-3 border-b border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800">
          <div class="flex items-center gap-2">
            <span class="font-mono text-sm font-semibold text-black-900 dark:text-white-100">env</span>
            <span class="text-[11px] text-black-700 dark:text-black-600">key · value</span>
            <span class="ml-auto text-[11px] font-medium transition-colors"
              class:text-green-500={saved}
              class:text-black-700={saving}
              class:dark:text-black-600={!saved && !saving}
            >{#if saving}Saving…{:else if saved}✓ Saved{:else}&nbsp;{/if}</span>
          </div>
          <p class="mt-0.5 text-xs text-black-700 dark:text-black-600">
            Available as <code class="font-mono text-[11px]">{"{{.Env.KEY}}"}</code> in nodes.
            Mark as secret to encrypt the value.
          </p>
        </div>

        <div class="p-5">
          <div class="rounded-lg border border-white-300 dark:border-navy-600 overflow-hidden mb-3">
            <!-- Header -->
            <div class="flex border-b border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800">
              <div class="flex-1 min-w-0 px-3 py-2 text-[11px] font-semibold uppercase tracking-wide text-black-700 dark:text-black-600 border-r border-white-300 dark:border-navy-600">Key</div>
              <div class="flex-1 min-w-0 px-3 py-2 text-[11px] font-semibold uppercase tracking-wide text-black-700 dark:text-black-600 border-r border-white-300 dark:border-navy-600">Value</div>
              <div class="w-20 shrink-0 px-3 py-2 text-[11px] font-semibold uppercase tracking-wide text-black-700 dark:text-black-600 border-r border-white-300 dark:border-navy-600 text-center">Secret</div>
              <div class="w-10 shrink-0"></div>
            </div>

            {#if rows.length === 0}
              <div class="px-4 py-5 text-center text-xs text-black-700 dark:text-black-600">
                No variables yet — click <strong>+ Add Row</strong> to start
              </div>
            {/if}
            {#each rows as row, i (i)}
              <div class="flex border-b border-white-300 dark:border-navy-600 last:border-b-0 hover:bg-white-200 dark:hover:bg-navy-800 transition-colors group">
                <!-- Key -->
                <div class="flex-1 min-w-0 px-3 py-2 border-r border-white-300 dark:border-navy-600">
                  <input
                    class="kv-key w-full bg-transparent outline-none text-xs font-mono placeholder:text-black-700 dark:placeholder:text-black-600"
                    class:text-neg-400={isDuplicateKey(row.key, i)}
                    class:text-black-900={!isDuplicateKey(row.key, i)}
                    class:dark:text-white-100={!isDuplicateKey(row.key, i)}
                    placeholder="KEY_NAME"
                    value={row.key}
                    oninput={(e) => {
                      const el = e.target as HTMLInputElement;
                      const norm = normaliseKey(el.value);
                      el.value = norm;
                      setRow(i, { key: norm });
                    }}
                  />
                  {#if isDuplicateKey(row.key, i)}
                    <span class="text-[10px] text-neg-400">duplicate</span>
                  {/if}
                </div>
                <!-- Value -->
                <div class="flex-1 min-w-0 px-3 py-0 border-r border-white-300 dark:border-navy-600 flex items-center gap-2">
                  <input
                    type={row.secret ? "password" : "text"}
                    class="flex-1 min-w-0 bg-transparent outline-none text-xs font-mono text-black-900 dark:text-white-100 placeholder:text-black-700 dark:placeholder:text-black-600 py-2"
                    placeholder={row.stored ? "Type new value to replace" : row.secret ? "Enter secret…" : "value"}
                    value={row.value}
                    oninput={(e) => setRow(i, { value: (e.target as HTMLInputElement).value, stored: false })}
                    onkeydown={(e) => onTabKey(e, i)}
                  />
                  {#if row.stored}
                    <span class="shrink-0 text-[10px] font-medium text-pos-400 py-2">set</span>
                  {/if}
                </div>
                <!-- Secret toggle -->
                <div class="w-20 shrink-0 border-r border-white-300 dark:border-navy-600 flex items-center justify-center">
                  <label class="flex items-center cursor-pointer select-none">
                    <input type="checkbox"
                      class="sr-only"
                      checked={row.secret}
                      onchange={(e) => setRow(i, { secret: (e.target as HTMLInputElement).checked })}
                    />
                    <span class="toggle-track {row.secret ? 'is-on' : ''} scale-75">
                      <span class="toggle-knob"></span>
                    </span>
                  </label>
                </div>
                <!-- Remove -->
                <div class="w-10 shrink-0 flex items-center justify-center">
                  <button type="button"
                    class="opacity-0 group-hover:opacity-100 flex items-center justify-center w-6 h-6 rounded text-black-700 dark:text-black-600 hover:text-neg-400 dark:hover:text-neg-400 hover:bg-white-300 dark:hover:bg-navy-600 transition-all"
                    onclick={() => removeRow(i)}
                    aria-label="Remove"
                  >
                    <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
                  </button>
                </div>
              </div>
            {/each}
          </div>

          <button type="button"
            class="w-full rounded-lg border border-dashed border-white-400 dark:border-navy-600 px-3 py-2 text-xs font-medium text-black-700 dark:text-black-600 hover:border-green-500 hover:text-green-500 transition-colors"
            onclick={addRow}
          >+ Add Row</button>
        </div>
      </div>

    {/if}
</div>
