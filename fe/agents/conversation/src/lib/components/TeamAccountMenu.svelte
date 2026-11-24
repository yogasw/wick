<script lang="ts">
  /* The roster's account row, at the foot of the sidebar (team-sidebar
     mockup): the viewer's initial and name open a menu that rises above
     it with the same items as wick's own account menu (nav.templ
     userMenu, sidebar variant): name and email, the Viewing as banner with
     its way back, Team settings (set apart at the top), Profile, Access Tokens,
     Connected Apps, MCP, Mini Tools, Admin panel (admins), Theme, versions, Sign out.
     The Team | Agents switch at the top of the sidebar is the way to the
     Agents pages, so there is no Agents item here.
     Esc or a press outside closes the menu; Esc gives focus back to the
     row. */
  import { tick } from "svelte";

  type Props = {
    viewerName: string;
    viewerEmail?: string;
    /** Picture URL; "" shows the initial, as the Agents sidebar does. */
    viewerAvatar?: string;
    isAdmin?: boolean;
    /** Who the admin is viewing wick as; "" when not impersonating. */
    viewingAs?: string;
    appVersion?: string;
    wickVersion?: string;
    buildCommit?: string;
    /** The Light/Dark switch: the mode on screen and the user's paired
        theme ids, posted to wick's /theme. null hides it (signed out). */
    theme: { mode: "light" | "dark"; light: string; dark: string } | null;
    onSettings: () => void;
  };
  let { viewerName, viewerEmail = "", viewerAvatar = "", isAdmin = false, viewingAs = "", appVersion = "", wickVersion = "", buildCommit = "", theme, onSettings }: Props = $props();

  type SourceDetails = {
    kind: string;
    repository?: string;
    branch?: string;
    upstream?: string;
    revision?: string;
    tag?: string;
    commit_date?: string;
    source_version?: string;
    build_time?: string;
    checked_at?: string;
    dirty?: boolean;
    behind?: number;
    ahead?: number;
    latest_release?: string;
    newer_releases?: number;
    compare_error?: string;
    release_error?: string;
  };

  let detailsOpen = $state(false);
  let details = $state<SourceDetails | null>(null);
  let loadingDetails = $state(false);
  let detailsError = $state("");
  const shortCommit = $derived(/^[0-9a-f]{13,40}$/i.test(buildCommit) ? buildCommit.slice(0, 12) : buildCommit);
  function displayDate(value?: string) {
    if (!value) return "Unknown";
    const date = new Date(value);
    return Number.isNaN(date.getTime()) ? value : date.toLocaleString();
  }
  async function toggleDetails() {
    detailsOpen = !detailsOpen;
    if (!detailsOpen || loadingDetails) return;
    loadingDetails = true;
    detailsError = "";
    try {
      const res = await fetch("/api/build-source", { credentials: "same-origin" });
      if (!res.ok) throw new Error("Build details unavailable");
      details = await res.json() as SourceDetails;
    } catch {
      detailsError = "Unable to check build details. Click the info button again to retry.";
    } finally {
      loadingDetails = false;
    }
  }

  let open = $state(false);
  let root = $state<HTMLDivElement>();
  let button = $state<HTMLButtonElement>();
  const initial = $derived((viewerName.trim()[0] ?? "?").toUpperCase());
  // /theme redirects back to this page, panel and all.
  const here = () => location.pathname + location.search;

  async function close(refocus: boolean) {
    open = false;
    detailsOpen = false;
    if (refocus) {
      await tick();
      button?.focus();
    }
  }
  function onKey(e: KeyboardEvent) {
    if (open && e.key === "Escape") {
      e.preventDefault();
      e.stopPropagation();
      void close(true);
    }
  }
  function onPointer(e: PointerEvent) {
    if (open && root && !root.contains(e.target as Node)) void close(false);
  }

  const item =
    "flex w-full items-center gap-2.5 px-4 py-2 text-left text-sm text-black-800 transition-colors hover:bg-white-200 hover:text-black-900 dark:text-black-600 dark:hover:bg-navy-800 dark:hover:text-white-100";
  const seg = "rounded-md px-2 py-1 text-xs font-medium transition-colors";
