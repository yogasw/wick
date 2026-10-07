<script lang="ts">
  /* The Access checklist: which of the owner's connectors an agent may use,
     at what level, on which accounts — plus whose access a turn runs with.
     ONE component for the + Agent wizard and Settings › Access; only how
     the parent saves differs. A mode on top picks "Same as me" (every
     connector the owner has) or "Choose connectors" (this list). Entries
     split into three type chips by tier: Connectors (granted by hand),
     Platform (on for every agent) and System (on for the Captain); a tier
     row stores a grant only when overridden. Each list is one table: row
     checkboxes only select, the row's level control changes it directly,
     and a bulk bar shows up once something is selected. The arithmetic
     lives in agentForm.ts / accessTiers.ts / accessList.ts. */
  import { Toggle } from "@wick-fe/common-ui";
  import type { ConnectorGrant, AgentConnector } from "../api/team.js";
  import { opStats, writeConnectors, type GrantErrors } from "../agentForm.js";
  import {
    tierOf, tierDefault, overrideOf, setOverride, tickedAccounts, toggleAccount,
    type Tier, type Override,
  } from "../accessTiers.js";
  import {
    filterRows, selectState, toggleShown, toggleOne, bulkApply, setRowLevel,
    type AccessMode, type BulkAction,
  } from "../accessList.js";

  type RunAs = "caller" | "owner";
  type Props = {
    catalog: AgentConnector[];
    loading?: boolean;
    loadError?: string;
    grants: ConnectorGrant[];
    includeNew?: boolean;
    runAs?: RunAs;
    /** "owner" = Same as me; the grants are kept for switching back. */
    accessMode?: AccessMode;
    /** Last 400 from the server, already mapped onto rows. */
    errors?: GrantErrors | null;
    /** The wizard hides these: a new agent starts as caller, include-new off. */
    showRunAs?: boolean;
    showIncludeNew?: boolean;
    /** The Captain's System rows default to Write; everyone else's to Off. */
    isCaptain?: boolean;
  };
  let {
    catalog,
    loading = false,
    loadError = "",
    grants = $bindable(),
    includeNew = $bindable(false),
    runAs = $bindable("caller"),
    accessMode = $bindable("choose"),
    errors = null,
    showRunAs = true,
    showIncludeNew = true,
    isCaptain = false,
  }: Props = $props();

  let tier = $state<Tier>("connectors");
  let query = $state("");
  let selected = $state<Set<string>>(new Set());
  let expanded = $state<Record<string, boolean>>({});
  // The Add picker (Connectors only): its own search and selection.
  let adding = $state(false);
  let addQuery = $state("");
  let picked = $state<Set<string>>(new Set());

  const grantOf = (id: string) => grants.find((g) => g.connector_id === id);
  const inTier = (t: Tier) => catalog.filter((c) => tierOf(c) === t);
  const tiers = $derived(
    (["connectors", "platform", "system"] as Tier[])
      .map((t) => ({ t, items: inTier(t) }))
      .filter((x) => x.t !== "system" || x.items.length > 0)
      .map((x) => ({ ...x, custom: x.items.filter((c) => grantOf(c.id)).length })),
  );
  const matches = (c: AgentConnector, query: string) => {
    const q = query.trim().toLowerCase();
    return !q || c.label.toLowerCase().includes(q) || c.key.toLowerCase().includes(q);
  };
  const tierRows = $derived(inTier(tier));
  // Connectors: the main list holds only what is added, the picker only
  // what is not. Platform / System rows are all listed.
  const added = $derived(tier === "connectors" ? filterRows(tierRows, grants, "granted", isCaptain) : tierRows);
  const available = $derived(tier === "connectors" ? filterRows(tierRows, grants, "not_granted", isCaptain) : []);
  const shown = $derived(added.filter((c) => matches(c, query)));
  const availShown = $derived(available.filter((c) => matches(c, addQuery)));
  const pickSel = $derived(selectState(picked, availShown));
  const pickCount = $derived(available.filter((c) => picked.has(c.id)).length);
  const sel = $derived(selectState(selected, shown));
  // Selected rows of this tier, hidden ones included: the bulk bar acts on them.
  const selCount = $derived(added.filter((c) => selected.has(c.id)).length);
  const sameAsMe = $derived(tier === "connectors" && accessMode === "owner");

  // Write ops the Connectors list grants, by their real op keys.
  const writes = $derived.by(() => {
    const out: string[] = [];
    for (const g of grants) {
      const c = catalog.find((x) => x.id === g.connector_id);
      if (!c || tierOf(c) !== "connectors") continue;
      for (const op of c.ops ?? []) {
        if (op.destructive && (g.level === "all" || (g.level === "pick" && g.ops.includes(op.key)))) out.push(`${c.label} · ${op.key}`);
      }
    }
    return out;
  });
  const writeConns = $derived(writeConnectors(grants, catalog));
  const tierLabel: Record<Tier, string> = { connectors: "Connectors", platform: "Platform", system: "System" };
  const tierHint: Record<Tier, string> = {
    connectors: "Connectors this agent can use.",
    platform: "wick's own working tools. On for every agent unless you change a row.",
    system: "Tools that build and manage wick. On for the Captain only unless you change a row.",
  };
  const levelName: Record<string, string> = { all: "Write", read: "Read", off: "Off", pick: "Picked ops" };
  // Plain words for the levels, shown under the list and as tooltips.
  const levelHelp: Record<string, string> = {
    read: "Read: can look things up",
    all: "Write: can also change things",
    pick: "Pick ops: only the operations you tick",
  };

  function pickTier(t: Tier) {
    tier = t;
    query = "";
    selected = new Set();
    closePicker();
  }
  function openPicker() {
    adding = true;
    addQuery = "";
    picked = new Set();
  }
  function closePicker() {
    adding = false;
    picked = new Set();
  }
  /** addPicked grants the picked connectors at one level; they move from
      the picker to the main list. */
  function addPicked(level: "read" | "all") {
    grants = bulkApply(grants, available, picked, level);
    closePicker();
  }
  function setOv(c: AgentConnector, o: Override) {
    grants = o === "default" ? setOverride(grants, c.id, o) : setRowLevel(grants, c, o);
    if (o === "pick") expanded[c.id] = true;
  }
  function bulk(a: BulkAction) {
    grants = bulkApply(grants, tierRows, selected, a);
  }
  function tickOp(c: AgentConnector, key: string, on: boolean) {
    const g = grantOf(c.id);
    if (!g) return;
    const ops = on ? (g.ops.includes(key) ? g.ops : [...g.ops, key]) : g.ops.filter((x) => x !== key);
    grants = grants.map((x) => (x.connector_id === c.id ? { ...x, ops } : x));
  }
  const accIds = (c: AgentConnector) => (c.accounts ?? []).map((a) => a.id);
  function indeterminate(node: HTMLInputElement, v: boolean) {
    node.indeterminate = v;
    return { update: (n: boolean) => (node.indeterminate = n) };
  }
  /** The level choices a row offers: tier rows add their default, wick's
      own tools only know on and off. */
  function levelChoices(c: AgentConnector): [Override, string][] {
    if (tierOf(c) === "connectors") return [["read", "Read"], ["all", "Write"], ["pick", "Pick ops"]];
    const def = tierDefault(c, isCaptain);
    if (c.tool) return [["default", `Default (${def === "all" ? "On" : "Off"})`], ["off", "Off"], ["all", "On"]];
    return [["default", `Default (${levelName[def]})`], ["off", "Off"], ["read", "Read"], ["all", "Write"], ["pick", "Pick ops"]];
  }

  const btn =
    "whitespace-nowrap rounded-lg border border-white-300 px-3 py-1.5 text-xs font-medium text-black-900 hover:bg-white-200 disabled:opacity-50 dark:border-navy-600 dark:text-white-100 dark:hover:bg-navy-600";
  const btnPrimary =
    "whitespace-nowrap rounded-lg border border-green-500 bg-green-500 px-3 py-1.5 text-xs font-medium text-white-100 hover:bg-green-600 disabled:opacity-50";
  const chip = "inline-flex cursor-pointer select-none items-center gap-1 rounded-full border px-3 py-1 text-xs";
  const chipOn = "border-green-500 bg-green-50 text-black-900 dark:bg-green-900/30 dark:text-white-100";
  const chipOff = "border-white-300 text-black-800 dark:border-navy-600 dark:text-black-600";
  const seg = "px-2 py-1 text-xs first:rounded-l-lg last:rounded-r-lg border-y border-l last:border-r border-white-300 dark:border-navy-600";
  const modeSeg = "px-3 py-1.5 text-xs font-medium first:rounded-l-lg last:rounded-r-lg border-y border-l last:border-r border-white-300 dark:border-navy-600";
  const segOn = "bg-green-500 text-white-100 border-green-500";
  const segOff = "text-black-800 hover:bg-white-200 dark:text-black-600 dark:hover:bg-navy-600";
