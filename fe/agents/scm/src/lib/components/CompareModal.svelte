<script lang="ts">
  // Compare two refs, JetBrains-style: pick base ↔ head, read the list of
  // what differs, and act on it.
  //
  // It is an overlay on the whole VIEWPORT rather than a tab in the panel,
  // because a compare carries two ref lists AND a file list that can run to
  // hundreds of rows — none of which fits the 320px dock the panel lives in
  // half the time. Opened from the repo menu in both layouts, so it is the
  // same big surface either way.
  import { get } from "svelte/store";
  import { portal } from "$lib/portal";
  import MonacoView from "$lib/components/MonacoView.svelte";
  import CompareTreeNode from "$lib/components/CompareTreeNode.svelte";
  import { splitPath } from "$lib/tree";
  import { byPath, compareTree, statsByPath, visibleFiles } from "$lib/compare-tree";
  import { ConfirmDialog, layer } from "@wick-fe/common-ui";
  import * as api from "$lib/api/scm";
  import type { HistoryRef } from "$lib/api/scm";
  import { sessionID, activeRepo, branch } from "$lib/stores/scm";
  import {
    compareRefs,
    loadRefCompare,
    restoreFromRef,
    resetCurrentTo,
    langFor,
    type CompareData,
    type CompareFile,
    type CompareResult,
  } from "$lib/git-actions";
  import { toastError } from "@wick-fe/common-stores";

  type Props = { onClose: () => void };
  let { onClose }: Props = $props();

  let refs = $state<HistoryRef[]>([]);
  let trunk = $state("");
  let base = $state("");
  let head = $state("");
  // Merge-base (`...`) is what a reviewer means by "compare these branches"
  // — what head ADDS, ignoring whatever base gained meanwhile — so it is
  // the default, the way it is in JetBrains. `..` diffs the two tips.
  let threeDot = $state(true);

  let result = $state<CompareResult | null>(null);
  let listLoading = $state(false);
  let selected = $state<CompareFile | null>(null);
  let sides = $state<CompareData | null>(null);
  let sideBySide = $state(true);
  let busy = $state(false);
  // Tree by default: a compare routinely runs to a few hundred files, and
  // as a flat list that is a wall of long paths — which is what the folder
  // view fixes. The Changes dock defaults to tree for the same reason.
  let viewMode = $state<"tree" | "list">(readViewMode());
  // Folder collapse state, keyed by folder path, default expanded — the
  // same shape and the same rule the Changes tree uses.
  let expanded = $state<Record<string, boolean>>({});
  // The scroll container, for putting a keyboard-selected row back in view.
  let listEl = $state<HTMLElement | null>(null);

  // Which picker is open, and its filter box. One pair of variables for
  // both pickers: only one can be open at a time anyway.
  let picker = $state<"base" | "head" | null>(null);
  let filter = $state("");

  // Pending confirmation. Every rollback goes through this, spelling out
  // what git will do — none of them is obvious from a button label.
  type Ask = {
    title: string;
    body: string;
    confirmLabel: string;
    destructive: boolean;
    run: () => Promise<unknown>;
  };
  let ask = $state<Ask | null>(null);
  let resetMenu = $state(false);

  // The last pair, per repository. Compare is a habit inside ONE repo
  // ("my branch against master"), so re-picking both refs on every open is
  // exactly the friction that makes people stop opening it. Keyed by repo
  // rel, next to the panel's other localStorage keys.
  const pairKey = (repo: string) => `wick.scm.compare.${repo}`;

  function readPair(repo: string): { base: string; head: string } | null {
    try {
      const raw = localStorage.getItem(pairKey(repo));
      if (!raw) return null;
      const v = JSON.parse(raw) as { base?: string; head?: string };
      return v.base && v.head ? { base: v.base, head: v.head } : null;
    } catch {
      return null;
    }
  }

  // The tree/list choice is a habit about reading, not about one repo, so
  // it is remembered globally — next to the per-repo pair, and next to the
  // panel's own wick.scm.viewMode.
  const VIEW_KEY = "wick.scm.compare.view";

  function readViewMode(): "tree" | "list" {
    try {
      return localStorage.getItem(VIEW_KEY) === "list" ? "list" : "tree";
    } catch {
      return "tree";
    }
  }

  function setViewMode(m: "tree" | "list") {
    viewMode = m;
    try { localStorage.setItem(VIEW_KEY, m); } catch { /* ignore */ }
  }

  function toggleDir(path: string) {
    expanded[path] = expanded[path] === false ? true : false;
  }

  function writePair(repo: string, b: string, h: string) {
    try {
      localStorage.setItem(pairKey(repo), JSON.stringify({ base: b, head: h }));
    } catch { /* private mode, full quota — not worth a toast */ }
  }

  // Focus the base picker as the overlay opens: choosing what to compare
  // against is the first thing anyone does here, and it makes Tab start
  // somewhere sensible instead of at the document.
  function autofocus(node: HTMLElement, enabled: boolean) {
    if (enabled) node.focus();
  }

  // Ref list. Both endpoints are asked: /refs carries the shas and the
  // trunk/current flags the pickers show, /branches is what the rest of
  // the panel already trusts for "which branches exist". Neither lists
  // tags — the filter box doubles as a text field for those and for a
  // raw sha.
  async function loadRefs() {
    const id = get(sessionID);
    const repo = get(activeRepo);
    if (!id) return;
    try {
      const [hr, bl] = await Promise.all([
        api.getHistoryRefs(id, repo),
        api.getBranches(id, repo),
      ]);
      const seen = new Set(hr.refs.map((r) => r.name));
      const extra: HistoryRef[] = [...bl.branches, ...(bl.remotes ?? [])]
        .filter((n) => n && !seen.has(n))
        .map((n) => ({ name: n, sha: "", remote: n.includes("/"), current: false, trunk: false }));
      refs = [...hr.refs, ...extra];
      trunk = hr.trunk;
      // A remembered pair only wins while both of its refs still exist:
      // a branch deleted since last time would otherwise greet every open
      // with git's error instead of a compare.
      const last = readPair(repo);
      const known = (n: string) => refs.some((r) => r.name === n);
      const resume = last && known(last.base) && known(last.head) ? last : null;
      if (!head) head = resume?.head || bl.current || get(branch)?.name || "";
      if (!base) base = resume?.base || defaultBase(hr.trunk, head, refs);
    } catch (e) {
      toastError("Compare", String(e));
    }
  }

  // Base defaults to the trunk — the question people actually open this
  // for is "what does my branch add". When the checked-out branch IS the
  // trunk, fall back to its upstream, then to any other ref, so the view
  // never opens on a ref compared with itself.
  function defaultBase(tr: string, hd: string, list: HistoryRef[]): string {
    if (tr && tr !== hd) return tr;
    const up = get(branch)?.upstream;
    if (up && up !== hd) return up;
    return list.find((r) => r.name !== hd)?.name ?? hd;
  }

  $effect(() => {
    // Re-read the refs when the selected repo changes underneath us.
    void $activeRepo;
    base = "";
    head = "";
    result = null;
    selected = null;
    // Folder state belongs to the tree that is going away with the repo.
    expanded = {};
    void loadRefs();
  });

  // Declared after the reset effect above, so a repo switch has already
  // blanked the pair by the time this runs — otherwise the new repo would
  // be handed the old repo's refs to remember.
  $effect(() => {
    const repo = $activeRepo;
    if (base && head) writePair(repo, base, head);
  });

  const listKey = $derived(base && head ? `${$activeRepo}|${base}|${head}|${threeDot}` : "");
  let listLoaded = "";
  $effect(() => {
    const key = listKey;
    if (!key || key === listLoaded) return;
    listLoaded = key;
    void reload();
  });

  async function reload() {
    if (!base || !head) return;
    listLoading = true;
    const prev = selected?.path;
    try {
      const r = await compareRefs(base, head, threeDot);
      result = r;
      const files = r?.files ?? [];
      // Keep the file that was open when the toggle or a rollback changed
      // the list under it; otherwise start at the top.
      selected = files.find((f) => f.path === prev) ?? files[0] ?? null;
    } finally {
      listLoading = false;
    }
  }

  const sideKey = $derived(selected ? `${$activeRepo}|${base}|${head}|${threeDot}|${selected.path}` : "");
  let sideLoaded = "";
  $effect(() => {
    const key = sideKey;
    const f = selected;
    if (!key || !f) return;
    if (key === sideLoaded) return;
    sideLoaded = key;
    sides = null;
    if (isBinary(f)) return;
    loadRefCompare(base, head, f, threeDot)
      .then((d) => {
        if (sideLoaded === key) sides = d;
      })
      .catch((e) => {
        sides = null;
        toastError("Diff failed", String(e));
      });
  });

  const changedFiles = $derived(result?.files ?? []);
  const fileMap = $derived(byPath(changedFiles));
  const tree = $derived(viewMode === "tree" ? compareTree(changedFiles) : []);
  const stats = $derived(statsByPath(tree, fileMap));
  // What ↑/↓ walk: in tree mode the rows actually drawn, so a collapsed
  // folder's children are stepped over rather than selected off-screen.
  const walkable = $derived(
    viewMode === "tree" ? visibleFiles(tree, expanded, fileMap) : changedFiles,
  );

  // ↑/↓ walk the changed-file list. Bound to the window rather than to the
  // column, so it works without clicking into the list first — on a compare
  // with a few hundred files, reaching for the mouse per step is the
  // difference between usable and not.
  function move(delta: number) {
    const list = walkable;
    if (list.length === 0) return;
    const cur = selected;
    const at = cur ? list.findIndex((f) => f.path === cur.path) : -1;
    const next = Math.min(list.length - 1, Math.max(0, at + delta));
    selected = list[next];
    scrollIntoView(list[next].path);
  }

  // The tree renders through a recursive component, so the rows are not an
  // array this component holds — find the row by the path it carries.
  function scrollIntoView(path: string) {
    const sel = `[data-path="${path.replace(/["\\]/g, "\\$&")}"]`;
    listEl?.querySelector(sel)?.scrollIntoView({ block: "nearest" });
  }

  // One step per Escape, so it never skips one: an open picker or menu
  // closes first. A confirm dialog is its own layer on top and owns the key.
  function onEscape() {
    if (picker) picker = null;
    else if (resetMenu) resetMenu = false;
    else onClose();
  }

  function onKeydown(e: KeyboardEvent) {
    if (e.key !== "ArrowDown" && e.key !== "ArrowUp") return;
    if (picker || resetMenu || ask) return;
    const t = e.target as HTMLElement | null;
    // Never steal the arrows from a text field, or from Monaco — it drives
    // the diff from its own hidden textarea.
    if (t && (t.tagName === "INPUT" || t.tagName === "TEXTAREA" || t.isContentEditable)) return;
    e.preventDefault();
    move(e.key === "ArrowDown" ? 1 : -1);
  }

  // git reports a binary file as "-" on both counts, which the server
  // turns into -1. Monaco would happily render the bytes; nobody wants
  // that.
  const isBinary = (f: CompareFile) => f.additions < 0 && f.deletions < 0;

  const totals = $derived.by(() => {
    let a = 0;
    let d = 0;
    for (const f of changedFiles) {
      if (f.additions > 0) a += f.additions;
      if (f.deletions > 0) d += f.deletions;
    }
    return { count: changedFiles.length, a, d };
  });

  const lang = $derived(selected ? langFor(selected.path) : "plaintext");
  const current = $derived($branch?.name ?? "");

  function statusColor(s: string): string {
    const k = s.charAt(0);
    if (k === "A") return "text-green-600 dark:text-green-400";
    if (k === "D") return "text-cau-600 dark:text-cau-400";
    if (k === "R" || k === "C") return "text-blue-600 dark:text-blue-400";
    return "text-amber-600 dark:text-amber-400";
  }

  function pick(side: "base" | "head", name: string) {
    if (side === "base") base = name;
    else head = name;
    picker = null;
    filter = "";
  }

  function swap() {
    const b = base;
    base = head;
    head = b;
  }

  const matches = $derived(
    refs.filter((r) => r.name.toLowerCase().includes(filter.trim().toLowerCase())),
  );

  // Restoring a file takes the working tree back to the base side — the
  // rollback this view exists to make reachable. It lands staged, which
  // is how `git checkout <ref> -- <path>` leaves it.
  function askRestore(f: CompareFile) {
    // Renames: the base side of the file lives under its OLD name, so that
    // is the path git can check out — and the name the prompt has to say,
    // or the confirm describes a file the command will not touch.
    const path = f.orig_path && f.status.startsWith("R") ? f.orig_path : f.path;
    ask = {
      title: "Restore from base?",
      body: `Overwrite ${path} in the working tree with its content at ${base}, and stage it. Uncommitted changes to that file are lost.`,
      confirmLabel: "Restore",
      destructive: true,
      run: async () => {
        if (await restoreFromRef(base, [path])) await reload();
      },
    };
  }

  function askReset(mode: "soft" | "mixed" | "hard") {
    resetMenu = false;
    const effect =
      mode === "soft"
        ? "Moves the branch pointer only — the index and the working tree keep everything they have, so the difference shows up as staged changes."
        : mode === "mixed"
          ? "Moves the branch pointer and resets the index. Your files on disk are untouched; the difference shows up as unstaged changes."
          : "Moves the branch pointer, the index AND the working tree. Uncommitted work is destroyed and commits only on this branch become unreachable.";
    ask = {
      title: `Reset ${current || "current branch"} to ${base}?`,
      body: `git reset --${mode} ${base}. ${effect}`,
      confirmLabel: `Reset --${mode}`,
      destructive: mode === "hard",
      run: async () => {
        if (await resetCurrentTo(base, mode)) await reload();
      },
    };
  }

  async function confirmAsk() {
    const a = ask;
    ask = null;
    if (!a) return;
    busy = true;
    try {
      await a.run();
    } finally {
      busy = false;
    }
  }

  const pickBtn =
    "flex min-w-0 max-w-[16rem] items-center gap-1 rounded border border-white-300 dark:border-navy-600 px-2 py-1 text-[11px] text-black-800 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800 transition-colors";
