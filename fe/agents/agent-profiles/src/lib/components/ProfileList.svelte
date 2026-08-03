<script lang="ts">
  /* Left rail: the roles in this scope, one row each. Selection is
     controlled by the parent so the editor and the list cannot disagree
     about what is being edited. */
  import type { AgentProfile } from "@wick-fe/common-api";
  import { AgentProfileRow } from "@wick-fe/common-ui";

  type Props = {
    profiles: AgentProfile[];
    selectedID: string;
    canCreate: boolean;
    onselect: (p: AgentProfile) => void;
    oncreate: () => void;
    /** Quick provider/model change. Omit for a read-only (non-admin) view. */
    onchangeModel?: (p: AgentProfile) => void;
    /** Flips `disabled`. Omit for a read-only view. */
    ontoggle?: (p: AgentProfile) => void;
    /** Asks to delete. Omit for a read-only view. */
    ondelete?: (p: AgentProfile) => void;
  };

  let {
    profiles,
    selectedID,
    canCreate,
    onselect,
    oncreate,
    onchangeModel,
    ontoggle,
    ondelete,
  }: Props = $props();
</script>

<div class="flex h-full flex-col">
  <div class="flex items-center justify-between px-4 py-3">
    <span
      class="text-[11px] font-semibold uppercase tracking-wider text-black-700 dark:text-black-600"
    >
      Global
    </span>
    {#if canCreate}
      <button
        type="button"
        class="text-[11px] font-semibold text-green-600 transition-colors hover:text-green-500 dark:text-green-400"
        onclick={oncreate}
      >
        + New
      </button>
    {/if}
  </div>

  {#if profiles.length === 0}
    <p class="px-4 pb-4 text-xs text-black-700 dark:text-black-600">
      No sub-agent roles yet.
    </p>
  {:else}
    <!-- Same row component the project tab renders, so a role looks and
         behaves identically in both scopes. -->
    <ul class="flex flex-1 flex-col gap-2 overflow-y-auto px-2 pb-4">
      {#each profiles as p (p.id)}
        <AgentProfileRow
          profile={p}
          selected={p.id === selectedID}
          readonly={!canCreate}
          onedit={onselect}
          {onchangeModel}
          {ontoggle}
          {ondelete}
        />
      {/each}
    </ul>
  {/if}
</div>
