<script lang="ts">
  /* Settings › Sharing: who else may chat with this agent. Chat only —
     the people listed see it in their roster, chat 1:1 and @mention it,
     with this agent's access; they cannot change, share or disable it,
     and their chats stay theirs. People are picked by wick user id. */
  import { onMount } from "svelte";
  import { Button } from "@wick-fe/common-ui";
  import { toastOk } from "@wick-fe/common-stores";
  import {
    listAgentShares, addAgentShare, removeAgentShare, listShareUsers, runApi,
    type AgentItem, type AgentShare, type ShareUser,
  } from "../api/team.js";
  import { pickableUsers } from "../agentSharing.js";

  type Props = { base: string; agent: AgentItem };
  let { base, agent }: Props = $props();

  let shares = $state<AgentShare[]>([]);
  let users = $state<ShareUser[]>([]);
  let shareable = $state(true);
  let reason = $state("");
  let loading = $state(true);
  let busy = $state("");
  let error = $state("");
  let query = $state("");

  const picks = $derived(pickableUsers(users, shares, query));

  async function load() {
    loading = true;
    error = "";
    try {
      const [s, u] = await Promise.all([runApi(listAgentShares(base, agent.id)), runApi(listShareUsers(base))]);
      shares = s.shares ?? [];
      shareable = s.shareable;
      reason = s.reason ?? "";
      users = u.users ?? [];
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      loading = false;
    }
  }
  onMount(load);

  async function add(u: ShareUser) {
    busy = u.id;
    error = "";
    try {
      await runApi(addAgentShare(base, agent.id, u.id));
      shares = [...shares, { user_id: u.id, name: u.name, created_at: new Date().toISOString() }];
      query = "";
      toastOk(`Shared with ${u.name}`);
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      busy = "";
    }
  }

  async function remove(s: AgentShare) {
    busy = s.user_id;
    error = "";
    try {
      await runApi(removeAgentShare(base, agent.id, s.user_id));
      shares = shares.filter((x) => x.user_id !== s.user_id);
      toastOk(`${s.name} can no longer chat with @${agent.handle}`);
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      busy = "";
    }
  }

  const initial = (n: string) => (n.trim()[0] ?? "?").toUpperCase();
</script>

<div data-testid="agent-sharing">
  <p class="text-sm font-semibold text-black-900 dark:text-white-100">Sharing</p>
  <p class="mt-1 text-xs text-black-800 dark:text-black-600">
    People you share @{agent.handle} with can chat with it and @mention it, using this agent's access. They can't change its settings, share it on or turn it off, and you don't see their chats.
  </p>
</div>

{#if loading}
  <p class="text-sm text-black-800 dark:text-black-600">Loading…</p>
{:else if !shareable}
  <div class="rounded-xl border border-amber-300 bg-amber-50 px-4 py-3 text-sm text-amber-800 dark:border-amber-700 dark:bg-navy-800 dark:text-amber-300" data-testid="sharing-blocked">
    {reason || "This agent cannot be shared."}
  </div>
{:else}
  <div>
    <label class="mb-1 block text-xs font-medium text-black-800 dark:text-black-600" for="share-search">Add people</label>
    <input
      id="share-search"
      class="w-full rounded-lg border border-white-300 bg-white-100 px-3 py-2 text-sm text-black-900 focus:border-green-500 focus:outline-none dark:border-navy-600 dark:bg-navy-800 dark:text-white-100"
      placeholder="Search wick users by name"
      autocomplete="off"
      bind:value={query}
    />
    {#if picks.length > 0}
      <ul class="mt-2 divide-y divide-white-300 rounded-xl border border-white-300 dark:divide-navy-600 dark:border-navy-600" data-testid="share-picker">
        {#each picks as u (u.id)}
          <li class="flex items-center gap-3 px-3 py-2">
            <span class="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-white-300 text-xs font-semibold text-black-900 dark:bg-navy-600 dark:text-white-100">{initial(u.name)}</span>
            <span class="min-w-0 flex-1 truncate text-sm text-black-900 dark:text-white-100">{u.name}</span>
            <Button size="sm" variant="ghost" disabled={!!busy} onclick={() => add(u)}>{busy === u.id ? "Sharing…" : "Share"}</Button>
          </li>
        {/each}
      </ul>
    {:else if query.trim()}
      <p class="mt-2 text-xs text-black-800 dark:text-black-600">No one else matches.</p>
    {/if}
  </div>

  <div>
    <p class="mb-1 text-xs font-medium text-black-800 dark:text-black-600">Shared with ({shares.length})</p>
    {#if shares.length === 0}
      <p class="text-sm text-black-800 dark:text-black-600">Only you can use this agent.</p>
    {:else}
      <ul class="divide-y divide-white-300 rounded-xl border border-white-300 dark:divide-navy-600 dark:border-navy-600" data-testid="share-list">
        {#each shares as s (s.user_id)}
          <li class="flex items-center gap-3 px-3 py-2">
            <span class="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-green-500 text-xs font-semibold text-white-100">{initial(s.name)}</span>
            <span class="min-w-0 flex-1">
              <span class="block truncate text-sm text-black-900 dark:text-white-100">{s.name}</span>
              <span class="block text-[11px] text-black-700">Chat only</span>
            </span>
            <Button size="sm" variant="ghost" disabled={!!busy} onclick={() => remove(s)}>{busy === s.user_id ? "Removing…" : "Remove"}</Button>
          </li>
        {/each}
      </ul>
    {/if}
  </div>
{/if}

{#if error}<p class="text-sm text-neg-400">{error}</p>{/if}