</script>

<!-- ↑/↓ walk the file list — see onKeydown. Escape is the layer's (onEscape). -->
<svelte:window onkeydown={onKeydown} />

{#snippet refPicker(side: "base" | "head", value: string)}
  <div class="relative min-w-0">
    <button
      type="button"
      use:autofocus={side === "base"}
      onclick={() => { picker = picker === side ? null : side; filter = ""; }}
      title={value}
      class={pickBtn}
    >
      <span class="shrink-0 text-[9px] uppercase tracking-wider text-black-600">{side}</span>
      <span class="min-w-0 flex-1 truncate font-mono">{value || "—"}</span>
      <span class="shrink-0 text-[8px]">▾</span>
    </button>
    {#if picker === side}
      <button type="button" class="fixed inset-0 z-10 cursor-default" aria-label="Close" onclick={() => (picker = null)}></button>
      <div class="absolute left-0 top-full z-20 mt-1 w-72 rounded-lg border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-1.5 shadow-lg">
        <!-- Enter takes whatever was typed: neither endpoint lists tags, and
             a sha is a perfectly good side of a compare. -->
        <input
          bind:value={filter}
          placeholder="Filter, or type a tag / sha + Enter"
          onkeydown={(e) => { if (e.key === "Enter" && filter.trim()) pick(side, filter.trim()); }}
          class="mb-1 w-full rounded border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-1.5 py-1 text-xs text-black-900 dark:text-white-100 focus:border-green-500 focus:outline-none"
        />
        <div class="max-h-72 overflow-y-auto">
          {#each matches as r (r.name)}
            <button
              type="button"
              onclick={() => pick(side, r.name)}
              class="flex w-full items-center gap-2 rounded px-2 py-1 text-left text-xs hover:bg-white-200 dark:hover:bg-navy-800"
            >
              <span class="w-3 text-[10px] text-green-600 dark:text-green-400">{value === r.name ? "✓" : ""}</span>
              <span class={"min-w-0 flex-1 truncate font-mono " + (r.remote ? "text-black-700 dark:text-black-600" : "text-black-800 dark:text-black-500")}>{r.name}</span>
              {#if r.current}<span class="shrink-0 text-[9px] text-green-600 dark:text-green-400">current</span>{/if}
              {#if r.trunk || r.name === trunk}<span class="shrink-0 text-[9px] text-green-600 dark:text-green-400">trunk</span>{/if}
              <span class="shrink-0 font-mono text-[9px] text-black-600 dark:text-black-700">{r.sha}</span>
            </button>
          {:else}
            <p class="px-2 py-1 text-[11px] text-black-700 dark:text-black-600">No ref matches — press Enter to use it as typed.</p>
          {/each}
        </div>
      </div>
    {/if}
  </div>
{/snippet}

<div
  use:portal
  use:layer={{ onEscape, focus: false }}
  style="z-index:9999"
  class="fixed inset-0 flex items-stretch justify-center bg-black/60 backdrop-blur-sm sm:p-4"
  role="presentation"
  onclick={(e) => { if (e.target === e.currentTarget) onClose(); }}
>
  <div class="flex h-full w-full flex-col overflow-hidden sm:rounded-2xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-2xl">

    <!-- Toolbar: the two refs, the range toggle, and the branch-wide rollback. -->
    <div class="flex flex-wrap items-center gap-2 border-b border-white-300 dark:border-navy-600 px-3 py-2">
      <svg viewBox="0 0 16 16" class="h-4 w-4 shrink-0 text-green-500" fill="none" stroke="currentColor" stroke-width="1.5"><circle cx="4" cy="4" r="1.5"/><circle cx="4" cy="12" r="1.5"/><circle cx="12" cy="5" r="1.5"/><path d="M4 5.5v5M5.5 4H9a2 2 0 012 2v0" stroke-linecap="round"/></svg>
      <span class="shrink-0 text-xs font-semibold text-black-900 dark:text-white-100">Compare</span>
      <span class="shrink-0 truncate font-mono text-[10px] text-black-700 dark:text-black-600">{$activeRepo}</span>

      {@render refPicker("base", base)}
      <button
        type="button"
        onclick={swap}
        title="Swap base and head"
        aria-label="Swap base and head"
        class="inline-flex h-6 w-6 shrink-0 items-center justify-center rounded text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800"
      >
        <svg viewBox="0 0 16 16" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M3 6h9M10 3.5L12.5 6 10 8.5M13 10H4M6 7.5L3.5 10 6 12.5" stroke-linecap="round" stroke-linejoin="round"/></svg>
      </button>
      {@render refPicker("head", head)}

      <!-- Which question is being asked. `...` is what head adds since the
           two parted; `..` is the raw difference between the two tips. -->
      <button
        type="button"
        onclick={() => (threeDot = !threeDot)}
        title={threeDot
          ? "Merge-base range (base...head) — what head adds since they diverged"
          : "Direct range (base..head) — the difference between the two tips"}
        class={"shrink-0 rounded border px-2 py-1 font-mono text-[11px] transition-colors " + (threeDot ? "border-green-400 text-green-600 dark:text-green-400" : "border-white-300 dark:border-navy-600 text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800")}
      >{threeDot ? "..." : ".."}</button>

      <div class="flex-1"></div>

      <!-- Branch-wide rollback. Disabled on a detached HEAD, where "reset
           the current branch" has no branch to mean. -->
      <div class="relative shrink-0">
        <button
          type="button"
          onclick={() => (resetMenu = !resetMenu)}
          disabled={busy || !base || !!$branch?.detached}
          class="rounded border border-white-300 dark:border-navy-600 px-2 py-1 text-[11px] text-black-700 dark:text-black-600 hover:border-red-400 hover:text-red-600 dark:hover:text-red-400 disabled:opacity-50 transition-colors"
        >Reset {current || "branch"} to {base || "…"}…</button>
        {#if resetMenu}
          <button type="button" class="fixed inset-0 z-10 cursor-default" aria-label="Close" onclick={() => (resetMenu = false)}></button>
          <div class="absolute right-0 top-full z-20 mt-1 w-56 rounded-lg border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-1 shadow-lg">
            <button type="button" class="block w-full rounded px-2 py-1 text-left text-xs text-black-800 hover:bg-white-200 dark:text-black-600 dark:hover:bg-navy-800" onclick={() => askReset("soft")}>Soft — keep index and files</button>
            <button type="button" class="block w-full rounded px-2 py-1 text-left text-xs text-black-800 hover:bg-white-200 dark:text-black-600 dark:hover:bg-navy-800" onclick={() => askReset("mixed")}>Mixed — keep files only</button>
            <button type="button" class="block w-full rounded px-2 py-1 text-left text-xs text-cau-600 hover:bg-white-200 dark:text-cau-400 dark:hover:bg-navy-800" onclick={() => askReset("hard")}>Hard — discard everything</button>
          </div>
        {/if}
      </div>

      <button
        type="button"
        onclick={() => setViewMode(viewMode === "tree" ? "list" : "tree")}
        title={viewMode === "tree" ? "Show the changed files as a flat list" : "Group the changed files by folder"}
        class="shrink-0 rounded border border-white-300 dark:border-navy-600 px-2 py-1 text-[11px] text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800 transition-colors"
      >View as {viewMode === "tree" ? "list" : "tree"}</button>
      <button
        type="button"
        onclick={() => (sideBySide = !sideBySide)}
        title={sideBySide ? "Inline diff" : "Side-by-side diff"}
        aria-label="Toggle side-by-side diff"
        class={"inline-flex h-6 w-6 shrink-0 items-center justify-center rounded border transition-colors " + (sideBySide ? "border-green-400 text-green-600 dark:text-green-400" : "border-white-300 dark:border-navy-600 text-black-600 hover:bg-white-200 dark:hover:bg-navy-800")}
      >
        <svg viewBox="0 0 16 16" class="h-3 w-3" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M8 2v12M2 4h4M2 8h4M2 12h4M10 4h4M10 8h4M10 12h4" stroke-linecap="round"/></svg>
      </button>
      <button
        type="button"
        onclick={onClose}
        title="Close"
        aria-label="Close compare"
        class="inline-flex h-6 w-6 shrink-0 items-center justify-center rounded text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800"
      >
        <svg viewBox="0 0 16 16" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="2"><path d="M4 4l8 8M12 4l-8 8" stroke-linecap="round"/></svg>
      </button>
    </div>

    <!-- What the range adds up to, in one line. -->
    <div class="flex flex-wrap items-center gap-3 border-b border-white-300 dark:border-navy-600 bg-white-200 px-3 py-1.5 text-[10px] text-black-700 dark:bg-navy-800 dark:text-black-600">
      <span>{totals.count} file{totals.count === 1 ? "" : "s"} changed</span>
      <span class="text-green-600 dark:text-green-400">+{totals.a}</span>
      <span class="text-cau-600 dark:text-cau-400">−{totals.d}</span>
      {#if result}
        <span>{result.ahead} ahead · {result.behind} behind</span>
        {#if result.merge_base}
          <span class="font-mono">merge-base {result.merge_base}</span>
        {:else}
          <span class="text-amber-600 dark:text-amber-400">no common ancestor</span>
        {/if}
      {/if}
      {#if listLoading}
        <span class="flex items-center gap-1">
          <svg viewBox="0 0 16 16" class="h-2.5 w-2.5 animate-spin" fill="none" stroke="currentColor" stroke-width="2">
            <circle cx="8" cy="8" r="6" opacity="0.25"/><path d="M14 8a6 6 0 00-6-6" stroke-linecap="round"/>
          </svg>
          Comparing…
        </span>
      {/if}
    </div>

    <div class="flex min-h-0 flex-1 overflow-hidden">
      <!-- Left: the changed files. Wider than the Changes column: these
           rows carry a status letter and both counts next to the path. -->
      <aside bind:this={listEl} class="flex w-[320px] shrink-0 flex-col overflow-y-auto border-r border-white-300 dark:border-navy-600">
        {#if !base || !head}
          <p class="px-3 py-3 text-[11px] text-black-700 dark:text-black-600">Pick two refs to compare.</p>
        {:else if listLoading}
          <!-- A skeleton rather than the previous pair's files: leaving the
               old list up under new refs invites clicking a row that is
               about to be replaced by a different one. -->
          <div class="space-y-1.5 p-2" aria-hidden="true">
            {#each [0, 1, 2, 3, 4, 5, 6, 7] as i (i)}
              <div class="h-3.5 animate-pulse rounded bg-white-300 dark:bg-navy-600" style={`width:${92 - i * 7}%`}></div>
            {/each}
          </div>
        {:else if (result?.files.length ?? 0) === 0}
          <p class="px-3 py-3 text-[11px] text-black-700 dark:text-black-600">
            No differences between <span class="font-mono">{base}</span> and <span class="font-mono">{head}</span>.
          </p>
        {:else if viewMode === "tree"}
          <!-- The Changes tree, fed compare rows: same chevron, same
               12px-per-depth indent, same default-expanded rule. -->
          {#each tree as node (node.path)}
            <CompareTreeNode
              {node}
              depth={1}
              {expanded}
              files={fileMap}
              {stats}
              selectedPath={selected?.path ?? ""}
              {busy}
              onToggleDir={toggleDir}
              onSelect={(f) => (selected = f)}
              onRestore={askRestore}
            />
          {/each}
        {:else}
          {#each changedFiles as f (f.path)}
            {@const parts = splitPath(f.path)}
            <div
              data-path={f.path}
              class={"group flex items-center gap-1.5 px-2 py-1 " + (selected?.path === f.path ? "bg-white-300 dark:bg-navy-600" : "hover:bg-white-200 dark:hover:bg-navy-800")}
            >
              <!-- Name first, folder after it in grey: the flat list has no
                   folder rows to carry the path, and this is the only layout
                   where a long one truncates the part nobody reads. -->
              <button type="button" onclick={() => (selected = f)} title={f.orig_path ? `${f.orig_path} → ${f.path}` : f.path} class="flex min-w-0 flex-1 items-center gap-1.5 text-left">
                <span class={"shrink-0 font-mono text-[10px] " + statusColor(f.status)}>{f.status}</span>
                <span class="shrink-0 truncate text-xs font-medium text-black-900 dark:text-white-100">{parts.name}</span>
                {#if parts.dir}
                  <span class="min-w-0 flex-1 truncate text-[11px] text-black-600 dark:text-black-700">{parts.dir}</span>
                {:else}
                  <span class="min-w-0 flex-1"></span>
                {/if}
                {#if isBinary(f)}
                  <span class="shrink-0 text-[9px] text-black-600">bin</span>
                {:else}
                  <span class="shrink-0 font-mono text-[9px] text-green-600 dark:text-green-400">+{Math.max(f.additions, 0)}</span>
                  <span class="shrink-0 font-mono text-[9px] text-cau-600 dark:text-cau-400">−{Math.max(f.deletions, 0)}</span>
                {/if}
              </button>
              <button
                type="button"
                onclick={() => askRestore(f)}
                disabled={busy}
                title={`Restore this file from ${base}`}
                aria-label={`Restore ${f.path} from base`}
                class="hidden h-5 w-5 shrink-0 items-center justify-center rounded text-black-600 hover:text-red-600 group-hover:inline-flex dark:hover:text-red-400 disabled:opacity-50"
              >
                <svg viewBox="0 0 16 16" class="h-3 w-3" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M2 8a6 6 0 0110.5-4M11 2v3H8M14 8a6 6 0 01-10.5 4M5 14v-3h3" stroke-linecap="round" stroke-linejoin="round"/></svg>
              </button>
            </div>
          {/each}
        {/if}
      </aside>

      <!-- Right: the diff itself, read-only — this is history, not the
           working tree, so there is nothing here to save. -->
      <main class="flex min-w-0 flex-1 flex-col overflow-hidden bg-white-200 dark:bg-navy-800">
        {#if selected}
          <div class="flex items-center gap-2 border-b border-white-300 dark:border-navy-600 bg-white-100 px-3 py-1.5 dark:bg-navy-700">
            <span class="min-w-0 flex-1 truncate font-mono text-[11px] text-black-900 dark:text-white-100">
              {selected.orig_path ? `${selected.orig_path} → ${selected.path}` : selected.path}
            </span>
            <span class="shrink-0 font-mono text-[10px] text-black-600">{base} {threeDot ? "..." : ".."} {head}</span>
            <button
              type="button"
              onclick={() => askRestore(selected!)}
              disabled={busy}
              class="shrink-0 rounded border border-white-300 dark:border-navy-600 px-2 py-0.5 text-[11px] text-black-700 dark:text-black-500 hover:border-red-400 hover:text-red-600 dark:hover:text-red-400 disabled:opacity-50 transition-colors"
            >Restore from base</button>
          </div>
          <div class="min-h-0 flex-1">
            {#if isBinary(selected)}
              <div class="flex h-full items-center justify-center px-6 text-center text-xs text-black-700 dark:text-black-600">
                Binary file — git counted no lines, so there is nothing to show side by side.
              </div>
            {:else if sides}
              <MonacoView mode="diff" original={sides.original} modified={sides.modified} language={lang} readOnly={true} {sideBySide} />
            {:else}
              <!-- Only this pane waits: the file list stays live, so the
                   next file can be picked before this one has arrived. -->
              <div class="flex h-full items-center justify-center gap-1.5 text-xs text-black-600">
                <svg viewBox="0 0 16 16" class="h-3 w-3 animate-spin" fill="none" stroke="currentColor" stroke-width="2">
                  <circle cx="8" cy="8" r="6" opacity="0.25"/><path d="M14 8a6 6 0 00-6-6" stroke-linecap="round"/>
                </svg>
                Loading diff…
              </div>
            {/if}
          </div>
        {:else}
          <div class="flex h-full items-center justify-center text-xs text-black-600">Select a file to see its diff.</div>
        {/if}
      </main>
    </div>
  </div>
</div>

<ConfirmDialog
  open={!!ask}
  title={ask?.title ?? ""}
  body={ask?.body ?? ""}
  confirmLabel={ask?.confirmLabel ?? "Confirm"}
  cancelLabel="Cancel"
  destructive={ask?.destructive ?? false}
  onConfirm={confirmAsk}
  onCancel={() => (ask = null)}
/>
