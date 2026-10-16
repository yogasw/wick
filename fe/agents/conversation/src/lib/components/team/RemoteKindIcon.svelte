<script lang="ts">
  import type { AgentItem } from "../../api/team.js";
  import { isA2ARemote, isPluginRemote, isSlackRemote, remoteBadge } from "../../remoteAgent.js";

  /* Where a remote agent lives, as a small icon after its name or in the
     header: the Slack logo for a Slack remote, a link glyph for A2A, a
     plug for a plugin remote. A wick agent gets nothing. The badge word
     stays readable to screen readers (sr-only). */
  let { agent, size = 13 }: { agent: Pick<AgentItem, "kind">; size?: number } = $props();

  const label = $derived(remoteBadge(agent));
</script>

{#if label}
  <span class="inline-flex shrink-0 items-center align-middle" data-testid="remote-kind-icon" data-kind={agent.kind}>
    {#if isSlackRemote(agent)}
      <svg width={size} height={size} viewBox="0 0 24 24" aria-hidden="true"><path fill="#E01E5A" d="M5 15a2 2 0 1 1-2-2h2v2zm1 0a2 2 0 1 1 4 0v5a2 2 0 1 1-4 0v-5z" /><path fill="#36C5F0" d="M9 5a2 2 0 1 1 2-2v2H9zm0 1a2 2 0 1 1 0 4H4a2 2 0 1 1 0-4h5z" /><path fill="#2EB67D" d="M19 9a2 2 0 1 1 2 2h-2V9zm-1 0a2 2 0 1 1-4 0V4a2 2 0 1 1 4 0v5z" /><path fill="#ECB22E" d="M15 19a2 2 0 1 1-2 2v-2h2zm0-1a2 2 0 1 1 0-4h5a2 2 0 1 1 0 4h-5z" /></svg>
    {:else if isA2ARemote(agent)}
      <svg class="text-black-700 dark:text-black-600" width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M10 13a5 5 0 0 0 7.5.5l3-3a5 5 0 0 0-7-7l-1.7 1.7" /><path d="M14 11a5 5 0 0 0-7.5-.5l-3 3a5 5 0 0 0 7 7l1.7-1.7" /></svg>
    {:else if isPluginRemote(agent)}
      <svg class="text-black-700 dark:text-black-600" width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M9 2v6M15 2v6M6 8h12v4a6 6 0 0 1-12 0zM12 18v4" /></svg>
    {/if}
    <span class="sr-only">{label}</span>
  </span>
{/if}
