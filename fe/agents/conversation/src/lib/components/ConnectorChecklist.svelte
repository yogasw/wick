<script lang="ts">
  /* The Access checklist: which of the owner's connectors an agent may use,
     at what level, on which accounts — plus whose access a turn runs with.
     ONE component for the + Agent wizard and Settings › Access; only how
     the parent saves differs. Entries split into three type chips by tier:
     Connectors (ticked by hand), Platform (on for every agent) and System
     (on for the Captain); a tier row stores a grant only when overridden.
     The arithmetic lives in agentForm.ts / accessTiers.ts. */
  import { Toggle } from "@wick-fe/common-ui";
  import type { ConnectorGrant, AgentConnector } from "../api/team.js";
  import { opStats, writeConnectors, type GrantErrors } from "../agentForm.js";
  import {
    tierOf, tierDefault, overrideOf, setOverride, tickedAccounts, toggleAccount, setLevelFor,
    type Tier, type Override,
  } from "../accessTiers.js";

  type RunAs = "caller" | "owner";
  type Props = {
    catalog: AgentConnector[];
    loading?: boolean;
    loadError?: string;
    grants: ConnectorGrant[];
    includeNew?: boolean;
    runAs?: RunAs;
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
    errors = null,
    showRunAs = true,
    showIncludeNew = true,
    isCaptain = false,
  }: Props = $props();

  let tier = $state<Tier>("connectors");
  let query = $state("");
  let adding = $state(false);
  let expanded = $state<Record<string, boolean>>({});

  const grantOf = (id: string) => grants.find((g) => g.connector_id === id);
  const inTier = (t: Tier) => catalog.filter((c) => tierOf(c) === t);
  const tiers = $derived(
    (["connectors", "platform", "system"] as Tier[])
      .map((t) => ({ t, items: inTier(t) }))
      .filter((x) => x.t !== "system" || x.items.length > 0)
      .map((x) => ({ ...x, custom: x.items.filter((c) => grantOf(c.id)).length })),
  );
  const matches = (c: AgentConnector) => {
    const q = query.trim().toLowerCase();
    return !q || c.label.toLowerCase().includes(q) || c.key.toLowerCase().includes(q);
  };
  const rows = $derived(inTier(tier).filter(matches));
  // Connectors: granted rows on top; the rest only through search or "+ Add".
  const granted = $derived(tier === "connectors" ? rows.filter((c) => grantOf(c.id)) : rows);
  const addable = $derived(
    tier === "connectors" && (adding || query.trim() !== "") ? rows.filter((c) => !grantOf(c.id)) : [],
  );
  // Write ops the Connectors list grants, by their real op keys.
  // Bulk level applies to the granted rows, or to every shown row while
  // adding/searching (Connectors), or to every shown row (Platform/System).
  const bulkTargets = $derived(tier === "connectors" ? [...granted, ...addable] : rows);
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
    connectors: "Integrations this agent may use. Nothing here is on until you add it.",
    platform: "wick's own working tools. On (write) for every agent unless you override a row.",
    system: "Tools that build and manage wick. On (write) for the Captain only unless you override a row.",
  };
  const levelName: Record<string, string> = { all: "Write", read: "Read", off: "Off", pick: "Picked ops" };

  function setOv(c: AgentConnector, o: Override) {
    grants = setOverride(grants, c.id, o);
    if (o === "pick") expanded[c.id] = true;
  }
  function addConnector(c: AgentConnector, level: "read" | "all") {
    grants = setOverride(grants, c.id, level);
  }
  function tickOp(c: AgentConnector, key: string, on: boolean) {
    const g = grantOf(c.id);
    if (!g) return;
    const ops = on ? (g.ops.includes(key) ? g.ops : [...g.ops, key]) : g.ops.filter((x) => x !== key);
    grants = grants.map((x) => (x.connector_id === c.id ? { ...x, ops } : x));
  }
  const accIds = (c: AgentConnector) => (c.accounts ?? []).map((a) => a.id);
  function tickInstance(c: AgentConnector, on: boolean) {
    if (!on) grants = grants.filter((g) => g.connector_id !== c.id);
    else grants = setOverride(grants, c.id, grantOf(c.id)?.level ?? "read").map((g) => (g.connector_id === c.id ? { ...g, accounts: [] } : g));
  }
  function resetSection() {
    const ids = new Set(inTier(tier).map((c) => c.id));
    grants = grants.filter((g) => !ids.has(g.connector_id));
  }
  function indeterminate(node: HTMLInputElement, v: boolean) {
    node.indeterminate = v;
    return { update: (n: boolean) => (node.indeterminate = n) };
  }

  const btn =
    "whitespace-nowrap rounded-lg border border-white-300 px-3 py-1.5 text-xs font-medium text-black-900 hover:bg-white-200 disabled:opacity-50 dark:border-navy-600 dark:text-white-100 dark:hover:bg-navy-600";
  const chip = "inline-flex cursor-pointer select-none items-center gap-1 rounded-full border px-3 py-1 text-xs";
  const chipOn = "border-green-500 bg-green-50 text-black-900 dark:bg-green-900/30 dark:text-white-100";
  const chipOff = "border-white-300 text-black-800 dark:border-navy-600 dark:text-black-600";
  const seg = "px-2 py-1 text-xs first:rounded-l-lg last:rounded-r-lg border-y border-l last:border-r border-white-300 dark:border-navy-600";
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