</script>

<svelte:window onkeydowncapture={onKey} onpointerdown={onPointer} />

<div class="relative flex items-center gap-1" bind:this={root}>
  <button
    type="button"
    bind:this={button}
    class="flex min-w-0 flex-1 items-center gap-2.5 rounded-xl px-2 py-1.5 text-left transition-colors hover:bg-white-300 focus:outline-none focus-visible:ring-2 focus-visible:ring-green-400 dark:hover:bg-navy-600 {open ? 'bg-white-300 dark:bg-navy-600' : ''}"
    aria-label="Account menu"
    aria-haspopup="menu"
    aria-expanded={open}
    onclick={() => { if (open) void close(false); else open = true; }}
  >
    <!-- Same avatar as the Agents sidebar's account row (nav.templ). -->
    {#if viewerAvatar}
      <img src={viewerAvatar} alt="" referrerpolicy="no-referrer" class="h-8 w-8 shrink-0 rounded-full border border-white-300 object-cover dark:border-navy-600" data-testid="account-avatar" />
    {:else}
      <span class="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-green-200 text-xs font-semibold text-green-700 select-none" aria-hidden="true">{initial}</span>
    {/if}
    <span class="min-w-0 flex-1 truncate text-sm font-medium text-black-900 dark:text-white-100" data-testid="account-name">{viewerName || "You"}</span>
    {#if viewingAs}<span class="h-2.5 w-2.5 shrink-0 rounded-full bg-cau-400" title="Viewing as {viewingAs}" data-testid="viewing-as-dot"></span>{/if}
    <svg class="h-3.5 w-3.5 shrink-0 text-black-700 transition-transform dark:text-black-600 {open ? '' : 'rotate-180'}" fill="none" stroke="currentColor" stroke-width="2.5" viewBox="0 0 24 24" aria-hidden="true"><path stroke-linecap="round" stroke-linejoin="round" d="m6 9 6 6 6-6" /></svg>
  </button>
  {#if open}
    <div class="absolute bottom-full left-0 z-50 mb-2 max-h-[80vh] w-64 overflow-y-auto rounded-xl border border-white-300 bg-white-100 shadow-lg dark:border-navy-600 dark:bg-navy-700" role="menu" aria-label="Account" data-account-menu>
      <div class="border-b border-white-300 px-4 py-3 dark:border-navy-600">
        <p class="truncate text-sm font-medium text-black-900 dark:text-white-100">{viewerName || "You"}</p>
        {#if viewerEmail}<p class="truncate text-xs text-black-700 dark:text-black-600">{viewerEmail}</p>{/if}
      </div>
      {#if viewingAs}
        <div class="border-b border-cau-400 bg-cau-100 px-4 py-3 text-cau-400" data-testid="viewing-as">
          <p class="truncate text-xs font-medium">Viewing as {viewingAs}</p>
          <p class="mt-1 text-xs leading-snug opacity-80">Actions you take are recorded as this user.</p>
          <form action="/admin/impersonate/stop" method="POST" class="mt-2">
            <button type="submit" class="flex w-full items-center justify-center gap-2 rounded-lg border border-cau-400 bg-white-100 px-3 py-2 text-xs font-medium text-cau-400 transition-colors hover:bg-white-200 dark:bg-navy-700">
              <svg class="h-4 w-4 shrink-0" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24" aria-hidden="true"><path stroke-linecap="round" stroke-linejoin="round" d="M9 15 3 9m0 0 6-6M3 9h12a6 6 0 0 1 0 12h-3" /></svg>
              Back to my account
            </button>
          </form>
        </div>
      {/if}
      <!-- Team's own settings, apart from the account-wide items below
           (the Agents sidebar does the same with Agent settings). -->
      <div class="border-b border-white-300 py-1 dark:border-navy-600" data-testid="space-settings">
        <button type="button" role="menuitem" class={item} onclick={() => { void close(false); onSettings(); }}>
          <svg class="h-4 w-4 shrink-0" fill="none" stroke="currentColor" stroke-width="1.5" viewBox="0 0 24 24" aria-hidden="true"><path stroke-linecap="round" stroke-linejoin="round" d="M9.594 3.94c.09-.542.56-.94 1.11-.94h2.593c.55 0 1.02.398 1.11.94l.213 1.281c.063.374.313.686.645.87.074.04.147.083.22.127.325.196.72.257 1.075.124l1.217-.456a1.125 1.125 0 0 1 1.37.49l1.296 2.247a1.125 1.125 0 0 1-.26 1.431l-1.003.827c-.293.241-.438.613-.43.992a7.723 7.723 0 0 1 0 .255c-.008.378.137.75.43.991l1.004.827c.424.35.534.955.26 1.43l-1.298 2.247a1.125 1.125 0 0 1-1.369.491l-1.217-.456c-.355-.133-.75-.072-1.076.124a6.47 6.47 0 0 1-.22.128c-.331.183-.581.495-.644.869l-.213 1.281c-.09.543-.56.94-1.11.94h-2.594c-.55 0-1.019-.398-1.11-.94l-.213-1.281c-.062-.374-.312-.686-.644-.87a6.52 6.52 0 0 1-.22-.127c-.325-.196-.72-.257-1.076-.124l-1.217.456a1.125 1.125 0 0 1-1.369-.49l-1.297-2.247a1.125 1.125 0 0 1 .26-1.431l1.004-.827c.292-.24.437-.613.43-.991a6.932 6.932 0 0 1 0-.255c.007-.38-.138-.751-.43-.992l-1.004-.827a1.125 1.125 0 0 1-.26-1.43l1.297-2.247a1.125 1.125 0 0 1 1.37-.491l1.216.456c.356.133.751.072 1.076-.124.072-.044.146-.086.22-.128.332-.183.582-.495.644-.869l.214-1.28Z" /><path stroke-linecap="round" stroke-linejoin="round" d="M15 12a3 3 0 1 1-6 0 3 3 0 0 1 6 0Z" /></svg>
          Team settings
        </button>
      </div>
      <div class="py-1">
        <!-- Same links and icons as wick's account menu (nav.templ). -->
        {#each [{ href: "/profile", label: "Profile", d: ["M15.75 6a3.75 3.75 0 1 1-7.5 0 3.75 3.75 0 0 1 7.5 0ZM4.501 20.118a7.5 7.5 0 0 1 14.998 0A17.933 17.933 0 0 1 12 21.75c-2.676 0-5.216-.584-7.499-1.632Z"] }, { href: "/profile/tokens", label: "Access Tokens", d: ["M15.75 5.25a3 3 0 0 1 3 3m3 0a6 6 0 0 1-7.029 5.912c-.563-.097-1.159.026-1.563.43L10.5 17.25H8.25v2.25H6v2.25H2.25v-2.818c0-.597.237-1.17.659-1.591l6.499-6.499c.404-.404.527-1 .43-1.563A6 6 0 1 1 21.75 8.25Z"] }, { href: "/profile/connections", label: "Connected Apps", d: ["M13.19 8.688a4.5 4.5 0 0 1 1.242 7.244l-4.5 4.5a4.5 4.5 0 0 1-6.364-6.364l1.757-1.757m13.35-.622 1.757-1.757a4.5 4.5 0 0 0-6.364-6.364l-4.5 4.5a4.5 4.5 0 0 0 1.242 7.244"] }, { href: "/profile/mcp", label: "MCP", d: ["M6.75 7.5l3 2.25-3 2.25m4.5 0h3m-9 8.25h13.5A2.25 2.25 0 0 0 21 18V6a2.25 2.25 0 0 0-2.25-2.25H5.25A2.25 2.25 0 0 0 3 6v12a2.25 2.25 0 0 0 2.25 2.25Z"] }] as l (l.href)}
          <a href={l.href} role="menuitem" class={item}>
            <svg class="h-4 w-4 shrink-0" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24" aria-hidden="true">{#each l.d as d}<path stroke-linecap="round" stroke-linejoin="round" {d} />{/each}</svg>
            {l.label}
          </a>
        {/each}
        <a href="/mini-tools" role="menuitem" class={item}>
          <svg class="h-4 w-4 shrink-0" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><path d="M6.5 2.5a3 3 0 00-3.5 4l-1 1 2.5 2.5 1-1a3 3 0 004-3.5L7.5 7 6 5.5l1-3z" stroke-linejoin="round"></path></svg>
          Mini Tools
        </a>
        {#if isAdmin}
          <a href="/admin" role="menuitem" class={item}>
            <svg class="h-4 w-4 shrink-0" fill="none" stroke="currentColor" stroke-width="1.5" viewBox="0 0 24 24" aria-hidden="true"><path stroke-linecap="round" stroke-linejoin="round" d="M12 3l7 3v5c0 4.5-3 8-7 10-4-2-7-5.5-7-10V6l7-3z" /></svg>
            Admin panel
          </a>
        {/if}
        {#if theme}
          <div class="flex items-center justify-between gap-2 px-4 py-2" role="group" aria-label="Theme">
            <span class="flex items-center gap-2.5 text-sm text-black-800 dark:text-black-600">
              <svg class="h-4 w-4 shrink-0" fill="none" stroke="currentColor" stroke-width="1.5" viewBox="0 0 24 24" aria-hidden="true"><path stroke-linecap="round" stroke-linejoin="round" d="M21.752 15.002A9.72 9.72 0 0 1 18 15.75 9.75 9.75 0 0 1 8.25 6c0-1.33.266-2.597.748-3.752A9.753 9.753 0 0 0 3 11.25 9.75 9.75 0 0 0 12.75 21a9.753 9.753 0 0 0 9.002-5.998Z" /></svg>
              Theme
            </span>
            <span class="flex rounded-lg bg-white-300 p-0.5 dark:bg-navy-800">
              {#each [{ mode: "light", id: theme.light, label: "Light" }, { mode: "dark", id: theme.dark, label: "Dark" }] as t (t.mode)}
                <form method="POST" action="/theme" class="flex">
                  <input type="hidden" name="theme" value={t.id} />
                  <input type="hidden" name="redirect" value={here()} />
                  <button
                    type="submit"
                    role="menuitemradio"
                    aria-checked={theme.mode === t.mode}
                    class="{seg} {theme.mode === t.mode
                      ? 'bg-white-100 text-black-900 shadow-sm dark:bg-navy-600 dark:text-white-100'
                      : 'text-black-700 hover:text-black-900 dark:text-black-600 dark:hover:text-white-100'}"
                  >{t.label}</button>
                </form>
              {/each}
            </span>
          </div>
        {/if}
      </div>
      {#if appVersion || wickVersion || buildCommit}
        <div class="border-t border-white-300 px-4 py-2 text-xs text-black-700 dark:border-navy-600 dark:text-black-600" data-testid="versions">
          {#if appVersion}<p class="flex justify-between"><span>App</span><span>{appVersion}</span></p>{/if}
          {#if wickVersion}<p class="flex justify-between"><span>Wick</span><span>{wickVersion}</span></p>{/if}
          <div class="flex items-center justify-between gap-2">
            {#if buildCommit}<span>Commit</span><span class="ml-auto font-mono" title={buildCommit}>{shortCommit}</span>{:else}<span>Build details</span>{/if}
            <button type="button" class="rounded p-0.5 hover:bg-white-300 focus-visible:ring-2 focus-visible:ring-green-400 dark:hover:bg-navy-600" aria-label="Build information" aria-expanded={detailsOpen} title="Build source, commit and release status" onclick={toggleDetails}>
              <svg class="h-3.5 w-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true"><circle cx="12" cy="12" r="9"/><path d="M12 11v5M12 7.5v1"/></svg>
            </button>
          </div>
          {#if detailsOpen}
            <div class="mt-2 space-y-1 border-t border-white-300 pt-2 dark:border-navy-600" data-testid="build-details" aria-live="polite">
              {#if loadingDetails}<p>Checking upstream…</p>{/if}
              {#if detailsError}<p>{detailsError}</p>{/if}
              {#if details}
                <p class="font-medium">{details.kind === "source" ? "Source build" : details.kind === "release" ? "Wick release" : "Build source unknown"}</p>
                {#if details.repository}<p class="break-all">Repository: {details.repository}</p>{/if}
                {#if details.branch}<p class="break-all">Branch: {details.branch}</p>{/if}
                {#if details.tag}<p class="break-all">Tag: {details.tag}</p>{/if}
                {#if details.upstream}<p class="break-all">Upstream: {details.upstream}</p>{/if}
                {#if details.revision}
                  <p class="break-all">Revision: <span class="font-mono" title={details.revision}>{details.revision.slice(0, 12)}</span></p>
                  {#if details.repository}<a class="underline" href={"https://github.com/" + details.repository + "/commit/" + details.revision} target="_blank" rel="noopener noreferrer">View commit on GitHub</a>{/if}
                {/if}
                {#if details.dirty}<p>Dirty build — revision is the base commit; local edits are not included in the comparison.</p>{/if}
                {#if details.commit_date}<p>Commit date: {displayDate(details.commit_date)}</p>{/if}
                {#if details.build_time}<p>Built: {displayDate(details.build_time)}</p>{/if}
                {#if details.behind !== undefined && details.ahead !== undefined}
                  <p>Behind origin: <strong>{details.behind}</strong> commits</p>
                  <p>Ahead of origin: <strong>{details.ahead}</strong> commits</p>
                {:else if details.compare_error}<p>{details.compare_error}</p>{/if}
                <div class="border-t border-white-300 pt-1 dark:border-navy-600">
                  {#if details.source_version}<p>Wick base version: {details.source_version}</p>{/if}
                  {#if details.latest_release}<p>Latest stable release: {details.latest_release}</p>{/if}
                  {#if details.newer_releases !== undefined}<p><strong>{details.newer_releases}</strong> newer official releases</p>{:else if details.release_error}<p>{details.release_error}</p>{/if}
                  <a class="underline" href="https://github.com/yogasw/wick/releases" target="_blank" rel="noopener noreferrer">View official releases</a>
                </div>
                {#if details.checked_at}<p class="opacity-70">Checked: {displayDate(details.checked_at)}</p>{/if}
              {/if}
            </div>
          {/if}
        </div>
      {/if}
      <form method="POST" action="/auth/logout" class="border-t border-white-300 py-1 dark:border-navy-600">
        <button type="submit" role="menuitem" class={item}>
          <svg class="h-4 w-4 shrink-0" fill="none" stroke="currentColor" stroke-width="1.5" viewBox="0 0 24 24" aria-hidden="true"><path stroke-linecap="round" stroke-linejoin="round" d="M15.75 9V5.25A2.25 2.25 0 0 0 13.5 3h-6a2.25 2.25 0 0 0-2.25 2.25v13.5A2.25 2.25 0 0 0 7.5 21h6a2.25 2.25 0 0 0 2.25-2.25V15m3 0 3-3m0 0-3-3m3 3H9" /></svg>
          Sign out
        </button>
      </form>
    </div>
  {/if}
</div>
