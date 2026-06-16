<script lang="ts">
  import FileTreeNode from "./FileTreeNode.svelte";
  import type { ContextFileEntry } from "../types/agents.js";
  import { formatSize, formatRelTime } from "../fileMeta.js";

  type TreeNode = { entry: ContextFileEntry; children: TreeNode[] };

  type Props = {
    node: TreeNode;
    depth: number;
    forceOpen: boolean;
    openDirs: Record<string, boolean>;
    onToggleDir: (path: string) => void;
    onOpen: (f: ContextFileEntry) => void;
    onDownload: (path: string) => void;
    onDelete: (path: string) => void;
    onNewHere: (dirPath: string) => void;
  };

  let { node, depth, forceOpen, openDirs, onToggleDir, onOpen, onDownload, onDelete, onNewHere }: Props = $props();

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
    <div class="hidden group-hover:flex items-center gap-0.5 shrink-0">
      <button type="button" title="New file here" onclick={() => onNewHere(e.path)} class="inline-flex h-6 w-6 items-center justify-center rounded text-black-700 dark:text-black-600 hover:bg-white-300 dark:hover:bg-navy-600">
        <svg viewBox="0 0 12 12" class="h-3 w-3" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M6 2v8M2 6h8" stroke-linecap="round" stroke-linejoin="round"/></svg>
      </button>
      <button type="button" title="Delete" onclick={() => onDelete(e.path)} class="inline-flex h-6 w-6 items-center justify-center rounded text-neg-600 dark:text-neg-400 hover:bg-neg-50 dark:hover:bg-neg-900/20">
        <svg viewBox="0 0 12 12" class="h-3 w-3" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M2 3h8M4 3V2h4v1M5 5v4M7 5v4M3 3l.5 7h5L9 3" stroke-linecap="round" stroke-linejoin="round"/></svg>
      </button>
    </div>
  </div>
  {#if open}
    {#each node.children as child (child.entry.path)}
      <FileTreeNode node={child} depth={depth + 1} {forceOpen} {openDirs} {onToggleDir} {onOpen} {onDownload} {onDelete} {onNewHere} />
    {/each}
  {/if}
{:else}
  <div class="group relative flex items-center gap-1.5 py-1.5 border-b border-white-300 dark:border-navy-600 hover:bg-white-200 dark:hover:bg-navy-800 transition-colors" style="padding-left:{indent + 18}px;padding-right:8px;">
    <span class="shrink-0 text-black-700 dark:text-black-600">
      <svg viewBox="0 0 16 16" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="1.5">
        <path d="M3 2h6l3 3v9a1 1 0 01-1 1H3a1 1 0 01-1-1V3a1 1 0 011-1z M9 2v3h3" stroke-linejoin="round"/>
      </svg>
    </span>
    <button type="button" onclick={() => onOpen(e)} class="min-w-0 flex-1 text-left pr-16">
      <div class="text-xs text-black-900 dark:text-white-100 truncate">{e.name}</div>
      <div class="text-[10px] text-black-700 dark:text-black-600 truncate font-mono">{formatSize(e.size)} · {formatRelTime(e.mtime)}</div>
    </button>
    <div class="hidden group-hover:flex items-center gap-0.5 absolute right-2 top-1/2 -translate-y-1/2 bg-white-200 dark:bg-navy-800 rounded-md shadow-sm">
      <button type="button" title="Download" onclick={() => onDownload(e.path)} class="inline-flex h-6 w-6 items-center justify-center rounded text-black-700 dark:text-black-600 hover:bg-white-300 dark:hover:bg-navy-600">
        <svg viewBox="0 0 12 12" class="h-3 w-3" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M6 2v6m0 0l-2-2m2 2l2-2M3 10h6" stroke-linecap="round" stroke-linejoin="round"/></svg>
      </button>
      <button type="button" title="Delete" onclick={() => onDelete(e.path)} class="inline-flex h-6 w-6 items-center justify-center rounded text-neg-600 dark:text-neg-400 hover:bg-neg-50 dark:hover:bg-neg-900/20">
        <svg viewBox="0 0 12 12" class="h-3 w-3" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M2 3h8M4 3V2h4v1M5 5v4M7 5v4M3 3l.5 7h5L9 3" stroke-linecap="round" stroke-linejoin="round"/></svg>
      </button>
    </div>
  </div>
{/if}
