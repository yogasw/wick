<script lang="ts">
  /* Settings drawer of a group chat: members, default responder, the
     turn cap (only lower than the members'), rename and delete. Opened in
     the same floating drawer as an agent's Settings; every change saves
     at once. Member changes show in the thread as system chips. */
  import { untrack } from "svelte";
  import { AgentAvatar } from "@wick-fe/common-avatar";
  import DrawerHeader from "./DrawerHeader.svelte";
  import { updateGroup, deleteGroup, runApi, type AgentItem, type GroupItem, type GroupWrite } from "../api/team.js";
  import { groupFormError, overrideChoices, toggleMember } from "../teamGroups.js";

  type Props = {
    base: string;
    group: GroupItem;
    agents: AgentItem[];
    onChanged: (g: GroupItem) => void;
    onDeleted: () => void;
    onClose: () => void;
  };
  let { base, group, agents, onChanged, onDeleted, onClose }: Props = $props();

  /* ── settings ─────────────────────────────────────────────────── */
  let sName = $state(untrack(() => group.name));
  let sMembers = $state<string[]>(untrack(() => group.members.map((m) => m.id)));
  let sResponder = $state<"captain" | "first">(untrack(() => group.default_responder));
  let sOverride = $state(untrack(() => group.max_hops_override));
  let sError = $state("");
  let sSaving = $state(false);
  let confirmDelete = $state(false);
  async function saveSettings(body: GroupWrite) {
    sSaving = true;
    sError = "";
    try {
      const g = await runApi(updateGroup(base, group.id, body));
      onChanged(g);
      sOverride = g.max_hops_override;
    } catch (e) {
      sError = e instanceof Error ? e.message : String(e);
    } finally {
      sSaving = false;
    }
  }
  function setMembers(next: string[]) {
    const err = groupFormError(sName || group.name, next);
    if (err) {
      sError = err;
      return;
    }
    sMembers = next;
    saveSettings({ members: next });
  }
  async function removeGroup() {
    try {
      await runApi(deleteGroup(base, group.id));
      onDeleted();
    } catch (e) {
      sError = e instanceof Error ? e.message : String(e);
    }
  }
  const sLabel = "mb-1 block text-xs font-medium text-black-800 dark:text-black-600";
  const sInput = "w-full rounded-lg border border-white-300 bg-white-100 px-3 py-2 text-sm text-black-900 focus:border-green-500 focus:outline-none dark:border-navy-600 dark:bg-navy-800 dark:text-white-100";

</script>

<DrawerHeader title="Group settings" subtitle={`${group.name} · ${group.members.length} agents`} {onClose} />
<div data-testid="group-settings" class="contents">
    <div class="flex-1 space-y-5 overflow-y-auto px-6 py-5">
  <div>
    <label class={sLabel} for="gs-name">Name</label>
    <input id="gs-name" class={sInput} bind:value={sName} maxlength="64" onblur={() => sName.trim() && sName.trim() !== group.name && saveSettings({ name: sName.trim() })} />
  </div>
  <div>
    <span class={sLabel}>Members</span>
    {#each group.members as m (m.id)}
      <div class="flex items-center gap-2 py-1">
        <AgentAvatar kind={m.avatar?.kind} shape={m.avatar?.shape} expression={m.avatar?.expression} color={m.avatar?.color} size={24} asleep={m.disabled} />
        <span class="min-w-0 flex-1 truncate text-sm text-black-900 dark:text-white-100">{m.name} <span class="text-xs text-black-700">@{m.handle} · max {m.max_hops}</span></span>
        <button type="button" class="text-xs text-neg-400 hover:underline disabled:opacity-40" disabled={group.members.length <= 2 || sSaving} onclick={() => setMembers(sMembers.filter((id) => id !== m.id))}>Remove</button>
      </div>
    {/each}
    {#if agents.some((a) => !sMembers.includes(a.id))}
      <select class="{sInput} mt-2" aria-label="Add agent" onchange={(e) => { const v = (e.currentTarget as HTMLSelectElement).value; if (v) setMembers(toggleMember(sMembers, v)); (e.currentTarget as HTMLSelectElement).value = ""; }}>
        <option value="">+ Add agent</option>
        {#each agents.filter((a) => !sMembers.includes(a.id)) as a (a.id)}
          <option value={a.id}>{a.name} (@{a.handle})</option>
        {/each}
      </select>
    {/if}
  </div>
  <div>
    <span class={sLabel}>Default responder (message with no @)</span>
    <div class="flex gap-2">
      {#each [["captain", "Captain"], ["first", "First agent"]] as [v, l] (v)}
        <button type="button" class="rounded-full border px-3 py-1 text-xs {sResponder === v ? 'border-green-500 bg-green-50 text-black-900 dark:bg-navy-700 dark:text-white-100' : 'border-white-300 text-black-800 dark:border-navy-600 dark:text-black-600'}" onclick={() => { sResponder = v as "captain" | "first"; saveSettings({ default_responder: sResponder }); }}>{l}</button>
      {/each}
    </div>
    <p class="mt-1 text-xs text-black-800 dark:text-black-600">Now: @{group.responder}. Captain falls back to the first agent when the Captain isn't a member.</p>
  </div>
  <div>
    <label class={sLabel} for="gs-hops">Max agent-to-agent turns</label>
    <select id="gs-hops" class="{sInput} w-48" value={sOverride} onchange={(e) => { sOverride = Number((e.currentTarget as HTMLSelectElement).value); saveSettings({ max_hops_override: sOverride }); }}>
      {#each overrideChoices(group.members_max_hops) as c (c.value)}
        <option value={c.value}>{c.label}</option>
      {/each}
    </select>
    <p class="mt-1 text-xs text-black-800 dark:text-black-600">The smallest limit of the members is {group.members_max_hops}; the group can only go lower.</p>
  </div>
  {#if sError}<p class="text-sm text-neg-400">{sError}</p>{/if}
  <div class="space-y-2 border-t border-white-300 pt-4 dark:border-navy-600">
    <p class="text-sm font-semibold text-neg-400">Danger zone</p>
    {#if confirmDelete}
      <p class="text-xs text-black-800 dark:text-black-600">Delete {group.name}? The thread goes; the agents and their own chats stay.</p>
      <div class="flex gap-2">
        <button type="button" class="rounded-lg bg-neg-400 px-3 py-1 text-sm text-white-100" onclick={removeGroup}>Delete group</button>
        <button type="button" class="rounded-lg px-3 py-1 text-sm text-black-800 dark:text-black-600" onclick={() => (confirmDelete = false)}>Cancel</button>
      </div>
    {:else}
      <button type="button" class="rounded-lg border border-neg-300 px-3 py-1 text-sm text-neg-400 hover:bg-neg-100 dark:hover:bg-navy-600" onclick={() => (confirmDelete = true)}>Delete group…</button>
    {/if}
  </div>
</div>
</div>
