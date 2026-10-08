<script lang="ts">
  import { scheduleCadence as cadence } from "@wick-fe/common-ui";
  import type { Schedule } from "./api.js";
  import { isProjectScoped, scheduleType, ago } from "./api.js";

  type Props = {
    s: Schedule;
    base: string;
    onCancel: (id: string) => void;
    onPause: (id: string) => void;
    onResume: (id: string) => void;
    onRunNow?: (id: string) => void;
    /* Remove the row and its history (watch rows; the page confirms first). */
    onDelete?: (s: Schedule) => void;
    /* Clicking the row opens the detail/edit modal. The page owns the modal so
       one instance serves every row, and editing state survives the 15s
       background refresh that replaces these row objects. */
    onOpen?: (s: Schedule) => void;
  };
  let { s, base, onCancel, onPause, onResume, onRunNow, onOpen, onDelete }: Props = $props();

  /* Where this schedule delivers, in one line. A project-scoped row has no
     fixed session, so the target is described by its mode instead. */
  const targetLabel = $derived.by(() => {
    if (s.session_mode === "new") return "new session each run";
    if (s.session_mode === "template") return s.session_template || "named session";
    return "";
  });

  const lastRunHref = $derived(
    s.last_session_id ? `${base}/sessions/${encodeURIComponent(s.last_session_id)}` : "",
  );

  function statusBadgeCls(status: string): string {
    switch (status) {
      case "pending":
      case "active":
        return "bg-green-100 text-green-700 dark:bg-green-900 dark:text-green-300";
      case "done":
        return "bg-white-300 text-black-700 dark:bg-navy-700 dark:text-white-200";
      case "failed":
        return "bg-neg-100 text-neg-400 dark:bg-neg-400/20 dark:text-neg-300";
      default:
        return "bg-white-300 text-black-600 dark:bg-navy-700 dark:text-black-600";
    }
  }

  function fmtWhen(iso: string): string {
    const d = new Date(iso);
    if (isNaN(d.getTime())) return iso;
    return d.toLocaleString(undefined, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" });
  }

  const isWatch = $derived(scheduleType(s) === "watch");

  /* Latest watch result: matched green, pending grey, error red. */
  function resultCls(r: string): string {
    switch (r) {
      case "matched":
        return "bg-green-100 text-green-700 dark:bg-green-900 dark:text-green-300";
      case "error":
        return "bg-neg-100 text-neg-400 dark:bg-neg-400/20 dark:text-neg-300";
      default:
        return "bg-white-300 text-black-600 dark:bg-navy-700 dark:text-black-600";
    }
  }

  const isLive = $derived(
    s.status === "pending" || s.status === "active" || s.status === "paused",
  );

  /* run_at is absent on a terminal row (there is no next fire — the backend
     stops publishing the claim sentinel), so fall back to when it last ran. */
  const whenLabel = $derived(
    s.run_at ? fmtWhen(s.run_at) : s.last_run_at ? fmtWhen(s.last_run_at) : "—",
  );
</script>

<div class="space-y-1.5" data-sid={s.id}>
  <!-- The summary opens the detail/edit modal. A button (not a click handler
       on the div) so keyboard and screen readers get it for free; the action
       row below sits OUTSIDE it, so Pause/Cancel aren't nested buttons. -->
  <svelte:element
    this={onOpen ? "button" : "div"}
    type={onOpen ? "button" : undefined}
    class={"block w-full space-y-1.5 text-left " + (onOpen ? "cursor-pointer" : "")}
    onclick={onOpen ? () => onOpen(s) : undefined}
    role={onOpen ? "button" : undefined}
    data-testid={onOpen ? "row-open" : undefined}
  >
  <div class="flex items-center gap-2 flex-wrap">
    <!-- Type: a Message wakes the agent on every fire; a Watch polls with no
         LLM and wakes it once. Icon + label so it never rests on colour. -->
    {#if isWatch}
      <span
        class="shrink-0 inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-[10px] font-medium bg-navy-700 text-white-100 dark:bg-white-300 dark:text-navy-800"
        data-testid="type-badge"
      >
        <svg viewBox="0 0 16 16" class="h-3 w-3" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
          <path d="M1.5 8S4 3.5 8 3.5 14.5 8 14.5 8 12 12.5 8 12.5 1.5 8 1.5 8z" stroke-linejoin="round"></path>
          <circle cx="8" cy="8" r="2"></circle>
        </svg>
        Watch
      </span>
    {:else}
      <span
        class="shrink-0 inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-[10px] font-medium bg-white-300 text-black-700 dark:bg-navy-700 dark:text-white-200"
        data-testid="type-badge"
      >
        <svg viewBox="0 0 16 16" class="h-3 w-3" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
          <path d="M2.5 3.5h11v7h-6l-3 2.5v-2.5h-2z" stroke-linejoin="round"></path>
        </svg>
        Message
      </span>
    {/if}
    {#if s.kind === "recurring"}
      <span class="text-xs font-medium text-black-900 dark:text-white-100">{cadence(s)}</span>
    {:else}
      <span class="text-xs font-medium text-black-900 dark:text-white-100">{whenLabel}</span>
    {/if}
    <span class={"shrink-0 rounded-full px-2 py-0.5 text-[10px] font-medium " + statusBadgeCls(s.status)}>
      {s.paused ? "paused" : s.status}
    </span>
    <!-- Whose access this runs with, not how the row was made. "by ai" said
         only that an agent typed it; what decides whether the job can reach
         anything is the identity its fires use. A schedule attached to nobody
         falls back to the internal principal, which carries no access tags —
         so that case is called out rather than left blank. -->
    {#if s.effective_run_as}
      <span
        class="shrink-0 rounded-full px-2 py-0.5 text-[10px] font-medium bg-white-300 text-black-600 dark:bg-navy-700 dark:text-black-600"
        title={"Runs with the access of " + (s.effective_run_as_name || s.effective_run_as) + (s.run_as_user_id ? " (admin override)" : "")}
        data-testid="runas-badge"
      >
        runs as {s.effective_run_as_name || s.effective_run_as}{s.run_as_user_id ? " ⚑" : ""}
      </span>
    {:else}
      <span
        class="shrink-0 rounded-full px-2 py-0.5 text-[10px] font-medium bg-amber-100 text-amber-700 dark:bg-amber-900 dark:text-amber-300"
        title="No identity attached — the fire falls back to wick's internal principal, which carries no access tags."
        data-testid="runas-badge"
      >
        no identity
      </span>
    {/if}
    {#if isProjectScoped(s)}
      <span
        class="shrink-0 rounded-full px-2 py-0.5 text-[10px] font-medium bg-green-100 text-green-700 dark:bg-green-900 dark:text-green-300"
        data-testid="scope-badge"
      >{targetLabel}</span>
    {/if}
  </div>

  <p class="text-xs text-black-800 dark:text-white-200 whitespace-pre-wrap break-words">{s.message}</p>

  {#if isWatch}
    <p class="flex flex-wrap items-center gap-1.5 text-[11px] text-black-700 dark:text-black-600" data-testid="watch-line">
      {#if s.last_result}
        <span class={"rounded-full px-1.5 py-px text-[10px] font-medium " + resultCls(s.last_result)} data-testid="watch-result">
          {s.running ? "running" : s.last_result}
        </span>
      {/if}
      <span>{s.step_count ?? 0} step{(s.step_count ?? 0) === 1 ? "" : "s"}</span>
      {#if s.on_match === "continue"}
        <span class="rounded-full bg-green-100 px-1.5 py-px text-[10px] font-medium text-green-700 dark:bg-green-900 dark:text-green-300" data-testid="watch-on-match">
          terus jalan · {s.notified ?? 0} notif
        </span>
      {:else}
        <span data-testid="watch-on-match">· berhenti setelah match</span>
      {/if}
      {#if s.no_timeout}
        <span class="rounded-full bg-white-300 px-1.5 py-px text-[10px] font-medium text-black-600 dark:bg-navy-700 dark:text-black-600" data-testid="watch-no-timeout">tanpa batas</span>
      {/if}
      {#if s.last_run_at}<span>· last run {ago(s.last_run_at)}</span>{/if}
    </p>
  {/if}

  {#if s.kind === "recurring"}
    <p class="text-[11px] text-black-700 dark:text-black-600">
      {#if !s.paused && s.run_at}next {fmtWhen(s.run_at)} · {/if}
      {#if s.last_run_at}last {fmtWhen(s.last_run_at)} · {/if}
      ran {s.run_count}{#if s.max_runs}/{s.max_runs}{/if}×
    </p>
  {:else if s.last_run_at}
    <p class="text-[11px] text-black-700 dark:text-black-600">fired {fmtWhen(s.last_run_at)}</p>
  {/if}

  {#if s.last_error}
    <p class="text-[11px] text-neg-400">{s.last_error}</p>
  {/if}
  </svelte:element>

  <!-- Outside the clickable summary: an anchor may not nest inside a button. -->
  {#if isProjectScoped(s) && lastRunHref}
    <p class="text-[11px] text-black-700 dark:text-black-600">
      last run in
      <a
        href={lastRunHref}
        class="font-medium text-black-800 dark:text-white-200 hover:text-green-700 dark:hover:text-green-400 transition-colors"
        data-testid="last-run-link"
      >{s.last_session_label || s.last_session_id}</a>
    </p>
  {/if}

  {#if isLive}
    <div class="flex items-center gap-3 pt-0.5">
      {#if s.kind === "recurring"}
        {#if s.paused}
          <button type="button" class="text-[11px] font-medium text-green-600 dark:text-green-400 hover:underline" onclick={() => onResume(s.id)}>Resume</button>
        {:else}
          <button type="button" class="text-[11px] font-medium text-black-700 dark:text-black-600 hover:underline" onclick={() => onPause(s.id)}>Pause</button>
        {/if}
      {/if}
      {#if onRunNow}
        <button
          type="button"
          class="text-[11px] font-medium text-black-700 dark:text-black-600 hover:underline"
          onclick={() => onRunNow(s.id)}
          data-testid="run-now"
        >Run now</button>
      {/if}
      <button type="button" class="ml-auto text-[11px] font-medium text-neg-400 hover:underline" onclick={() => onCancel(s.id)}>Cancel</button>
      {#if isWatch && onDelete && s.can_edit !== false}
        <button type="button" class="text-[11px] font-medium text-neg-400 hover:underline" onclick={() => onDelete(s)} data-testid="delete">Delete</button>
      {/if}
    </div>
  {:else if isWatch && onDelete && s.can_edit !== false}
    <div class="flex items-center pt-0.5">
      <button type="button" class="ml-auto text-[11px] font-medium text-neg-400 hover:underline" onclick={() => onDelete(s)} data-testid="delete">Delete</button>
    </div>
  {/if}
</div>
