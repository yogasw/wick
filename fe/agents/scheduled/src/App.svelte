<script lang="ts">
  import { onMount, onDestroy } from "svelte";
  import { Effect } from "effect";
  import { WickClientLayer } from "@wick-fe/common-api";
  import { toastError } from "@wick-fe/common-stores";
  import { ToastHost } from "@wick-fe/common-ui";
  import type { Schedule } from "./lib/api.js";
  import { listAll, cancelById, pauseById, resumeById } from "./lib/api.js";
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

  const isLive = (s: Schedule) => s.status === "active" || s.status === "pending";

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

  const filtered = $derived(schedules.filter(matchesFilter));

  const groups = $derived.by(() => {
    const bySession = new Map<string, { label: string; items: Schedule[] }>();
    for (const s of filtered) {
      const g = bySession.get(s.session_id);
      if (g) g.items.push(s);
      else bySession.set(s.session_id, { label: s.session_label || s.session_id, items: [s] });
    }
    return [...bySession.entries()].map(([sessionId, g]) => ({ sessionId, ...g }));
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

  let timer: ReturnType<typeof setInterval> | undefined;
  onMount(() => {
    load();
    timer = setInterval(load, 15_000);
  });
  onDestroy(() => {
    if (timer) clearInterval(timer);
  });

  const sessionHref = (sessionId: string) => `${base}/sessions/${encodeURIComponent(sessionId)}`;

  const STAT_TILES = $derived([
    { label: "Live", value: liveCount },
    { label: "Recurring", value: recurringCount },
    { label: "Failed", value: failedCount },
  ]);
</script>

<ToastHost />

<div class="h-full overflow-y-auto bg-white-200 dark:bg-navy-800">
  <div class="mx-auto w-full max-w-container px-4 py-6 sm:px-6 sm:py-8 space-y-6">
    <!-- Header -->
    <div class="flex items-start justify-between gap-4">
      <div class="space-y-1">
        <h1 class="text-xl font-semibold text-black-900 dark:text-white-100">Scheduled</h1>
        <p class="max-w-xl text-sm text-black-800 dark:text-black-600">
          Every scheduled message across the sessions you can see. Create schedules from a
          session's Scheduled tab, or by asking the agent.
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
    <div class="grid grid-cols-3 gap-3 sm:gap-4">
      {#each STAT_TILES as t (t.label)}
        <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-4 py-3">
          <div class="text-2xl font-semibold text-black-900 dark:text-white-100 tabular-nums">{t.value}</div>
          <div class="text-xs font-medium text-black-800 dark:text-black-600">{t.label}</div>
        </div>
      {/each}
    </div>

    <!-- Filter tabs (underline) -->
    <div class="flex items-center gap-1 border-b border-white-300 dark:border-navy-600">
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
        {#each groups as g (g.sessionId)}
          <section class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 overflow-hidden">
            <!-- Session header -->
            <div class="flex items-center gap-2 border-b border-white-300 dark:border-navy-600 px-4 py-2.5 bg-white-200 dark:bg-navy-800">
              <svg viewBox="0 0 16 16" class="h-4 w-4 shrink-0 text-black-700 dark:text-black-600" fill="none" stroke="currentColor" stroke-width="1.5">
                <path d="M2 4a1 1 0 011-1h3l2 2h5a1 1 0 011 1v6a1 1 0 01-1 1H3a1 1 0 01-1-1V4z" stroke-linejoin="round"></path>
              </svg>
              <a
                href={sessionHref(g.sessionId)}
                class="min-w-0 flex-1 truncate text-sm font-medium text-black-900 dark:text-white-100 hover:text-green-700 dark:hover:text-green-400 transition-colors"
                title={g.sessionId}
              >{g.label}</a>
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
                    onCancel={(id) => act(cancelById, id, "Cancel")}
                    onPause={(id) => act(pauseById, id, "Pause")}
                    onResume={(id) => act(resumeById, id, "Resume")}
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
