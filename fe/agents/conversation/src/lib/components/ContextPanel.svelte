<script lang="ts">
  import type { ContextFileEntry } from "../types/agents.js";
  import FileTreeNode from "./FileTreeNode.svelte";

  type TreeNode = { entry: ContextFileEntry; children: TreeNode[] };

  type Props = {
    cwd: string;
    files: ContextFileEntry[];
    search: string;
    openDirs: Record<string, boolean>;
    loadedDirs?: Record<string, boolean>;
    loadingDirs?: Record<string, boolean>;
    /** Rows mid-delete: shown collapsing rather than vanishing. */
    deletingPaths?: Record<string, boolean>;
    /** Whole-tree search, run on the server. The client holds only the
        levels someone has opened, so deep search cannot be done here. */
    onFind?: (q: string, deep: boolean) => void;
    /** The server capped the last deep search. */
    findTruncated?: boolean;
    onSearch: (s: string) => void;
    onToggleDir: (path: string) => void;
    onOpen: (f: ContextFileEntry) => void;
    onRefresh: () => void;
    onNewFile: () => void;
    onNewDir: () => void;
    onDownload?: (path: string) => void;
    onDelete?: (path: string) => void;
    onNewHere?: (dirPath: string) => void;
    loading?: boolean;
    loadError?: string;
  };

  let { cwd, files, search, openDirs, loadedDirs = {}, loadingDirs = {}, deletingPaths = {}, onFind = () => {}, findTruncated = false, onSearch, onToggleDir, onOpen, onRefresh, onNewFile, onNewDir, onDownload = () => {}, onDelete = () => {}, onNewHere = () => {}, loading = false, loadError = "" }: Props = $props();

  type SortKey = "name" | "recent" | "type";

  const SORT_KEY = "wick.files.sort";
  const DEEP_KEY = "wick.files.deep";

  // Explorer defaults: folders first, sorted by name, and the filter only
  // looks at the level you are actually looking at. Searching the whole
  // 4000-file tree by default made "find a folder" slow and buried the
  // hit under every nested path that happened to contain the string.
  let sortKey = $state<SortKey>(readSort());
  let deep = $state<boolean>(readDeep());

  function readSort(): SortKey {
    try {
      const v = localStorage.getItem(SORT_KEY);
      return v === "recent" || v === "type" ? v : "name";
    } catch {
      return "name";
    }
  }
  function readDeep(): boolean {
    try {
      return localStorage.getItem(DEEP_KEY) === "1";
    } catch {
      return false;
    }
  }
  function setSort(k: SortKey) {
    sortKey = k;
    try { localStorage.setItem(SORT_KEY, k); } catch { /* ignore */ }
  }
  function toggleDeep() {
    deep = !deep;
    try { localStorage.setItem(DEEP_KEY, deep ? "1" : "0"); } catch { /* ignore */ }
  }

  function ext(name: string): string {
    const i = name.lastIndexOf(".");
    return i <= 0 ? "" : name.slice(i + 1).toLowerCase();
  }

  // Folders always come before files — the one ordering rule every file
  // manager shares — then the chosen key decides within each group.
  function compare(a: TreeNode, b: TreeNode, key: SortKey): number {
    if (a.entry.isDir !== b.entry.isDir) return a.entry.isDir ? -1 : 1;
    const byName = a.entry.name.localeCompare(b.entry.name, undefined, {
      numeric: true,
      sensitivity: "base",
    });
    if (key === "recent") return (b.entry.mtime ?? 0) - (a.entry.mtime ?? 0) || byName;
    if (key === "type" && !a.entry.isDir) {
      const byExt = ext(a.entry.name).localeCompare(ext(b.entry.name));
      if (byExt !== 0) return byExt;
    }
    return byName;
  }

  function sortTree(node: TreeNode, key: SortKey): void {
    node.children.sort((a, b) => compare(a, b, key));
    for (const c of node.children) sortTree(c, key);
  }

  function buildTree(entries: ContextFileEntry[], key: SortKey): TreeNode {
    const root: TreeNode = { entry: { path: "", name: "", isDir: true, size: 0, mtime: 0 }, children: [] };
    const byPath: Record<string, TreeNode> = { "": root };
    for (const e of entries) {
      byPath[e.path] = { entry: e, children: [] };
    }
    for (const p of Object.keys(byPath)) {
      if (p === "") continue;
      const parent = p.indexOf("/") === -1 ? "" : p.slice(0, p.lastIndexOf("/"));
      if (byPath[parent]) byPath[parent].children.push(byPath[p]);
    }
    sortTree(root, key);
    return root;
  }

  function nameMatches(node: TreeNode, q: string): boolean {
    return node.entry.name.toLowerCase().includes(q);
  }

  // Shallow (default): each level is filtered by its OWN names, like the
  // filter box in a file manager — a folder that matches keeps its whole
  // subtree so you can browse into it, one that doesn't is simply gone.
  // Deep: keep any branch leading to a match and expand it.
  function filterTree(node: TreeNode, q: string, recursive: boolean): TreeNode {
    if (!q) return node;
    const kept: TreeNode[] = [];
    for (const c of node.children) {
      if (nameMatches(c, q)) {
        kept.push(c);
        continue;
      }
      if (!recursive || !c.entry.isDir) continue;
      const sub = filterTree(c, q, true);
      if (sub.children.length > 0) kept.push({ entry: c.entry, children: sub.children });
    }
    return { entry: node.entry, children: kept };
  }

  const tree = $derived(buildTree(files, sortKey));
  const q = $derived(search.toLowerCase().trim());
  const visible = $derived(filterTree(tree, q, deep).children);

  // Deep search has to go to the server: the tree is loaded a level at a
  // time, so a folder nobody has expanded is simply not here to filter —
  // and finding one is the main reason to search at all. Shallow search
  // stays local; it only ever means "the names in front of me".
  $effect(() => {
    if (deep && q) onFind(q, true);
  });

  const fileCount = $derived(files.filter((f) => !f.isDir).length);
  const dirCount = $derived(files.filter((f) => f.isDir).length);
  const matchCount = $derived(
    q ? files.filter((f) => f.name.toLowerCase().includes(q)).length : 0,
  );
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
        placeholder={deep ? "Search all subfolders…" : "Filter this folder…"}
        value={search}
        oninput={(e) => onSearch((e.currentTarget as HTMLInputElement).value)}
        class="w-full rounded-lg border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 pl-8 {search ? 'pr-8' : 'pr-3'} py-1.5 text-xs text-black-900 dark:text-white-100 placeholder-black-600 dark:placeholder-black-700 focus:border-green-500 focus:ring-2 focus:ring-green-200 dark:focus:ring-green-800 focus:outline-none"
      />
      {#if search}
        <button
          type="button"
          title="Clear"
          aria-label="Clear filter"
          onclick={() => onSearch("")}
          class="absolute right-2 top-1/2 -translate-y-1/2 inline-flex h-4 w-4 items-center justify-center rounded text-black-700 dark:text-black-600 hover:bg-white-300 dark:hover:bg-navy-600"
        >
          <svg viewBox="0 0 12 12" class="h-2.5 w-2.5" fill="none" stroke="currentColor" stroke-width="1.5">
            <path d="M3 3l6 6M9 3l-6 6" stroke-linecap="round"/>
          </svg>
        </button>
      {/if}
    </div>

    <!-- Scope + sort. Scope defaults to this folder only; sort defaults to
         name with folders first — the shape people expect from a file
         manager, rather than one flat always-recursive search. -->
    <div class="flex items-center justify-between gap-2">
      <button
        type="button"
        onclick={toggleDeep}
        title={deep ? "Searching every subfolder — click for this folder only" : "Searching this folder only — click to include subfolders"}
        class="inline-flex items-center gap-1 rounded-lg border px-2 py-1 text-[11px] transition-colors
               {deep
          ? 'border-green-500 text-green-600 dark:text-green-400 bg-green-50 dark:bg-green-900/20'
          : 'border-white-300 dark:border-navy-600 text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800'}"
      >
        <svg viewBox="0 0 16 16" class="h-3 w-3" fill="none" stroke="currentColor" stroke-width="1.5">
          <path d="M2 4a1 1 0 011-1h3l2 2h5a1 1 0 011 1v6a1 1 0 01-1 1H3a1 1 0 01-1-1V4z" stroke-linejoin="round"/>
        </svg>
        Subfolders
      </button>

      <div class="inline-flex items-center rounded-lg border border-white-300 dark:border-navy-600 overflow-hidden">
        {#each [["name", "Name"], ["recent", "Recent"], ["type", "Type"]] as [key, label]}
          <button
            type="button"
            onclick={() => setSort(key as SortKey)}
            title={`Sort by ${label.toLowerCase()} (folders first)`}
            class="px-2 py-1 text-[11px] transition-colors
                   {sortKey === key
              ? 'bg-white-300 dark:bg-navy-600 text-black-900 dark:text-white-100 font-medium'
              : 'text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800'}"
          >{label}</button>
        {/each}
      </div>
    </div>

    <p class="text-[11px] text-black-700 dark:text-black-600">
      {#if q}
        {matchCount} match{matchCount === 1 ? "" : "es"} · {deep ? "all subfolders" : "this folder"}{findTruncated && deep ? " · capped" : ""}
      {:else}
        {fileCount} file{fileCount === 1 ? "" : "s"}{dirCount ? ` · ${dirCount} folder${dirCount === 1 ? "" : "s"}` : ""}
      {/if}
    </p>
  </div>

  <!-- File tree -->
  <div class="flex-1 overflow-y-auto">
    {#if loading}
      <div class="px-4 py-12 text-center text-xs text-black-700 dark:text-black-600" data-loading-placeholder>
        Loading files…
      </div>
    {:else if loadError}
      <div class="px-4 py-12 text-center text-xs text-neg-400" data-load-error>
        {loadError}
      </div>
    {:else if files.length === 0}
      <div class="px-4 py-12 text-center text-xs text-black-700 dark:text-black-600">
        Empty. Use + to add a file or folder.
      </div>
    {:else if visible.length === 0}
      <div class="px-4 py-12 text-center text-xs text-black-700 dark:text-black-600">
        No matches.
      </div>
    {:else}
      {#each visible as node (node.entry.path)}
        <FileTreeNode {node} depth={0} forceOpen={!!q && deep} {openDirs} {loadedDirs} {loadingDirs} {deletingPaths} {onToggleDir} {onOpen} {onDownload} {onDelete} {onNewHere} />
      {/each}
    {/if}
  </div>
</div>
