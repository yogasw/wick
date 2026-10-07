<script lang="ts">
  /* The file browser: one component for the session's Files rail and for
     the Source panel's Files tab, which lists the selected repo instead of
     the session cwd. Purely presentational — every piece of state arrives
     as a prop and every action leaves as a callback, so the two callers can
     load, create and delete however their own API does it. */
  import { onDestroy } from "svelte";
  import type { SessionFileEntry } from "./file-browser-types.js";
  import type { SortKey } from "./file-browser-tree.js";
  import { buildFileTree, filterFileTree } from "./file-browser-tree.js";
  import { rankPathHits } from "./file-search.js";
  import FileBrowserNode from "./FileBrowserNode.svelte";

  type Props = {
    cwd: string;
    files: SessionFileEntry[];
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
    onOpen: (f: SessionFileEntry) => void;
    onRefresh: () => void;
    onNewFile: () => void;
    onNewDir: () => void;
    onDownload?: (path: string) => void;
    onDelete?: (path: string) => void;
    onNewHere?: (dirPath: string) => void;
    /** Create a folder inside a folder row, from its ⋮ menu. */
    onNewDirHere?: (dirPath: string) => void;
    /** Re-root the tree at a folder (Source panel only — see the node). */
    onOpenDir?: (path: string) => void;
    /** Path flavours for a row's "Copy path" — see FileBrowserNode. */
    copyVariants?: (e: SessionFileEntry) => { label: string; value: string }[];
    /** Go to file: the caller searches ITS OWN scope — the selected repo in
        the Source panel, the session cwd in the rail — and hands back
        candidates. Ranking, the list and the keys are here. Omit the prop
        and the quick-open (button and Ctrl/Cmd+P) is simply not offered. */
    onQuickFind?: (q: string) => Promise<SessionFileEntry[]>;
    /** What the quick-open says it is searching, e.g. a repo name. */
    quickScope?: string;
    loading?: boolean;
    loadError?: string;
  };

  let { cwd, files, search, openDirs, loadedDirs = {}, loadingDirs = {}, deletingPaths = {}, onFind = () => {}, findTruncated = false, onSearch, onToggleDir, onOpen, onRefresh, onNewFile, onNewDir, onDownload = () => {}, onDelete = () => {}, onNewHere = () => {}, onNewDirHere, onOpenDir, copyVariants, onQuickFind, quickScope = "", loading = false, loadError = "" }: Props = $props();

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

  // Typing filters as you type. The box holds the keystrokes; the term the
  // tree (and the server search) actually sees lands one quiet beat later,
  // so a 4000-row tree is not refiltered on every letter and the deep
  // search does not fire a request per keystroke. No Enter anywhere.
  const DEBOUNCE_MS = 150;
  let draft = $state(search);
  let emitted = search;
  let typeTimer: ReturnType<typeof setTimeout> | null = null;

  // A term changed from OUTSIDE (a parent resetting it, a tab switch) is
  // adopted; our own echo is ignored, so typing is never fought by the
  // value coming back down.
  $effect(() => {
    if (search !== emitted) {
      emitted = search;
      draft = search;
    }
  });

  function typeFilter(v: string) {
    draft = v;
    if (typeTimer) clearTimeout(typeTimer);
    typeTimer = setTimeout(() => {
      emitted = v;
      onSearch(v);
    }, DEBOUNCE_MS);
  }

  function clearFilter() {
    if (typeTimer) clearTimeout(typeTimer);
    draft = "";
    emitted = "";
    onSearch("");
  }

  // Go to file. Walking the tree is fine when you are browsing and useless
  // when you already know the name — so this jumps straight there, over the
  // caller's whole scope rather than the level on screen.
  let quickOpen = $state(false);
  let quickQuery = $state("");
  let quickHits = $state<SessionFileEntry[]>([]);
  let quickBusy = $state(false);
  let quickIdx = $state(0);
  let quickTimer: ReturnType<typeof setTimeout> | null = null;
  // Responses can land out of order; only the newest query may paint.
  let quickSeq = 0;

  function openQuick() {
    if (!onQuickFind) return;
    quickOpen = true;
    quickIdx = 0;
    // Whatever was typed in the filter is usually what you were looking
    // for — carry it over rather than making it be typed twice.
    quickQuery = draft;
    if (quickQuery.trim()) runQuick(quickQuery);
    else quickHits = [];
  }

  function closeQuick() {
    quickOpen = false;
    if (quickTimer) clearTimeout(quickTimer);
    quickTimer = null;
    quickBusy = false;
  }

  function typeQuick(v: string) {
    quickQuery = v;
    if (quickTimer) clearTimeout(quickTimer);
    quickTimer = setTimeout(() => runQuick(v), DEBOUNCE_MS);
  }

  function runQuick(v: string) {
    const term = v.trim();
    if (!onQuickFind || term === "") {
      quickHits = [];
      quickBusy = false;
      return;
    }
    const seq = ++quickSeq;
    quickBusy = true;
    onQuickFind(term)
      .then((found) => {
        if (seq !== quickSeq) return;
        quickHits = rankPathHits(found, term);
        quickIdx = 0;
      })
      .catch(() => {
        if (seq === quickSeq) quickHits = [];
      })
      .finally(() => {
        if (seq === quickSeq) quickBusy = false;
      });
  }

  function quickKeydown(e: KeyboardEvent) {
    if (e.key === "Escape") {
      e.preventDefault();
      closeQuick();
      return;
    }
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      if (quickHits.length === 0) return;
      const next = quickIdx + (e.key === "ArrowDown" ? 1 : -1);
      quickIdx = Math.min(quickHits.length - 1, Math.max(0, next));
      return;
    }
    if (e.key === "Enter") {
      e.preventDefault();
      const hit = quickHits[quickIdx];
      if (hit) chooseQuick(hit);
    }
  }

  function chooseQuick(f: SessionFileEntry) {
    closeQuick();
    onOpen(f);
  }

  // Ctrl/Cmd+P, the shortcut every editor uses for this. The listener only
  // exists while the browser is on screen, so it cannot steal the key from
  // a page that is showing something else.
  function windowKeydown(e: KeyboardEvent) {
    if (!onQuickFind) return;
    if ((e.ctrlKey || e.metaKey) && (e.key === "p" || e.key === "P")) {
      e.preventDefault();
      if (quickOpen) closeQuick();
      else openQuick();
    }
  }

  onDestroy(() => {
    if (typeTimer) clearTimeout(typeTimer);
    if (quickTimer) clearTimeout(quickTimer);
  });

  const tree = $derived(buildFileTree(files, sortKey));
  const q = $derived(search.toLowerCase().trim());
  const visible = $derived(filterFileTree(tree, q, deep).children);

  // Deep search has to go to the server: the tree is loaded a level at a
  // time, so a folder nobody has expanded is simply not here to filter —
  // and finding one is the main reason to search at all. Shallow search
  // stays local; it only ever means "the names in front of me".
  $effect(() => {
    if (deep && q) onFind(q, true);
  });

  // The header counts THIS folder, not everything loaded. Since the tree
  // fetches a level at a time, counting every entry in memory made the
  // number climb each time someone expanded something — "406 files · 116
  // folders" for a directory that holds 326 and 84. A per-folder count
  // already sits on each folder row; this line is the root's.
  const rootEntries = $derived(files.filter((f) => !f.path.includes("/")));
  const fileCount = $derived(rootEntries.filter((f) => !f.isDir).length);
  const dirCount = $derived(rootEntries.length - fileCount);
  // Deep search reaches past this level, so its match count is allowed to;
  // the shallow filter only ever looks at names in front of you.
  const matchCount = $derived(
    q ? (deep ? files : rootEntries).filter((f) => f.name.toLowerCase().includes(q)).length : 0,
  );
