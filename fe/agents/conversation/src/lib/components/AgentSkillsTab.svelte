<!--
  Settings › Skills: every skill the agent's spawns see (project-local,
  global, built-in), a switch per skill, and "Create skill from this chat".
-->
<script lang="ts">
  import { onMount } from "svelte";
  import { Toggle } from "@wick-fe/common-ui";
  import { toastOk } from "@wick-fe/common-stores";
  import { getAgentSkills, sendToChat, runApi, type AgentSkill } from "../api/team.js";
  import { SKILL_SOURCE_LABEL, createSkillPrompt, skillNote, toggleSkill } from "../agentSkills.js";

  type Props = {
    base: string;
    agentId: string;
    mainSessionId: string;
    disabled: string[];
    onChange: (d: string[]) => void;
  };
  let { base, agentId, mainSessionId, disabled, onChange }: Props = $props();

  let items = $state<AgentSkill[]>([]);
  let localDir = $state("");
  let loading = $state(true);
  let error = $state("");
  let sending = $state(false);

  onMount(async () => {
    try {
      const r = await runApi(getAgentSkills(base, agentId));
      items = r.items ?? [];
      localDir = r.local_dir;
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      loading = false;
    }
  });

  async function createFromChat() {
    if (!mainSessionId) return;
    sending = true;
    try {
      await runApi(sendToChat(base, mainSessionId, createSkillPrompt(localDir ? `${localDir}/` : "")));
      toastOk("Sent to the agent's chat — it will write the skill.");
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      sending = false;
    }
  }
  const chip: Record<AgentSkill["source"], string> = {
    local: "bg-green-50 text-green-700 dark:bg-green-900/40 dark:text-green-300",
    global: "bg-white-200 text-black-800 dark:bg-navy-600 dark:text-black-600",
    builtin: "bg-sky-100 text-sky-800 dark:bg-sky-800 dark:text-sky-100",
  };
</script>

<div class="flex items-start justify-between gap-3">
  <div>
    <p class="text-sm font-semibold text-black-900 dark:text-white-100">Skills</p>
    <p class="mt-1 text-xs text-black-800 dark:text-black-600">Skills this agent can load. A skill in the agent's own project folder wins over a global one with the same name.</p>
  </div>
  <button type="button" class="shrink-0 rounded-lg border border-white-300 px-3 py-1.5 text-xs font-medium text-black-900 hover:bg-white-200 disabled:opacity-50 dark:border-navy-600 dark:text-white-100 dark:hover:bg-navy-700" disabled={!mainSessionId || sending} title={mainSessionId ? "Ask the agent to write a SKILL.md from its chat" : "Open the agent's chat first"} onclick={createFromChat} data-testid="create-skill">Create skill from this chat</button>
</div>
{#if error}<p class="text-xs text-red-600 dark:text-red-400">{error}</p>{/if}
{#if loading}
  <p class="text-xs text-black-800 dark:text-black-600">Loading skills…</p>
{:else if items.length === 0}
  <p class="text-xs text-black-800 dark:text-black-600">No skills found.</p>
{:else}
  <ul class="divide-y divide-white-300 rounded-xl border border-white-300 dark:divide-navy-600 dark:border-navy-600" data-testid="skills-list">
    {#each items as s (s.source + ":" + s.name)}
      {@const note = skillNote(s)}
      <li class="flex items-start gap-3 px-4 py-3 {s.shadowed ? 'opacity-60' : ''}">
        {#if s.shadowed || s.required}
          <span class="w-9 shrink-0"></span>
        {:else}
          <Toggle checked={!disabled.includes(s.name)} onChange={(v) => onChange(toggleSkill(disabled, s.name, v))} label={s.name} />
        {/if}
        <span class="min-w-0 flex-1">
          <span class="flex items-center gap-2">
            <span class="truncate text-sm font-medium text-black-900 dark:text-white-100">{s.name}</span>
            <span class="shrink-0 rounded-full px-2 py-0.5 text-[10px] font-semibold {chip[s.source]}">{SKILL_SOURCE_LABEL[s.source]}</span>
            {#if s.required}<span class="shrink-0 rounded-full bg-white-200 px-2 py-0.5 text-[10px] font-semibold text-black-800 dark:bg-navy-600 dark:text-black-600">required</span>{/if}
          </span>
          {#if s.description}<span class="mt-0.5 line-clamp-2 block text-xs text-black-800 dark:text-black-600">{s.description}</span>{/if}
          {#if note}<span class="mt-0.5 block text-xs italic text-black-800 dark:text-black-600">{note}</span>{/if}
        </span>
      </li>
    {/each}
  </ul>
{/if}
