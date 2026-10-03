<script lang="ts">
  /* The Akses checklist: which of the owner's connectors an agent may use,
     at what level, on which accounts — plus whose access a turn runs with.
     Shared by Settings › Access and (later) the + Agent wizard. All the
     arithmetic lives in agentForm.ts; this file only draws it. */
  import { Toggle } from "@wick-fe/common-ui";
  import type { ConnectorGrant, AgentConnector } from "../api/team.js";
  import {
    newGrant, opStats, checkedCount, selectAll, clearAll, accountTicked, toggleAccount,
    destructiveAllowed, writeConnectors, type GrantErrors,
  } from "../agentForm.js";

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
  }: Props = $props();

  let query = $state("");
  const visible = $derived(
    catalog.filter((c) => {
      const q = query.trim().toLowerCase();
      return !q || c.label.toLowerCase().includes(q) || c.key.toLowerCase().includes(q);
    }),
  );
  const count = $derived(checkedCount(grants, catalog));
  const writes = $derived(destructiveAllowed(grants, catalog));
  const writeConns = $derived(writeConnectors(grants, catalog));

  const grantOf = (id: string) => grants.find((g) => g.connector_id === id);
  function tick(c: AgentConnector, on: boolean) {
    if (on && !grantOf(c.id)) grants = [...grants, newGrant(c.id)];
    else if (!on) grants = grants.filter((g) => g.connector_id !== c.id);
  }
  function setLevel(g: ConnectorGrant, level: ConnectorGrant["level"]) {
    g.level = level;
    if (level !== "pick") g.ops = [];
  }
  function tickAccount(g: ConnectorGrant, c: AgentConnector, id: string, on: boolean) {
    const next = toggleAccount(g, (c.accounts ?? []).map((a) => a.id), id, on);
    if (next) g.accounts = next;
  }
  function tickOp(g: ConnectorGrant, key: string, on: boolean) {
    g.ops = on ? (g.ops.includes(key) ? g.ops : [...g.ops, key]) : g.ops.filter((x) => x !== key);
  }

  const btn =
    "whitespace-nowrap rounded-lg border border-white-300 px-3 py-2 text-xs font-medium text-black-900 hover:bg-white-200 disabled:opacity-50 dark:border-navy-600 dark:text-white-100 dark:hover:bg-navy-600";
  const select =
    "rounded-lg border border-white-300 bg-white-100 px-2 py-1 text-xs text-black-900 disabled:opacity-50 dark:border-navy-600 dark:bg-navy-800 dark:text-white-100";
  const chip = "inline-flex cursor-pointer select-none items-center gap-1 rounded-full border px-3 py-1 text-xs";
  const chipOn = "border-green-500 bg-green-50 text-black-900 dark:bg-green-900/30 dark:text-white-100";
  const chipOff = "border-white-300 text-black-800 dark:border-navy-600 dark:text-black-600";
</script>

