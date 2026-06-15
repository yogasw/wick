<script lang="ts">
  /* Per-job admin view, ported from job_detail.templ + jobs.js. Shows the
     schedule settings form (cron / max_runs / max_timeout_min / enabled), a
     manual Run button with run-status polling, and the runtime configs table
     (reusing the Phase-2 ConfigsForm with an injected job-scoped save).
     Polling: after Run returns a run_id, poll runs/{runID} every 1.5s and
     stop on success/error or on unmount — the interval is cleared in both
     paths so no timer leaks. */
  import { onDestroy } from "svelte";
  import { Button, TextInput, NumberInput } from "@wick-fe/common-ui";
  import { toastError, toastOk } from "@wick-fe/common-stores";
  import { getJob, updateJobSettings, setJobConfig, runJob, getJobRun } from "$lib/api.js";
  import type { JobDetail } from "$lib/types.js";
  import ConfigsForm from "../fields/ConfigsForm.svelte";
  import { renderMarkdownSafe } from "./markdown.js";

  type Props = { jobKey: string };
  let { jobKey }: Props = $props();

  const POLL_MS = 1500;

  let data = $state<JobDetail | null>(null);
  let loading = $state(true);
  let error = $state("");

  let schedule = $state("");
  let maxRuns = $state(0);
  let maxTimeoutMin = $state(30);
  let enabled = $state(false);
  let savingSettings = $state(false);

  let running = $state(false);
  let runStatus = $state("");
  let runOutput = $state("");
  let pollTimer: ReturnType<typeof setInterval> | null = null;

  let canConfigure = $derived(data?.can_configure ?? false);

  function clearPoll(): void {
    if (pollTimer) {
      clearInterval(pollTimer);
      pollTimer = null;
    }
  }

  async function load(): Promise<void> {
    loading = true;
    error = "";
    try {
      data = await getJob(jobKey);
      schedule = data.schedule;
      maxRuns = data.max_runs;
      maxTimeoutMin = data.max_timeout_min;
      enabled = data.enabled;
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      loading = false;
    }
  }

  async function saveSettings(): Promise<void> {
    if (savingSettings) return;
    savingSettings = true;
    try {
      await updateJobSettings(jobKey, {
        schedule,
        enabled,
        max_runs: maxRuns,
        max_timeout_min: maxTimeoutMin,
      });
      toastOk("Settings saved");
    } catch (e) {
      toastError("Save failed", e instanceof Error ? e.message : String(e));
    } finally {
      savingSettings = false;
    }
  }

  async function saveConfig(key: string, value: string): Promise<void> {
    await setJobConfig(jobKey, key, value);
  }

  async function run(): Promise<void> {
    if (running) return;
    running = true;
    runStatus = "running";
    runOutput = "";
    clearPoll();
    try {
      const runID = await runJob(jobKey);
      pollTimer = setInterval(() => poll(runID), POLL_MS);
    } catch (e) {
      runStatus = "error";
      runOutput = renderMarkdownSafe(e instanceof Error ? e.message : String(e));
      running = false;
    }
  }

  async function poll(runID: string): Promise<void> {
    try {
      const r = await getJobRun(jobKey, runID);
      if (r.status === "running") return;
      clearPoll();
      runStatus = r.status;
      runOutput = r.result ? renderMarkdownSafe(r.result) : "";
      running = false;
    } catch {
      /* transient fetch error — keep polling until the deadline-less loop
         either succeeds or the user navigates away (unmount clears it) */
    }
  }

  const statusClasses: Record<string, string> = {
    running: "bg-prog-100 text-prog-400",
    success: "bg-pos-100 text-pos-400",
    error: "bg-neg-100 text-neg-400",
  };

  $effect(() => { load(); });
  onDestroy(clearPoll);
</script>

