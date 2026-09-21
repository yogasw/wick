<script lang="ts">
  import { onMount } from "svelte";
  import type { AnalyticsPoint, AnalyticsProject, AnalyticsResponse, AnalyticsUser } from "$lib/types";
  import { ago, isDormant, channelClass, channelColor } from "$lib/format";
  import { loadAnalytics } from "$lib/stream";
  import Chart from "$lib/Chart.svelte";
  import RecentSessions from "$lib/RecentSessions.svelte";
  import { UsageReport, fetchUsageReport, compactTokens, formatCost } from "@wick-fe/common-ui";
  import type { UsageReportData } from "@wick-fe/common-ui";

  type Props = { endpoint: string };
  let { endpoint }: Props = $props();
  /* The token ledger is served from the same mount as this page's own
     data, so it is derived from the endpoint rather than hard-coded —
     the page does not know where it is mounted either. */
  const ledgerEndpoint = $derived(endpoint.replace(/\/users\.json$/, "/ledger"));
  /* The ledger reads the SAME range as the page. One filter for
     everything below it was the point of the filter bar; a card with its
     own range turns "who is expensive this week" into a comparison
     between two different weeks. */
  const ledgerWindow = $derived(
    range === "custom" ? "custom" : range === "all" ? "all" : range === 365 ? "1y" : range === 1 ? "today" : `${range}d`,
  );
  const ledgerSince = $derived(range === "custom" ? customFrom : "");
  const ledgerUntil = $derived(range === "custom" ? customTo : "");
  /* A second copy of the report, for the drill-downs: the card renders
     the fleet, a person's panel needs that person's row. The server
     caches per range, so asking twice costs one walk. */
  let ledger = $state<UsageReportData | null>(null);
  async function loadLedger() {
    try {
      ledger = await fetchUsageReport(ledgerEndpoint, false, ledgerWindow, ledgerSince, ledgerUntil, {
        channels: picked,
        instances: pickedInstances,
      });
    } catch {
      ledger = null; // a missing ledger must not take the page down
    }
  }
  const userSpend = $derived.by(() => {
    const id = openUser?.id;
    return id ? (ledger?.by_user.find((r) => r.key === id) ?? null) : null;
  });
  const projectSpend = $derived.by(() => {
    const id = openProject?.id;
    return id ? (ledger?.by_project.find((r) => r.key === id) ?? null) : null;
  });

  type Tab = "people" | "projects" | "channels" | "providers";
  /** "all" runs back to the oldest conversation; "custom" uses the dates. */
  type Range = 1 | 7 | 30 | 90 | 365 | "all" | "custom";

  let data = $state<AnalyticsResponse | null>(null);
  let error = $state<string | null>(null);
  let loading = $state(true);
  let progress = $state({ done: 0, total: 0 });
  /* Today by default. The page reads every session on disk for the range
     it is given, so a month is the slowest possible first paint — and
     "what is happening now" is the question people open it with. A wider
     window is one click away; the wait for it is not. */
  let range = $state<Range>(1);
  let customFrom = $state("");
  let customTo = $state("");
  /** Channels the whole page is restricted to. Empty = all of them. */
  let picked = $state<string[]>([]);
  /** Specific bots ("slack:<owner id>"). Empty = every bot of those channels. */
  let pickedInstances = $state<string[]>([]);
  let query = $state("");
  let metric = $state<"sessions" | "logins">("sessions");
  /** Series drawn over the total. Empty = just the total. */
  let overlaid = $state<string[]>([]);
  let overlaidProviders = $state<string[]>([]);
  let openProject = $state<AnalyticsProject | null>(null);
  /** Clicking a person opens them here rather than rearranging the page. */
  let openUser = $state<AnalyticsUser | null>(null);
  let tab = $state<Tab>("people");

  async function load() {
    loading = true;
    error = null;
    // The ledger is part of "the page under this filter", not a card with
    // a life of its own: it reloads on the same call, in parallel, so a
    // channel click cannot leave the cost figures describing the
    // previous selection.
    void loadLedger();
    progress = { done: 0, total: 0 };
    try {
      data = await loadAnalytics(endpoint, {
        days: range === "custom" ? undefined : range,
        from: range === "custom" ? customFrom : undefined,
        to: range === "custom" ? customTo : undefined,
        channels: picked,
        instances: pickedInstances,
        onProgress: (done, total) => (progress = { done, total }),
      });
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      loading = false;
    }
  }

  onMount(() => load());

  function setRange(r: Range) {
    if (r === range && r !== "custom") return;
    range = r;
    if (r === "custom") {
      // Seed the inputs with the window already on screen, so "custom"
      // starts from what you were looking at rather than from blank.
      if (!customFrom) customFrom = data?.window.from ?? "";
      if (!customTo) customTo = data?.window.to ?? "";
      if (!customFrom || !customTo) return; // wait for both before asking the server
    }
    // Every reload re-reads the session files; the numbers below all move
    // with it, which is the point — a filter that only moved the chart
    // would be reporting two different slices at once.
    void load();
  }

  function toggleChannel(ch: string) {
    picked = picked.includes(ch) ? picked.filter((c) => c !== ch) : [...picked, ch];
    // A bot belongs to a channel, so narrowing the channels drops any bot
    // filter that no longer makes sense rather than silently showing zero.
    pickedInstances = pickedInstances.filter((k) => picked.length === 0 || picked.includes(k.split(":")[0]));
    void load();
  }

  function toggleInstance(key: string) {
    pickedInstances = pickedInstances.includes(key)
      ? pickedInstances.filter((k) => k !== key)
      : [...pickedInstances, key];
    void load();
  }

  function clearChannels() {
    if (picked.length === 0 && pickedInstances.length === 0) return;
    picked = [];
    pickedInstances = [];
    void load();
  }

  const users = $derived.by<AnalyticsUser[]>(() => {
    const all = data?.users ?? [];
    const q = query.trim().toLowerCase();
    return all.filter((u) => {
      if (!q) return true;
      return (
        u.name.toLowerCase().includes(q) ||
        u.email.toLowerCase().includes(q) ||
        (u.projects ?? []).some((p) => p.name.toLowerCase().includes(q) || p.id.toLowerCase().includes(q)) ||
        (u.agents ?? []).some((a) => a.toLowerCase().includes(q)) ||
        (u.tokens ?? []).some((t) => t.name.toLowerCase().includes(q))
      );
    });
  });

  const projects = $derived.by<AnalyticsProject[]>(() => {
    const all = data?.projects ?? [];
    const q = query.trim().toLowerCase();
    if (!q) return all;
    return all.filter((p) => p.name.toLowerCase().includes(q) || p.id.toLowerCase().includes(q));
  });

  const neverSignedIn = $derived((data?.users ?? []).filter((u) => !u.last_login_at).length);
  // Sign-ins were not recorded at all until recently — wick's sessions are
  // stateless, so nothing was ever written down. An empty column means "no
  // record", not "nobody signed in", and the page has to say which.
  const loginsRecorded = $derived(Boolean(data?.logins_recorded_since));

  const projectsByID = $derived.by(() => {
    const m = new Map<string, AnalyticsProject>();
    for (const p of data?.projects ?? []) m.set(p.id, p);
    return m;
  });

  const points = $derived<AnalyticsPoint[]>(data?.series.points ?? []);
  const overlays = $derived([
    ...overlaid.map((ch) => ({ label: ch, color: channelColor(ch), points: data?.series.by_channel?.[ch] ?? [] })),
    ...overlaidProviders.map((key) => ({ label: key, color: channelColor(key), points: data?.series.by_provider?.[key] ?? [] })),
  ].filter((o) => o.points.length > 0));

  const providers = $derived(data?.providers ?? []);
  const knownChannels = $derived(data?.known_channels ?? data?.channels.map((c) => c.channel) ?? []);

  // What the window itself says — distinct from the all-time cards above,
  // which do not move when the range changes.
  const windowStats = $derived.by(() => {
    const conversations = points.reduce((n, p) => n + (p.sessions ?? 0), 0);
    const logins = points.reduce((n, p) => n + (p.logins ?? 0), 0);
    const busiest = points.reduce<AnalyticsPoint | null>((best, p) => ((p.sessions ?? 0) > (best?.sessions ?? -1) ? p : best), null);
    const activeDays = points.filter((p) => (p.sessions ?? 0) > 0).length;
    const peakPeople = points.reduce((n, p) => Math.max(n, p.people ?? 0), 0);
    return { conversations, logins, busiest, activeDays, peakPeople, days: points.length };
  });

  /** Channel totals inside the window, busiest first — the answer to
   *  "which door is actually active", which the all-time table cannot give. */
  const channelsInWindow = $derived.by(() => {
    const by = data?.series.by_channel ?? {};
    return Object.entries(by)
      .map(([channel, pts]) => ({ channel, sessions: pts.reduce((n, p) => n + (p.sessions ?? 0), 0) }))
      .filter((c) => c.sessions > 0)
      .sort((a, b) => b.sessions - a.sessions);
  });

  const topPeople = $derived([...(data?.users ?? [])].sort((a, b) => b.sessions - a.sessions).filter((u) => u.sessions > 0).slice(0, 5));
  const topProjects = $derived([...(data?.projects ?? [])].sort((a, b) => b.sessions - a.sessions).slice(0, 5));

  function toggleOverlay(ch: string) {
    overlaid = overlaid.includes(ch) ? overlaid.filter((c) => c !== ch) : [...overlaid, ch];
  }

  function openProjectByID(id: string) {
    const p = projectsByID.get(id);
    if (p) {
      openUser = null;
      openProject = p;
    }
  }

  const tabs: Array<{ key: Tab; label: string; count?: number }> = $derived([
    { key: "people", label: "People", count: data?.users.length },
    { key: "projects", label: "Projects", count: data?.projects.length },
    { key: "channels", label: "Channels", count: data?.channels.length },
    { key: "providers", label: "Providers", count: providers.length },
  ]);

  const ranges: Array<{ key: Range; label: string }> = [
    { key: 1, label: "Today" },
    { key: 7, label: "7d" },
    { key: 30, label: "30d" },
    { key: 90, label: "90d" },
    { key: 365, label: "1y" },
    { key: "all", label: "All" },
    { key: "custom", label: "Custom" },
  ];

  function toggleProviderOverlay(key: string) {
    overlaidProviders = overlaidProviders.includes(key)
      ? overlaidProviders.filter((k) => k !== key)
      : [...overlaidProviders, key];
  }

  const pct = $derived(progress.total > 0 ? Math.round((progress.done / progress.total) * 100) : 0);