{#if showRunAs}
  <div>
    <label class="mb-1 block text-xs font-medium text-black-800 dark:text-black-600" for="cc-runas">Run as</label>
    <select
      id="cc-runas"
      class="w-full rounded-lg border border-white-300 bg-white-100 px-3 py-2 text-sm text-black-900 focus:border-green-500 focus:outline-none dark:border-navy-600 dark:bg-navy-800 dark:text-white-100"
      bind:value={runAs}
    >
      <option value="caller">Caller (default) — the caller's access ∩ checklist</option>
      <option value="owner">Owner — always your access ∩ checklist</option>
    </select>
    <p class="mt-1 text-xs text-black-800 dark:text-black-600">Bot, schedule and cron triggers use the owner's access.</p>
    {#if errors?.runAs}<p class="mt-1 text-xs text-neg-400">{errors.runAs}</p>{/if}
  </div>
{/if}

<div class="flex items-center gap-2">
  <input type="search" class="min-w-0 flex-1 rounded-lg border border-white-300 bg-white-100 px-3 py-2 text-sm text-black-900 focus:border-green-500 focus:outline-none dark:border-navy-600 dark:bg-navy-800 dark:text-white-100" bind:value={query} placeholder="Search connectors…" />
  <button type="button" class={btn} disabled={visible.length === 0} onclick={() => (grants = selectAll(grants, visible))}>Select all</button>
  <button type="button" class={btn} disabled={visible.length === 0} onclick={() => (grants = clearAll(grants, visible))}>Clear</button>
</div>
<p class="text-xs text-black-800 dark:text-black-600">{count.checked} of your {count.total} connectors checked</p>

{#if loading}
  <p class="text-sm text-black-800 dark:text-black-600">Loading connectors…</p>
{:else if loadError}
  <p class="text-sm text-neg-400">{loadError}</p>
{:else if visible.length === 0}
  <p class="text-sm text-black-800 dark:text-black-600">No connectors.</p>
{/if}

{#if errors && errors.missing.length > 0}
  <p class="text-xs text-neg-400">No longer accessible to you: {errors.missing.join(", ")} — remove them and save again.</p>
{/if}

<ul class="space-y-2">
  {#each visible as c (c.id)}
    {@const g = grantOf(c.id)}
    {@const st = opStats(c)}
    {@const rowErr = errors?.byConnector[c.id] ?? []}
    <li class="rounded-xl border px-3 py-2 {rowErr.length > 0
      ? 'border-neg-300'
      : g
        ? 'border-green-300 bg-green-50 dark:border-green-700 dark:bg-green-900/10'
        : 'border-white-300 dark:border-navy-600'}">
      <div class="flex items-center gap-3">
        <input
          type="checkbox"
          class="h-4 w-4 shrink-0 accent-green-500"
          id="conn-{c.id}"
          checked={!!g}
          onchange={(e) => tick(c, (e.currentTarget as HTMLInputElement).checked)}
        />
        <span class="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-white-200 text-sm font-semibold text-black-800 dark:bg-navy-600 dark:text-black-600" aria-hidden="true">
          {(c.label || c.key).slice(0, 1).toUpperCase()}
        </span>
        <label for="conn-{c.id}" class="min-w-0 flex-1">
          <span class="block truncate text-sm font-medium text-black-900 dark:text-white-100">{c.label}</span>
          <span class="block truncate text-xs text-black-800 dark:text-black-600">
            {st.total} ops · {st.write > 0 ? `${st.write} write` : "read-only"}
          </span>
        </label>
        <select
          class={select}
          value={g?.level ?? "read"}
          disabled={!g}
          onchange={(e) => g && setLevel(g, (e.currentTarget as HTMLSelectElement).value as ConnectorGrant["level"])}
          aria-label="Access level for {c.label}"
        >
          <option value="all">All operations</option>
          <option value="read">Read only</option>
          <option value="pick">Pick operations…</option>
        </select>
      </div>
      {#if g && (c.accounts ?? []).length > 0}
        <div class="mt-2 flex flex-wrap items-center gap-2 pl-16">
          <span class="text-xs text-black-800 dark:text-black-600">Accounts</span>
          {#each c.accounts ?? [] as acc (acc.id)}
            {@const on = accountTicked(g, acc.id)}
            <label class="{chip} {on ? chipOn : chipOff}">
              <input
                type="checkbox"
                class="sr-only"
                checked={on}
                onchange={(e) => tickAccount(g, c, acc.id, (e.currentTarget as HTMLInputElement).checked)}
              />{on ? "✓ " : ""}{acc.display_name || acc.id || "bot / instance"}
            </label>
          {/each}
          <span class="text-xs text-black-700">other people's accounts are not shown</span>
        </div>
      {/if}
      {#if g && g.level === "pick"}
        <div class="mt-2 flex flex-wrap gap-2 pl-16">
          {#each c.ops ?? [] as op (op.key)}
            {@const on = g.ops.includes(op.key)}
            <label class="{chip} {on ? chipOn : chipOff}" title={op.name || op.key}>
              <input
                type="checkbox"
                class="sr-only"
                checked={on}
                onchange={(e) => tickOp(g, op.key, (e.currentTarget as HTMLInputElement).checked)}
              /><span class="font-mono text-[11px]">{op.key}</span>
              {#if op.destructive}<span class="text-cau-400">write</span>{/if}
            </label>
          {/each}
          {#if (c.ops ?? []).length === 0}<span class="text-xs text-black-800 dark:text-black-600">No enabled operations.</span>{/if}
        </div>
      {/if}
      {#each rowErr as line (line)}
        <p class="mt-1 pl-16 text-xs text-neg-400">{line}</p>
      {/each}
    </li>
  {/each}
</ul>

{#if showIncludeNew}
  <div>
    <Toggle checked={includeNew} onChange={(v) => (includeNew = v)} label="Include all my connectors, new ones too (read only)" />
    <p class="mt-1 text-xs text-black-800 dark:text-black-600">Unchecked connectors open too, read operations only, limited to connectors you own — suits a personal agent; off by default.</p>
  </div>
{/if}

{#if writes.length > 0}
  <div class="rounded-xl border border-white-300 px-4 py-2 text-xs text-black-800 dark:border-navy-600 dark:text-black-600">
    <p class="font-medium text-black-900 dark:text-white-100">Write operations allowed ({writes.length})</p>
    <p class="mt-1">{writes.slice(0, 8).join(", ")}{writes.length > 8 ? ", …" : ""}</p>
  </div>
{/if}

{#if showRunAs && runAs === "owner"}
  <div class="rounded-xl border border-cau-300 bg-cau-100 px-4 py-2 text-xs text-black-900 dark:bg-navy-800 dark:text-cau-300" role="alert">
    ⚠️ Anyone who chats with this agent uses <b>your</b> access, up to the checklist{#if writeConns.length > 0}
      — including <b>write</b> access to {writeConns.join(", ")}{/if}.
  </div>
{/if}
