<script lang="ts">
  import { onMount, onDestroy } from "svelte";
  import { Effect } from "effect";
  import { WickClientLayer } from "@wick-fe/common-api";
  import { toastError, toastOk } from "@wick-fe/common-stores";
  import { ToastHost, ScheduleEditModal } from "@wick-fe/common-ui";
  import type { ProjectOption, ReschedulePatch, Schedule } from "./lib/api.js";
  import {
    listAll,
    cancelById,
    pauseById,
    resumeById,
    rescheduleById,
    runNowById,
    listProjects,
    isProjectScoped,
  } from "./lib/api.js";
  import ScheduleRow from "./lib/ScheduleRow.svelte";

  // `|| ` (not `??`): the dev index.html hard-codes data-base="", which is
  // non-nullish, so `??` would keep the empty string and break API routing
  // in standalone `npm run dev`. Production injects the real base via templ.
  const base = document.getElementById("app")?.dataset.base || "/tools/agents";

  const run = <A,>(eff: Effect.Effect<A, unknown, never>) => Effect.runPromise(eff);

  let schedules = $state<Schedule[]>([]);
  let loading = $state(true);
  let loadError = $state("");

  type Filter = "live" | "done" | "failed" | "cancelled" | "all";
  let filter = $state<Filter>("live");

  const FILTERS: { id: Filter; label: string }[] = [
    { id: "live", label: "Live" },
    { id: "done", label: "Done" },
    { id: "failed", label: "Failed" },
    { id: "cancelled", label: "Cancelled" },
    { id: "all", label: "All" },
  ];

  // "paused" is a live status: the schedule still holds its place in the
  // series and can be resumed, so it must keep its actions and its tile.
  const isLive = (s: Schedule) =>
    s.status === "active" || s.status === "pending" || s.status === "paused";

  const sessionHref = (sessionId: string) => `${base}/sessions/${encodeURIComponent(sessionId)}`;

  function matchesFilter(s: Schedule): boolean {
    switch (filter) {
      case "live":
        return isLive(s);
      case "done":
        return s.status === "done";
      case "failed":
        return s.status === "failed";
      case "cancelled":
        return s.status === "cancelled";
      default:
        return true;
    }
  }

  type Scope = "all" | "project" | "session";
  let scope = $state<Scope>("all");

  const SCOPES: { id: Scope; label: string }[] = [
    { id: "all", label: "All scopes" },
    { id: "project", label: "Project jobs" },
    { id: "session", label: "Session nudges" },
  ];

  function matchesScope(s: Schedule): boolean {
    if (scope === "project") return isProjectScoped(s);
    if (scope === "session") return !isProjectScoped(s);
    return true;
  }

  const filtered = $derived(schedules.filter((s) => matchesFilter(s) && matchesScope(s)));

  /* Grouping is scope-aware: a session-scoped row groups under its target
     session, a project-scoped row under its project — it has no fixed
     session to group by, and keying on the empty session_id would collapse
     every project job in the system into one nameless group. */
  type Group = { key: string; label: string; href: string; scoped: boolean; items: Schedule[] };

  const groups = $derived.by(() => {
    const byKey = new Map<string, Group>();
    for (const s of filtered) {
      const scoped = isProjectScoped(s);
      const key = scoped ? "p:" + (s.project_id ?? "") : "s:" + s.session_id;
      const g = byKey.get(key);
      if (g) {
        g.items.push(s);
        continue;
      }
      byKey.set(key, {
        key,
        label: scoped
          ? s.project_name || s.project_id || "Unknown project"
          : s.session_label || s.session_id,
        href: scoped ? "" : sessionHref(s.session_id),
        scoped,
        items: [s],
      });
    }
    // Project jobs first — they are standalone work, session nudges belong to
    // a conversation the user is already reading.
    return [...byKey.values()].sort((a, b) => Number(b.scoped) - Number(a.scoped));
  });

  /* Stat tiles — computed over the full set, not the filtered view. */
  const liveCount = $derived(schedules.filter(isLive).length);
  const recurringCount = $derived(schedules.filter((s) => s.kind === "recurring" && isLive(s)).length);
  const failedCount = $derived(schedules.filter((s) => s.status === "failed").length);

  function load() {
    run(listAll(base).pipe(Effect.provide(WickClientLayer)))
      .then((rows) => {
        schedules = rows;
        loadError = "";
      })
      .catch((e: unknown) => {
        loadError = e instanceof Error ? e.message : String(e);
      })
      .finally(() => {
        loading = false;
      });
  }

  function act(
    fn: (b: string, id: string) => Effect.Effect<Schedule, unknown, never>,
    id: string,
    label: string,
  ) {
    run(fn(base, id).pipe(Effect.provide(WickClientLayer)))
      .then(load)
      .catch((e: unknown) => toastError(`${label}: ${e instanceof Error ? e.message : String(e)}`));
  }

  /* Editing a schedule's scope needs the projects the caller may target.
     Fetched once — a failure is not fatal: the editor still shows the current
     project, so the picker degrades instead of blocking the whole page. */
  let projects = $state<ProjectOption[]>([]);

  function loadProjects() {
    run(listProjects(base).pipe(Effect.provide(WickClientLayer)))
      .then((rows) => {
        projects = rows;
      })
      .catch(() => {
        projects = [];
      });
  }

  /* The modal is owned here, not per-row: one instance, and the open row is
     re-resolved from `schedules` by id so the 15s refresh doesn't leave the
     modal showing a stale copy (or reset the form by remounting). */
  let openID = $state("");
  let saving = $state(false);
  const openRow = $derived(schedules.find((s) => s.id === openID));

  function reschedule(id: string, patch: ReschedulePatch) {
    saving = true;
    run(rescheduleById(base, id, patch).pipe(Effect.provide(WickClientLayer)))
      .then(() => {
        load();
        openID = "";
      })
      .catch((e: unknown) => toastError(`Edit: ${e instanceof Error ? e.message : String(e)}`))
      .finally(() => {
        saving = false;
      });
  }

  function runNow(id: string) {
    run(runNowById(base, id).pipe(Effect.provide(WickClientLayer)))
      .then(() => {
        toastOk("Firing now — it will show up as a run shortly.");
        load();
      })
      .catch((e: unknown) => toastError(`Run now: ${e instanceof Error ? e.message : String(e)}`));
  }

  let timer: ReturnType<typeof setInterval> | undefined;
  onMount(() => {
    load();
    loadProjects();
    timer = setInterval(load, 15_000);
  });
  onDestroy(() => {
    if (timer) clearInterval(timer);
  });

  const projectJobCount = $derived(schedules.filter((s) => isProjectScoped(s) && isLive(s)).length);

  const STAT_TILES = $derived([
    { label: "Live", value: liveCount },
    { label: "Recurring", value: recurringCount },
    { label: "Project jobs", value: projectJobCount },
    { label: "Failed", value: failedCount },
  ]);
