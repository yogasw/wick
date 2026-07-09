<script lang="ts">
  import type { Schedule } from "./api.js";

  type Props = {
    s: Schedule;
    onCancel: (id: string) => void;
    onPause: (id: string) => void;
    onResume: (id: string) => void;
  };
  let { s, onCancel, onPause, onResume }: Props = $props();

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

  function cadence(s: Schedule): string {
    if (s.cron) return `cron ${s.cron}`;
    if (s.interval_ms) {
      const min = Math.round(s.interval_ms / 60000);
      if (min % 1440 === 0) return `every ${min / 1440}d`;
      if (min % 60 === 0) return `every ${min / 60}h`;
      if (min >= 1) return `every ${min}m`;
      return `every ${Math.round(s.interval_ms / 1000)}s`;
    }
    return "recurring";
  }

  const isLive = $derived(s.status === "pending" || s.status === "active");
</script>

<div class="space-y-1.5" data-sid={s.id}>
  <div class="flex items-center gap-2 flex-wrap">
    {#if s.kind === "recurring"}
      <span class="text-xs font-medium text-black-900 dark:text-white-100">{cadence(s)}</span>
    {:else}
      <span class="text-xs font-medium text-black-900 dark:text-white-100">{fmtWhen(s.run_at)}</span>
    {/if}
    <span class={"shrink-0 rounded-full px-2 py-0.5 text-[10px] font-medium " + statusBadgeCls(s.status)}>
      {s.paused ? "paused" : s.status}
    </span>
    <span class="shrink-0 rounded-full px-2 py-0.5 text-[10px] font-medium bg-white-300 text-black-600 dark:bg-navy-700 dark:text-black-600">
      by {s.created_by}
    </span>
  </div>

  <p class="text-xs text-black-800 dark:text-white-200 whitespace-pre-wrap break-words">{s.message}</p>

  {#if s.kind === "recurring"}
    <p class="text-[11px] text-black-700 dark:text-black-600">
      {#if s.status === "active" && !s.paused}next {fmtWhen(s.run_at)} · {/if}
      {#if s.last_run_at}last {fmtWhen(s.last_run_at)} · {/if}
      ran {s.run_count}{#if s.max_runs}/{s.max_runs}{/if}×
    </p>
  {:else if s.last_run_at}
    <p class="text-[11px] text-black-700 dark:text-black-600">fired {fmtWhen(s.last_run_at)}</p>
  {/if}

  {#if s.last_error}
    <p class="text-[11px] text-neg-400">{s.last_error}</p>
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
      <button type="button" class="ml-auto text-[11px] font-medium text-neg-400 hover:underline" onclick={() => onCancel(s.id)}>Cancel</button>
    </div>
  {/if}
</div>
