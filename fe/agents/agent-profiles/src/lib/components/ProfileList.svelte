<script lang="ts">
  /* Left rail: the roles in this scope, one row each. Selection is
     controlled by the parent so the editor and the list cannot disagree
     about what is being edited. */
  import type { AgentProfile } from "@wick-fe/common-api";

  type Props = {
    profiles: AgentProfile[];
    selectedID: string;
    canCreate: boolean;
    onselect: (p: AgentProfile) => void;
    oncreate: () => void;
  };

  let { profiles, selectedID, canCreate, onselect, oncreate }: Props = $props();
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
    <ul class="flex-1 overflow-y-auto px-2 pb-4">
      {#each profiles as p (p.id)}
        <li>
          <button
            type="button"
            class="mb-0.5 flex w-full flex-col items-start gap-0.5 rounded-lg px-3 py-2 text-left transition-colors
              {p.id === selectedID
              ? 'bg-white-200 dark:bg-navy-700'
              : 'hover:bg-white-200 dark:hover:bg-navy-700'}"
            onclick={() => onselect(p)}
          >
            <span class="flex w-full items-center gap-2">
              <span class="truncate text-sm font-medium text-black-900 dark:text-white-100">
                {p.name || p.key}
              </span>
              {#if p.disabled}
                <span
                  class="ml-auto shrink-0 rounded px-1.5 py-0.5 text-[10px] font-medium text-cau-400 ring-1 ring-cau-400/40"
                >
                  Disabled
                </span>
              {/if}
            </span>
            <span class="truncate text-[11px] text-black-700 dark:text-black-600">
              {p.provider}{p.model ? ` · ${p.model}` : ""}
            </span>
          </button>
        </li>
      {/each}
    </ul>
  {/if}
</div>