<div class="flex flex-wrap items-center gap-2" role="tablist" aria-label="Access type">
  {#each tiers as x (x.t)}
    <button
      type="button"
      role="tab"
      aria-selected={tier === x.t}
      class="{chip} {tier === x.t ? chipOn : chipOff}"
      onclick={() => { tier = x.t; query = ""; adding = false; }}
    >
      {tierLabel[x.t]} <span class="text-black-700">· {x.items.length}</span>
      {#if x.custom > 0}<span class="rounded-full bg-white-200 px-1.5 text-[10px] dark:bg-navy-600">{x.custom} {x.t === "connectors" ? "granted" : "custom"}</span>{/if}
    </button>
  {/each}
</div>
<p class="text-xs text-black-800 dark:text-black-600">{tierHint[tier]}</p>

<div class="flex flex-wrap items-center gap-2">
  <input type="search" aria-label="Search {tierLabel[tier]}" class="min-w-0 flex-1 rounded-lg border border-white-300 bg-white-100 px-3 py-2 text-sm text-black-900 focus:border-green-500 focus:outline-none dark:border-navy-600 dark:bg-navy-800 dark:text-white-100" bind:value={query} placeholder="Search {tierLabel[tier].toLowerCase()}…" />
  {#if tier === "connectors"}
    <button type="button" class={btn} aria-expanded={adding} onclick={() => (adding = !adding)}>{adding ? "Done adding" : "+ Add connectors"}</button>
  {:else}
    <button type="button" class={btn} disabled={!inTier(tier).some((c) => grantOf(c.id))} onclick={resetSection}>Reset section</button>
  {/if}
</div>
<div class="flex flex-wrap items-center gap-2 text-xs text-black-800 dark:text-black-600">
  <span>Set level for {tier === "connectors" && addable.length === 0 ? "granted" : "shown"} ({bulkTargets.length}):</span>
  {#each [["read", "Read"], ["all", "Write"], ["off", tier === "connectors" ? "Off (clear)" : "Off"]] as [lv, name] (lv)}
    <button type="button" class={btn} disabled={bulkTargets.length === 0}
      onclick={() => (grants = setLevelFor(grants, bulkTargets, lv as "read" | "all" | "off"))}>{name}</button>
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
    <div class="mt-2 flex flex-wrap gap-2 pl-12">
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

{#if tier === "connectors"}
  <section aria-label="Granted connectors">
    <p class="mb-1 text-xs font-medium text-black-900 dark:text-white-100">Granted · {granted.length}</p>
    {#if granted.length === 0}
      <p class="rounded-xl border border-dashed border-white-300 px-3 py-3 text-xs text-black-800 dark:border-navy-600 dark:text-black-600">No connectors granted yet. Use “+ Add connectors” or search to add one.</p>
    {/if}
    <ul class="space-y-2">
      {#each granted as c (c.id)}
        {@const g = grantOf(c.id)}
        {@const st = opStats(c)}
        {@const all = accIds(c)}
        {@const ticked = tickedAccounts(g, all)}
        {@const rowErr = errors?.byConnector[c.id] ?? []}
        <li class="rounded-xl border px-3 py-2 {rowErr.length > 0 ? 'border-neg-300' : 'border-green-300 bg-green-50 dark:border-green-700 dark:bg-green-900/10'}">
          <div class="flex items-center gap-3">
            <input type="checkbox" class="h-4 w-4 shrink-0 accent-green-500" id="conn-{c.id}" checked={!!g}
              use:indeterminate={all.length > 0 && ticked.size > 0 && ticked.size < all.length}
              onchange={(e) => tickInstance(c, (e.currentTarget as HTMLInputElement).checked)} aria-label="Grant {c.label}" />
            {@render icon(c)}
            <label for="conn-{c.id}" class="min-w-0 flex-1">
              <span class="block truncate text-sm font-medium text-black-900 dark:text-white-100">{c.label}</span>
              <span class="block truncate text-xs text-black-800 dark:text-black-600">{st.total} ops · {st.write > 0 ? `${st.write} write` : "read-only"}</span>
            </label>
            <div class="inline-flex" role="radiogroup" aria-label="Access level for {c.label}">
              {#each [["read", "Read"], ["all", "Write"], ["pick", "Pick ops"]] as [lv, name] (lv)}
                <button type="button" role="radio" aria-checked={g?.level === lv} class="{seg} {g?.level === lv ? segOn : segOff}" onclick={() => setOv(c, lv as Override)}>{name}</button>
              {/each}
            </div>
          </div>
          {#if all.length > 0}
            <ul class="mt-2 space-y-1 pl-7" aria-label="Accounts of {c.label}">
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
          {#each rowErr as line (line)}<p class="mt-1 pl-12 text-xs text-neg-400">{line}</p>{/each}
        </li>
      {/each}
    </ul>
  </section>
  {#if addable.length > 0}
    <section aria-label="Add connectors">
      <p class="mb-1 mt-2 text-xs font-medium text-black-900 dark:text-white-100">Add connectors · {addable.length}</p>
      <ul class="max-h-80 space-y-1 overflow-y-auto">
        {#each addable as c (c.id)}
          {@const st = opStats(c)}
          <li class="flex items-center gap-3 rounded-xl border border-white-300 px-3 py-2 dark:border-navy-600">
            {@render icon(c)}
            <span class="min-w-0 flex-1">
              <span class="block truncate text-sm text-black-900 dark:text-white-100">{c.label}</span>
              <span class="block truncate text-xs text-black-800 dark:text-black-600">{st.total} ops · {st.write > 0 ? `${st.write} write` : "read-only"}</span>
            </span>
            <button type="button" class={btn} onclick={() => addConnector(c, "read")} aria-label="Add {c.label} read only">+ Read</button>
            <button type="button" class={btn} onclick={() => addConnector(c, "all")} aria-label="Add {c.label} with write">+ Write</button>
          </li>
        {/each}
      </ul>
    </section>
  {:else if adding && query.trim() === ""}
    <p class="text-xs text-black-800 dark:text-black-600">Every connector is already granted.</p>
  {/if}
{:else}
  {#if rows.length === 0 && !loading}
    <p class="text-sm text-black-800 dark:text-black-600">Nothing matches.</p>
  {/if}
  <ul class="space-y-2">
    {#each rows as c (c.id)}
      {@const ov = overrideOf(grants, c.id)}
      {@const def = tierDefault(c, isCaptain)}
      {@const rowErr = errors?.byConnector[c.id] ?? []}
      <li class="rounded-xl border px-3 py-2 {rowErr.length > 0 ? 'border-neg-300' : 'border-white-300 dark:border-navy-600'}">
        <div class="flex flex-wrap items-center gap-3">
          {@render icon(c)}
          <span class="min-w-0 flex-1">
            <span class="flex items-center gap-2 truncate text-sm font-medium text-black-900 dark:text-white-100">{c.label}
              {#if ov !== "default"}<span class="rounded-full bg-cau-100 px-1.5 text-[10px] text-black-900 dark:bg-navy-600 dark:text-cau-300">Custom</span>{/if}
            </span>
            <span class="block truncate text-xs text-black-800 dark:text-black-600">{c.description || c.key}</span>
          </span>
          <div class="inline-flex" role="radiogroup" aria-label="Access for {c.label}">
            {#each (c.tool ? [["default", `Default (${def === "all" ? "On" : "Off"})`], ["off", "Off"], ["all", "On"]] : [["default", `Default (${levelName[def]})`], ["off", "Off"], ["read", "Read"], ["all", "Write"], ["pick", "Pick ops"]]) as [o, name] (o)}
              <button type="button" role="radio" aria-checked={ov === o} class="{seg} {ov === o ? segOn : segOff}" onclick={() => setOv(c, o as Override)}>{name}</button>
            {/each}
          </div>
          {#if ov !== "default"}
            <button type="button" class="text-xs text-green-600 hover:underline" onclick={() => setOv(c, "default")} aria-label="Reset {c.label} to default">Reset</button>
          {/if}
        </div>
        {@render opsPicker(c)}
        {#each rowErr as line (line)}<p class="mt-1 pl-12 text-xs text-neg-400">{line}</p>{/each}
      </li>
    {/each}
  </ul>
{/if}

{#if showIncludeNew && tier === "connectors"}
  <div class="flex items-start gap-3">
    <Toggle checked={includeNew} onChange={(v) => (includeNew = v)} label="Open other connectors read-only" describedBy="cc-incl-hint" />
    <span class="min-w-0">
      <span class="block text-sm text-black-900 dark:text-white-100">Open other connectors read-only</span>
      <span id="cc-incl-hint" class="block text-xs text-black-800 dark:text-black-600">Connectors not added above, new ones included, open with read operations only.</span>
    </span>
  </div>
{/if}

{#if tier === "connectors" && writes.length > 0}
  <details class="rounded-xl border border-white-300 px-4 py-2 text-xs text-black-800 dark:border-navy-600 dark:text-black-600">
    <summary class="cursor-pointer select-none font-medium text-black-900 dark:text-white-100">Write operations allowed ({writes.length})</summary>
    <ul class="mt-2 space-y-0.5">
      {#each writes as w (w)}<li class="font-mono text-[11px]">{w}</li>{/each}
    </ul>
  </details>
{/if}

{#if showRunAs && runAs === "owner"}
  <div class="rounded-xl border border-cau-300 bg-cau-100 px-4 py-2 text-xs text-black-900 dark:bg-navy-800 dark:text-cau-300" role="alert">
    ⚠️ Anyone who chats with this agent uses <b>your</b> access, up to the checklist{#if writeConns.length > 0}
      — including <b>write</b> access to {writeConns.join(", ")}{/if}.
  </div>
{/if}
