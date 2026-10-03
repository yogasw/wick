<script lang="ts">
  /* A group's members as overlapping avatars: at most `max`, each on a
     disc of the surface colour (`ring`) so the overlap reads as a cut,
     6px of overlap, then a "+N" disc for the rest. */
  import { AgentAvatar } from "@wick-fe/common-avatar";
  import type { GroupMember } from "../api/team.js";
  import { stacked } from "../teamGroups.js";

  type Props = { members: GroupMember[]; size?: number; max?: number; ring?: string };
  let { members, size = 32, max = 3, ring = "bg-white-100 dark:bg-navy-800" }: Props = $props();
  const st = $derived(stacked(members, max));
  const OVERLAP = 6;
</script>

<span class="flex shrink-0 items-center" data-testid="group-avatars">
  {#each st.shown as m, i (m.id)}
    <span class="relative flex items-center justify-center rounded-full {ring}" style="padding:2px;margin-left:{i ? -OVERLAP : 0}px;z-index:{st.shown.length - i}">
      <AgentAvatar kind={m.avatar?.kind} shape={m.avatar?.shape} expression={m.avatar?.expression} color={m.avatar?.color} {size} asleep={m.disabled} />
    </span>
  {/each}
  {#if st.more}
    <span class="relative flex items-center justify-center rounded-full {ring}" style="padding:2px;margin-left:-{OVERLAP}px">
      <span class="flex items-center justify-center rounded-full bg-white-300 text-[11px] font-semibold text-black-900 dark:bg-navy-600 dark:text-white-100" style="width:{size}px;height:{size}px">+{st.more}</span>
    </span>
  {/if}
</span>