</script>

<ToastHost />

{#if openRow}
  <ScheduleEditModal
    open={true}
    schedule={openRow}
    {projects}
    {sessionHref}
    {saving}
    onSave={(patch) => reschedule(openRow.id, patch)}
    onClose={() => (openID = "")}
  />
{/if}

<div class="h-full overflow-y-auto bg-white-200 dark:bg-navy-800">
  <div class="mx-auto w-full max-w-container px-4 py-6 sm:px-6 sm:py-8 space-y-6">
    <!-- Header -->
    <div class="flex items-start justify-between gap-4">
      <div class="space-y-1">
        <h1 class="text-xl font-semibold text-black-900 dark:text-white-100">Scheduled</h1>
        <p class="max-w-xl text-sm text-black-800 dark:text-black-600">
          Every schedule you can see — project jobs that open their own session each run,
          and nudges into an existing session. Create them from a session's Scheduled tab,
          or by asking the agent.
        </p>
      </div>
      <button
        type="button"
        class="shrink-0 inline-flex items-center gap-1.5 rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-3 py-2 text-sm font-medium text-black-800 dark:text-white-200 hover:bg-white-200 dark:hover:bg-navy-600 transition-colors"
        onclick={load}
      >
        <svg viewBox="0 0 16 16" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M13.5 8a5.5 5.5 0 1 1-1.6-3.9M13.5 2v3h-3" stroke-linecap="round" stroke-linejoin="round"></path>
        </svg>
        Refresh
      </button>
    </div>

    <!-- Stat tiles -->
    <div class="grid grid-cols-2 gap-3 sm:grid-cols-4 sm:gap-4">
      {#each STAT_TILES as t (t.label)}
        <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-4 py-3">
          <div class="text-2xl font-semibold text-black-900 dark:text-white-100 tabular-nums">{t.value}</div>
          <div class="text-xs font-medium text-black-800 dark:text-black-600">{t.label}</div>
        </div>
      {/each}
    </div>

    <!-- Filter tabs (underline) + scope selector -->
    <div class="flex flex-wrap items-center gap-3 border-b border-white-300 dark:border-navy-600">
      <div class="flex items-center gap-1">
        {#each FILTERS as f (f.id)}
          <button
            type="button"
            class={"relative -mb-px px-3 py-2 text-sm font-medium transition-colors " +
              (filter === f.id
                ? "text-green-700 dark:text-green-400 border-b-2 border-green-500"
                : "text-black-800 dark:text-black-600 border-b-2 border-transparent hover:text-black-900 dark:hover:text-white-200")}
            onclick={() => (filter = f.id)}
            data-testid={"filter-" + f.id}
          >{f.label}</button>
        {/each}
      </div>
      <select
        class="mb-2 ml-auto rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-3 py-1.5 text-xs font-medium text-black-800 dark:text-white-200"
        bind:value={scope}
        aria-label="Scope"
        data-testid="scope-filter"
      >
        {#each SCOPES as sc (sc.id)}
          <option value={sc.id}>{sc.label}</option>
        {/each}
      </select>
    </div>

    <!-- Body -->
    {#if loading}
      <p class="text-sm text-black-800 dark:text-black-600">Loading…</p>
    {:else if loadError}
      <div class="rounded-xl border border-neg-300 bg-neg-100 px-4 py-3">
        <p class="text-sm font-medium text-neg-400" data-testid="load-error">{loadError}</p>
      </div>
    {:else if groups.length === 0}
      <!-- Empty state -->
      <div class="flex flex-col items-center justify-center gap-3 py-16 text-center" data-testid="empty">
        <div class="flex h-12 w-12 items-center justify-center rounded-full bg-white-300 dark:bg-navy-700 text-black-700 dark:text-black-600">
          <svg viewBox="0 0 24 24" class="h-6 w-6" fill="none" stroke="currentColor" stroke-width="2">
            <circle cx="12" cy="13" r="8"></circle>
            <path d="M12 9v4l2.5 1.5M9 2h6" stroke-linecap="round" stroke-linejoin="round"></path>
          </svg>
        </div>
        <div class="space-y-1">
          <p class="text-sm font-medium text-black-900 dark:text-white-100">
            No {filter === "all" ? "" : filter + " "}scheduled messages
          </p>
          <p class="text-xs text-black-800 dark:text-black-600">
            Create one from a session's Scheduled tab, or ask the agent to check back later.
          </p>
        </div>
      </div>
    {:else}
      <div class="space-y-4">
        {#each groups as g (g.key)}
          <section class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 overflow-hidden">
            <!-- Group header: a session (linked) or a project (no single session to open) -->
            <div class="flex items-center gap-2 border-b border-white-300 dark:border-navy-600 px-4 py-2.5 bg-white-200 dark:bg-navy-800">
              {#if g.scoped}
                <svg viewBox="0 0 16 16" class="h-4 w-4 shrink-0 text-green-600 dark:text-green-400" fill="none" stroke="currentColor" stroke-width="1.5">
                  <path d="M2 4.5A1.5 1.5 0 013.5 3h2.4l1.2 1.5h5.4A1.5 1.5 0 0114 6v6a1.5 1.5 0 01-1.5 1.5h-9A1.5 1.5 0 012 12V4.5z" stroke-linejoin="round"></path>
                  <path d="M8 7.5v3M6.5 9h3" stroke-linecap="round"></path>
                </svg>
                <span class="min-w-0 flex-1 truncate text-sm font-medium text-black-900 dark:text-white-100" title={g.label}>
                  {g.label}
                </span>
                <span class="shrink-0 rounded-full bg-green-100 dark:bg-green-900 px-2 py-0.5 text-[10px] font-medium text-green-700 dark:text-green-300">
                  project
                </span>
              {:else}
                <svg viewBox="0 0 16 16" class="h-4 w-4 shrink-0 text-black-700 dark:text-black-600" fill="none" stroke="currentColor" stroke-width="1.5">
                  <path d="M2 4a1 1 0 011-1h3l2 2h5a1 1 0 011 1v6a1 1 0 01-1 1H3a1 1 0 01-1-1V4z" stroke-linejoin="round"></path>
                </svg>
                <a
                  href={g.href}
                  class="min-w-0 flex-1 truncate text-sm font-medium text-black-900 dark:text-white-100 hover:text-green-700 dark:hover:text-green-400 transition-colors"
                  title={g.label}
                >{g.label}</a>
              {/if}
              <span class="shrink-0 rounded-full bg-white-300 dark:bg-navy-600 px-2 py-0.5 text-[11px] font-medium text-black-800 dark:text-black-600 tabular-nums">
                {g.items.length}
              </span>
            </div>
            <!-- Rows -->
            <div class="divide-y divide-white-300 dark:divide-navy-600">
              {#each g.items as s (s.id)}
                <div class="px-3 py-3">
                  <ScheduleRow
                    {s}
                    {base}
                    onCancel={(id) => act(cancelById, id, "Cancel")}
                    onPause={(id) => act(pauseById, id, "Pause")}
                    onResume={(id) => act(resumeById, id, "Resume")}
                    onRunNow={runNow}
                    onOpen={(row) => (openID = row.id)}
                  />
                </div>
              {/each}
            </div>
          </section>
        {/each}
      </div>
    {/if}
  </div>
</div>
