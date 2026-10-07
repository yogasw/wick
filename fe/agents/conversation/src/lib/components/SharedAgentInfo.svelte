<script lang="ts">
  /* The Settings drawer of an agent shared with the user: read-only. Its
     owner manages everything; the recipient only chats with it. */
  import DrawerHeader from "./DrawerHeader.svelte";
  import type { AgentItem } from "../api/team.js";
  import { sharedLabel } from "../agentSharing.js";

  type Props = { agent: AgentItem; onClose: () => void };
  let { agent, onClose }: Props = $props();
</script>

<DrawerHeader title="Agent info" subtitle={`${agent.name} · @${agent.handle} · ${sharedLabel(agent)}`} avatar={agent.avatar} {onClose} />

<div class="min-h-0 flex-1 space-y-4 overflow-y-auto px-6 py-4" data-testid="shared-agent-info">
  {#if agent.tagline}<p class="text-sm font-semibold text-black-900 dark:text-white-100">{agent.tagline}</p>{/if}
  {#if agent.description}<p class="whitespace-pre-line text-sm text-black-800 dark:text-black-600">{agent.description}</p>{/if}
  <div class="rounded-xl border border-white-300 px-4 py-3 text-xs text-black-800 dark:border-navy-600 dark:text-black-600">
    <p class="font-semibold text-black-900 dark:text-white-100">Shared with you to chat</p>
    <p class="mt-1">{agent.shared_by || "Its owner"} manages this agent's persona, access and schedule. You can chat with it and @mention it; it works with the access its owner gave it. Your chats with it are yours alone.</p>
  </div>
</div>
