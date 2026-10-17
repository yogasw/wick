<script lang="ts">
  /* A Team-sidebar folder's icon: up to four member faces on a neutral,
     static tile. Each face is the member's own RosterAvatar, fed the same
     status as its row, so whatever the row's avatar acts out (and any cue
     added to it later) shows here too; the folder adds no cue of its own.
     Small faces only wobble (AvatarActivity), and overflow stays visible so
     a ring is never clipped. */
  import RosterAvatar from "./RosterAvatar.svelte";
  import type { AgentItem } from "../../api/team.js";
  import type { RosterStatus } from "../../rosterStatus.js";

  export type FolderTileMember = { agent: AgentItem; st: RosterStatus; open: boolean; dozing?: boolean; hatching?: boolean };
  let { members }: { members: FolderTileMember[] } = $props();
</script>

<span class="folder-tile grid shrink-0 grid-cols-2 place-items-center gap-0.5 rounded-xl bg-white-300 p-1 dark:bg-navy-600" data-testid="roster-folder-icon">
  {#each members.slice(0, 4) as m (m.agent.id)}
    <RosterAvatar agent={m.agent} st={m.st} open={m.open} dozing={m.dozing} hatching={m.hatching} size={13} />
  {/each}
</span>

<style>
  .folder-tile { width: 38px; height: 38px; overflow: visible; }
</style>
