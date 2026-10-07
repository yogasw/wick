<script lang="ts">
  // Browse and edit the files of the SELECTED repo without leaving the
  // Source panel. The tree is rooted at $activeRepo and re-roots whenever
  // that selection moves — the whole point of having it here rather than in
  // the session Files panel, which always shows the session cwd.
  //
  // The list itself is the SHARED FileBrowser, the same component the
  // session's Files rail renders: the + / delete / download row actions,
  // the filter, the Subfolders search, the Name/Recent/Type chips, the
  // per-folder counts and the file meta all come with it. What lives here
  // is everything repo-shaped — which API to call, how a path maps onto the
  // session cwd, and the Monaco half on the right.
  //
  // Listings are lazy, one request per folder: a session can hold dozens of
  // clones, and preloading the tree is both slow and, past the server's cap,
  // silently incomplete.
  import { untrack } from "svelte";
  import { get } from "svelte/store";
  import * as files from "$lib/api/files";
  import type { FileEntry, FileContent } from "$lib/api/files";
  import { sessionID, activeRepo, loadStatus } from "$lib/stores/scm";
  import { langFor } from "$lib/git-actions";
  import { toastOk, toastError } from "@wick-fe/common-stores";
  import { ConfirmDialog, FileBrowser, layer, withAncestorDirs } from "@wick-fe/common-ui";
  import type { SessionFileEntry } from "@wick-fe/common-ui";
  import MonacoView from "$lib/components/MonacoView.svelte";
  import {
    sessionPath, toRepoRel, joinRel, parentRel, ancestorRels, breadcrumbs,
    baseName, isWithin, sortEntries, normalizeRel,
  } from "$lib/files-root";

  type Props = {
    // sidebar: the tree fills the dock and a file opens in an overlay.
    // full: tree on the left, editor beside it.
    mode?: "sidebar" | "full";
  };
  let { mode = "full" }: Props = $props();

  // Everything below is REPO-relative; files-root converts on the way to and
  // from the API. "" is the repo root.
  let dirs = $state<Record<string, FileEntry[]>>({});
  let loadingDirs = $state<Record<string, boolean>>({});
  let loadedDirs = $state<Record<string, boolean>>({});
  let truncatedDirs = $state<Record<string, boolean>>({});
  let expanded = $state<Record<string, boolean>>({});
  let deletingPaths = $state<Record<string, boolean>>({});
  // Entries a deep search turned up. Kept apart from the per-folder
  // listings: they are results, not a folder anyone opened, and they go
  // away with the next search rather than pretending to be loaded state.
  let found = $state<FileEntry[]>([]);
  let findTruncated = $state(false);
  // The folder the tree hangs from — moved by the breadcrumb and by the
  // "open as root" arrow on a folder row. Deep trees get unreadable in a
  // 300px dock, so scoping down is worth a click.
  let root = $state("");
  let filter = $state("");

  let openPath = $state<string | null>(null);
  let content = $state<FileContent | null>(null);
  // Non-null means the editor holds an unsaved edit.
  let buffer = $state<string | null>(null);
  let saving = $state(false);

  let creating = $state<{ isDir: boolean } | null>(null);
  let newName = $state("");
  // Which folder the pending create lands in, relative to the root.
  let createIn = $state("");
  let deleteAsk = $state<{ path: string; isDir: boolean } | null>(null);

  const crumbs = $derived(breadcrumbs($activeRepo, root));
  const editable = $derived(!!content && !content.binary && !content.tooBig);
  const lang = $derived(openPath ? langFor(openPath) : "plaintext");
  const dirty = $derived(buffer !== null);
  const downloadHref = $derived(
    openPath ? files.downloadURL($sessionID, sessionPath($activeRepo, openPath)) : "",
  );

  // The browser builds its tree by path, so every row it is handed has to
  // be relative to the folder on screen — otherwise a subtree rooted at
  // src/lib has nothing to hang "src/lib/x.ts" off and the row vanishes.
  const rootPrefix = $derived(root ? root + "/" : "");
  function underRoot(path: string): string | null {
    if (!root) return path;
    return path.startsWith(rootPrefix) ? path.slice(rootPrefix.length) : null;
  }
  const fromRoot = (rel: string) => joinRel(root, rel);

  // Filled from the first listing (see loadDir).
  let sessionCwd = $state("");

  /* Three flavours here rather than the rail's two, because a path inside the
     Source panel is relative to THREE different things and all of them get
     pasted somewhere: the repo (a PR comment, a build command), the session
     (an agent prompt, the rail's own search) and the host (a terminal). Rows
     are ordered widest-to-narrowest so the one you want most often is first.
     A row whose base is unknown is dropped, never shown wrong. */
  const fileCopyVariants = (e: { path: string; name: string }) => {
    const repoRel = fromRoot(e.path);
    const sessRel = sessionPath($activeRepo, repoRel);
    return [
      ...(sessionCwd ? [{ label: "Full path", value: `${sessionCwd}/${sessRel}` }] : []),
      { label: "Session path", value: sessRel },
      { label: "Repo path", value: repoRel },
      { label: "Name", value: e.name },
    ];
  };

  // What the browser lists: every loaded folder inside the current root,
  // plus whatever the last deep search found, all rebased on that root.
  const browserFiles = $derived.by(() => {
    const out: SessionFileEntry[] = [];
    const seen = new Set<string>();
    const add = (e: FileEntry) => {
      const rel = underRoot(e.path);
      if (rel === null || rel === "" || seen.has(rel)) return;
      seen.add(rel);
      out.push({ path: rel, name: e.name, size: e.size, isDir: e.isDir, mtime: e.mtime });
    };
    for (const [dir, entries] of Object.entries(dirs)) {
      if (dir !== root && !isWithin(root, dir)) continue;
      for (const e of entries) add(e);
    }
    for (const e of found) add(e);
    // A hit whose folders nobody expanded would have no parent to attach
    // to; give it the folders it needs rather than dropping the row.
    return withAncestorDirs(out);
  });

  // The browser keys its open folders by the same rebased paths.
  const openDirsRebased = $derived.by(() => {
    const out: Record<string, boolean> = {};
    for (const [p, v] of Object.entries(expanded)) {
      const rel = underRoot(p);
      if (rel) out[rel] = v;
    }
    return out;
  });
  function rebaseFlags(src: Record<string, boolean>): Record<string, boolean> {
    const out: Record<string, boolean> = {};
    for (const [p, v] of Object.entries(src)) {
      const rel = underRoot(p);
      if (rel !== null) out[rel === "" ? "" : rel] = v;
    }
    return out;
  }
  const loadedRebased = $derived(rebaseFlags(loadedDirs));
  const loadingRebased = $derived(rebaseFlags(loadingDirs));
  const deletingRebased = $derived(rebaseFlags(deletingPaths));

  async function loadDir(dir: string, force = false): Promise<void> {
    if (!force && dirs[dir]) return;
    const id = get(sessionID);
    if (!id) return;
    const repo = get(activeRepo);
    loadingDirs = { ...loadingDirs, [dir]: true };
    try {
      const r = await files.listDir(id, sessionPath(repo, dir));
      // The absolute session cwd rides along on every listing; keep the last
      // one so "Copy path" can offer a full path without its own request.
      if (r.cwd) sessionCwd = r.cwd;
      // A listing that lands after the user switched repos describes a tree
      // that no longer exists; toRepoRel rejects its paths rather than
      // hanging one repo's files under another.
      if (get(activeRepo) !== repo) return;
      const entries: FileEntry[] = [];
      for (const f of r.files) {
        const rel = toRepoRel(repo, f.path);
        if (!rel) continue;
        entries.push({ ...f, path: rel });
      }
      dirs = { ...dirs, [dir]: sortEntries(entries) };
      loadedDirs = { ...loadedDirs, [dir]: true };
      truncatedDirs = { ...truncatedDirs, [dir]: r.truncated === true };
    } catch (e) {
      toastError("Files", String(e));
    } finally {
      loadingDirs = { ...loadingDirs, [dir]: false };
    }
  }

  function toggleDir(rel: string) {
    const path = fromRoot(rel);
    const next = !expanded[path];
    expanded = { ...expanded, [path]: next };
    if (next) void loadDir(path);
  }

  function setRoot(path: string) {
    root = path;
    filter = "";
    found = [];
    void loadDir(path);
  }

  // Deep search runs on the server — the tree is loaded a level at a time,
  // so a folder nobody has opened is simply not here to filter, and finding
  // one is the main reason to search at all. Results outside the selected
  // repo (the endpoint is session-wide) are dropped.
  async function deepFind(q: string): Promise<void> {
    const id = get(sessionID);
    if (!id) return;
    const repo = get(activeRepo);
    try {
      const r = await files.searchFiles(id, q);
      if (get(activeRepo) !== repo) return;
      const hits: FileEntry[] = [];
      for (const f of r.files) {
        const rel = toRepoRel(repo, f.path);
        if (rel === null || underRoot(rel) === null) continue;
        hits.push({ ...f, path: rel });
      }
      found = hits;
      findTruncated = r.truncated;
    } catch {
      // A failed search leaves the tree as it was.
    }
  }

  // Go to file: the same endpoint, scoped to this repo, ranked by the
  // browser. Files only — the quick-open opens something.
  async function quickFind(q: string): Promise<SessionFileEntry[]> {
    const id = get(sessionID);
    if (!id) return [];
    const repo = get(activeRepo);
    try {
      const r = await files.searchFiles(id, q);
      if (get(activeRepo) !== repo) return [];
      const out: SessionFileEntry[] = [];
      for (const f of r.files) {
        const rel = toRepoRel(repo, f.path);
        if (rel === null) continue;
        const under = underRoot(rel);
        if (under === null || under === "") continue;
        out.push({ path: under, name: f.name, size: f.size, isDir: f.isDir, mtime: f.mtime });
      }
      return out;
    } catch {
      return [];
    }
  }

  // Leaving a dirty buffer behind silently is the one way this panel could
  // lose work, so it asks — the same shape deleteBranch uses for git's
  // "not fully merged" refusal.
  function mayLeave(): boolean {
    if (buffer === null || !openPath) return true;
    return confirm(`Discard unsaved changes to ${baseName(openPath)}?`);
  }

  function openEntry(f: SessionFileEntry) {
    if (f.isDir) return;
    void openFile(fromRoot(f.path));
  }

  async function openFile(path: string) {
    if (openPath === path) return;
    if (!mayLeave()) return;
    openPath = path;
    content = null;
    buffer = null;
    const repo = get(activeRepo);
    try {
      const c = await files.readFile(get(sessionID), sessionPath(repo, path));
      if (get(activeRepo) !== repo || openPath !== path) return;
      content = c;
    } catch (e) {
      toastError("Open failed", String(e));
      if (openPath === path) openPath = null;
    }
  }

  function closeFile() {
    if (!mayLeave()) return;
    openPath = null;
    content = null;
    buffer = null;
  }

  async function save() {
    const path = openPath;
    const text = buffer;
    if (path === null || text === null) return;
    saving = true;
    const repo = get(activeRepo);
    try {
      await files.saveFile(get(sessionID), sessionPath(repo, path), text);
      if (openPath === path && get(activeRepo) === repo) {
        content = content ? { ...content, content: text } : content;
        buffer = null;
      }
      toastOk("Saved", path);
      // The file is very likely tracked: refresh the snapshot so the Changes
      // tab and the rail badge count the edit that just happened.
      void loadStatus();
    } catch (e) {
      toastError("Save failed", String(e));
    } finally {
      saving = false;
    }
  }

  // New file / new folder. The browser's toolbar asks for one at the root;
  // a folder row's + asks for one inside that folder.
  function startCreate(isDir: boolean, dir = "") {
    creating = { isDir };
    newName = "";
    createIn = dir;
  }

  async function submitCreate() {
    const c = creating;
    if (!c) return;
    // A name may carry slashes — "lib/util.ts" creates the folder too, which
    // is quicker than making each level by hand.
    const name = normalizeRel(newName);
    if (!name) return;
    const path = joinRel(joinRel(root, createIn), name);
    const repo = get(activeRepo);
    try {
      await files.createEntry(get(sessionID), sessionPath(repo, path), c.isDir);
      creating = null;
      newName = "";
      toastOk(c.isDir ? "Folder created" : "File created", path);
      // Re-list every folder on the way down so the new entry is visible
      // even when the name created intermediate levels.
      for (const a of ancestorRels(path)) {
        if (a) expanded = { ...expanded, [a]: true };
        await loadDir(a, true);
      }
      if (c.isDir) {
        expanded = { ...expanded, [path]: true };
        void loadDir(path, true);
      } else {
        await openFile(path);
      }
      void loadStatus();
    } catch (e) {
      toastError("Create failed", String(e));
    }
  }

  async function confirmDelete() {
    const d = deleteAsk;
    deleteAsk = null;
    if (!d) return;
    const repo = get(activeRepo);
    // The row collapses in place while the request is out, so the list does
    // not snap and lose the reader's position.
    deletingPaths = { ...deletingPaths, [d.path]: true };
    try {
      await files.deleteEntry(get(sessionID), sessionPath(repo, d.path));
      toastOk("Deleted", d.path);
      if (openPath && isWithin(d.path, openPath)) {
        openPath = null;
        content = null;
        buffer = null;
      }
      // Drop every cached listing that lived inside it — those folders are
      // gone, and keeping them would draw rows for files that no longer are.
      const keep: Record<string, FileEntry[]> = {};
      for (const [k, v] of Object.entries(dirs)) {
        if (!isWithin(d.path, k)) keep[k] = v;
      }
      dirs = keep;
      found = found.filter((f) => !isWithin(d.path, f.path));
      if (isWithin(d.path, root)) root = parentRel(d.path);
      await loadDir(parentRel(d.path), true);
      void loadStatus();
    } catch (e) {
      toastError("Delete failed", String(e));
    } finally {
      const { [d.path]: _gone, ...rest } = deletingPaths;
      deletingPaths = rest;
    }
  }

  function onKeydown(e: KeyboardEvent) {
    if ((e.ctrlKey || e.metaKey) && (e.key === "s" || e.key === "S")) {
      if (buffer === null) return;
      e.preventDefault();
      void save();
      return;
    }
  }

  function fmtSize(n: number): string {
    if (n < 1024) return `${n} B`;
    if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
    return `${(n / (1024 * 1024)).toFixed(1)} MB`;
  }

  // Re-root on every repo switch (and once the session id arrives — the
  // panel mounts before it is told which session it belongs to).
  $effect(() => {
    void $activeRepo;
    void $sessionID;
    untrack(() => {
      dirs = {};
      loadingDirs = {};
      loadedDirs = {};
      truncatedDirs = {};
      expanded = {};
      deletingPaths = {};
      found = [];
      findTruncated = false;
      root = "";
      filter = "";
      openPath = null;
      content = null;
      buffer = null;
      creating = null;
      newName = "";
      createIn = "";
      void loadDir("", true);
    });
  });
