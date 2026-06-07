<script lang="ts">
  import type { FileChange } from "$lib/api/scm";
  import { buildTree } from "$lib/tree";
  import { statusBadge } from "$lib/git-actions";
  import FileTreeNode from "$lib/components/FileTreeNode.svelte";

  type Props = {
    title: string;
    items: FileChange[];
    staged: boolean;            // section group → drives row action + open side
    viewMode: "tree" | "list";
    expanded: Record<string, boolean>;
    onToggleDir: (path: string) => void;
    onOpen: (path: string, staged: boolean) => void;
    onAction: (paths: string[], untracked: string[]) => void;  // stage/unstage all in scope
    onDiscard: (paths: string[], untracked: string[]) => void;
    actionIcon: "stage" | "unstage";
  };
  let {
    title, items, staged, viewMode, expanded,
    onToggleDir, onOpen, onAction, onDiscard, actionIcon,
  }: Props = $props();

  let open = $state(true);
  const tree = $derived(viewMode === "tree" ? buildTree(items) : []);

  function allScope(): { paths: string[]; untracked: string[] } {
    return {
      paths: items.map((c) => c.path),
      untracked: items.filter((c) => c.untracked).map((c) => c.path),
    };
  }
</script>

{#if items.length > 0}
  <div class="group/section flex items-center gap-1.5 px-2 py-1.5">
    <button type="button" onclick={() => (open = !open)} class="flex min-w-0 flex-1 items-center gap-1 text-left">
      <svg class={"h-3 w-3 shrink-0 text-black-600 transition-transform " + (open ? "rotate-90" : "")} fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" d="M9 5l7 7-7 7"/></svg>
      <span class="text-[10px] font-semibold uppercase tracking-wide text-black-700 dark:text-black-600">{title}</span>
      <span class="rounded-full bg-white-300 px-1.5 text-[10px] font-semibold text-black-700 dark:bg-navy-600 dark:text-black-600">{items.length}</span>
    </button>
    <!-- Section-scope actions (hover) -->
    <div class="hidden shrink-0 items-center gap-2 group-hover/section:flex">
      <button type="button" title="Discard all" onclick={() => { const s = allScope(); onDiscard(s.paths, s.untracked); }} class="text-black-600 hover:text-cau-600 dark:hover:text-cau-400">
        <svg viewBox="0 0 16 16" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M2 8a6 6 0 0110.5-4M11 2v3H8M14 8a6 6 0 01-10.5 4M5 14v-3h3" stroke-linecap="round" stroke-linejoin="round"/></svg>
      </button>
      <button type="button" title={actionIcon === "stage" ? "Stage all" : "Unstage all"} onclick={() => { const s = allScope(); onAction(s.paths, s.untracked); }} class="text-[11px] text-green-600 dark:text-green-400 hover:underline">
        {actionIcon === "stage" ? "Stage all" : "Unstage all"}
      </button>
    </div>
  </div>

  {#if open}
    {#if viewMode === "tree"}
      {#each tree as node (node.path)}
        <FileTreeNode {node} depth={1} {staged} {expanded} {onToggleDir} {onOpen} {onAction} {onDiscard} {actionIcon} />
      {/each}
    {:else}
      {#each items as c (c.path)}
        <div class="group flex items-center gap-2 py-1 pr-2 pl-6 hover:bg-white-200 dark:hover:bg-navy-800">
          <button type="button" onclick={() => onOpen(c.path, staged)} class="min-w-0 flex-1 truncate text-left text-xs text-black-800 dark:text-black-600">{c.path}</button>
          <div class="hidden shrink-0 items-center gap-1 group-hover:flex">
            <button type="button" title="Discard" onclick={() => onDiscard([c.path], c.untracked ? [c.path] : [])} class="text-black-600 hover:text-cau-600 dark:hover:text-cau-400">
              <svg viewBox="0 0 16 16" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M2 8a6 6 0 0110.5-4M11 2v3H8M14 8a6 6 0 01-10.5 4M5 14v-3h3" stroke-linecap="round" stroke-linejoin="round"/></svg>
            </button>
            <button type="button" title={actionIcon === "stage" ? "Stage" : "Unstage"} onclick={() => onAction([c.path], c.untracked ? [c.path] : [])} class="text-black-600 hover:text-green-600 dark:hover:text-green-400">
              {#if actionIcon === "stage"}
                <svg viewBox="0 0 16 16" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="1.75"><path d="M8 3v10M3 8h10" stroke-linecap="round"/></svg>
              {:else}
                <svg viewBox="0 0 16 16" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="1.75"><path d="M3 8h10" stroke-linecap="round"/></svg>
              {/if}
            </button>
          </div>
          <span class={"shrink-0 text-[10px] font-mono " + (staged ? "text-amber-600 dark:text-amber-400" : "text-green-600 dark:text-green-400")}>{statusBadge(c)}</span>
        </div>
      {/each}
    {/if}
  {/if}
{/if}