</script>

{#if showRunAs}
  <div>
    <label class="mb-1 block text-xs font-medium text-black-800 dark:text-black-600" for="cc-runas">Run as</label>
    <select
      id="cc-runas"
      class="w-full rounded-lg border border-white-300 bg-white-100 px-3 py-2 text-sm text-black-900 focus:border-green-500 focus:outline-none dark:border-navy-600 dark:bg-navy-800 dark:text-white-100"
      bind:value={runAs}
    >
      <option value="caller">Caller (default) — your access, limited to this list</option>
      <option value="owner">Owner — always your access, limited to this list</option>
    </select>
    <p class="mt-1 text-xs text-black-800 dark:text-black-600">Bot, schedule and cron triggers use the owner's access.</p>
    {#if errors?.runAs}<p class="mt-1 text-xs text-neg-400">{errors.runAs}</p>{/if}
  </div>
{/if}

<div>
  <p class="mb-1 text-xs font-medium text-black-800 dark:text-black-600" id="cc-mode-label">Connector access</p>
  <div class="inline-flex" role="radiogroup" aria-labelledby="cc-mode-label">
    {#each [["owner", "Same as me"], ["choose", "Choose connectors"]] as [m, name] (m)}
      <button type="button" role="radio" aria-checked={accessMode === m} class="{modeSeg} {accessMode === m ? segOn : segOff}"
        onclick={() => (accessMode = m as AccessMode)}>{name}</button>
    {/each}
  </div>
  <p class="mt-1 text-xs text-black-800 dark:text-black-600">
    {accessMode === "owner"
      ? "Every connector you can use, at your level, new ones included."
      : "Only the connectors you add below."}
  </p>
</div>

<div class="flex flex-wrap items-center gap-2" role="tablist" aria-label="Access type">
  {#each tiers as x (x.t)}
    <button
      type="button"
      role="tab"
      aria-selected={tier === x.t}
      class="{chip} {tier === x.t ? chipOn : chipOff}"
      onclick={() => pickTier(x.t)}
    >
      {tierLabel[x.t]} <span class="text-black-700">· {x.items.length}</span>
      {#if x.t === "connectors" && accessMode === "owner"}<span class="rounded-full bg-white-200 px-1.5 text-[10px] dark:bg-navy-600">same as you</span>
      {:else if x.custom > 0}<span class="rounded-full bg-white-200 px-1.5 text-[10px] dark:bg-navy-600">{x.custom} {x.t === "connectors" ? "granted" : "changed"}</span>{/if}
    </button>
  {/each}
</div>

{#if loading}
  <p class="text-sm text-black-800 dark:text-black-600">Loading connectors…</p>
{:else if loadError}
  <p class="text-sm text-neg-400">{loadError}</p>
{/if}

{#if errors && errors.missing.length > 0}
  <p class="text-xs text-neg-400">No longer accessible to you: {errors.missing.join(", ")} — remove them and save again.</p>
{/if}

{#snippet opsPicker(c: AgentConnector)}
  {@const g = grantOf(c.id)}
  {#if g && g.level === "pick"}
    <div class="mt-2 flex flex-wrap gap-2 pl-[4.25rem]">
      {#each c.ops ?? [] as op (op.key)}
        {@const on = g.ops.includes(op.key)}
        <label class="{chip} {on ? chipOn : chipOff}" title={op.name || op.key}>
          <input type="checkbox" class="sr-only" checked={on} onchange={(e) => tickOp(c, op.key, (e.currentTarget as HTMLInputElement).checked)} /><span class="font-mono text-[11px]">{op.key}</span>
          {#if op.destructive}<span class="text-cau-400">write</span>{/if}
        </label>
      {/each}
      {#if (c.ops ?? []).length === 0}<span class="text-xs text-black-800 dark:text-black-600">No enabled operations.</span>{/if}
    </div>
  {/if}
{/snippet}

{#snippet icon(c: AgentConnector)}
  <span class="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-white-200 text-sm font-semibold text-black-800 dark:bg-navy-600 dark:text-black-600" aria-hidden="true">{(c.label || c.key).slice(0, 1).toUpperCase()}</span>
{/snippet}

{#if sameAsMe}
  <div class="rounded-xl border border-green-300 bg-green-50 px-4 py-3 text-xs text-black-900 dark:border-green-700 dark:bg-green-900/10 dark:text-white-100" data-testid="same-as-me">
    <p class="font-medium">This agent uses the same connectors you do ({tierRows.length}), with write access.</p>
    <p class="mt-1 text-black-800 dark:text-black-600">Connectors you get later are included. Platform and System tools still follow their own tabs. Pick “Choose connectors” to limit it.</p>
  </div>
{:else if adding}
  <section class="overflow-hidden rounded-xl border border-green-300 dark:border-green-700" aria-label="Add connectors">
    <div class="border-b border-white-300 px-3 py-2 dark:border-navy-600">
      <p class="text-sm font-medium text-black-900 dark:text-white-100">Available <span class="text-black-700">· {available.length}</span></p>
      <p class="text-xs text-black-800 dark:text-black-600">Pick connectors to give this agent.</p>
    </div>
    {#if available.length === 0}
      <p class="px-3 py-4 text-xs text-black-800 dark:text-black-600">Every connector is already added.</p>
    {:else}
      <div class="px-3 pt-2">
        <input type="search" aria-label="Search available connectors" class="w-full rounded-lg border border-white-300 bg-white-100 px-3 py-2 text-sm text-black-900 focus:border-green-500 focus:outline-none dark:border-navy-600 dark:bg-navy-800 dark:text-white-100" bind:value={addQuery} placeholder="Search connectors…" />
      </div>
      <div class="flex items-center gap-3 px-3 py-2 text-xs text-black-800 dark:text-black-600">
        <input type="checkbox" class="h-4 w-4 shrink-0 accent-green-500" id="cc-pick-all" checked={pickSel === "all"} disabled={availShown.length === 0}
          use:indeterminate={pickSel === "some"} onchange={() => (picked = toggleShown(picked, availShown))} aria-label="Select all available shown" />
        <label for="cc-pick-all">Select all shown ({availShown.length})</label>
      </div>
      <ul class="max-h-80 divide-y divide-white-300 overflow-y-auto border-y border-white-300 dark:divide-navy-600 dark:border-navy-600">
        {#each availShown as c (c.id)}
          {@const st = opStats(c)}
          <li class="flex items-center gap-3 px-3 py-2 {picked.has(c.id) ? 'bg-green-50 dark:bg-green-900/10' : ''}">
            <input type="checkbox" class="h-4 w-4 shrink-0 accent-green-500" id="pick-{c.id}" checked={picked.has(c.id)}
              onchange={(e) => (picked = toggleOne(picked, c.id, (e.currentTarget as HTMLInputElement).checked))} aria-label="Pick {c.label}" />
            {@render icon(c)}
            <label for="pick-{c.id}" class="min-w-0 flex-1">
              <span class="block truncate text-sm text-black-900 dark:text-white-100">{c.label}</span>
              <span class="block truncate text-xs text-black-800 dark:text-black-600">{st.total} ops · {st.write > 0 ? `${st.write} can change things` : "look-up only"}</span>
            </label>
          </li>
        {/each}
        {#if availShown.length === 0}<li class="px-3 py-3 text-xs text-black-800 dark:text-black-600">Nothing matches.</li>{/if}
      </ul>
    {/if}
    <div class="flex flex-wrap items-center gap-2 px-3 py-2">
      <button type="button" class={btnPrimary} disabled={pickCount === 0} title={levelHelp.read} onclick={() => addPicked("read")}>Add {pickCount} as Read</button>
      <button type="button" class={btn} disabled={pickCount === 0} title={levelHelp.all} onclick={() => addPicked("all")}>Add {pickCount} as Write</button>
      <button type="button" class="ml-auto text-xs text-green-600 hover:underline" onclick={closePicker}>Cancel</button>
    </div>
  </section>
{:else}
  <div class="flex flex-wrap items-center justify-between gap-2">
    <div class="min-w-0">
      <p class="text-sm font-medium text-black-900 dark:text-white-100">
        {tier === "connectors" ? "Granted" : tierLabel[tier]} <span class="text-black-700">· {added.length}</span>
      </p>
      <p class="text-xs text-black-800 dark:text-black-600">{tierHint[tier]}</p>
    </div>
    {#if tier === "connectors" && added.length > 0}
      <button type="button" class={btn} onclick={openPicker}>+ Add connectors</button>
    {/if}
  </div>

  {#if tier === "connectors" && added.length === 0 && !loading}
    <div class="rounded-xl border border-dashed border-white-300 px-4 py-6 text-center dark:border-navy-600" data-testid="access-empty">
      <p class="text-sm font-medium text-black-900 dark:text-white-100">No connectors yet</p>
      <p class="mt-1 text-xs text-black-800 dark:text-black-600">This agent can't use any connector until you add one.</p>
      <button type="button" class="{btn} mt-3" onclick={openPicker}>+ Add connectors</button>
    </div>
  {:else}
    {#if added.length > 0}
      <input type="search" aria-label="Search {tierLabel[tier]}" class="w-full rounded-lg border border-white-300 bg-white-100 px-3 py-2 text-sm text-black-900 focus:border-green-500 focus:outline-none dark:border-navy-600 dark:bg-navy-800 dark:text-white-100" bind:value={query} placeholder="Search {tierLabel[tier].toLowerCase()}…" />
    {/if}
    <div class="overflow-hidden rounded-xl border border-white-300 dark:border-navy-600">
      {#if selCount > 0}
        <div class="sticky top-0 z-10 flex flex-wrap items-center gap-2 border-b border-green-300 bg-green-50 px-3 py-2 text-xs dark:border-green-700 dark:bg-navy-800" role="toolbar" aria-label="Bulk actions">
          <span class="mr-1 font-medium text-black-900 dark:text-white-100">{selCount} selected</span>
          <button type="button" class={btn} title={levelHelp.read} onclick={() => bulk("read")}>Set Read</button>
          <button type="button" class={btn} title={levelHelp.all} onclick={() => bulk("all")}>Set Write</button>
          <button type="button" class={btn} onclick={() => bulk("off")}>{tier === "connectors" ? "Remove" : "Set Off"}</button>
          {#if tier !== "connectors"}<button type="button" class={btn} onclick={() => bulk("default")}>Reset to default</button>{/if}
          <button type="button" class="ml-auto text-xs text-green-600 hover:underline" onclick={() => (selected = new Set())}>Clear</button>
        </div>
      {/if}
      <div class="flex items-center gap-3 border-b border-white-300 bg-white-200 px-3 py-2 text-xs text-black-800 dark:border-navy-600 dark:bg-navy-800 dark:text-black-600">
        <input type="checkbox" class="h-4 w-4 shrink-0 accent-green-500" id="cc-sel-all" checked={sel === "all"} disabled={shown.length === 0}
          use:indeterminate={sel === "some"} onchange={() => (selected = toggleShown(selected, shown))} aria-label="Select all shown" />
        <label for="cc-sel-all" class="flex-1">Select all ({shown.length})</label>
        <span>Access</span>
      </div>
      {#if shown.length === 0 && !loading}
        <p class="px-3 py-3 text-xs text-black-800 dark:text-black-600">Nothing matches.</p>
      {/if}
      <ul class="divide-y divide-white-300 dark:divide-navy-600">
        {#each shown as c (c.id)}
          {@const g = grantOf(c.id)}
          {@const conn = tierOf(c) === "connectors"}
          {@const ov = overrideOf(grants, c.id)}
          {@const st = opStats(c)}
          {@const all = accIds(c)}
          {@const ticked = tickedAccounts(g, all)}
          {@const rowErr = errors?.byConnector[c.id] ?? []}
          <li class="px-3 py-2 {rowErr.length > 0 ? 'bg-neg-100/40 dark:bg-neg-400/10' : selected.has(c.id) ? 'bg-green-50 dark:bg-green-900/10' : ''}">
            <div class="flex flex-wrap items-center gap-3">
              <input type="checkbox" class="h-4 w-4 shrink-0 accent-green-500" id="sel-{c.id}" checked={selected.has(c.id)}
                onchange={(e) => (selected = toggleOne(selected, c.id, (e.currentTarget as HTMLInputElement).checked))} aria-label="Select {c.label}" />
              {@render icon(c)}
              <label for="sel-{c.id}" class="min-w-0 flex-1">
                <span class="flex items-center gap-2 truncate text-sm font-medium text-black-900 dark:text-white-100">{c.label}
                  {#if !conn && ov !== "default"}<span class="rounded-full bg-cau-100 px-1.5 text-[10px] text-black-900 dark:bg-navy-600 dark:text-cau-300">Changed</span>{/if}
                </span>
                <span class="block truncate text-xs text-black-800 dark:text-black-600">
                  {conn ? `${st.total} ops · ${st.write > 0 ? `${st.write} can change things` : "look-up only"}` : c.description || c.key}
                </span>
              </label>
              <div class="inline-flex" role="radiogroup" aria-label="Access level for {c.label}">
                {#each levelChoices(c) as [o, name] (o)}
                  <button type="button" role="radio" aria-checked={ov === o} title={levelHelp[o] ?? ""} class="{seg} {ov === o ? segOn : segOff}" onclick={() => setOv(c, o)}>{name}</button>
                {/each}
              </div>
            </div>
            {#if conn && g && all.length > 0}
              <ul class="mt-2 space-y-1 pl-[4.25rem]" aria-label="Accounts of {c.label}">
                {#each c.accounts ?? [] as acc (acc.id)}
                  <li class="flex items-center gap-2 text-xs text-black-900 dark:text-white-100">
                    <input type="checkbox" class="h-3.5 w-3.5 accent-green-500" id="acc-{c.id}-{acc.id}" checked={ticked.has(acc.id)}
                      onchange={(e) => (grants = toggleAccount(grants, c.id, all, acc.id, (e.currentTarget as HTMLInputElement).checked))} />
                    <label for="acc-{c.id}-{acc.id}" class="flex items-center gap-1">
                      <span aria-hidden="true">{acc.id === "" ? "🤖" : "👤"}</span>
                      {acc.id === "" ? "Bot / instance" : `@${acc.display_name || acc.id} (yours)`}
                    </label>
                  </li>
                {/each}
              </ul>
            {/if}
            {@render opsPicker(c)}
            {#each rowErr as line (line)}<p class="mt-1 pl-[4.25rem] text-xs text-neg-400">{line}</p>{/each}
          </li>
        {/each}
      </ul>
    </div>
    <p class="text-xs text-black-800 dark:text-black-600">{levelHelp.read} · {levelHelp.all} · {levelHelp.pick}.</p>
  {/if}
{/if}

{#if showIncludeNew && tier === "connectors" && accessMode !== "owner" && !adding}
  <div class="flex items-start gap-3">
    <Toggle checked={includeNew} onChange={(v) => (includeNew = v)} label="Open other connectors read-only" describedBy="cc-incl-hint" />
    <span class="min-w-0">
      <span class="block text-sm text-black-900 dark:text-white-100">Open other connectors read-only</span>
      <span id="cc-incl-hint" class="block text-xs text-black-800 dark:text-black-600">Connectors not added above, new ones included, can look things up but not change them.</span>
    </span>
  </div>
{/if}

{#if tier === "connectors" && accessMode !== "owner" && !adding && writes.length > 0}
  <details class="rounded-xl border border-white-300 px-4 py-2 text-xs text-black-800 dark:border-navy-600 dark:text-black-600">
    <summary class="cursor-pointer select-none font-medium text-black-900 dark:text-white-100">Write operations allowed ({writes.length})</summary>
    <ul class="mt-2 space-y-0.5">
      {#each writes as w (w)}<li class="font-mono text-[11px]">{w}</li>{/each}
    </ul>
  </details>
{/if}

{#if showRunAs && runAs === "owner"}
  <div class="rounded-xl border border-cau-300 bg-cau-100 px-4 py-2 text-xs text-black-900 dark:bg-navy-800 dark:text-cau-300" role="alert">
    {#if accessMode === "owner"}
      ⚠️ Anyone who chats with this agent uses <b>your</b> access — including <b>write</b> access to every connector you have.
    {:else}
      ⚠️ Anyone who chats with this agent uses <b>your</b> access, up to the checklist{#if writeConns.length > 0}
        — including <b>write</b> access to {writeConns.join(", ")}{/if}.
    {/if}
  </div>
{/if}