</script>

<svelte:window onkeydown={onKeydown} />

{#snippet tree()}
  <!-- Breadcrumb: where the tree is rooted, and the way back up. At the repo
       root there is nothing to say — the crumb would just repeat the repo
       name the header above already carries — so the row only appears once
       you are inside a folder, and the root itself is a home button rather
       than the repo's name spelled a second time. -->
  {#if crumbs.length > 1}
    <div class="flex items-center gap-0.5 overflow-x-auto border-b border-white-300 dark:border-navy-600 px-2 py-1 text-[11px]">
      {#each crumbs as c, i (c.path)}
        {#if i > 0}<span class="shrink-0 text-black-600">/</span>{/if}
        {#if i === 0}
          <button
            type="button"
            onclick={() => setRoot("")}
            title="Repository root"
            aria-label="Repository root"
            class="inline-flex h-5 w-5 shrink-0 items-center justify-center rounded text-black-700 transition-colors hover:bg-white-200 dark:text-black-600 dark:hover:bg-navy-800"
          >
            <svg viewBox="0 0 16 16" class="h-3 w-3" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M2 7l6-4.5L14 7M3.5 6v7h9V6" stroke-linecap="round" stroke-linejoin="round"/></svg>
          </button>
        {:else}
          <button
            type="button"
            onclick={() => setRoot(c.path)}
            class={"shrink-0 truncate rounded px-1 py-0.5 transition-colors hover:bg-white-200 dark:hover:bg-navy-800 " + (i === crumbs.length - 1 ? "font-medium text-black-900 dark:text-white-100" : "text-black-700 dark:text-black-600")}
          >{c.name}</button>
        {/if}
      {/each}
    </div>
  {/if}

  {#if creating}
    <!-- svelte-ignore a11y_autofocus -->
    <form
      class="flex items-center gap-1 border-b border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800 px-2 py-1"
      onsubmit={(e) => { e.preventDefault(); void submitCreate(); }}
    >
      <span class="shrink-0 text-[10px] text-black-600">{creating.isDir ? "Folder" : "File"} in {createIn || (crumbs.length > 1 ? crumbs[crumbs.length - 1].name : "repo root")}</span>
      <input
        type="text"
        autofocus
        bind:value={newName}
        placeholder={creating.isDir ? "name" : "name.ts"}
        onkeydown={(e) => { if (e.key === "Escape") { creating = null; newName = ""; } }}
        class="min-w-0 flex-1 rounded border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-1.5 py-0.5 font-mono text-[11px] text-black-900 dark:text-white-100 focus:border-green-500 focus:outline-none"
      />
      <button type="submit" class="shrink-0 rounded bg-green-500 px-2 py-0.5 text-[11px] font-medium text-white-100 hover:bg-green-600">Create</button>
      <button type="button" onclick={() => { creating = null; newName = ""; }} class="shrink-0 rounded border border-white-300 dark:border-navy-600 px-2 py-0.5 text-[11px] text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-700">Cancel</button>
    </form>
  {/if}

  <!-- The shared browser. cwd is deliberately empty: the panel header
       already names the repo, and the breadcrumb above says where inside it
       we are — a third copy of the same string is noise. -->
  <div class="flex min-h-0 flex-1 flex-col">
    <FileBrowser
      cwd=""
      copyVariants={fileCopyVariants}
      files={browserFiles}
      search={filter}
      openDirs={openDirsRebased}
      loadedDirs={loadedRebased}
      loadingDirs={loadingRebased}
      deletingPaths={deletingRebased}
      {findTruncated}
      onFind={(q, isDeep) => { if (isDeep) void deepFind(q); }}
      onSearch={(v) => { filter = v; if (!v) found = []; }}
      onToggleDir={toggleDir}
      onOpen={openEntry}
      onRefresh={() => loadDir(root, true)}
      onNewFile={() => startCreate(false)}
      onNewDir={() => startCreate(true)}
      onNewHere={(dir) => startCreate(false, dir)}
      onNewDirHere={(dir) => startCreate(true, dir)}
      onDownload={(p) => window.open(files.downloadURL($sessionID, sessionPath($activeRepo, fromRoot(p))), "_blank")}
      onDelete={(p) => {
        const abs = fromRoot(p);
        const entry = browserFiles.find((f) => f.path === p);
        deleteAsk = { path: abs, isDir: entry?.isDir ?? false };
      }}
      onOpenDir={(p) => setRoot(fromRoot(p))}
      onQuickFind={quickFind}
      quickScope={$activeRepo && $activeRepo !== "." ? $activeRepo : "this repo"}
      loading={!!loadingDirs[root] && !dirs[root]}
    />
    {#if truncatedDirs[root]}
      <p class="shrink-0 px-3 py-2 text-[10px] text-amber-600 dark:text-amber-400">This folder has more entries than the panel lists.</p>
    {/if}
  </div>
{/snippet}

{#snippet editor()}
  <div class="flex h-full min-h-0 flex-col">
    <div class="flex items-center gap-2 border-b border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-3 py-1.5">
      <span class="min-w-0 flex-1 truncate font-mono text-[11px] text-black-900 dark:text-white-100">{openPath}</span>
      {#if dirty}
        <button type="button" onclick={save} disabled={saving} class="shrink-0 rounded bg-green-500 px-2 py-0.5 text-[11px] font-medium text-white-100 hover:bg-green-600 disabled:opacity-50">Save</button>
        <button type="button" onclick={() => (buffer = null)} class="shrink-0 rounded border border-white-300 dark:border-navy-600 px-2 py-0.5 text-[11px] text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800">Revert edit</button>
      {/if}
      <a
        href={downloadHref}
        download
        title="Download"
        class="inline-flex h-6 w-6 shrink-0 items-center justify-center rounded text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800"
      >
        <svg viewBox="0 0 16 16" class="h-3 w-3" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M8 2v8M5 7l3 3 3-3M3 13h10" stroke-linecap="round" stroke-linejoin="round"/></svg>
      </a>
      <button type="button" onclick={closeFile} title="Close" class="inline-flex h-6 w-6 shrink-0 items-center justify-center rounded text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800">
        <svg viewBox="0 0 16 16" class="h-3 w-3" fill="none" stroke="currentColor" stroke-width="2"><path d="M4 4l8 8M12 4l-8 8" stroke-linecap="round"/></svg>
      </button>
    </div>
    <div class="min-h-0 flex-1">
      {#if !content}
        <div class="flex h-full items-center justify-center text-xs text-black-700 dark:text-black-600">Loading…</div>
      {:else if !editable}
        <!-- Bytes Monaco cannot show: a binary file, or one past the
             server's 2 MiB read cap. Say which, and offer the download. -->
        <div class="flex h-full flex-col items-center justify-center gap-2 px-6 text-center">
          <p class="text-xs text-black-700 dark:text-black-600">
            {content.binary ? "This looks like a binary file" : "This file is too large to edit here"}
            — {fmtSize(content.size)}.
          </p>
          <a href={downloadHref} download class="rounded-lg border border-white-300 dark:border-navy-600 px-2.5 py-1 text-[11px] text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800">Download</a>
        </div>
      {:else}
        <!-- Keyed on the path so switching files builds a fresh model
             instead of streaming a new value through the live one, which
             would look like the user had just typed the whole file. -->
        {#key openPath}
          <MonacoView
            mode="edit"
            modified={buffer ?? content.content ?? ""}
            language={lang}
            onChange={(v) => (buffer = v === (content?.content ?? "") ? null : v)}
          />
        {/key}
      {/if}
    </div>
  </div>
{/snippet}

{#if mode === "sidebar"}
  <div class="flex min-h-0 flex-1 flex-col">
    {@render tree()}
  </div>
  {#if openPath}
    <!-- The dock is too narrow for an editor, so the file opens over the
         panel — the same shape the Changes tab's DiffModal uses. -->
    <div
      class="fixed inset-0 z-[60] flex items-end justify-center bg-black/60 backdrop-blur-sm sm:items-center sm:p-4"
      role="presentation"
      onclick={(e) => { if (e.target === e.currentTarget) closeFile(); }}
    >
      <div use:layer={{ onEscape: closeFile }} role="dialog" aria-modal="true" aria-label={openPath} class="flex h-full w-full flex-col overflow-hidden border-t border-white-300 bg-white-100 shadow-2xl sm:h-[90vh] sm:max-w-6xl sm:rounded-2xl sm:border dark:border-navy-600 dark:bg-navy-700">
        {@render editor()}
      </div>
    </div>
  {/if}
{:else}
  <div class="flex min-h-0 w-full flex-1 overflow-hidden">
    <aside class="flex w-[300px] shrink-0 flex-col overflow-hidden border-r border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700">
      {@render tree()}
    </aside>
    <main class="flex min-w-0 flex-1 flex-col overflow-hidden bg-white-200 dark:bg-navy-800">
      {#if openPath}
        {@render editor()}
      {:else}
        <div class="flex h-full items-center justify-center text-xs text-black-700 dark:text-black-600">Select a file to edit.</div>
      {/if}
    </main>
  </div>
{/if}

<ConfirmDialog
  open={!!deleteAsk}
  title={deleteAsk?.isDir ? "Delete folder?" : "Delete file?"}
  body={deleteAsk
    ? `Delete ${deleteAsk.path}${deleteAsk.isDir ? " and everything inside it" : ""}? This removes it from disk and cannot be undone.`
    : ""}
  confirmLabel="Delete"
  cancelLabel="Cancel"
  destructive={true}
  onConfirm={confirmDelete}
  onCancel={() => (deleteAsk = null)}
/>
