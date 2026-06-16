<script lang="ts">
  import type { ContextFileEntry } from "../types/agents.js";
  import FileTreeNode from "./FileTreeNode.svelte";

  type TreeNode = { entry: ContextFileEntry; children: TreeNode[] };

  type Props = {
    cwd: string;
    files: ContextFileEntry[];
    search: string;
    openDirs: Record<string, boolean>;
    onSearch: (s: string) => void;
    onToggleDir: (path: string) => void;
    onOpen: (f: ContextFileEntry) => void;
    onRefresh: () => void;
    onNewFile: () => void;
    onNewDir: () => void;
    onDownload?: (path: string) => void;
    onDelete?: (path: string) => void;
    onNewHere?: (dirPath: string) => void;
  };

  let { cwd, files, search, openDirs, onSearch, onToggleDir, onOpen, onRefresh, onNewFile, onNewDir, onDownload = () => {}, onDelete = () => {}, onNewHere = () => {} }: Props = $props();

  function buildTree(entries: ContextFileEntry[]): TreeNode {
    const root: TreeNode = { entry: { path: "", name: "", isDir: true, size: 0, mtime: 0 }, children: [] };
    const byPath: Record<string, TreeNode> = { "": root };
    const sorted = [...entries].sort((a, b) => a.path.localeCompare(b.path));
    for (const e of sorted) {
      byPath[e.path] = { entry: e, children: [] };
    }
    for (const p of Object.keys(byPath)) {
      if (p === "") continue;
      const parent = p.indexOf("/") === -1 ? "" : p.slice(0, p.lastIndexOf("/"));
      if (byPath[parent]) byPath[parent].children.push(byPath[p]);
    }
    return root;
  }

  function matchesFilter(node: TreeNode, q: string): boolean {
    if (!q) return true;
    if (node.entry.path && node.entry.path.toLowerCase().includes(q)) return true;
    return node.children.some((c) => matchesFilter(c, q));
  }

  const tree = $derived(buildTree(files));
  const q = $derived(search.toLowerCase().trim());
  const visible = $derived(tree.children.filter((c) => matchesFilter(c, q)));

  const fileCount = $derived(files.filter((f) => !f.isDir).length);
  const dirCount = $derived(files.filter((f) => f.isDir).length);
</script>

<div class="flex flex-col h-full">
  <!-- cwd + toolbar -->
  <div class="flex items-center justify-between px-4 py-2 border-b border-white-300 dark:border-navy-600 shrink-0">
    <p class="text-[11px] text-black-700 dark:text-black-600 truncate flex-1 min-w-0">{cwd}</p>
    <div class="flex items-center gap-1 shrink-0 ml-2">
      <button type="button" title="New file" onclick={onNewFile}
        class="inline-flex h-7 w-7 items-center justify-center rounded-lg text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800 transition-colors">
        <svg viewBox="0 0 16 16" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="1.5">
          <path d="M9 2H4a1 1 0 00-1 1v10a1 1 0 001 1h8a1 1 0 001-1V6L9 2z M9 2v4h4M8 8v4M6 10h4" stroke-linecap="round" stroke-linejoin="round"/>
        </svg>
      </button>
      <button type="button" title="New folder" onclick={onNewDir}
        class="inline-flex h-7 w-7 items-center justify-center rounded-lg text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800 transition-colors">
        <svg viewBox="0 0 16 16" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="1.5">
          <path d="M2 4a1 1 0 011-1h3l2 2h5a1 1 0 011 1v6a1 1 0 01-1 1H3a1 1 0 01-1-1V4z M8 8v3M6.5 9.5h3" stroke-linecap="round" stroke-linejoin="round"/>
        </svg>
      </button>
      <button type="button" title="Refresh" onclick={onRefresh}
        class="inline-flex h-7 w-7 items-center justify-center rounded-lg text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800 transition-colors">
        <svg viewBox="0 0 16 16" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="1.5">
          <path d="M2 8a6 6 0 0110.5-4M14 8a6 6 0 01-10.5 4M11 2v3h3M5 14v-3H2" stroke-linecap="round" stroke-linejoin="round"/>
        </svg>
      </button>
    </div>
  </div>

  <!-- Search + count -->
  <div class="px-4 py-2 border-b border-white-300 dark:border-navy-600 shrink-0 space-y-2">
    <div class="relative">
      <svg viewBox="0 0 16 16" class="absolute left-2.5 top-1/2 -translate-y-1/2 h-3.5 w-3.5 text-black-600 dark:text-black-700 pointer-events-none" fill="none" stroke="currentColor" stroke-width="1.5">
        <circle cx="6.5" cy="6.5" r="4.5"/>
        <path d="M10.5 10.5l3 3" stroke-linecap="round"/>
      </svg>
      <input
        type="text"
        placeholder="Filter files…"
        value={search}
        oninput={(e) => onSearch((e.currentTarget as HTMLInputElement).value)}
        class="w-full rounded-lg border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 pl-8 pr-3 py-1.5 text-xs text-black-900 dark:text-white-100 placeholder-black-600 dark:placeholder-black-700 focus:border-green-500 focus:ring-2 focus:ring-green-200 dark:focus:ring-green-800 focus:outline-none"
      />
    </div>
    <p class="text-[11px] text-black-700 dark:text-black-600">
      {fileCount} file{fileCount === 1 ? "" : "s"}{dirCount ? ` · ${dirCount} folder${dirCount === 1 ? "" : "s"}` : ""}
    </p>
  </div>

  <!-- File tree -->
  <div class="flex-1 overflow-y-auto">
    {#if files.length === 0}
      <div class="px-4 py-12 text-center text-xs text-black-700 dark:text-black-600">
        Empty. Use + to add a file or folder.
      </div>
    {:else if visible.length === 0}
      <div class="px-4 py-12 text-center text-xs text-black-700 dark:text-black-600">
        No matches.
      </div>
    {:else}
      {#each visible as node (node.entry.path)}
        <FileTreeNode {node} depth={0} forceOpen={!!q} {openDirs} {onToggleDir} {onOpen} {onDownload} {onDelete} {onNewHere} />
      {/each}
    {/if}
  </div>
</div>
