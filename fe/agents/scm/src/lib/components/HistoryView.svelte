<script lang="ts">
  import { untrack } from "svelte";
  import { get } from "svelte/store";
  import * as api from "$lib/api/scm";
  import type { LogEntry, CommitDetail, FileChange, HistoryRef } from "$lib/api/scm";
  import { sessionID, activeRepo, branch, changes } from "$lib/stores/scm";
  import { buildGraph, graphWidth } from "$lib/graph";
  import GraphRail from "$lib/components/GraphRail.svelte";
  import RefPicker from "$lib/components/RefPicker.svelte";
  import { toastError } from "@wick-fe/common-stores";

  type Props = {
    onOpenCommitFile: (sha: string, file: FileChange) => void;
    /** Jump to the Changes tab — what the working-changes row is for. */
    onShowChanges?: () => void;
  };
  let { onOpenCommitFile, onShowChanges }: Props = $props();

  let commits = $state<LogEntry[]>([]);
  // email -> avatar URL, only for authors who are wick users WITH a picture.
  let avatars = $state<Record<string, string>>({});
  const avatarFor = (c: LogEntry) => (c.author_email ? avatars[c.author_email.toLowerCase()] : undefined);
  let refs = $state<HistoryRef[]>([]);
  let trunk = $state("");
  let loading = $state(true);
  // Paging. PAGE is how many commits one request brings back; hasMore comes
  // from the server (a full page means there is probably another).
  const PAGE = 80;
  let hasMore = $state(false);
  let loadingMore = $state(false);
  let expanded = $state<string | null>(null);
  let detail = $state<CommitDetail | null>(null);

  // The panel is a side rail at 300px and a full pane at 900px, and the same
  // row cannot read well at both: wide wants one line with columns, narrow
  // wants the metadata stacked under the subject. Measured, not guessed from
  // the viewport — this lives inside a resizable pane, so a media query would
  // describe the window rather than the space the rows actually have.
  let panelEl = $state<HTMLElement | null>(null);
  let wide = $state(false);
  $effect(() => {
    const el = panelEl;
    if (!el || typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(([entry]) => {
      wide = entry.contentRect.width >= 560;
    });
    ro.observe(el);
    return () => ro.disconnect();
  });

  // Search. Non-matching rows are DIMMED, not removed: dropping them would
  // cut the rail into disconnected pieces and the graph would stop being a
  // graph. Matching rows keep their place and you step between them.
  let query = $state("");
  let matchIdx = $state(0);
  // filter = hide what does not match (the list becomes the result set);
  // highlight = keep every row, dim the rest. Filtering reads better when
  // you are looking FOR something; highlighting when you want to see where
  // the matches sit in the history.
  let filterMode = $state(true);

  // Qualifiers, GitLens-style: author:name, message:text, -message:text,
  // ref:branch. Anything unqualified matches subject, author, sha and refs.
  // Terms combine with AND, which is what makes them worth typing.
  type Term = { kind: "any" | "author" | "message" | "ref"; value: string; negate: boolean };
  function parseQuery(q: string): Term[] {
    return q
      .trim()
      .split(/\s+/)
      .filter(Boolean)
      .map((tok) => {
        let negate = false;
        if (tok.startsWith("-")) {
          negate = true;
          tok = tok.slice(1);
        }
        const m = /^(author|message|ref):(.*)$/i.exec(tok);
        if (m) {
          return { kind: m[1].toLowerCase() as Term["kind"], value: m[2].toLowerCase(), negate };
        }
        return { kind: "any" as const, value: tok.toLowerCase(), negate };
      })
      .filter((t) => t.value !== "");
  }
  function hit(c: LogEntry, t: Term): boolean {
    const refs = (c.refs ?? []).join(" ").toLowerCase();
    switch (t.kind) {
      case "author":
        return c.author.toLowerCase().includes(t.value);
      case "message":
        return c.subject.toLowerCase().includes(t.value);
      case "ref":
        return refs.includes(t.value);
      default:
        return `${c.subject} ${c.author} ${c.sha} ${refs}`.toLowerCase().includes(t.value);
    }
  }
  const terms = $derived(parseQuery(query));
  const matches = $derived.by(() => {
    if (terms.length === 0) return [] as number[];
    const out: number[] = [];
    commits.forEach((c, i) => {
      if (terms.every((t) => (t.negate ? !hit(c, t) : hit(c, t)))) out.push(i);
    });
    return out;
  });
  const searching = $derived(terms.length > 0);
  const isMatch = (i: number) => matches.includes(i);
  function step(delta: number) {
    if (matches.length === 0) return;
    matchIdx = (matchIdx + delta + matches.length) % matches.length;
    const sha = commits[matches[matchIdx]]?.sha;
    if (sha) document.getElementById(`commit-${sha}`)?.scrollIntoView({ block: "center" });
  }
  $effect(() => {
    void query;
    matchIdx = 0;
  });

  // Hover card. The detail is fetched per commit and kept, because moving
  // down a list re-hovers rows you already looked at and refetching each
  // time makes the card flicker for data that cannot have changed.
  let hoverSha = $state<string | null>(null);
  let hoverAt = $state(0);
  // Distance from the viewport's right edge to the row's right edge. The
  // panel is docked, so anchoring the card to the ROW keeps it inside the
  // panel; centring it on the viewport put it over the conversation, half a
  // screen away from the commit it describes.
  let hoverRight = $state(0);
  // When the row sits low on the screen there is no room underneath, so the
  // card flips and hangs ABOVE it. Without this it grew past the bottom edge
  // and the end of the message was unreachable.
  let hoverFlip = $state(false);
  // A plain Map is invisible to the renderer: writing into it changes no
  // reference, so the card kept showing "Loading…" for a request that had
  // already returned 200. Reactive record + a fresh object on write.
  let detailCache = $state<Record<string, CommitDetail>>({});
  let hoverTimer: ReturnType<typeof setTimeout> | undefined;

  function hoverIn(e: MouseEvent, sha: string) {
    clearTimeout(hoverTimer);
    const rect = (e.currentTarget as HTMLElement).getBoundingClientRect();
    // A card that opens the instant the pointer crosses a row turns a scroll
    // into a strobe; the delay is what makes it feel deliberate.
    hoverTimer = setTimeout(() => {
      hoverFlip = rect.bottom > window.innerHeight * 0.55;
      hoverAt = hoverFlip ? window.innerHeight - rect.top + 4 : rect.bottom + 4;
      hoverRight = Math.max(window.innerWidth - rect.right, 8);
      hoverSha = sha;
      void loadHover(sha);
    }, 250);
  }
  function hoverOut() {
    clearTimeout(hoverTimer);
    hoverSha = null;
  }
  // In-flight shas, so sweeping back over a row that is still loading does
  // not fire the request a second time. Together with the 250 ms settle in
  // hoverIn this keeps a fast scroll down the list at zero requests: you pay
  // only for rows you actually stop on.
  const inFlight = new Set<string>();
  async function loadHover(sha: string) {
    if (detailCache[sha] || inFlight.has(sha)) return;
    inFlight.add(sha);
    try {
      const d = await api.getCommit(get(sessionID), get(activeRepo), sha);
      detailCache = { ...detailCache, [sha]: d };
    } catch {
      /* the row still works without a card */
    } finally {
      inFlight.delete(sha);
    }
  }
  const hoverDetail = $derived(hoverSha ? (detailCache[hoverSha] ?? null) : null);
  // The row we already have. The card opens with THIS immediately; the
  // fetched half (body, file counts) fills in underneath. Waiting for the
  // request before drawing anything is what made hovering look broken —
  // nothing happened, so people hovered again.
  const hoverRow = $derived(hoverSha ? (commits.find((c) => c.sha === hoverSha) ?? null) : null);
  // Trim in JS rather than with line-clamp: a commit body can be forty lines,
  // and the card has to stay a card. The full text is one click away in the
  // expanded row.
  const hoverBody = $derived.by(() => {
    const b = (hoverDetail?.body ?? "").trim();
    if (!b) return "";
    const lines = b.split("\n").slice(0, 6);
    const cut = lines.join("\n");
    return cut.length < b.length ? cut.replace(/\s+$/, "") + " …" : cut;
  });

  function fileDelta(f: { additions?: number; deletions?: number }): string {
    if (f.additions === -1 && f.deletions === -1) return "binary";
    return `+${f.additions ?? 0} \u2212${f.deletions ?? 0}`;
  }
  const detailTotals = $derived.by(() => {
    const fs = detail?.files ?? [];
    let a = 0;
    let d = 0;
    for (const f of fs) {
      if (f.additions && f.additions > 0) a += f.additions;
      if (f.deletions && f.deletions > 0) d += f.deletions;
    }
    return { files: fs.length, a, d };
  });

  // Which references to walk. [] = auto (checked-out branch + upstream),
  // ["all"] = everything, otherwise the named refs. Remembered per repo:
  // "show me all branches" is a property of the repo you are reading, not
  // of the panel, and re-picking it on every switch got old fast.
  let selectedRefs = $state<string[]>([]);
  const refsKey = (repo: string) => `wick.scm.graphRefs.${repo}`;
  function readRefs(repo: string): string[] {
    try {
      const raw = localStorage.getItem(refsKey(repo));
      return raw ? (JSON.parse(raw) as string[]) : [];
    } catch {
      return [];
    }
  }
  function writeRefs(repo: string, v: string[]): void {
    try {
      localStorage.setItem(refsKey(repo), JSON.stringify(v));
    } catch {
      /* selection just won't persist */
    }
  }

  async function load() {
    loading = true;
    try {
      const id = get(sessionID);
      const repo = get(activeRepo);
      const [log, refList] = await Promise.all([
        api.getLog(id, repo, PAGE, selectedRefs, 0),
        // The picker's contents, not the history itself — a failure here
        // must not empty the graph.
        api.getHistoryRefs(id, repo).catch(() => ({ refs: [], trunk: "" })),
      ]);
      commits = log.commits;
      avatars = log.avatars ?? {};
      hasMore = log.has_more ?? false;
      refs = refList.refs;
      trunk = refList.trunk;
      // The selection is remembered per repo, so a branch deleted since the
      // last visit is still selected — and the picker would keep showing a
      // name that is gone, while every request carried a ref the repo no
      // longer has. Drop those here; the server already walks what is left.
      // Only when the list actually loaded: an empty list means the refs
      // call failed, not that the repo has no branches.
      // [] (auto) and ["all"] are mode sentinels, not ref names — never prune those.
      if (refList.refs.length > 0 && !(selectedRefs.length === 1 && selectedRefs[0] === "all")) {
        const alive = new Set(refList.refs.map((r) => r.name));
        const kept = selectedRefs.filter((n) => alive.has(n));
        if (kept.length !== selectedRefs.length) {
          selectedRefs = kept;
          writeRefs(repo, kept);
        }
      }
    } catch (e) {
      toastError("History", String(e));
    } finally {
      loading = false;
    }
  }

  // Fetch the next page and append. Guarded on loadingMore so a scroll that
  // keeps firing while the request is in flight asks once, not once per
  // frame; the ref selection is captured in `commits.length`, so a page that
  // arrives after the user changed refs simply appends to a list that has
  // already been replaced — hence the length check before appending.
  async function loadMore() {
    if (!hasMore || loadingMore || loading) return;
    loadingMore = true;
    const before = commits.length;
    try {
      const log = await api.getLog(get(sessionID), get(activeRepo), PAGE, selectedRefs, before);
      if (commits.length !== before) return; // the list moved under us
      commits = [...commits, ...log.commits];
      avatars = { ...avatars, ...(log.avatars ?? {}) };
      hasMore = log.has_more ?? false;
    } catch (e) {
      toastError("History", String(e));
      hasMore = false;
    } finally {
      loadingMore = false;
    }
  }

  function onListScroll(e: Event) {
    const el = e.currentTarget as HTMLElement;
    // 300px of runway: start fetching before the user hits the end, so the
    // next page is usually there by the time they get to it.
    if (el.scrollHeight - el.scrollTop - el.clientHeight < 300) void loadMore();
  }

  function pickRefs(next: string[]) {
    selectedRefs = next;
    writeRefs(get(activeRepo), next);
    void load();
  }

  async function toggle(sha: string) {
    if (expanded === sha) {
      expanded = null;
      detail = null;
      return;
    }
    expanded = sha;
    detail = null;
    try {
      detail = await api.getCommit(get(sessionID), get(activeRepo), sha);
    } catch (e) {
      toastError("Commit", String(e));
    }
  }

  function statusColor(s: string): string {
    if (s === "A") return "text-green-600 dark:text-green-400";
    if (s === "D") return "text-cau-600 dark:text-cau-400";
    return "text-amber-600 dark:text-amber-400";
  }

  const rows = $derived(buildGraph(commits.map((c) => ({ sha: c.sha, parents: c.parents ?? [] }))));
  const lanes = $derived(graphWidth(rows));
  // How much of the local work is unpushed, for the header. A count is
  // what makes "you have not pushed" impossible to scroll past.
  // Which decorations become a badge. HEAD and origin/HEAD are pointers AT
  // another ref in the same list, so drawing them adds a second name for a
  // place already labelled. Tags keep their name but read as local.
  type Badge = { name: string; kind: "local" | "remote" | "tag" };
  const remoteNames = $derived(new Set(refs.filter((r) => r.remote).map((r) => r.name)));
  const localNames = $derived(new Set(refs.filter((r) => !r.remote).map((r) => r.name)));
  function badgeRefs(c: LogEntry): Badge[] {
    return (c.refs ?? [])
      .filter((r) => r !== "HEAD" && !r.endsWith("/HEAD"))
      .map((r): Badge => {
        if (r.startsWith("tag: ")) return { name: r.slice(5), kind: "tag" };
        // Ask the ref list, do not guess from the slash: `feature/login` is
        // a perfectly ordinary LOCAL branch and was being drawn with the
        // remote's cloud. The list comes from for-each-ref, which knows
        // which refs live under refs/remotes.
        if (remoteNames.has(r)) return { name: r, kind: "remote" };
        if (localNames.has(r)) return { name: r, kind: "local" };
        // Not in the list (it can lag a fetch): fall back to the shape.
        return { name: r, kind: r.includes("/") ? "remote" : "local" };
      });
  }
  // A release commit can carry a dozen tags. Rendering them all made the row
  // wider than the panel, which turned the whole list into something you
  // scroll sideways — and sideways scrolling hides the rail and the sha. The
  // row height is fixed (the rail lanes have to line up), so wrapping is not
  // available either: show the first two and count the rest.
  const BADGE_LIMIT = 2;
  const shownBadges = (c: LogEntry) => badgeRefs(c).slice(0, BADGE_LIMIT);
  const extraBadges = (c: LogEntry) => badgeRefs(c).slice(BADGE_LIMIT);

  const localCount = $derived(commits.filter((c) => c.state === "local").length);
  const pushedCount = $derived(commits.filter((c) => c.state === "pushed").length);

  $effect(() => {
    // Reload when the active repo changes, picking up that repo's saved
    // reference selection first.
    const repo = $activeRepo;
    // untrack, or the effect re-runs itself forever: it WRITES selectedRefs
    // and load() READS it synchronously (before its first await), so the
    // write invalidates the very effect that made it. readRefs hands back a
    // fresh array each time, so the identity never settles either — the
    // panel sat on "Loading history…" while re-firing /git/log + /git/refs
    // in a loop.
    untrack(() => {
      selectedRefs = readRefs(repo);
      load();
    });
  });
</script>

<div class="flex flex-1 flex-col overflow-hidden" bind:this={panelEl}>
  <!-- One line, always. Wrapping moved the ref button to the left edge and
       cost a whole row of a panel that is already short; the branch name is
       the only part that can be long, so that is what gives — it truncates
       and keeps its full text in the tooltip. -->
  <div class="flex flex-nowrap items-center gap-x-2 border-b border-white-300 dark:border-navy-600 px-3 py-1.5">
    <span class="shrink-0 text-[10px] font-medium uppercase tracking-wide text-black-700 dark:text-black-600">Graph</span>
    <!-- The per-row badges collapsed to one line: what is not yet out of this
         clone, what is out but not landed, and where the trunk is. -->
    {#if localCount > 0}
      <span
        class="flex shrink-0 items-center gap-1 rounded border border-amber-500/60 px-1 text-[9px] text-amber-600 dark:text-amber-400"
        title="Commits that exist only in this clone"
      >
        <svg viewBox="0 0 16 16" class="h-2.5 w-2.5" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M8 11V3M5 6l3-3 3 3" stroke-linecap="round" stroke-linejoin="round"/><path d="M3 13h10" stroke-linecap="round"/></svg>
        {localCount} unpushed
      </span>
    {/if}
    {#if pushedCount > 0}
      <span
        class="flex shrink-0 items-center gap-1 rounded border border-link-400/60 px-1 text-[9px] text-link-400"
        title={`Pushed to their remote branch, not yet on ${trunk || "the trunk"}`}
      >
        <svg viewBox="0 0 16 16" class="h-2.5 w-2.5" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M4.5 12a3 3 0 01-.3-6A4 4 0 0112 6.5a2.75 2.75 0 01-.25 5.5z" stroke-linejoin="round"/></svg>
        {pushedCount} pushed
      </span>
    {/if}
    {#if trunk}
      <span class="flex min-w-0 items-center gap-1 text-[9px] text-black-600 dark:text-black-700" title={`Commits below this are merged — ${trunk}`}>
        <svg viewBox="0 0 16 16" class="h-2.5 w-2.5 shrink-0" fill="none" stroke="currentColor" stroke-width="1.5"><circle cx="4" cy="4" r="1.5"/><circle cx="4" cy="12" r="1.5"/><circle cx="12" cy="5" r="1.5"/><path d="M4 5.5v5M5.5 4H9a2 2 0 012 2v0" stroke-linecap="round"/></svg>
        <span class="truncate">{trunk}</span>
      </span>
    {/if}
    <span class="min-w-[0.5rem] flex-1"></span>
    <div class="flex shrink-0 items-center gap-1">
      <input
        bind:value={query}
        placeholder="Search commits…"
        onkeydown={(e) => { if (e.key === "Enter") step(e.shiftKey ? -1 : 1); }}
        class="w-28 rounded border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-1.5 py-0.5 text-[10px] text-black-900 dark:text-white-100 focus:w-40 focus:border-green-500 focus:outline-none transition-[width]"
      />
      {#if searching}
        <button
          type="button"
          onclick={() => (filterMode = !filterMode)}
          title={filterMode ? "Filtering: only matches are listed" : "Highlighting: everything stays, matches stand out"}
          class={"shrink-0 rounded border px-1 text-[9px] " + (filterMode ? "border-green-500 text-green-600 dark:text-green-400" : "border-white-300 text-black-700 dark:border-navy-600 dark:text-black-600")}
        >{filterMode ? "filter" : "highlight"}</button>
        <span class="shrink-0 text-[9px] text-black-700 dark:text-black-600">
          {matches.length === 0 ? "0" : matchIdx + 1} of {matches.length}
        </span>
        <button type="button" onclick={() => step(-1)} title="Previous match" aria-label="Previous match" class="rounded px-1 text-[10px] text-black-700 hover:bg-white-200 dark:text-black-600 dark:hover:bg-navy-800">↑</button>
        <button type="button" onclick={() => step(1)} title="Next match" aria-label="Next match" class="rounded px-1 text-[10px] text-black-700 hover:bg-white-200 dark:text-black-600 dark:hover:bg-navy-800">↓</button>
      {/if}
    </div>
    <RefPicker {refs} {trunk} selected={selectedRefs} onchange={pickRefs} />
  </div>

  <!-- Working changes sit ABOVE the first commit, the way an editor shows
       them: what you have not committed is part of the history you are
       reading, and having to switch tabs to remember it is the thing that
       makes people forget it. -->
  {#if $changes.length > 0}
    <button
      type="button"
      onclick={() => onShowChanges?.()}
      class="flex w-full items-center gap-2 border-b border-white-300 dark:border-navy-600 bg-white-200/60 px-3 py-1.5 text-left hover:bg-white-200 dark:bg-navy-800/60 dark:hover:bg-navy-800"
    >
      <span class="text-xs font-medium italic text-black-900 dark:text-white-100">Working changes</span>
      <span class="rounded-full bg-white-300 px-1.5 text-[9px] text-black-800 dark:bg-navy-600 dark:text-black-600">
        {$changes.length} file{$changes.length === 1 ? "" : "s"}
      </span>
      {#if $branch}
        <span
          class="flex items-center gap-1 rounded-full bg-link-400/15 px-1.5 text-[9px] text-link-400"
          title={`On ${$branch.name}${$branch.ahead ? ` · ${$branch.ahead} ahead` : ""}${$branch.behind ? ` · ${$branch.behind} behind` : ""}`}
        >
          <svg viewBox="0 0 16 16" class="h-2.5 w-2.5" fill="none" stroke="currentColor" stroke-width="1.5"><circle cx="4" cy="4" r="1.5"/><circle cx="4" cy="12" r="1.5"/><circle cx="12" cy="5" r="1.5"/><path d="M4 5.5v5M5.5 4H9a2 2 0 012 2v0" stroke-linecap="round"/></svg>
          {$branch.name}
          {#if $branch.ahead}<span>↑{$branch.ahead}</span>{/if}
          {#if $branch.behind}<span>↓{$branch.behind}</span>{/if}
        </span>
      {/if}
    </button>
  {/if}

  <!-- overflow-x-hidden: a row whose badges outgrow the panel must clip, not
       turn the list into a sideways-scrolling strip — scrolling right hides
       the rail and the sha, the two things you navigate by. -->
  <div class="flex-1 overflow-y-auto overflow-x-hidden" onscroll={onListScroll}>
    {#if loading}
      <p class="p-4 text-xs text-black-700 dark:text-black-600">Loading history…</p>
    {:else if commits.length === 0}
      <p class="p-4 text-xs text-black-700 dark:text-black-600">No commits.</p>
    {:else}
      {#each commits as c, i (c.sha)}
        <div
          id={`commit-${c.sha}`}
          class={"border-b border-white-300 dark:border-navy-600 last:border-0 " +
            (searching && filterMode && !isMatch(i) ? "hidden " : "") +
            (searching && !filterMode && !isMatch(i) ? "opacity-40 " : "")}
        >
          <div class="flex items-stretch">
            <!-- The rail sits outside the button so its lanes stay
                 continuous down the list rather than restarting per row. -->
            <GraphRail row={rows[i]} {lanes} />
            <button
              type="button"
              onclick={() => toggle(c.sha)}
              onmouseenter={(e) => hoverIn(e, c.sha)}
              onmouseleave={hoverOut}
              onfocus={(e) => hoverIn(e as unknown as MouseEvent, c.sha)}
              onblur={hoverOut}
              class="min-w-0 flex-1 px-2 py-1 text-left hover:bg-white-200 dark:hover:bg-navy-800 transition-colors"
              style="height:44px"
            >
              <div class={"flex items-center gap-1.5 " + (wide ? "h-full" : "")}>
                {#if avatarFor(c)}
                  <img
                    src={avatarFor(c)}
                    alt=""
                    loading="lazy"
                    title={c.author}
                    class="h-4 w-4 shrink-0 rounded-full object-cover"
                  />
                {/if}
                <span class="min-w-0 flex-1 truncate text-xs text-black-900 dark:text-white-100">{c.subject}</span>
                <!-- No state words on the rows. "merged" is what almost every
                     line is, so saying it on each one hides the few that
                     differ; and a per-row "local" repeated thirteen times
                     crowds out the subject, which is the thing being read.
                     What earns a badge is a REF sitting on that commit — the
                     branch you are on, and where its remote has got to —
                     drawn as an icon, the way an editor does it. HEAD and
                     */HEAD are aliases of those and would just double them. -->
                {#each shownBadges(c) as r (r.name)}
                  <span
                    class={"flex max-w-[35%] shrink items-center gap-1 overflow-hidden rounded-full px-1.5 text-[9px] " +
                      (r.kind === "remote"
                        ? "bg-purple-400/15 text-purple-500 dark:text-purple-300"
                        : r.kind === "tag"
                          ? "bg-amber-400/15 text-amber-600 dark:text-amber-400"
                          : "bg-link-400/15 text-link-400")}
                    title={r.kind === "remote" ? "Remote branch" : r.kind === "tag" ? "Tag" : "Local branch"}
                  >
                    {#if r.kind === "remote"}
                      <svg viewBox="0 0 16 16" class="h-2.5 w-2.5 shrink-0" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M4.5 12a3 3 0 01-.3-6A4 4 0 0112 6.5a2.75 2.75 0 01-.25 5.5z" stroke-linejoin="round"/></svg>
                    {:else if r.kind === "tag"}
                      <svg viewBox="0 0 16 16" class="h-2.5 w-2.5 shrink-0" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M2 7V2.5h4.5L14 10l-4.5 4.5L2 7z" stroke-linejoin="round"/><circle cx="4.6" cy="4.6" r="0.9"/></svg>
                    {:else}
                      <svg viewBox="0 0 16 16" class="h-2.5 w-2.5 shrink-0" fill="none" stroke="currentColor" stroke-width="1.5"><circle cx="4" cy="4" r="1.5"/><circle cx="4" cy="12" r="1.5"/><circle cx="12" cy="5" r="1.5"/><path d="M4 5.5v5M5.5 4H9a2 2 0 012 2v0" stroke-linecap="round"/></svg>
                    {/if}
                    <span class="truncate">{r.name}</span>
                  </span>
                {/each}
                {#if extraBadges(c).length > 0}
                  <span
                    class="shrink-0 rounded-full bg-white-300 px-1.5 text-[9px] text-black-700 dark:bg-navy-600 dark:text-black-600"
                    title={extraBadges(c).map((r) => r.name).join("\n")}
                  >+{extraBadges(c).length}</span>
                {/if}
                <!-- Wide: the metadata rides on the same line as columns on
                     the right. Narrow: it drops to a second line, because
                     squeezing author + sha + date beside a subject is how you
                     end up reading none of them. -->
                {#if wide}
                  <span class="hidden shrink-0 truncate text-[10px] text-black-700 md:block dark:text-black-600" style="width:9rem">{c.author}</span>
                  <span class="shrink-0 font-mono text-[10px] text-green-600 dark:text-green-400" style="width:4.5rem">{c.sha}</span>
                  <span class="shrink-0 text-right text-[10px] text-black-700 dark:text-black-600" style="width:5rem">{c.rel_date}</span>
                {/if}
              </div>
              {#if !wide}
                <div class="mt-0.5 flex items-center gap-2 text-[10px] text-black-700 dark:text-black-600">
                  <span class="font-mono text-green-600 dark:text-green-400">{c.sha}</span>
                  <span class="truncate">{c.author}</span>
                  <span>·</span>
                  <span class="shrink-0">{c.rel_date}</span>
                </div>
              {/if}
            </button>
          </div>
          {#if expanded === c.sha}
            <div class="bg-white-200 dark:bg-navy-800 px-3 py-2">
              {#if !detail}
                <p class="text-[11px] text-black-700 dark:text-black-600">Loading files…</p>
              {:else}
                {#if detail.body}
                  <p class="mb-1.5 whitespace-pre-wrap text-[11px] text-black-800 dark:text-black-600">{detail.body}</p>
                {/if}
                <p class="mb-1 flex flex-wrap items-center gap-2 text-[10px] text-black-700 dark:text-black-600">
                  <span>{detailTotals.files} file{detailTotals.files === 1 ? "" : "s"} changed</span>
                  <span class="text-green-600 dark:text-green-400">+{detailTotals.a}</span>
                  <span class="text-cau-600 dark:text-cau-400">−{detailTotals.d}</span>
                  {#if detail.email}<span class="truncate">{detail.email}</span>{/if}
                </p>
                {#if detail.files.length === 0}
                  <p class="text-[11px] text-black-700 dark:text-black-600">No file changes.</p>
                {:else}
                  {#each detail.files as f (f.path)}
                    <button
                      type="button"
                      onclick={() => onOpenCommitFile(c.sha, { path: f.path } as FileChange)}
                      class="flex w-full items-center gap-2 rounded px-1.5 py-1 text-left hover:bg-white-300 dark:hover:bg-navy-700"
                    >
                      <span class={"shrink-0 font-mono text-[10px] " + statusColor(f.status)}>{f.status}</span>
                      <span class="min-w-0 flex-1 truncate text-[11px] text-black-800 dark:text-black-600">{f.path}</span>
                      <span class="shrink-0 font-mono text-[9px] text-black-600 dark:text-black-700">{fileDelta(f)}</span>
                    </button>
                  {/each}
                {/if}
              {/if}
            </div>
          {/if}
        </div>
      {/each}
      {#if loadingMore}
        <p class="flex items-center justify-center gap-1.5 px-3 py-2 text-[10px] text-black-700 dark:text-black-600">
          <svg viewBox="0 0 16 16" class="h-2.5 w-2.5 animate-spin" fill="none" stroke="currentColor" stroke-width="2">
            <circle cx="8" cy="8" r="6" opacity="0.25"/><path d="M14 8a6 6 0 00-6-6" stroke-linecap="round"/>
          </svg>
          Loading more…
        </p>
      {:else if !hasMore && commits.length >= PAGE}
        <p class="px-3 py-2 text-center text-[10px] italic text-black-700 dark:text-black-600">
          End of history — {commits.length} commits.
        </p>
      {/if}
      {#if searching && filterMode}
        <p class="px-3 py-2 text-[10px] italic text-black-700 dark:text-black-600">
          {matches.length === 0
            ? "No commits match."
            : `Showing ${matches.length} of ${commits.length} commits.`}
        </p>
      {/if}
    {/if}
  </div>

  <!-- Hover card: what a row cannot say in 44 pixels — the full message, who
       wrote it, and how big the change was — without having to open it. -->
  {#if hoverSha && hoverRow}
    <div
      class="pointer-events-none fixed z-30 max-h-[45vh] w-[20rem] max-w-[calc(100vw-1rem)] overflow-hidden rounded-lg border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-2.5 shadow-xl"
      style={`${hoverFlip ? "bottom" : "top"}:${hoverAt}px;right:${hoverRight}px`}
    >
      <p class="flex items-center gap-2 text-[11px] font-medium text-black-900 dark:text-white-100">
        <span>{hoverRow.author}</span>
        <span class="font-mono text-[10px] text-green-600 dark:text-green-400">{hoverRow.sha}</span>
      </p>
      <p class="mt-0.5 text-[10px] text-black-700 dark:text-black-600">
        {new Date(hoverRow.iso_date).toLocaleString()}
        {#if hoverDetail?.email} · {hoverDetail.email}{/if}
      </p>
      <p class="mt-1.5 whitespace-pre-wrap text-[11px] text-black-900 dark:text-white-100">{hoverRow.subject}</p>
      {#if hoverDetail}
        {#if hoverBody}
          <p class="mt-1 whitespace-pre-wrap text-[11px] text-black-800 dark:text-black-600">{hoverBody}</p>
        {/if}
        <p class="mt-1.5 flex items-center gap-2 text-[10px] text-black-700 dark:text-black-600">
          <span>{hoverDetail.files.length} file{hoverDetail.files.length === 1 ? "" : "s"} changed</span>
          <span class="text-green-600 dark:text-green-400">
            +{hoverDetail.files.reduce((n, f) => n + Math.max(f.additions ?? 0, 0), 0)}
          </span>
          <span class="text-cau-600 dark:text-cau-400">
            −{hoverDetail.files.reduce((n, f) => n + Math.max(f.deletions ?? 0, 0), 0)}
          </span>
        </p>
      {:else}
        <p class="mt-1.5 flex items-center gap-1.5 text-[10px] italic text-black-700 dark:text-black-600">
          <svg viewBox="0 0 16 16" class="h-2.5 w-2.5 animate-spin" fill="none" stroke="currentColor" stroke-width="2">
            <circle cx="8" cy="8" r="6" opacity="0.25"/><path d="M14 8a6 6 0 00-6-6" stroke-linecap="round"/>
          </svg>
          Loading details…
        </p>
      {/if}
    </div>
  {/if}
</div>
