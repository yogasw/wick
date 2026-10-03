<script lang="ts">
  /* + Group: pick at least two of the owner's agents and a name. The
     first agent picked is the "first" default responder. */
  import { AgentAvatar } from "@wick-fe/common-avatar";
  import { createGroup, runApi, type AgentItem, type GroupItem } from "../api/team.js";
  import { groupFormError, toggleMember } from "../teamGroups.js";

  type Props = { base: string; agents: AgentItem[]; onClose: () => void; onCreated: (g: GroupItem) => void };
  let { base, agents, onClose, onCreated }: Props = $props();

  let name = $state("");
  let members = $state<string[]>([]);
  let saving = $state(false);
  let error = $state("");
  let touched = $state(false);
  const formError = $derived(groupFormError(name, members));

  async function create() {
    touched = true;
    if (formError || saving) return;
    saving = true;
    error = "";
    try {
      onCreated(await runApi(createGroup(base, { name: name.trim(), members, default_responder: "captain" })));
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      saving = false;
    }
  }
</script>

<div class="fixed inset-0 z-50 flex items-center justify-center bg-navy-900/40 p-4" role="dialog" aria-modal="true" aria-label="New group" data-testid="new-group-dialog">
  <div class="flex max-h-full w-full max-w-md flex-col rounded-2xl bg-white-100 shadow-xl dark:bg-navy-800">
    <div class="flex items-center border-b border-white-300 px-5 py-4 dark:border-navy-600">
      <h2 class="flex-1 text-base font-semibold text-black-900 dark:text-white-100">New group</h2>
      <button type="button" class="text-black-700 hover:text-black-900 dark:hover:text-white-100" aria-label="Close" onclick={onClose}>✕</button>
    </div>
    <div class="space-y-4 overflow-y-auto px-5 py-4">
      <div>
        <label for="ng-name" class="mb-1 block text-xs font-medium text-black-800 dark:text-black-600">Name</label>
        <input id="ng-name" bind:value={name} maxlength="64" placeholder="e.g. Incident room" class="w-full rounded-lg border border-white-300 bg-white-100 px-3 py-2 text-sm text-black-900 focus:border-green-500 focus:outline-none dark:border-navy-600 dark:bg-navy-800 dark:text-white-100" />
      </div>
      <div>
        <span class="mb-1 block text-xs font-medium text-black-800 dark:text-black-600">Members · {members.length} picked</span>
        {#each agents as a (a.id)}
          <label class="flex cursor-pointer items-center gap-3 rounded-xl px-2 py-1.5 hover:bg-white-200 dark:hover:bg-navy-700">
            <input type="checkbox" checked={members.includes(a.id)} onchange={() => (members = toggleMember(members, a.id))} />
            <AgentAvatar kind={a.avatar?.kind} shape={a.avatar?.shape} expression={a.avatar?.expression} color={a.avatar?.color} size={28} asleep={a.disabled} />
            <span class="min-w-0 flex-1">
              <span class="block truncate text-sm font-medium text-black-900 dark:text-white-100">{a.name}{#if a.is_captain}<span class="ml-1.5 text-[9px] font-bold tracking-wider text-green-600 dark:text-green-400">★ CAPTAIN</span>{/if}</span>
              <span class="block truncate text-xs text-black-800 dark:text-black-600">@{a.handle}</span>
            </span>
          </label>
        {/each}
      </div>
      {#if touched && formError}<p class="text-sm text-neg-400">{formError}</p>{/if}
      {#if error}<p class="text-sm text-neg-400">{error}</p>{/if}
    </div>
    <div class="flex justify-end gap-2 border-t border-white-300 px-5 py-3 dark:border-navy-600">
      <button type="button" class="rounded-lg px-3 py-1.5 text-sm text-black-800 hover:bg-white-200 dark:text-black-600 dark:hover:bg-navy-700" onclick={onClose}>Cancel</button>
      <button type="button" class="rounded-lg bg-green-500 px-3 py-1.5 text-sm font-medium text-white-100 hover:bg-green-600 disabled:opacity-50" disabled={saving || (touched && !!formError)} onclick={create}>Create group</button>
    </div>
  </div>
</div>
