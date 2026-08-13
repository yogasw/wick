<script lang="ts">
  import { scheduleCadence as cadence } from "@wick-fe/common-ui";
  import type { Schedule } from "./api.js";
  import { isProjectScoped } from "./api.js";

  type Props = {
    s: Schedule;
    base: string;
    onCancel: (id: string) => void;
    onPause: (id: string) => void;
    onResume: (id: string) => void;
    onRunNow?: (id: string) => void;
    /* Clicking the row opens the detail/edit modal. The page owns the modal so
       one instance serves every row, and editing state survives the 15s
       background refresh that replaces these row objects. */
    onOpen?: (s: Schedule) => void;
  };
  let { s, base, onCancel, onPause, onResume, onRunNow, onOpen }: Props = $props();

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
    {#if s.kind === "recurring"}
      <span class="text-xs font-medium text-black-900 dark:text-white-100">{cadence(s)}</span>
    {:else}
      <span class="text-xs font-medium text-black-900 dark:text-white-100">{whenLabel}</span>
    {/if}
    <span class={"shrink-0 rounded-full px-2 py-0.5 text-[10px] font-medium " + statusBadgeCls(s.status)}>
      {s.paused ? "paused" : s.status}
    </span>
    <span class="shrink-0 rounded-full px-2 py-0.5 text-[10px] font-medium bg-white-300 text-black-600 dark:bg-navy-700 dark:text-black-600">
      by {s.created_by}
    </span>
    {#if isProjectScoped(s)}
      <span
        class="shrink-0 rounded-full px-2 py-0.5 text-[10px] font-medium bg-green-100 text-green-700 dark:bg-green-900 dark:text-green-300"
        data-testid="scope-badge"
      >{targetLabel}</span>
    {/if}
  </div>

  <p class="text-xs text-black-800 dark:text-white-200 whitespace-pre-wrap break-words">{s.message}</p>

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
    </div>
  {/if}
</div>