</script>

<svelte:window onkeydown={windowKeydown} />

<div class="flex flex-col h-full">
  <!-- The working directory, when the caller has one to show. Just the path:
       the actions used to sit here too, which left the Source panel — whose
       repo name lives in its own header — with a bare strip of three icons
       above the filter, and left the path itself truncating at half width. -->
  {#if cwd}
    <div class="flex items-center px-4 py-2 border-b border-white-300 dark:border-navy-600 shrink-0">
      <p class="text-[11px] text-black-700 dark:text-black-600 truncate min-w-0">{cwd}</p>
    </div>
  {/if}

  <!-- Search + count -->
  <div class="px-4 py-2 border-b border-white-300 dark:border-navy-600 shrink-0 space-y-2">
    <div class="flex items-center gap-1">
    <div class="relative min-w-0 flex-1">
      <svg viewBox="0 0 16 16" class="absolute left-2.5 top-1/2 -translate-y-1/2 h-3.5 w-3.5 text-black-600 dark:text-black-700 pointer-events-none" fill="none" stroke="currentColor" stroke-width="1.5">
        <circle cx="6.5" cy="6.5" r="4.5"/>
        <path d="M10.5 10.5l3 3" stroke-linecap="round"/>
      </svg>
      <input
        type="text"
        placeholder={deep ? "Search all subfolders…" : "Filter this folder…"}
        value={draft}
        oninput={(e) => typeFilter((e.currentTarget as HTMLInputElement).value)}
        class="w-full rounded-lg border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 pl-8 {search ? 'pr-8' : 'pr-3'} py-1.5 text-xs text-black-900 dark:text-white-100 placeholder-black-600 dark:placeholder-black-700 focus:border-green-500 focus:ring-2 focus:ring-green-200 dark:focus:ring-green-800 focus:outline-none"
      />
      {#if draft}
        <button
          type="button"
          title="Clear"
          aria-label="Clear filter"
          onclick={clearFilter}
          class="absolute right-2 top-1/2 -translate-y-1/2 inline-flex h-4 w-4 items-center justify-center rounded text-black-700 dark:text-black-600 hover:bg-white-300 dark:hover:bg-navy-600"
        >
          <svg viewBox="0 0 12 12" class="h-2.5 w-2.5" fill="none" stroke="currentColor" stroke-width="1.5">
            <path d="M3 3l6 6M9 3l-6 6" stroke-linecap="round"/>
          </svg>
        </button>
      {/if}
    </div>
    {#if onQuickFind}
      <button
        type="button"
        onclick={openQuick}
        title="Go to file (Ctrl/Cmd+P)"
        aria-label="Go to file"
        class="inline-flex h-7 w-7 shrink-0 items-center justify-center rounded-lg text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800 transition-colors"
      >
        <!-- A page with a magnifier. It sat next to "new file" wearing a page
             with a PLUS, and at 14px the two were the same button twice. -->
        <svg viewBox="0 0 16 16" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="1.5">
          <path d="M12 7.5V5L9 2H3a1 1 0 00-1 1v11a1 1 0 001 1h3.5M9 2v3h3" stroke-linejoin="round" stroke-linecap="round"/>
          <circle cx="10.5" cy="10.5" r="2.5"/>
          <path d="M12.4 12.4L14.5 14.5" stroke-linecap="round"/>
        </svg>
      </button>
    {/if}
      <button type="button" title="New file" aria-label="New file" onclick={onNewFile}
        class="inline-flex h-7 w-7 shrink-0 items-center justify-center rounded-lg text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800 transition-colors">
        <svg viewBox="0 0 16 16" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="1.5">
          <path d="M9 2H4a1 1 0 00-1 1v10a1 1 0 001 1h8a1 1 0 001-1V6L9 2z M9 2v4h4M8 8v4M6 10h4" stroke-linecap="round" stroke-linejoin="round"/>
        </svg>
      </button>
      <button type="button" title="New folder" aria-label="New folder" onclick={onNewDir}
        class="inline-flex h-7 w-7 shrink-0 items-center justify-center rounded-lg text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800 transition-colors">
        <svg viewBox="0 0 16 16" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="1.5">
          <path d="M2 4a1 1 0 011-1h3l2 2h5a1 1 0 011 1v6a1 1 0 01-1 1H3a1 1 0 01-1-1V4z M8 8v3M6.5 9.5h3" stroke-linecap="round" stroke-linejoin="round"/>
        </svg>
      </button>
      <button type="button" title="Refresh" aria-label="Refresh" onclick={onRefresh}
        class="inline-flex h-7 w-7 shrink-0 items-center justify-center rounded-lg text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800 transition-colors">
        <svg viewBox="0 0 16 16" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="1.5">
          <path d="M2 8a6 6 0 0110.5-4M14 8a6 6 0 01-10.5 4M11 2v3h3M5 14v-3H2" stroke-linecap="round" stroke-linejoin="round"/>
        </svg>
      </button>
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
        <FileBrowserNode {node} depth={0} forceOpen={!!q && deep} {openDirs} {loadedDirs} {loadingDirs} {deletingPaths} {onToggleDir} {onOpen} {onDownload} {onDelete} {onNewHere} {onNewDirHere} {onOpenDir} {copyVariants} />
      {/each}
    {/if}
  </div>
</div>

{#if quickOpen}
  <!-- Go to file. A viewport overlay rather than a dropdown under the box:
       the browser lives in a 320px rail in one caller and a dock in the
       other, and a list of full paths fits neither. -->
  <div
    class="fixed inset-0 z-[70] flex items-start justify-center bg-black/50 backdrop-blur-sm p-4 pt-[12vh]"
    role="presentation"
    onclick={(e) => { if (e.target === e.currentTarget) closeQuick(); }}
  >
    <div class="w-full max-w-xl overflow-hidden rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-2xl">
      <div class="flex items-center gap-2 border-b border-white-300 dark:border-navy-600 px-3 py-2">
        <svg viewBox="0 0 16 16" class="h-3.5 w-3.5 shrink-0 text-black-600 dark:text-black-700" fill="none" stroke="currentColor" stroke-width="1.5">
          <circle cx="6.5" cy="6.5" r="4.5"/><path d="M10.5 10.5l3 3" stroke-linecap="round"/>
        </svg>
        <!-- svelte-ignore a11y_autofocus -->
        <input
          type="text"
          autofocus
          value={quickQuery}
          oninput={(e) => typeQuick((e.currentTarget as HTMLInputElement).value)}
          onkeydown={quickKeydown}
          placeholder={quickScope ? `Go to file in ${quickScope}…` : "Go to file…"}
          class="min-w-0 flex-1 bg-transparent text-sm text-black-900 dark:text-white-100 placeholder-black-600 dark:placeholder-black-700 focus:outline-none"
        />
        {#if quickBusy}
          <svg viewBox="0 0 16 16" class="h-3 w-3 shrink-0 animate-spin text-black-600" fill="none" stroke="currentColor" stroke-width="2">
            <circle cx="8" cy="8" r="6" opacity="0.25"/><path d="M14 8a6 6 0 00-6-6" stroke-linecap="round"/>
          </svg>
        {/if}
        <kbd class="shrink-0 rounded border border-white-300 dark:border-navy-600 px-1 text-[10px] text-black-700 dark:text-black-600">Esc</kbd>
      </div>
      <div class="max-h-[50vh] overflow-y-auto">
        {#if quickQuery.trim() === ""}
          <p class="px-3 py-6 text-center text-xs text-black-700 dark:text-black-600">Type part of a file name or path.</p>
        {:else if quickHits.length === 0}
          <p class="px-3 py-6 text-center text-xs text-black-700 dark:text-black-600">{quickBusy ? "Searching…" : "No file matches."}</p>
        {:else}
          {#each quickHits as hit, i (hit.path)}
            <button
              type="button"
              onclick={() => chooseQuick(hit)}
              onmouseenter={() => (quickIdx = i)}
              class={"flex w-full items-baseline gap-2 px-3 py-1.5 text-left " + (i === quickIdx ? "bg-white-300 dark:bg-navy-600" : "hover:bg-white-200 dark:hover:bg-navy-800")}
            >
              <span class="shrink-0 text-xs font-medium text-black-900 dark:text-white-100">{hit.name}</span>
              <span class="min-w-0 flex-1 truncate font-mono text-[10px] text-black-700 dark:text-black-600">{hit.path}</span>
            </button>
          {/each}
        {/if}
      </div>
    </div>
  </div>
{/if}
