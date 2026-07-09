<script lang="ts">
  import type { Schedule } from "../types/agents.js";

  type CreateArgs = { message: string; runAt?: string; every?: string; cron?: string; maxRuns?: number };

  type Props = {
    schedules: Schedule[];
    /* onCreate returns a promise so the panel clears the form only on success. */
    onCreate: (args: CreateArgs) => Promise<boolean>;
    onCancel: (id: string) => void;
    onPause: (id: string) => void;
    onResume: (id: string) => void;
  };

  let { schedules, onCreate, onCancel, onPause, onResume }: Props = $props();

  const INPUT_CLASS =
    "w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-sm text-black-900 dark:text-white-100 placeholder-black-600 dark:placeholder-black-700 focus:border-green-500 focus:ring-2 focus:ring-green-200 dark:focus:ring-green-800 focus:outline-none";

  type Mode = "once" | "repeat";
  let mode = $state<Mode>("once");

  /* one-shot presets + custom */
  const ONCE_PRESETS = [
    { label: "in 20 min", value: "20m" },
    { label: "in 1 hour", value: "1h" },
    { label: "in 5 hours", value: "5h" },
    { label: "tomorrow", value: "1d" },
    { label: "custom…", value: "custom" },
  ];
  let onceWhen = $state("1h");
  let onceCustom = $state("");

  /* recurring: interval preset OR cron */
  const EVERY_PRESETS = [
    { label: "every 5 min", value: "5m" },
    { label: "every 30 min", value: "30m" },
    { label: "every hour", value: "1h" },
    { label: "every day", value: "1d" },
    { label: "custom interval…", value: "custom" },
    { label: "cron…", value: "cron" },
  ];
  let repeatEvery = $state("5m");
  let repeatCustom = $state("");
  let repeatCron = $state("");
  let maxRuns = $state("");

  let message = $state("");
  let submitting = $state(false);
  let formError = $state("");

  const canSubmit = $derived(!submitting && message.trim().length > 0);

  function buildArgs(): CreateArgs | null {
    const msg = message.trim();
    const max = maxRuns.trim() ? Number(maxRuns.trim()) : undefined;
    if (mode === "once") {
      const runAt = onceWhen === "custom" ? onceCustom.trim() : onceWhen;
      if (!runAt) return null;
      return { message: msg, runAt };
    }
    if (repeatEvery === "cron") {
      const cron = repeatCron.trim();
      if (!cron) return null;
      return { message: msg, cron, maxRuns: max };
    }
    const every = repeatEvery === "custom" ? repeatCustom.trim() : repeatEvery;
    if (!every) return null;
    return { message: msg, every, maxRuns: max };
  }

  async function submit() {
    if (!canSubmit) return;
    const args = buildArgs();
    if (!args) {
      formError = "Fill in the timing.";
      return;
    }
    submitting = true;
    formError = "";
    try {
      const ok = await onCreate(args);
      if (ok) {
        message = "";
        onceCustom = "";
        repeatCustom = "";
        repeatCron = "";
        maxRuns = "";
      }
    } catch (e: unknown) {
      formError = e instanceof Error ? e.message : String(e);
    } finally {
      submitting = false;
    }
  }

  function statusBadgeCls(status: string): string {
    switch (status) {
      case "pending":
      case "active":
        return "bg-green-100 text-green-700 dark:bg-green-900 dark:text-green-300";
      case "done":
        return "bg-white-300 text-black-700 dark:bg-navy-700 dark:text-white-200";
      case "failed":
        return "bg-neg-100 text-neg-400 dark:bg-neg-400/20 dark:text-neg-300";
      default: /* cancelled */
        return "bg-white-300 text-black-600 dark:bg-navy-700 dark:text-black-600";
    }
  }

  function fmtWhen(iso: string): string {
    const d = new Date(iso);
    if (isNaN(d.getTime())) return iso;
    return d.toLocaleString(undefined, {
      month: "short",
      day: "numeric",
      hour: "2-digit",
      minute: "2-digit",
    });
  }

  /* Human-readable cadence for a recurring row. */
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
</script>