</script>

{#snippet bar()}
  <div class="h-1.5 w-full overflow-hidden rounded-full bg-white-300 dark:bg-navy-600">
    <div
      class="h-full rounded-full bg-green-500 transition-[width] duration-200 {progress.total === 0 ? 'w-1/4 animate-pulse' : ''}"
      style={progress.total > 0 ? `width:${pct}%` : ""}
    ></div>
  </div>
{/snippet}

<div class="space-y-5">
  <div>
    <h1 class="text-[1.375rem] font-semibold text-black-900 dark:text-white-100">People &amp; usage</h1>
    <p class="mt-1 text-sm text-black-800 dark:text-black-600">
      Who signs in, who is still working here, through which channel, and — when the caller is a machine —
      with whose token. Read from the accounts table and the sessions on disk.
    </p>
  </div>

  {#if error}
    <div class="rounded-xl border border-neg-400/40 bg-neg-400/10 px-4 py-3 text-sm text-neg-400">
      Could not load the numbers: {error}
    </div>
  {/if}

  {#if loading && !data}
    <!-- First load: there is nothing to show yet, so the progress IS the page.
         Real counts, not a spinner — the server streams how far it has read. -->
    <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-5 shadow-sm">
      <p class="text-sm text-black-800 dark:text-black-600">
        Reading conversations…
        {#if progress.total > 0}
          <span class="font-mono text-black-900 dark:text-white-100">{progress.done}</span>
          <span class="text-black-600 dark:text-black-700">/ {progress.total}</span>
        {/if}
      </p>
      <div class="mt-3">{@render bar()}</div>
    </div>
  {:else if data}
    <!-- One filter bar for the whole page. Everything below — totals,
         people, projects, providers, the chart — is computed under it. -->
    <div class="flex flex-wrap items-center gap-2 rounded-xl border border-white-300 bg-white-100 px-4 py-3 shadow-sm dark:border-navy-600 dark:bg-navy-700">
      <span class="text-xs font-medium uppercase tracking-wide text-black-700 dark:text-black-600">Range</span>
      <div class="flex gap-0.5 rounded-lg border border-white-300 p-0.5 dark:border-navy-600">
        {#each ranges as r}
          <button
            type="button"
            onclick={() => setRange(r.key)}
            disabled={loading}
            class="rounded px-2 py-1 text-xs disabled:opacity-50 {range === r.key
              ? 'bg-green-500/15 font-medium text-green-700 dark:text-green-300'
              : 'text-black-700 dark:text-black-600'}"
          >
            {r.label}
          </button>
        {/each}
      </div>
      {#if range === "custom"}
        <input type="date" bind:value={customFrom} onchange={() => load()} class="rounded-lg border border-white-400 bg-white-100 px-2 py-1 text-xs text-black-900 dark:border-navy-600 dark:bg-navy-800 dark:text-white-100" />
        <span class="text-xs text-black-600 dark:text-black-700">→</span>
        <input type="date" bind:value={customTo} onchange={() => load()} class="rounded-lg border border-white-400 bg-white-100 px-2 py-1 text-xs text-black-900 dark:border-navy-600 dark:bg-navy-800 dark:text-white-100" />
      {/if}

      <span class="ml-2 text-xs font-medium uppercase tracking-wide text-black-700 dark:text-black-600">Channels</span>
      <button
        type="button"
        onclick={clearChannels}
        disabled={loading}
        class="rounded-lg border px-2 py-1 text-xs disabled:opacity-50 {picked.length === 0
          ? 'border-green-400 bg-green-50 text-green-700 dark:bg-green-900/20 dark:text-green-300'
          : 'border-white-300 text-black-700 dark:border-navy-600 dark:text-black-600'}"
      >
        All
      </button>
      {#each knownChannels as ch}
        <button
          type="button"
          onclick={() => toggleChannel(ch)}
          disabled={loading}
          class="rounded-lg border px-2 py-1 text-xs disabled:opacity-50 {picked.includes(ch)
            ? 'border-green-400'
            : 'border-white-300 dark:border-navy-600'}"
        >
          <span class="rounded px-1.5 py-0.5 text-[10px] font-medium {channelClass(ch)}">{ch}</span>
        </button>
      {/each}
      {#if pickedInstances.length}
        <span class="text-xs text-black-600 dark:text-black-700">·</span>
        {#each pickedInstances as key}
          {@const inst = data.channels.flatMap((c) => c.instances ?? []).find((i) => i.key === key)}
          <button
            type="button"
            onclick={() => toggleInstance(key)}
            class="rounded-lg border border-green-400 px-2 py-1 text-xs text-green-700 dark:text-green-300"
          >
            {inst?.owner_name || key} ✕
          </button>
        {/each}
      {/if}
      <span class="ml-auto text-xs text-black-600 dark:text-black-700">
        {data.window.from} → {data.window.to}{picked.length > 0 ? ` · ${picked.join(" + ")}` : ""}
      </span>
    </div>

    <!-- Totals. Accounts is all-time (an account exists regardless of a
         date range); everything else is the range. -->
    <div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
      {#each [
        { label: "Accounts", value: data.total_users, hint: loginsRecorded ? `${neverSignedIn} never signed in` : "sign-ins not recorded yet" },
        { label: "People in range", value: data.users_in_window, hint: `${data.active_users_7d} worked in the last 7 days` },
        { label: "Conversations in range", value: data.sessions, hint: `of ${data.sessions_all_time} all time · ${data.unattributed_sessions} unattributed` },
        { label: "Channels in range", value: data.channels.length, hint: data.channels.map((c) => c.channel).join(", ") || "—" },
      ] as card}
        <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-4 shadow-sm">
          <p class="text-xs font-medium uppercase tracking-wide text-black-700 dark:text-black-600">{card.label}</p>
          <p class="mt-1 text-2xl font-semibold text-black-900 dark:text-white-100">{card.value}</p>
          <p class="mt-0.5 truncate text-xs text-black-600 dark:text-black-700" title={card.hint}>{card.hint}</p>
        </div>
      {/each}
    </div>

    <!-- What it COST. Same component as the providers page, reading the
         same report from this page's own mount: two renderers over one
         ledger, so the two pages cannot disagree about the bill. It
         carries its own range, because "spent today" and "who signed in
         over 30 days" are different questions that happen to share a
         page. -->
    <!-- Fed from the page's own fetch: the drill-downs need these rows
         anyway, so asking the server twice for the same range would be a
         second walk over every session for nothing. -->
    <UsageReport
      base=""
      endpoint={ledgerEndpoint}
      title="Token usage"
      range={ledgerWindow}
      since={ledgerSince}
      until={ledgerUntil}
      channels={picked}
      instances={pickedInstances}
      report={ledger}
    />

    <!-- Overview: always on top, never a tab. It is the context every list
         below is read against. -->
    <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-sm">
      <div class="flex flex-wrap items-center gap-2 border-b border-white-300 dark:border-navy-600 px-5 py-3">
        <h2 class="text-sm font-semibold text-black-900 dark:text-white-100">Overview</h2>
        <span class="text-xs text-black-600 dark:text-black-700">
          {data.series.from} → today · {windowStats.days} days
        </span>
        <div class="ml-auto flex flex-wrap items-center gap-2">
          <div class="flex gap-0.5 rounded-lg border border-white-300 p-0.5 dark:border-navy-600">
            {#each [["sessions", "Conversations"], ["logins", "Sign-ins"]] as [key, label]}
              <button
                type="button"
                onclick={() => (metric = key as "sessions" | "logins")}
                class="rounded px-2 py-1 text-xs {metric === key
                  ? 'bg-green-500/15 font-medium text-green-700 dark:text-green-300'
                  : 'text-black-700 dark:text-black-600'}"
              >
                {label}
              </button>
            {/each}
          </div>
        </div>
      </div>

      {#if loading}
        <!-- Reloading with data already on screen: the old numbers stay, the
             bar says a bigger window is being read. "All" on a real install
             is thousands of files, and silence there reads as a hang. -->
        <div class="flex items-center gap-3 border-b border-white-300 px-5 py-2 dark:border-navy-600">
          <span class="whitespace-nowrap text-xs text-black-700 dark:text-black-600">
            Reading {progress.total > 0 ? `${progress.done}/${progress.total}` : "…"}
          </span>
          <div class="flex-1">{@render bar()}</div>
        </div>
      {/if}

      <div class="px-5 py-4">
        <Chart {points} {metric} {overlays} />
      </div>

      <!-- What this window contains. -->
      <div class="grid gap-3 border-t border-white-300 px-5 py-4 dark:border-navy-600 sm:grid-cols-2 lg:grid-cols-4">
        {#each [
          { label: "Conversations started", value: windowStats.conversations, hint: `over ${windowStats.days} days` },
          { label: "Busiest day", value: windowStats.busiest?.sessions ?? 0, hint: windowStats.busiest?.date ?? "—" },
          { label: "Days with activity", value: windowStats.activeDays, hint: `${windowStats.days - windowStats.activeDays} quiet` },
          { label: "Sign-ins", value: windowStats.logins, hint: loginsRecorded ? "in this window" : "not recorded yet" },
        ] as s}
          <div>
            <p class="text-[11px] uppercase tracking-wide text-black-700 dark:text-black-600">{s.label}</p>
            <p class="text-lg font-semibold text-black-900 dark:text-white-100">{s.value}</p>
            <p class="truncate text-[11px] text-black-600 dark:text-black-700">{s.hint}</p>
          </div>
        {/each}
      </div>

      <!-- Which door is busy, in THIS window. Click to draw it on the chart. -->
      <div class="border-t border-white-300 px-5 py-4 dark:border-navy-600">
        <p class="mb-2 text-[11px] uppercase tracking-wide text-black-700 dark:text-black-600">
          By channel · click to draw it over the total
        </p>
        <div class="flex flex-wrap gap-1.5">
          {#each channelsInWindow as c}
            {@const share = windowStats.conversations > 0 ? Math.round((c.sessions / windowStats.conversations) * 100) : 0}
            <button
              type="button"
              onclick={() => toggleOverlay(c.channel)}
              class="flex items-center gap-1.5 rounded-lg border px-2 py-1 text-xs transition-colors {overlaid.includes(c.channel)
                ? 'border-green-400'
                : 'border-white-300 hover:border-green-400 dark:border-navy-600'}"
            >
              <span class="h-2 w-2 rounded-full" style={`background:${channelColor(c.channel)}`}></span>
              <span class="font-medium text-black-900 dark:text-white-100">{c.channel}</span>
              <span class="font-mono text-black-800 dark:text-black-600">{c.sessions}</span>
              <span class="text-black-600 dark:text-black-700">{share}%</span>
            </button>
          {:else}
            <span class="text-xs text-black-600 dark:text-black-700">No conversations in this window.</span>
          {/each}
        </div>
      </div>

      <!-- Which account, and which model. Same axis, same window. -->
      {#if providers.length}
        <div class="border-t border-white-300 px-5 py-4 dark:border-navy-600">
          <p class="mb-2 text-[11px] uppercase tracking-wide text-black-700 dark:text-black-600">
            By provider · click to draw it over the total
          </p>
          <div class="flex flex-wrap gap-1.5">
            {#each providers as pv}
              {@const share = windowStats.conversations > 0 ? Math.round((pv.sessions / windowStats.conversations) * 100) : 0}
              <button
                type="button"
                onclick={() => toggleProviderOverlay(pv.key)}
                title={(pv.models ?? []).map((m) => `${m.key} ${m.sessions}`).join(" · ")}
                class="flex items-center gap-1.5 rounded-lg border px-2 py-1 text-xs transition-colors {overlaidProviders.includes(pv.key)
                  ? 'border-green-400'
                  : 'border-white-300 hover:border-green-400 dark:border-navy-600'}"
              >
                <span class="h-2 w-2 rounded-full" style={`background:${channelColor(pv.key)}`}></span>
                <span class="font-medium text-black-900 dark:text-white-100">{pv.key}</span>
                <span class="font-mono text-black-800 dark:text-black-600">{pv.sessions}</span>
                <span class="text-black-600 dark:text-black-700">{share}%</span>
              </button>
            {/each}
          </div>
        </div>
      {/if}

      <!-- Leaderboards, over the same range as everything else. -->
      <div class="grid gap-5 border-t border-white-300 px-5 py-4 dark:border-navy-600 sm:grid-cols-2">
        <div>
          <p class="mb-2 text-[11px] uppercase tracking-wide text-black-700 dark:text-black-600">Busiest people in range</p>
          <div class="space-y-1">
            {#each topPeople as u}
              <button type="button" onclick={() => (openUser = u)} class="flex w-full items-baseline gap-2 text-xs hover:underline">
                <span class="truncate text-black-900 dark:text-white-100">{u.name || u.email}</span>
                <span class="ml-auto font-mono text-black-800 dark:text-black-600">{u.sessions}</span>
              </button>
            {:else}
              <span class="text-xs text-black-600 dark:text-black-700">Nothing attributed yet.</span>
            {/each}
          </div>
        </div>
        <div>
          <p class="mb-2 text-[11px] uppercase tracking-wide text-black-700 dark:text-black-600">Busiest projects in range</p>
          <div class="space-y-1">
            {#each topProjects as p}
              <button type="button" onclick={() => (openProject = p)} class="flex w-full items-baseline gap-2 text-xs hover:underline">
                <span class="truncate text-black-900 dark:text-white-100">{p.name}</span>
                <span class="ml-auto font-mono text-black-800 dark:text-black-600">{p.sessions}</span>
              </button>
            {:else}
              <span class="text-xs text-black-600 dark:text-black-700">No projects yet.</span>
            {/each}
          </div>
        </div>
      </div>
    </div>

    <p class="text-xs text-black-600 dark:text-black-700">
      The curve counts conversations by the day they started — that is the only history the session files keep.
      {#if loginsRecorded}
        Sign-ins recorded since {data.logins_recorded_since?.slice(0, 10)}; anything before that was never written down.
      {:else}
        Sign-ins have only just started being recorded — wick's own sessions are stateless, so there is no history
        before now. An empty “Last login” means “no record”, not “never signed in”.
      {/if}
      Generated {ago(data.generated_at)}.
    </p>

    <!-- Lists, one tab each. -->
    <div class="flex flex-wrap items-center gap-1 border-b border-white-300 dark:border-navy-600">
      {#each tabs as t}
        <button
          type="button"
          onclick={() => (tab = t.key)}
          class="-mb-px border-b-2 px-3 py-2 text-sm transition-colors {tab === t.key
            ? 'border-green-500 font-medium text-black-900 dark:text-white-100'
            : 'border-transparent text-black-700 hover:text-black-900 dark:text-black-600 dark:hover:text-white-100'}"
        >
          {t.label}
          {#if t.count !== undefined}
            <span class="ml-1 rounded bg-white-300 px-1.5 py-0.5 text-[10px] text-black-700 dark:bg-navy-600 dark:text-black-600">{t.count}</span>
          {/if}
        </button>
      {/each}
    </div>

    {#if tab === "people"}
      <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-sm overflow-hidden">
        <div class="flex flex-wrap items-center gap-2 border-b border-white-300 dark:border-navy-600 px-5 py-3">
          <h2 class="text-sm font-semibold text-black-900 dark:text-white-100">People</h2>
          <span class="rounded bg-white-300 px-2 py-0.5 text-xs font-medium text-black-700 dark:bg-navy-600 dark:text-black-600">{users.length}</span>
          <span class="text-xs text-black-600 dark:text-black-700">click a name for their detail</span>
          <input
            type="text"
            bind:value={query}
            placeholder="Search name, email, project, agent, token"
            class="ml-auto min-w-[14rem] flex-1 rounded-lg border border-white-400 bg-white-100 px-3 py-1.5 text-xs text-black-900 dark:border-navy-600 dark:bg-navy-800 dark:text-white-100"
          />
        </div>
        <div class="overflow-x-auto">
          <table class="w-full text-xs">
            <thead>
              <tr class="border-b border-white-300 text-left text-black-700 dark:border-navy-600 dark:text-black-600">
                <th class="px-5 py-2.5">Person</th>
                <th class="px-5 py-2.5">Last login</th>
                <th class="px-5 py-2.5">Last activity</th>
                <th class="px-5 py-2.5">Conversations</th>
                <th class="px-5 py-2.5">Channels</th>
                <th class="px-5 py-2.5">Provider · model</th>
                <th class="px-5 py-2.5">Projects</th>
                <th class="px-5 py-2.5">Tokens</th>
              </tr>
            </thead>
            <tbody>
              {#each users as u (u.id)}
                {@const dormant = isDormant(u.last_active_at ?? u.last_login_at)}
                <tr class="border-b border-white-300 last:border-0 dark:border-navy-600 {dormant ? 'opacity-60' : ''}">
                  <td class="px-5 py-2">
                    <div class="flex items-center gap-2">
                      <button
                        type="button"
                        onclick={() => (openUser = u)}
                        class="font-medium text-black-900 hover:underline dark:text-white-100"
                      >
                        {u.name || u.email}
                      </button>
                      {#if u.role === "owner" || u.role === "admin"}
                        <span class="rounded bg-amber-100 px-1.5 py-0.5 text-[10px] font-medium text-amber-700 dark:bg-amber-900/40 dark:text-amber-300">{u.role}</span>
                      {/if}
                      {#if !u.approved}
                        <span class="rounded bg-neg-400/15 px-1.5 py-0.5 text-[10px] font-medium text-neg-400">not approved</span>
                      {/if}
                      {#if u.signed_in}
                        <span class="rounded bg-green-100 px-1.5 py-0.5 text-[10px] font-medium text-green-700 dark:bg-green-900/40 dark:text-green-300" title="a browser session of theirs is still valid">signed in</span>
                      {/if}
                    </div>
                    <div class="text-[11px] text-black-600 dark:text-black-700">{u.email}</div>
                  </td>
                  <td
                    class="px-5 py-2 whitespace-nowrap text-black-800 dark:text-black-600"
                    title={u.last_login_at ?? (loginsRecorded ? "never signed in" : "sign-ins are only recorded from this version onward")}
                  >
                    {#if u.last_login_at}
                      {ago(u.last_login_at)}
                      {#if u.logins > 1}<span class="text-black-600 dark:text-black-700"> · {u.logins}×</span>{/if}
                    {:else}
                      <span class="text-black-600 dark:text-black-700">{loginsRecorded ? "never" : "no record"}</span>
                    {/if}
                  </td>
                  <td class="px-5 py-2 whitespace-nowrap text-black-800 dark:text-black-600" title={u.last_active_at ?? "no sessions"}>{ago(u.last_active_at)}</td>
                  <td class="px-5 py-2 font-mono text-black-800 dark:text-black-600">
                    {u.sessions}{#if u.joined > 0}<span class="text-black-600 dark:text-black-700" title="conversations they took part in but did not start"> +{u.joined}</span>{/if}
                  </td>
                  <td class="px-5 py-2">
                    <div class="flex flex-wrap gap-1">
                      {#each u.channels ?? [] as ch}
                        <span class="rounded px-1.5 py-0.5 text-[10px] font-medium {channelClass(ch)}">{ch}</span>
                      {/each}
                    </div>
                  </td>
                  <td class="px-5 py-2 max-w-[16rem]">
                    {#if u.providers?.length}
                      <div class="flex items-center gap-1.5">
                        <span class="h-2 w-2 shrink-0 rounded-full" style={`background:${channelColor(u.providers[0].key)}`}></span>
                        <span class="truncate text-black-900 dark:text-white-100" title={u.providers.map((p) => `${p.key} ${p.sessions}`).join(" · ")}>
                          {u.providers[0].key}
                        </span>
                        <span class="font-mono text-black-700 dark:text-black-600">{u.providers[0].sessions}</span>
                        {#if u.providers.length > 1}
                          <span class="text-black-600 dark:text-black-700" title={u.providers.slice(1).map((p) => `${p.key} ${p.sessions}`).join(" · ")}>
                            +{u.providers.length - 1}
                          </span>
                        {/if}
                      </div>
                      <div class="truncate text-[11px] text-black-600 dark:text-black-700" title={(u.models ?? []).map((m) => `${m.key} ${m.sessions}`).join(" · ")}>
                        {(u.models ?? []).map((m) => `${m.key} ${m.sessions}`).join(" · ") || "—"}
                      </div>
                    {:else}
                      <span class="text-black-600 dark:text-black-700">—</span>
                    {/if}
                  </td>
                  <td class="px-5 py-2 max-w-[16rem]">
                    <div class="flex flex-wrap gap-1">
                      {#each u.projects ?? [] as p}
                        <button
                          type="button"
                          onclick={() => openProjectByID(p.id)}
                          title={p.id}
                          class="max-w-[10rem] truncate rounded bg-white-300 px-1.5 py-0.5 text-[10px] text-black-800 hover:text-green-700 dark:bg-navy-600 dark:text-black-600 dark:hover:text-green-300"
                        >
                          {p.name}
                        </button>
                      {:else}
                        <span class="text-black-600 dark:text-black-700">—</span>
                      {/each}
                    </div>
                  </td>
                  <td class="px-5 py-2 max-w-[14rem]">
                    <div class="flex flex-wrap gap-1">
                      {#each u.tokens ?? [] as t}
                        <span
                          class="rounded px-1.5 py-0.5 text-[10px] {t.revoked
                            ? 'bg-white-300 text-black-600 line-through dark:bg-navy-600 dark:text-black-700'
                            : 'bg-amber-100 text-amber-700 dark:bg-amber-900/40 dark:text-amber-300'}"
                          title={`${t.masked} · ${t.sessions} conversations · last used ${ago(t.last_used_at)}${t.revoked ? " · revoked" : ""}`}
                        >
                          {t.name}{#if t.sessions}<span class="ml-1 font-mono">{t.sessions}</span>{/if}
                        </span>
                      {:else}
                        <span class="text-black-600 dark:text-black-700">—</span>
                      {/each}
                    </div>
                  </td>
                </tr>
              {:else}
                <tr><td colspan="8" class="px-5 py-8 text-center text-black-700 dark:text-black-600">Nobody matches that.</td></tr>
              {/each}
            </tbody>
          </table>
        </div>
      </div>
    {:else if tab === "projects"}
      <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-sm overflow-hidden">
        <div class="flex flex-wrap items-center gap-2 border-b border-white-300 dark:border-navy-600 px-5 py-3">
          <h2 class="text-sm font-semibold text-black-900 dark:text-white-100">Projects</h2>
          <span class="text-xs text-black-600 dark:text-black-700">click one for its conversations</span>
          <input
            type="text"
            bind:value={query}
            placeholder="Search project"
            class="ml-auto min-w-[12rem] rounded-lg border border-white-400 bg-white-100 px-3 py-1.5 text-xs text-black-900 dark:border-navy-600 dark:bg-navy-800 dark:text-white-100"
          />
        </div>
        <div class="overflow-x-auto">
          <table class="w-full text-xs">
            <thead>
              <tr class="border-b border-white-300 text-left text-black-700 dark:border-navy-600 dark:text-black-600">
                <th class="px-5 py-2.5">Project</th>
                <th class="px-5 py-2.5">Conversations</th>
                <th class="px-5 py-2.5">People</th>
                <th class="px-5 py-2.5">Channels</th>
                <th class="px-5 py-2.5">Last activity</th>
              </tr>
            </thead>
            <tbody>
              {#each projects as p (p.id)}
                <tr class="border-b border-white-300 last:border-0 dark:border-navy-600">
                  <td class="px-5 py-2">
                    <button type="button" onclick={() => (openProject = p)} title={p.id} class="font-medium text-black-900 hover:underline dark:text-white-100">
                      {p.name}
                    </button>
                  </td>
                  <td class="px-5 py-2 font-mono text-black-800 dark:text-black-600">
                    {p.sessions}{#if p.unattributed}<span class="text-black-600 dark:text-black-700" title="created before the caller was recorded"> · {p.unattributed} unattributed</span>{/if}
                  </td>
                  <td class="px-5 py-2 font-mono text-black-800 dark:text-black-600">{p.users}</td>
                  <td class="px-5 py-2">
                    <div class="flex flex-wrap gap-1">
                      {#each p.channels ?? [] as c}
                        <span class="rounded px-1.5 py-0.5 text-[10px] font-medium {channelClass(c.key)}">{c.key} {c.sessions}</span>
                      {/each}
                    </div>
                  </td>
                  <td class="px-5 py-2 whitespace-nowrap text-black-800 dark:text-black-600">{ago(p.last_active_at)}</td>
                </tr>
              {:else}
                <tr><td colspan="5" class="px-5 py-8 text-center text-black-700 dark:text-black-600">No project matches that.</td></tr>
              {/each}
            </tbody>
          </table>
        </div>
      </div>
    {:else if tab === "providers"}
      <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-sm overflow-hidden">
        <div class="flex items-center gap-2 border-b border-white-300 dark:border-navy-600 px-5 py-3">
          <h2 class="text-sm font-semibold text-black-900 dark:text-white-100">Providers</h2>
          <span class="text-xs text-black-600 dark:text-black-700">
            which account ran the work, and with which model · in this range
          </span>
        </div>
        <div class="overflow-x-auto">
          <table class="w-full text-xs">
            <thead>
              <tr class="border-b border-white-300 text-left text-black-700 dark:border-navy-600 dark:text-black-600">
                <th class="px-5 py-2.5">Provider</th>
                <th class="px-5 py-2.5">Account</th>
                <th class="px-5 py-2.5">Conversations</th>
                <th class="px-5 py-2.5">Share</th>
                <th class="px-5 py-2.5">People</th>
                <th class="px-5 py-2.5">Models</th>
                <th class="px-5 py-2.5">Last activity</th>
              </tr>
            </thead>
            <tbody>
              {#each providers as pv (pv.key)}
                {@const share = windowStats.conversations > 0 ? Math.round((pv.sessions / windowStats.conversations) * 100) : 0}
                <tr class="border-b border-white-300 last:border-0 dark:border-navy-600 {overlaidProviders.includes(pv.key) ? 'bg-green-500/5' : ''}">
                  <td class="px-5 py-2">
                    <button type="button" onclick={() => toggleProviderOverlay(pv.key)} class="flex items-center gap-1.5 font-medium text-black-900 hover:underline dark:text-white-100">
                      <span class="h-2 w-2 rounded-full" style={`background:${channelColor(pv.key)}`}></span>
                      {pv.type}
                    </button>
                  </td>
                  <td class="px-5 py-2 text-black-800 dark:text-black-600">{pv.instance || "—"}</td>
                  <td class="px-5 py-2 font-mono text-black-800 dark:text-black-600">{pv.sessions}</td>
                  <td class="px-5 py-2 font-mono text-black-800 dark:text-black-600">{share}%</td>
                  <td class="px-5 py-2 font-mono text-black-800 dark:text-black-600">{pv.users}</td>
                  <td class="px-5 py-2">
                    <div class="flex flex-wrap gap-1">
                      {#each pv.models ?? [] as m}
                        <span
                          class="rounded bg-white-300 px-1.5 py-0.5 text-[10px] text-black-800 dark:bg-navy-600 dark:text-black-600"
                          title={m.key === "(default)" ? "no model pinned — the provider picked its own" : m.key}
                        >
                          {m.key} <span class="font-mono">{m.sessions}</span>
                        </span>
                      {:else}
                        <span class="text-black-600 dark:text-black-700">—</span>
                      {/each}
                    </div>
                  </td>
                  <td class="px-5 py-2 whitespace-nowrap text-black-800 dark:text-black-600">{ago(pv.last_active_at)}</td>
                </tr>
              {:else}
                <tr><td colspan="7" class="px-5 py-8 text-center text-black-700 dark:text-black-600">
                  No conversation in this range recorded a provider.
                </td></tr>
              {/each}
            </tbody>
          </table>
        </div>
      </div>
    {:else if tab === "channels"}
      <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-sm overflow-hidden">
        <div class="flex items-center gap-2 border-b border-white-300 dark:border-navy-600 px-5 py-3">
          <h2 class="text-sm font-semibold text-black-900 dark:text-white-100">Channels</h2>
          <span class="text-xs text-black-600 dark:text-black-700">
            in this range · a row per configured bot · click any of them to filter the whole page
          </span>
        </div>
        <div class="overflow-x-auto">
          <table class="w-full text-xs">
            <thead>
              <tr class="border-b border-white-300 text-left text-black-700 dark:border-navy-600 dark:text-black-600">
                <th class="px-5 py-2.5">Channel · bot</th>
                <th class="px-5 py-2.5">Conversations</th>
                <th class="px-5 py-2.5">People</th>
                <th class="px-5 py-2.5">Unattributed</th>
                <th class="px-5 py-2.5">Last activity</th>
              </tr>
            </thead>
            <tbody>
              {#each data.channels as c (c.channel)}
                <tr class="border-b border-white-300 dark:border-navy-600 {picked.includes(c.channel) ? 'bg-green-500/5' : ''}">
                  <td class="px-5 py-2">
                    <button type="button" onclick={() => toggleChannel(c.channel)}>
                      <span class="rounded px-1.5 py-0.5 text-[11px] font-medium {channelClass(c.channel)}">{c.channel}</span>
                    </button>
                  </td>
                  <td class="px-5 py-2 font-mono text-black-800 dark:text-black-600">{c.sessions}</td>
                  <td class="px-5 py-2 font-mono text-black-800 dark:text-black-600">{c.users}</td>
                  <td class="px-5 py-2 font-mono text-black-800 dark:text-black-600" title="created before the caller was recorded — there is nothing to attribute them to">
                    {c.unattributed ?? 0}
                  </td>
                  <td class="px-5 py-2 whitespace-nowrap text-black-800 dark:text-black-600">{ago(c.last_active_at)}</td>
                </tr>
                <!-- One row per configured bot. "slack: 372" does not say
                     which app is busy, or whose connection it is. -->
                {#each c.instances ?? [] as inst (inst.key)}
                  <tr class="border-b border-white-300 last:border-0 dark:border-navy-600 {pickedInstances.includes(inst.key) ? 'bg-green-500/5' : ''}">
                    <td class="py-1.5 pl-10 pr-5">
                      <button type="button" onclick={() => toggleInstance(inst.key)} class="text-left hover:underline">
                        <span class="text-black-900 dark:text-white-100">
                          {inst.owner_name || (inst.owner_id ? inst.owner_id.slice(0, 8) : "default")}
                        </span>
                        {#if inst.owner_email}
                          <span class="text-[11px] text-black-600 dark:text-black-700"> · {inst.owner_email}</span>
                        {:else if !inst.owner_id}
                          <span class="text-[11px] text-black-600 dark:text-black-700" title="no bot behind it — typed in the dashboard, or called directly"> · no bot</span>
                        {:else}
                          <span class="text-[11px] text-black-600 dark:text-black-700" title={inst.owner_id}> · owner not in the accounts table</span>
                        {/if}
                      </button>
                    </td>
                    <td class="py-1.5 px-5 font-mono text-black-800 dark:text-black-600">{inst.sessions}</td>
                    <td class="py-1.5 px-5 font-mono text-black-800 dark:text-black-600">{inst.users}</td>
                    <td class="py-1.5 px-5 font-mono text-black-800 dark:text-black-600">{inst.unattributed ?? 0}</td>
                    <td class="py-1.5 px-5 whitespace-nowrap text-black-800 dark:text-black-600">{ago(inst.last_active_at)}</td>
                  </tr>
                {/each}
              {:else}
                <tr><td colspan="5" class="px-5 py-8 text-center text-black-700 dark:text-black-600">No sessions recorded yet.</td></tr>
              {/each}
            </tbody>
          </table>
        </div>
      </div>
    {/if}
  {/if}

  <!-- One person, in full. A modal rather than rearranging the page under
       the cursor: the list stays where it was when you close it. -->
  {#if openUser}
    {@const u = openUser}
    <div
      class="fixed inset-0 z-50 flex items-start justify-center overflow-y-auto bg-black/40 p-4 sm:p-8"
      role="presentation"
      onclick={(e) => { if (e.target === e.currentTarget) openUser = null; }}
    >
      <div class="w-full max-w-3xl rounded-xl border border-white-300 bg-white-100 shadow-xl dark:border-navy-600 dark:bg-navy-700">
        <div class="flex items-start gap-3 border-b border-white-300 px-5 py-3 dark:border-navy-600">
          <div class="min-w-0">
            <h2 class="truncate text-sm font-semibold text-black-900 dark:text-white-100">{u.name || u.email}</h2>
            <p class="truncate text-[11px] text-black-600 dark:text-black-700">{u.email} · {u.role}</p>
          </div>
          <button type="button" onclick={() => (openUser = null)} class="ml-auto rounded px-2 py-1 text-xs text-black-700 hover:bg-white-300 dark:text-black-600 dark:hover:bg-navy-600">
            close
          </button>
        </div>

        <div class="grid gap-4 px-5 py-4 sm:grid-cols-3 lg:grid-cols-6">
          {#each [
            { label: "Conversations", value: String(u.sessions) },
            { label: "Joined", value: String(u.joined) },
            { label: "Last activity", value: ago(u.last_active_at) },
            { label: "Last login", value: u.last_login_at ? ago(u.last_login_at) : loginsRecorded ? "never" : "no record" },
            // What their work cost, over the same range as everything else
            // on this page. "—" is a real answer here: a person can be
            // active and spend nothing if their sessions ran on a provider
            // that reports no cost.
            { label: "Tokens", value: userSpend ? compactTokens(userSpend.totals.total) : "—" },
            { label: "Spent", value: userSpend ? formatCost(userSpend.totals.cost_usd) : "—" },
          ] as card}
            <div>
              <p class="text-[11px] uppercase tracking-wide text-black-700 dark:text-black-600">{card.label}</p>
              <p class="text-lg font-semibold text-black-900 dark:text-white-100">{card.value}</p>
            </div>
          {/each}
        </div>

        {#if u.daily?.length}
          <div class="border-t border-white-300 px-5 py-4 dark:border-navy-600">
            <p class="mb-1 text-[11px] uppercase tracking-wide text-black-700 dark:text-black-600">
              Their curve · same window as the page ({windowStats.days} days)
            </p>
            <Chart points={u.daily} {metric} height={120} />
          </div>
        {/if}

        <div class="grid gap-4 border-t border-white-300 px-5 py-4 dark:border-navy-600 sm:grid-cols-3">
          <div>
            <p class="mb-1.5 text-[11px] uppercase tracking-wide text-black-700 dark:text-black-600">Channels</p>
            <div class="flex flex-wrap gap-1">
              {#each u.channels ?? [] as ch}
                <span class="rounded px-1.5 py-0.5 text-[10px] font-medium {channelClass(ch)}">{ch}</span>
              {:else}
                <span class="text-xs text-black-600 dark:text-black-700">—</span>
              {/each}
            </div>
          </div>
          <div>
            <p class="mb-1.5 text-[11px] uppercase tracking-wide text-black-700 dark:text-black-600">Projects</p>
            <div class="flex flex-wrap gap-1">
              {#each u.projects ?? [] as p}
                <button
                  type="button"
                  onclick={() => openProjectByID(p.id)}
                  title={p.id}
                  class="max-w-[12rem] truncate rounded bg-white-300 px-1.5 py-0.5 text-[10px] text-black-800 hover:text-green-700 dark:bg-navy-600 dark:text-black-600 dark:hover:text-green-300"
                >
                  {p.name}
                </button>
              {:else}
                <span class="text-xs text-black-600 dark:text-black-700">—</span>
              {/each}
            </div>
          </div>
          <div>
            <p class="mb-1.5 text-[11px] uppercase tracking-wide text-black-700 dark:text-black-600">Tokens</p>
            <div class="space-y-1">
              {#each u.tokens ?? [] as t}
                <div class="flex items-baseline gap-2 text-[11px]">
                  <span class="{t.revoked ? 'text-black-600 line-through dark:text-black-700' : 'text-black-900 dark:text-white-100'}">{t.name}</span>
                  <span class="font-mono text-black-600 dark:text-black-700">{t.masked}</span>
                  <span class="ml-auto font-mono text-black-800 dark:text-black-600">{t.sessions}</span>
                </div>
              {:else}
                <span class="text-xs text-black-600 dark:text-black-700">—</span>
              {/each}
            </div>
          </div>
        </div>

        {#if u.providers?.length}
          <div class="border-t border-white-300 px-5 py-4 dark:border-navy-600">
            <p class="mb-2 text-[11px] uppercase tracking-wide text-black-700 dark:text-black-600">
              Providers &amp; models · in this range
            </p>
            <div class="grid gap-3 sm:grid-cols-2">
              <div class="space-y-1">
                {#each u.providers as pv}
                  {@const share = u.sessions > 0 ? Math.round((pv.sessions / u.sessions) * 100) : 0}
                  <div class="flex items-baseline gap-2 text-xs">
                    <span class="h-2 w-2 shrink-0 rounded-full" style={`background:${channelColor(pv.key)}`}></span>
                    <span class="truncate text-black-900 dark:text-white-100">{pv.key}</span>
                    <span class="ml-auto font-mono text-black-800 dark:text-black-600">{pv.sessions}</span>
                    <span class="w-10 text-right text-black-600 dark:text-black-700">{share}%</span>
                  </div>
                {/each}
              </div>
              <div class="space-y-1">
                {#each u.models ?? [] as m}
                  <div class="flex items-baseline gap-2 text-xs">
                    <span
                      class="truncate text-black-900 dark:text-white-100"
                      title={m.key === "(default)" ? "no model pinned — the provider picked its own" : m.key}
                    >
                      {m.key}
                    </span>
                    <span class="ml-auto font-mono text-black-800 dark:text-black-600">{m.sessions}</span>
                  </div>
                {:else}
                  <span class="text-xs text-black-600 dark:text-black-700">No model recorded.</span>
                {/each}
              </div>
            </div>
          </div>
        {/if}

        {#if u.recent?.length}
          <RecentSessions
            items={u.recent}
            total={u.sessions + u.joined}
            showUser={false}
            showProject
            onProject={openProjectByID}
          />
        {/if}

        {#if u.agents?.length}
          <div class="border-t border-white-300 px-5 py-3 text-xs text-black-800 dark:border-navy-600 dark:text-black-600">
            Agents used: {u.agents.join(", ")}
          </div>
        {/if}
      </div>
    </div>
  {/if}

  <!-- Project drill-down. A list of 800 conversations does not belong in a
       table cell, so the cell holds the name and this holds the detail. -->
  {#if openProject}
    {@const p = openProject}
    <div
      class="fixed inset-0 z-50 flex items-start justify-center overflow-y-auto bg-black/40 p-4 sm:p-8"
      role="presentation"
      onclick={(e) => { if (e.target === e.currentTarget) openProject = null; }}
    >
      <div class="w-full max-w-3xl rounded-xl border border-white-300 bg-white-100 shadow-xl dark:border-navy-600 dark:bg-navy-700">
        <div class="flex items-start gap-3 border-b border-white-300 px-5 py-3 dark:border-navy-600">
          <div class="min-w-0">
            <h2 class="truncate text-sm font-semibold text-black-900 dark:text-white-100">{p.name}</h2>
            <p class="truncate font-mono text-[11px] text-black-600 dark:text-black-700">{p.id}</p>
          </div>
          <button type="button" onclick={() => (openProject = null)} class="ml-auto rounded px-2 py-1 text-xs text-black-700 hover:bg-white-300 dark:text-black-600 dark:hover:bg-navy-600">
            close
          </button>
        </div>

        <div class="grid gap-4 px-5 py-4 sm:grid-cols-3 lg:grid-cols-5">
          {#each [
            { label: "Conversations", value: String(p.sessions) },
            { label: "People", value: String(p.users) },
            { label: "Last activity", value: ago(p.last_active_at) },
            // Same range as the page, same ledger as the card above.
            { label: "Tokens", value: projectSpend ? compactTokens(projectSpend.totals.total) : "—" },
            { label: "Spent", value: projectSpend ? formatCost(projectSpend.totals.cost_usd) : "—" },
          ] as card}
            <div>
              <p class="text-[11px] uppercase tracking-wide text-black-700 dark:text-black-600">{card.label}</p>
              <p class="text-lg font-semibold text-black-900 dark:text-white-100">{card.value}</p>
            </div>
          {/each}
        </div>

        {#if p.channels?.length}
          <div class="flex flex-wrap gap-1.5 px-5 pb-4">
            {#each p.channels as c}
              <span class="rounded px-1.5 py-0.5 text-[10px] font-medium {channelClass(c.key)}">{c.key} · {c.sessions}</span>
            {/each}
            {#if p.unattributed}
              <span class="rounded bg-white-300 px-1.5 py-0.5 text-[10px] text-black-700 dark:bg-navy-600 dark:text-black-600">
                {p.unattributed} unattributed
              </span>
            {/if}
          </div>
        {/if}

        {#if p.members?.length}
          <div class="border-t border-white-300 px-5 py-3 dark:border-navy-600">
            <h3 class="mb-2 text-xs font-semibold text-black-900 dark:text-white-100">Who works here</h3>
            <div class="space-y-1">
              {#each p.members as m}
                <div class="flex items-baseline gap-2 text-xs">
                  <span class="text-black-900 dark:text-white-100">{m.name}</span>
                  <span class="font-mono text-black-700 dark:text-black-600">{m.sessions}</span>
                  <span class="ml-auto text-black-600 dark:text-black-700">{ago(m.last_active_at)}</span>
                </div>
              {/each}
            </div>
          </div>
        {/if}

        {#if p.recent?.length}
          <RecentSessions items={p.recent} total={p.sessions} />
        {/if}
      </div>
    </div>
  {/if}
</div>