{#if loading}
  <div class="px-5 py-12 text-center text-sm text-black-700 dark:text-black-600">Loading…</div>
{:else if error}
  <div class="rounded-lg border border-red-300 dark:border-red-800 bg-red-50 dark:bg-red-900/20 px-4 py-3 text-sm text-red-700 dark:text-red-400">{error}</div>
{:else if data}
  <div class="space-y-6">
    <div class="flex items-start justify-between gap-4">
      <div class="flex items-center gap-3">
        <div class="flex h-12 w-12 flex-shrink-0 items-center justify-center rounded-lg bg-green-200 dark:bg-green-800 text-lg font-semibold text-green-700 dark:text-green-300">{data.icon}</div>
        <div>
          <div class="flex items-center gap-2">
            <h1 class="text-lg font-semibold text-black-900 dark:text-white-100">{data.name}</h1>
            <span class="rounded-full px-2 py-0.5 text-[10px] font-medium {enabled ? statusClasses[data.last_status] ?? statusClasses.success : 'bg-white-300 dark:bg-navy-600 text-black-700 dark:text-black-600'}">{enabled ? data.last_status : "disabled"}</span>
          </div>
          {#if data.description}
            <p class="mt-0.5 text-sm text-black-800 dark:text-black-600">{data.description}</p>
          {/if}
        </div>
      </div>
      <Button size="md" disabled={running} onclick={run}>{running ? "Running…" : "Run Now"}</Button>
    </div>

    {#if runStatus}
      <section class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-4">
        <div class="flex items-center gap-2 text-xs text-black-700 dark:text-black-600">
          <span class="rounded-full px-2 py-0.5 font-medium {statusClasses[runStatus] ?? statusClasses.running}">{runStatus === "running" ? "Running" : runStatus}</span>
        </div>
        <div class="mt-2 max-h-96 overflow-auto rounded-lg bg-white-200 dark:bg-navy-800 p-3 text-sm text-black-900 dark:text-white-100">
          {#if runStatus === "running"}
            <p class="text-black-700 dark:text-black-600">Waiting for result…</p>
          {:else if runOutput}
            <!-- eslint-disable-next-line svelte/no-at-html-tags -->
            {@html runOutput}
          {:else}
            <p class="text-black-700 dark:text-black-600">No output.</p>
          {/if}
        </div>
      </section>
    {/if}

    <section>
      <h2 class="text-base font-semibold text-black-900 dark:text-white-100">Schedule</h2>
      <div class="mt-3 space-y-4 rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-4">
        <div>
          <label for="job-schedule" class="block text-xs font-medium text-black-800 dark:text-black-600">Cron expression</label>
          <div class="mt-1 max-w-sm">
            <TextInput value={schedule} onChange={(v) => (schedule = v)} disabled={!canConfigure} placeholder="0 * * * *" ariaLabel="Cron expression" class="font-mono" />
          </div>
          <p class="mt-1 text-xs text-black-700 dark:text-black-600">Standard 5-field cron. Leave blank to disable scheduled runs.</p>
        </div>
        <div>
          <label for="job-max-runs" class="block text-xs font-medium text-black-800 dark:text-black-600">Max runs (0 = unlimited)</label>
          <div class="mt-1 w-40">
            <NumberInput value={maxRuns} min={0} onChange={(n) => (maxRuns = n)} disabled={!canConfigure} ariaLabel="Max runs" />
          </div>
        </div>
        <div>
          <label for="job-max-timeout" class="block text-xs font-medium text-black-800 dark:text-black-600">Max timeout (minutes)</label>
          <div class="mt-1 w-40">
            <NumberInput value={maxTimeoutMin} min={1} onChange={(n) => (maxTimeoutMin = n)} disabled={!canConfigure} ariaLabel="Max timeout (minutes)" />
          </div>
          <p class="mt-1 text-xs text-black-700 dark:text-black-600">Auto-cancel run if it exceeds this duration. Default 30.</p>
        </div>
        <label class="inline-flex items-center gap-3 cursor-pointer select-none">
          <input type="checkbox" class="h-5 w-5 rounded accent-green-500" checked={enabled} disabled={!canConfigure} onchange={(e) => (enabled = (e.target as HTMLInputElement).checked)} />
          <span class="text-sm text-black-900 dark:text-white-100">Enabled</span>
        </label>
        {#if canConfigure}
          <div>
            <Button disabled={savingSettings} onclick={saveSettings}>{savingSettings ? "Saving…" : "Save"}</Button>
          </div>
        {/if}
      </div>
    </section>

    <section>
      <h2 class="text-base font-semibold text-black-900 dark:text-white-100">Configuration</h2>
      <p class="mt-1 text-sm text-black-800 dark:text-black-600">Runtime variables for this job. Values are read by Run() at invocation time.</p>
      <ConfigsForm fields={data.fields ?? []} canConfigure={canConfigure} save={saveConfig} />
    </section>
  </div>
{/if}