<div class="flex-1 overflow-y-auto p-3 space-y-3">
  <!-- Create form -->
  <div class="rounded-xl border border-white-300 dark:border-navy-600 p-3 space-y-2">
    <p class="text-xs font-medium text-black-800 dark:text-white-200">Schedule a message</p>

    <!-- Mode toggle -->
    <div class="inline-flex rounded-lg border border-white-400 dark:border-navy-600 overflow-hidden text-xs font-medium">
      <button
        type="button"
        class={"px-3 py-1.5 transition-colors " + (mode === "once" ? "bg-green-500 text-white-100" : "bg-white-100 dark:bg-navy-800 text-black-700 dark:text-white-200")}
        onclick={() => (mode = "once")}
        data-testid="mode-once"
      >Once</button>
      <button
        type="button"
        class={"px-3 py-1.5 transition-colors " + (mode === "repeat" ? "bg-green-500 text-white-100" : "bg-white-100 dark:bg-navy-800 text-black-700 dark:text-white-200")}
        onclick={() => (mode = "repeat")}
        data-testid="mode-repeat"
      >Repeat</button>
    </div>

    {#if mode === "once"}
      <select class={INPUT_CLASS} bind:value={onceWhen} data-testid="once-when">
        {#each ONCE_PRESETS as p (p.value)}
          <option value={p.value}>{p.label}</option>
        {/each}
      </select>
      {#if onceWhen === "custom"}
        <input
          class={INPUT_CLASS}
          bind:value={onceCustom}
          placeholder="90m, 2h, 1d — or 2026-07-09T12:40:00Z"
          data-testid="once-custom"
        />
      {/if}
    {:else}
      <select class={INPUT_CLASS} bind:value={repeatEvery} data-testid="repeat-when">
        {#each EVERY_PRESETS as p (p.value)}
          <option value={p.value}>{p.label}</option>
        {/each}
      </select>
      {#if repeatEvery === "custom"}
        <input
          class={INPUT_CLASS}
          bind:value={repeatCustom}
          placeholder="5m, 90s, 1h30m, 1d"
          data-testid="repeat-custom"
        />
      {:else if repeatEvery === "cron"}
        <input
          class={INPUT_CLASS}
          bind:value={repeatCron}
          placeholder="0 9 * * 1  (min hour dom mon dow)"
          data-testid="repeat-cron"
        />
      {/if}
      <input
        class={INPUT_CLASS}
        bind:value={maxRuns}
        inputmode="numeric"
        placeholder="Max runs (optional — blank = forever)"
        data-testid="repeat-maxruns"
      />
    {/if}

    <textarea
      class={INPUT_CLASS}
      rows="3"
      bind:value={message}
      placeholder="Message to deliver into this session when it fires…"
      data-testid="sched-message"
    ></textarea>

    {#if formError}
      <p class="text-xs font-medium text-neg-400" data-testid="sched-error">{formError}</p>
    {/if}

    <button
      type="button"
      class="rounded-lg bg-green-500 px-4 py-2 text-sm font-medium text-white-100 hover:bg-green-600 active:bg-green-700 transition-colors disabled:opacity-50"
      disabled={!canSubmit}
      onclick={submit}
    >{submitting ? "Scheduling…" : "Schedule"}</button>
  </div>

  <!-- List -->
  {#if schedules.length === 0}
    <p class="text-xs text-black-700 dark:text-black-600">No scheduled messages.</p>
  {:else}
    {#each schedules as s (s.id)}
      <div
        class="rounded-xl border border-white-300 dark:border-navy-600 p-3 space-y-1.5"
        data-sid={s.id}
      >
        <div class="flex items-center gap-2 flex-wrap">
          {#if s.kind === "recurring"}
            <span class="text-xs font-medium text-black-900 dark:text-white-100">{cadence(s)}</span>
          {:else}
            <span class="text-xs font-medium text-black-900 dark:text-white-100">{fmtWhen(s.run_at)}</span>
          {/if}
          <span class={"shrink-0 rounded-full px-2 py-0.5 text-[10px] font-medium " + statusBadgeCls(s.status)}>
            {s.paused ? "paused" : s.status}
          </span>
          {#if s.created_by === "ai"}
            <span class="shrink-0 rounded-full px-2 py-0.5 text-[10px] font-medium bg-white-300 text-black-600 dark:bg-navy-700 dark:text-black-600">
              by agent
            </span>
          {/if}
        </div>

        <p class="text-xs text-black-800 dark:text-white-200 whitespace-pre-wrap break-words">{s.message}</p>

        <!-- Meta line: next / last / count for recurring -->
        {#if s.kind === "recurring"}
          <p class="text-[11px] text-black-700 dark:text-black-600">
            {#if s.status === "active" && !s.paused}next {fmtWhen(s.run_at)} · {/if}
            {#if s.last_run_at}last {fmtWhen(s.last_run_at)} · {/if}
            ran {s.run_count}{#if s.max_runs}/{s.max_runs}{/if}×
          </p>
        {/if}

        {#if s.last_error}
          <p class="text-[11px] text-neg-400">{s.last_error}</p>
        {/if}

        <!-- Actions -->
        {#if s.status === "pending" || s.status === "active"}
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
    {/each}
  {/if}
</div>
