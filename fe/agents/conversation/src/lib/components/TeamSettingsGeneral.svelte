<script lang="ts">
  /* The General tab of Team settings: the Team prompt, the landing and
     idle-animation toggles. Edits the drawer's draft in place; saving is the drawer's. */
  import { Toggle } from "@wick-fe/common-ui";
  import type { TeamSettings, TeamSettingValues } from "../api/team.js";
  import { promptBytes } from "../teamSettingsTabs.js";

  type Props = { draft: TeamSettingValues; saved: TeamSettings };
  let { draft = $bindable(), saved }: Props = $props();

  const bytes = $derived(promptBytes(draft.prompt));
  const tooLong = $derived(bytes > saved.max_prompt_bytes);

  const input =
    "w-full rounded-lg border border-white-300 bg-white-100 px-3 py-2 text-sm text-black-900 focus:border-green-500 focus:outline-none dark:border-navy-600 dark:bg-navy-800 dark:text-white-100";
  const label = "mb-1 block text-xs font-medium text-black-800 dark:text-black-600";
</script>

<div>
  <label class={label} for="ts-prompt">Team prompt</label>
  <textarea
    id="ts-prompt"
    class={input}
    rows="10"
    placeholder="e.g. Answer in Indonesian. Always link the ticket you worked on."
    bind:value={draft.prompt}
  ></textarea>
  <div class="mt-1 flex flex-wrap items-start justify-between gap-2">
    <p class="text-xs text-black-800 dark:text-black-600">
      Markdown. Every agent in your Team reads it, after the operator prompt and before its own persona.
    </p>
    <span class="shrink-0 text-xs {tooLong ? 'text-neg-400' : 'text-black-700'}" data-testid="team-prompt-size">
      {(bytes / 1024).toFixed(1)} / {Math.round(saved.max_prompt_bytes / 1024)} KB
    </span>
  </div>
</div>
<!-- Toggle draws only the switch; the name and hint sit beside it. -->
<div class="flex items-start gap-3">
  <Toggle checked={draft.open_team} onChange={(v) => (draft.open_team = v)} label="Open Team when I open Agents" describedBy="ts-open-team-hint" />
  <span class="min-w-0">
    <span class="block text-sm text-black-900 dark:text-white-100">Open Team when I open Agents</span>
    <span id="ts-open-team-hint" class="block text-xs text-black-800 dark:text-black-600">
      The Agents home opens Team. "Switch to Agents" in your account menu still takes you to the classic page.
    </span>
  </span>
</div>
<div class="flex items-start gap-3">
  <Toggle checked={draft.idle_animations} onChange={(v) => (draft.idle_animations = v)} label="Idle animations" describedBy="ts-idle-hint" />
  <span class="min-w-0">
    <span class="block text-sm text-black-900 dark:text-white-100">Idle animations</span>
    <span id="ts-idle-hint" class="block text-xs text-black-800 dark:text-black-600">
      An agent with nothing to do glances around, yawns or dozes off now and then. Off keeps it to breathing and blinking.
    </span>
  </span>
</div>
{#if saved.operator_prompt_href}
  <div class="border-t border-white-300 pt-4 dark:border-navy-600">
    <a href={saved.operator_prompt_href} class="text-sm font-medium text-green-600 hover:underline dark:text-green-400" data-testid="operator-prompt-link">
      Operator prompt (all users) →
    </a>
    <p class="mt-1 text-xs text-black-800 dark:text-black-600">Admin only: the Team agents system prompt every user's agents get.</p>
  </div>
{/if}
