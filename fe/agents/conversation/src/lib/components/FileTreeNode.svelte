<script lang="ts">
  import FileTreeNode from "./FileTreeNode.svelte";
  import type { ContextFileEntry } from "../types/agents.js";

  type TreeNode = { entry: ContextFileEntry; children: TreeNode[] };

  type Props = {
    node: TreeNode;
    depth: number;
    forceOpen: boolean;
    openDirs: Record<string, boolean>;
    onToggleDir: (path: string) => void;
    onOpen: (f: ContextFileEntry) => void;
  };

  let { node, depth, forceOpen, openDirs, onToggleDir, onOpen }: Props = $props();

  const e = $derived(node.entry);
  const indent = $derived(depth * 14 + 8);
  const open = $derived(forceOpen || !!openDirs[e.path]);
</script>

{#if e.isDir}
  <div class="group relative flex items-center gap-1.5 py-1.5 border-b border-white-300 dark:border-navy-600 hover:bg-white-200 dark:hover:bg-navy-800 transition-colors" style="padding-left:{indent}px;padding-right:8px;">
    <button type="button" onclick={() => onToggleDir(e.path)}
      class="flex items-center gap-1.5 min-w-0 flex-1 text-left">
      <span class="shrink-0 text-black-700 dark:text-black-600">
        <svg viewBox="0 0 16 16" class={`h-3 w-3 transition-transform ${open ? "rotate-90" : ""}`} fill="none" stroke="currentColor" stroke-width="1.5">
          <path d="M6 4l4 4-4 4" stroke-linecap="round" stroke-linejoin="round"/>
        </svg>
      </span>
      <span class="shrink-0">
        <svg viewBox="0 0 16 16" class="h-3.5 w-3.5 text-green-500" fill="none" stroke="currentColor" stroke-width="1.5">
          <path d="M2 4a1 1 0 011-1h3l2 2h5a1 1 0 011 1v6a1 1 0 01-1 1H3a1 1 0 01-1-1V4z" stroke-linejoin="round"/>
        </svg>
      </span>
      <span class="text-xs font-medium text-black-900 dark:text-white-100 truncate">{e.name}</span>
    </button>
  </div>
  {#if open}
    {#each node.children as child (child.entry.path)}
      <FileTreeNode node={child} depth={depth + 1} {forceOpen} {openDirs} {onToggleDir} {onOpen} />
    {/each}
  {/if}
{:else}
  <div class="group relative flex items-center gap-1.5 py-1.5 border-b border-white-300 dark:border-navy-600 hover:bg-white-200 dark:hover:bg-navy-800 transition-colors" style="padding-left:{indent + 18}px;padding-right:8px;">
    <span class="shrink-0 text-black-700 dark:text-black-600">
      <svg viewBox="0 0 16 16" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="1.5">
        <path d="M3 2h6l3 3v9a1 1 0 01-1 1H3a1 1 0 01-1-1V3a1 1 0 011-1z M9 2v3h3" stroke-linejoin="round"/>
      </svg>
    </span>
    <button type="button" onclick={() => onOpen(e)} class="min-w-0 flex-1 text-left">
      <div class="text-xs text-black-900 dark:text-white-100 truncate">{e.name}</div>
    </button>
  </div>
{/if}
