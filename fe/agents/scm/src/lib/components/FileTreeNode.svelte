<script lang="ts">
  import type { TreeNode } from "$lib/tree";
  import { allFilePaths, allChanges } from "$lib/tree";
  import { statusBadge } from "$lib/git-actions";
  import Self from "$lib/components/FileTreeNode.svelte";

  type Props = {
    node: TreeNode;
    depth: number;
    staged: boolean; // which group this tree belongs to (drives the row action)
    expanded: Record<string, boolean>;
    onToggleDir: (path: string) => void;
    onOpen: (path: string, staged: boolean) => void;
    onAction: (paths: string[], untracked: string[]) => void; // stage or unstage (group-dependent)
    onDiscard: (paths: string[], untracked: string[]) => void;
    actionIcon: "stage" | "unstage";
  };
  let { node, depth, staged, expanded, onToggleDir, onOpen, onAction, onDiscard, actionIcon }: Props = $props();

  const isOpen = $derived(expanded[node.path] !== false); // default expanded
  const pad = $derived(`padding-left: ${depth * 12 + 8}px`);

  function folderPaths(): { paths: string[]; untracked: string[] } {
    const changes = allChanges(node);
    return {
      paths: changes.map((c) => c.path),
      untracked: changes.filter((c) => c.untracked).map((c) => c.path),
    };
  }
</script>

{#if node.isDir}
  <div class="group flex items-center gap-1 py-1 pr-2 hover:bg-white-200 dark:hover:bg-navy-800" style={pad}>
    <button type="button" onclick={() => onToggleDir(node.path)} class="flex min-w-0 flex-1 items-center gap-1 text-left">
      <svg class={"h-3 w-3 shrink-0 text-black-600 transition-transform " + (isOpen ? "rotate-90" : "")} fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" d="M9 5l7 7-7 7"/></svg>
      <span class="truncate text-xs font-medium text-black-800 dark:text-black-600">{node.name}</span>
    </button>
    <!-- Folder-scope actions (hover) -->
    <div class="hidden shrink-0 items-center gap-1 group-hover:flex">
      <button type="button" title="Discard folder" onclick={() => { const f = folderPaths(); onDiscard(f.paths, f.untracked); }} class="text-black-600 hover:text-cau-600 dark:hover:text-cau-400">
        <svg viewBox="0 0 16 16" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M2 8a6 6 0 0110.5-4M11 2v3H8M14 8a6 6 0 01-10.5 4M5 14v-3h3" stroke-linecap="round" stroke-linejoin="round"/></svg>
      </button>
      <button type="button" title={actionIcon === "stage" ? "Stage folder" : "Unstage folder"} onclick={() => { const f = folderPaths(); onAction(f.paths, f.untracked); }} class="text-black-600 hover:text-green-600 dark:hover:text-green-400">
        {#if actionIcon === "stage"}
          <svg viewBox="0 0 16 16" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="1.75"><path d="M8 3v10M3 8h10" stroke-linecap="round"/></svg>
        {:else}
          <svg viewBox="0 0 16 16" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="1.75"><path d="M3 8h10" stroke-linecap="round"/></svg>
        {/if}
      </button>
    </div>
  </div>
  {#if isOpen}
    {#each node.children ?? [] as child (child.path)}
      <Self node={child} depth={depth + 1} {staged} {expanded} {onToggleDir} {onOpen} {onAction} {onDiscard} {actionIcon} />
    {/each}
  {/if}
{:else if node.change}
  {@const ch = node.change}
  <div class="group flex items-center gap-2 py-1 pr-2 hover:bg-white-200 dark:hover:bg-navy-800" style={pad}>
    {#if node.dirEntry}
      <!-- A whole folder reported as one change (nested repo): nothing to
           diff, so the name is a label, not a button. -->
      <span class="flex min-w-0 flex-1 items-center gap-1 truncate text-xs text-black-800 dark:text-black-600" title="{ch.path} — a folder git reports as a single entry (nested repository)">
        <svg viewBox="0 0 16 16" class="h-3 w-3 shrink-0 text-black-600" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M1.5 4.5A1 1 0 012.5 3.5h3l1.5 2h6a1 1 0 011 1v6a1 1 0 01-1 1h-10a1 1 0 01-1-1z" stroke-linejoin="round"/></svg>
        <span class="truncate">{node.name}/</span>
      </span>
    {:else}
      <button type="button" onclick={() => onOpen(ch.path, staged)} class="min-w-0 flex-1 truncate text-left text-xs text-black-800 dark:text-black-600">{node.name}</button>
    {/if}
    <div class="hidden shrink-0 items-center gap-1 group-hover:flex">
      <button type="button" title="Discard" onclick={() => onDiscard([ch.path], ch.untracked ? [ch.path] : [])} class="text-black-600 hover:text-cau-600 dark:hover:text-cau-400">
        <svg viewBox="0 0 16 16" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M2 8a6 6 0 0110.5-4M11 2v3H8M14 8a6 6 0 01-10.5 4M5 14v-3h3" stroke-linecap="round" stroke-linejoin="round"/></svg>
      </button>
      <button type="button" title={actionIcon === "stage" ? "Stage" : "Unstage"} onclick={() => onAction([ch.path], ch.untracked ? [ch.path] : [])} class="text-black-600 hover:text-green-600 dark:hover:text-green-400">
        {#if actionIcon === "stage"}
          <svg viewBox="0 0 16 16" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="1.75"><path d="M8 3v10M3 8h10" stroke-linecap="round"/></svg>
        {:else}
          <svg viewBox="0 0 16 16" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="1.75"><path d="M3 8h10" stroke-linecap="round"/></svg>
        {/if}
      </button>
    </div>
    <span class={"shrink-0 text-[10px] font-mono " + (staged ? "text-amber-600 dark:text-amber-400" : "text-green-600 dark:text-green-400")}>{statusBadge(ch)}</span>
  </div>
{/if}
