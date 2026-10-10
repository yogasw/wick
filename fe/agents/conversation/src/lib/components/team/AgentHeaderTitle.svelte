<script lang="ts">
  /* Chat header line 1: the name, then " · Team lead" for the captain
     (from the flag, not the name) or else its tagline. The captain shows
     "Team lead" in the tagline's place, as in the grouped mockup §1. */
  import type { AgentItem } from "../../api/team.js";

  let { agent }: { agent: Pick<AgentItem, "name" | "tagline" | "is_captain"> } = $props();

  const suffix = $derived(agent.is_captain ? "Team lead" : agent.tagline);
</script>

<div class="truncate text-base font-semibold text-black-900 dark:text-white-100" data-testid="header-title">
  {agent.name}{#if suffix}<span class="font-normal text-black-700 dark:text-black-600" data-testid="header-suffix">&nbsp;·&nbsp;{suffix}</span>{/if}
</div>
