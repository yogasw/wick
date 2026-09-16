<script lang="ts">
  import { onMount } from "svelte";
  import type { AnalyticsProject, AnalyticsResponse, AnalyticsUser } from "$lib/types";
  import { ago, isDormant, channelClass } from "$lib/format";
  import { loadAnalytics } from "$lib/stream";
  import Chart from "$lib/Chart.svelte";

  type Props = { endpoint: string };
  let { endpoint }: Props = $props();

  let data = $state<AnalyticsResponse | null>(null);
  let error = $state<string | null>(null);
  let loading = $state(true);
  let progress = $state({ done: 0, total: 0 });
  let query = $state("");
  let channelFilter = $state("");
  let metric = $state<"sessions" | "logins">("sessions");
  /** Whose curve the chart is drawing; null = everyone. */
  let focus = $state<AnalyticsUser | null>(null);
  /** The project drill-down, opened by clicking a project name. */
  let openProject = $state<AnalyticsProject | null>(null);

  onMount(async () => {
    try {
      data = await loadAnalytics(endpoint, {
        onProgress: (done, total) => (progress = { done, total }),
      });
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      loading = false;
    }
  });

  const users = $derived.by<AnalyticsUser[]>(() => {
    const all = data?.users ?? [];
    const q = query.trim().toLowerCase();
    return all.filter((u) => {
      if (channelFilter && !(u.channels ?? []).includes(channelFilter)) return false;
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

  // "Never signed in" is a different problem from "signed in once and left",
  // and an admin cleaning up accounts wants the first list, not a sort.
  const neverSignedIn = $derived((data?.users ?? []).filter((u) => !u.last_login_at).length);
  // Sign-ins were not recorded at all until recently — wick's sessions are
  // stateless, so nothing was ever written down. Until the first one lands,
  // an empty column means "no record", not "nobody signed in", and the page
  // has to say which.
  const loginsRecorded = $derived(Boolean(data?.logins_recorded_since));

  const projectsByID = $derived.by(() => {
    const m = new Map<string, AnalyticsProject>();
    for (const p of data?.projects ?? []) m.set(p.id, p);
    return m;
  });

  // The curve on screen: one person's if somebody is selected, everyone's
  // otherwise. A channel filter becomes the dashed comparison line, so
  // "it went up, and it was Slack" is one glance rather than two charts.
  const chartPoints = $derived(focus?.daily ?? data?.series.points ?? []);
  const compare = $derived(
    !focus && channelFilter && data?.series.by_channel?.[channelFilter]
      ? { label: channelFilter, points: data.series.by_channel[channelFilter] }
      : null,
  );

  function openProjectByID(id: string) {
    openProject = projectsByID.get(id) ?? null;
  }

  const pct = $derived(progress.total > 0 ? Math.round((progress.done / progress.total) * 100) : 0);
</script>

<div class="space-y-6">
  <div>
    <h1 class="text-[1.375rem] font-semibold text-black-900 dark:text-white-100">People &amp; usage</h1>
    <p class="mt-1 text-sm text-black-800 dark:text-black-600">
      Who signs in, who is still working here, through which channel, and — when the caller is a machine —
      with whose token. Read from the accounts table and the sessions on disk; nothing extra is recorded to
      build this page.
    </p>
  </div>

  {#if loading}
    <!-- Real progress, not a spinner: the server counts the session files
         first and streams its position, so a slow load is legibly slow
         rather than indistinguishable from a hang. -->
    <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-5 shadow-sm">
      <p class="text-sm text-black-800 dark:text-black-600">
        Reading conversations…
        {#if progress.total > 0}
          <span class="font-mono text-black-900 dark:text-white-100">{progress.done}</span>
          <span class="text-black-600 dark:text-black-700">/ {progress.total}</span>
        {/if}
      </p>
      <div class="mt-3 h-1.5 w-full overflow-hidden rounded-full bg-white-300 dark:bg-navy-600">
        <div
          class="h-full rounded-full bg-green-500 transition-[width] duration-200 {progress.total === 0 ? 'w-1/4 animate-pulse' : ''}"
          style={progress.total > 0 ? `width:${pct}%` : ""}
        ></div>
      </div>
    </div>
  {:else if error}
    <div class="rounded-xl border border-neg-400/40 bg-neg-400/10 px-4 py-3 text-sm text-neg-400">
      Could not load the numbers: {error}
    </div>
  {:else if data}
    <!-- Summary: four numbers that answer "how big is this, and how live". -->
    <div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
      {#each [
        { label: "Accounts", value: data.total_users, hint: loginsRecorded ? `${neverSignedIn} never signed in` : "sign-ins not recorded yet" },
        { label: "Active last 7 days", value: data.active_users_7d, hint: "worked in a session" },
        { label: "Conversations", value: data.sessions, hint: `${data.unattributed_sessions} with no account attached` },
        { label: "Channels in use", value: data.channels.length, hint: data.channels.map((c) => c.channel).join(", ") || "—" },
      ] as card}
        <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-4 shadow-sm">
          <p class="text-xs font-medium uppercase tracking-wide text-black-700 dark:text-black-600">{card.label}</p>
          <p class="mt-1 text-2xl font-semibold text-black-900 dark:text-white-100">{card.value}</p>
          <p class="mt-0.5 truncate text-xs text-black-600 dark:text-black-700" title={card.hint}>{card.hint}</p>
        </div>
      {/each}
    </div>

    <!-- The curve. Global by default; click a person to see theirs. -->
    <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-sm">
      <div class="flex flex-wrap items-center gap-2 border-b border-white-300 dark:border-navy-600 px-5 py-3">
        <h2 class="text-sm font-semibold text-black-900 dark:text-white-100">
          {focus ? `${focus.name || focus.email}` : "Everyone"}
        </h2>
        <span class="text-xs text-black-600 dark:text-black-700">last {data.series.days} days</span>
        {#if focus}
          <button type="button" onclick={() => (focus = null)} class="text-xs text-green-600 hover:underline dark:text-green-400">
            back to everyone
          </button>
        {/if}
        <div class="ml-auto flex gap-1 rounded-lg border border-white-300 p-0.5 dark:border-navy-600">
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
      <div class="px-5 py-4">
        <Chart points={chartPoints} {metric} {compare} />
      </div>
    </div>

    <!-- Channels: the doors into wick, busiest first. Click one to filter. -->
    <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-sm">
      <div class="flex items-center gap-2 border-b border-white-300 dark:border-navy-600 px-5 py-3">
        <h2 class="text-sm font-semibold text-black-900 dark:text-white-100">Channels</h2>
        {#if channelFilter}
          <button type="button" onclick={() => (channelFilter = "")} class="text-xs text-green-600 hover:underline dark:text-green-400">
            clear filter ({channelFilter})
          </button>
        {/if}
      </div>
      <div class="flex flex-wrap gap-2 px-5 py-4">
        {#each data.channels as c}
          <button
            type="button"
            onclick={() => (channelFilter = channelFilter === c.channel ? "" : c.channel)}
            title={`last activity ${ago(c.last_active_at)}`}
            class="rounded-lg border px-3 py-1.5 text-left text-xs transition-colors {channelFilter === c.channel
              ? 'border-green-400 bg-green-50 dark:bg-green-900/20'
              : 'border-white-300 hover:border-green-400 dark:border-navy-600'}"
          >
            <span class="rounded px-1.5 py-0.5 text-[11px] font-medium {channelClass(c.channel)}">{c.channel}</span>
            <span class="ml-2 font-mono text-black-900 dark:text-white-100">{c.sessions}</span>
            <span class="text-black-600 dark:text-black-700">conversations · {c.users} people</span>
            {#if c.unattributed}
              <span class="text-black-600 dark:text-black-700" title="created before the caller was recorded — nothing to attribute them to">
                · {c.unattributed} unattributed
              </span>
            {/if}
          </button>
        {:else}
          <p class="text-xs text-black-700 dark:text-black-600">No sessions recorded yet.</p>
        {/each}
      </div>
    </div>

    <!-- Projects, by name. The id is a UUID; nobody reads a UUID. -->
    <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-sm">
      <div class="flex items-center gap-2 border-b border-white-300 dark:border-navy-600 px-5 py-3">
        <h2 class="text-sm font-semibold text-black-900 dark:text-white-100">Projects</h2>
        <span class="text-xs text-black-600 dark:text-black-700">click one for its conversations</span>
      </div>
      <div class="flex flex-wrap gap-2 px-5 py-4">
        {#each data.projects as p}
          <button
            type="button"
            onclick={() => (openProject = p)}
            title={p.id}
            class="rounded-lg border border-white-300 px-3 py-1.5 text-left text-xs transition-colors hover:border-green-400 dark:border-navy-600"
          >
            <span class="font-medium text-black-900 dark:text-white-100">{p.name}</span>
            <span class="ml-2 font-mono text-black-900 dark:text-white-100">{p.sessions}</span>
            <span class="text-black-600 dark:text-black-700">· {p.users} people · {ago(p.last_active_at)}</span>
          </button>
        {:else}
          <p class="text-xs text-black-700 dark:text-black-600">No projects have sessions yet.</p>
        {/each}
      </div>
    </div>

    <!-- People. Sorted by the server, newest activity first. -->
    <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-sm overflow-hidden">
      <div class="flex flex-wrap items-center gap-2 border-b border-white-300 dark:border-navy-600 px-5 py-3">
        <h2 class="text-sm font-semibold text-black-900 dark:text-white-100">People</h2>
        <span class="rounded bg-white-300 px-2 py-0.5 text-xs font-medium text-black-700 dark:bg-navy-600 dark:text-black-600">{users.length}</span>
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
              <th class="px-5 py-2.5">Last worked</th>
              <th class="px-5 py-2.5">Conversations</th>
              <th class="px-5 py-2.5">Channels</th>
              <th class="px-5 py-2.5">Projects</th>
              <th class="px-5 py-2.5">Tokens</th>
            </tr>
          </thead>
          <tbody>
            {#each users as u (u.id)}
              {@const dormant = isDormant(u.last_active_at ?? u.last_login_at)}
              <tr class="border-b border-white-300 last:border-0 dark:border-navy-600 {dormant ? 'opacity-60' : ''} {focus?.id === u.id ? 'bg-green-500/5' : ''}">
                <td class="px-5 py-2">
                  <div class="flex items-center gap-2">
                    <button
                      type="button"
                      onclick={() => (focus = focus?.id === u.id ? null : u)}
                      title="show this person's curve"
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
                <td class="px-5 py-2 max-w-[18rem]">
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
              <tr><td colspan="7" class="px-5 py-8 text-center text-black-700 dark:text-black-600">Nobody matches that.</td></tr>
            {/each}
          </tbody>
        </table>
      </div>
    </div>

    <p class="text-xs text-black-600 dark:text-black-700">
      Generated {ago(data.generated_at)} · a “login” is a browser sign-in; “worked” is activity in a conversation.
      The curve counts conversations by the day they started — that is the only history the session files keep.
      {#if loginsRecorded}
        Sign-ins recorded since {data.logins_recorded_since?.slice(0, 10)}; anything before that was never written down.
      {:else}
        Sign-ins have only just started being recorded — wick's own sessions are stateless, so there is no history
        before now. Empty means “no record”, not “never signed in”.
      {/if}
    </p>
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

        <div class="grid gap-4 px-5 py-4 sm:grid-cols-3">
          {#each [
            { label: "Conversations", value: p.sessions },
            { label: "People", value: p.users },
            { label: "Last activity", value: ago(p.last_active_at) },
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
          <div class="border-t border-white-300 px-5 py-3 dark:border-navy-600">
            <h3 class="mb-2 text-xs font-semibold text-black-900 dark:text-white-100">
              Recent conversations
              <span class="font-normal text-black-600 dark:text-black-700">newest {p.recent.length} of {p.sessions}</span>
            </h3>
            <div class="space-y-1">
              {#each p.recent as s}
                <div class="flex items-baseline gap-2 text-xs">
                  <span class="rounded px-1.5 py-0.5 text-[10px] font-medium {channelClass(s.channel)}">{s.channel}</span>
                  <a href={`/agents/conversation?session=${encodeURIComponent(s.id)}`} class="truncate text-black-900 hover:underline dark:text-white-100">
                    {s.label || s.id}
                  </a>
                  {#if s.token}
                    <span class="rounded bg-amber-100 px-1.5 py-0.5 text-[10px] text-amber-700 dark:bg-amber-900/40 dark:text-amber-300" title="created with this token">
                      {s.token}
                    </span>
                  {/if}
                  <span class="ml-auto whitespace-nowrap text-black-600 dark:text-black-700">
                    {s.user || "unattributed"} · {ago(s.last_active_at)}
                  </span>
                </div>
              {/each}
            </div>
          </div>
        {/if}
      </div>
    </div>
  {/if}
</div>
