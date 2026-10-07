<script lang="ts">
  /* ManagedBinaryPanel — the managed part of the "Binary" section for one
     wick-managed type (omp, opencode). Summary on top: active version, host, newest release
     and what to do about it (Download vX, then Activate vX). Below, one
     version list = cached GitHub releases + downloaded versions, each row
     with its own status and action: Download (fetch + verify + store only,
     `current` untouched), Activate (instant switch, sha256 re-checked),
     Remove. A first install activates on its own — nothing to switch from.
     Progress is the server job, so it survives a reload; while it runs
     every action for the type is disabled and shows the job's progress.
     Nothing downloads unless an admin clicks; the server re-checks
     everything (sha256, --version, in-use) regardless of what this shows. */
  import { onDestroy, onMount } from "svelte";
  import { Button, ProgressBar, Select } from "@wick-fe/common-ui";
  import { toastError, toastOk, toastWarn } from "@wick-fe/common-stores";
  import {
    apiManagedList,
    apiManagedDownload,
    apiManagedActivate,
    apiManagedRemove,
    apiManagedCheck,
    apiManagedVerify,
    CheckTooSoonError,
    isRunning,
    jobLabel,
    jobPct,
    jobShort,
    jobSummary,
    jobVersion,
    latestState,
    sessionsNote,
    versionRows,
    type ManagedBinary,
    type ManagedJob,
  } from "$lib/managedbin.js";

  type Props = {
    base: string;
    type: string;
    /* Compact = inside the Add form: status + first download only. */
    compact?: boolean;
    /* Embedded = inside the Detail page's "Binary" section: no card or
       header of its own, the summary becomes definition rows that line
       up with the section's Resolved path / Version rows. */
    embedded?: boolean;
    onChange?: (m: ManagedBinary | null) => void;
  };
  let { base, type, compact = false, embedded = false, onChange }: Props = $props();

  let data = $state<ManagedBinary | null>(null);
  let isAdmin = $state(false);
  let loading = $state(true);
  let busy = $state("");
  let verifyOut = $state<{ output: string; error: string } | null>(null);
  let timer: ReturnType<typeof setTimeout> | null = null;
  /* The job that just finished, kept on screen for a few seconds: a fast
     download otherwise went from "resolving" straight to nothing. */
  let justDone = $state<ManagedJob | null>(null);
  let justDoneTimer: ReturnType<typeof setTimeout> | null = null;

  function errText(e: unknown): string {
    const msg = e instanceof Error ? e.message : String(e);
    try {
      const j = JSON.parse(msg);
      if (j && typeof j.error === "string") return j.error;
    } catch {
      /* not JSON */
    }
    return msg;
  }

  async function load(): Promise<void> {
    try {
      const r = await apiManagedList(base);
      isAdmin = r.isAdmin;
      const prevRunning = isRunning(data?.job ?? null);
      data = r.types.find((t) => t.type === type) ?? null;
      onChange?.(data);
      if (prevRunning && data && !isRunning(data.job) && data.job) {
        if (data.job.phase === "error") toastError(`${type}: ${data.job.error}`);
        else {
          justDone = data.job;
          if (justDoneTimer) clearTimeout(justDoneTimer);
          justDoneTimer = setTimeout(() => { justDone = null; }, 8000);
          toastOk(`${type}: ${jobSummary(data.job)}`);
        }
      }
    } catch {
      data = null;
    } finally {
      loading = false;
    }
    schedule();
  }

  // Poll fast while a job runs, slowly otherwise (session counts change
  // as old processes finish).
  function schedule(): void {
    if (timer) clearTimeout(timer);
    // load() awaits before it gets here; after unmount it must not re-arm.
    if (destroyed) return;
    timer = setTimeout(() => void load(), isRunning(data?.job ?? null) ? 500 : 15000);
  }

  let destroyed = false;
  onMount(() => void load());
  onDestroy(() => { destroyed = true; if (timer) clearTimeout(timer); if (justDoneTimer) clearTimeout(justDoneTimer); });

  async function act(key: string, f: () => Promise<unknown>): Promise<void> {
    busy = key;
    try {
      await f();
    } catch (e) {
      if (e instanceof CheckTooSoonError) toastWarn(e.message);
      else toastError(errText(e));
    } finally {
      busy = "";
      await load();
    }
  }

  // A 409 already_running is not an error: adopt the job in flight so its
  // progress shows right away, then keep polling it.
  const download = (tag = "") =>
    act("dl-" + (tag || "latest"), async () => {
      const r = await apiManagedDownload(base, type, tag);
      if (data && r.job) data.job = r.job as ManagedJob;
    });
  const activate = (v: string) => act("act-" + v, async () => { await apiManagedActivate(base, type, v); toastOk(`${type} v${v} is now active`); });
  const remove = (v: string) => act("rm-" + v, async () => { await apiManagedRemove(base, type, v); toastOk(`Removed ${type} v${v}`); });
  const check = () => act("check", () => apiManagedCheck(base, type));
  const verify = () => act("verify", async () => { verifyOut = await apiManagedVerify(base, type); });

  const job = $derived(data && isRunning(data.job) ? data.job : null);
  const jobV = $derived(job ? jobVersion(job) : "");
  const locked = $derived(!!job || busy !== "");
  const note = $derived(data ? sessionsNote(data) : "");
  const failed = $derived(data?.lastJob?.phase === "error" && !job ? data.lastJob : null);
  const rows = $derived(data ? versionRows(data) : []);
  const onDisk = $derived(rows.filter((r) => r.status !== "not_downloaded"));
  const available = $derived(rows.filter((r) => r.status === "not_downloaded"));
  let pickVer = $state("");
  // A download started from the picker (not the latest/first-install button).
  const pickJob = $derived(job && !onDisk.some((r) => r.version === jobV) && data?.current ? job : null);
  const latestAct = $derived(data ? latestState(data) : "");
</script>

<div data-testid="managed-binary-panel" data-type={type} class="{embedded ? '' : `rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 ${compact ? 'p-3' : 'p-5'}`} space-y-3">
  {#if !embedded}
    <div class="flex flex-wrap items-center gap-2">
      <h3 class="text-sm font-semibold text-black-900 dark:text-white-100">Binary · {type}</h3>
      <span class="rounded bg-white-300 dark:bg-navy-600 px-1.5 py-0.5 text-[11px] font-medium text-black-800 dark:text-black-600">managed by wick</span>
      {#if data?.hostLabel}
        <span data-testid="managed-host" class="text-[11px] text-black-700 dark:text-black-600">{data.hostLabel}</span>
      {/if}
    </div>
  {/if}

  {#if loading}
    <p class="text-xs text-black-700 dark:text-black-600">Checking…</p>
  {:else if !data}
    <p class="text-xs text-black-700 dark:text-black-600">Managed binaries are not available for {type}.</p>
  {:else}
    {#if !data.enabled}
      <p class="text-xs text-black-700 dark:text-black-600">Disabled in config (providers.managed_binaries.{type}.enabled).</p>
    {/if}
    {#if embedded}
      <dl class="grid grid-cols-[6rem_minmax(0,1fr)] gap-x-3 gap-y-1.5 text-xs">
        {#if data.hostLabel}
          <dt class="text-black-700 dark:text-black-600">Platform</dt>
          <dd data-testid="managed-host" class="min-w-0 text-black-900 dark:text-white-100">{data.hostLabel}</dd>
        {/if}
        <dt class="text-black-700 dark:text-black-600">Active</dt>
        <dd class="min-w-0 flex flex-wrap items-center gap-x-2 gap-y-1">
          {#if data.current}
            <span data-testid="managed-current" class="font-mono font-medium text-black-900 dark:text-white-100">v{data.current}</span>
          {:else}
            <span data-testid="managed-not-installed" class="text-neg-400 font-medium">Binary not installed</span>
          {/if}
          {#if note}
            <span data-testid="managed-sessions-old" class="text-black-800 dark:text-black-600">{note}</span>
          {/if}
        </dd>
        {#if data.latest}
          <dt class="text-black-700 dark:text-black-600">Latest</dt>
          <dd class="min-w-0 flex flex-wrap items-center gap-x-2 gap-y-1">
            <span class="font-mono text-black-900 dark:text-white-100" title={data.latestCheckedAt ? `checked ${new Date(data.latestCheckedAt).toLocaleString()}` : ""}>{data.latest}</span>
            <span class="text-black-700 dark:text-black-600">on GitHub</span>
            {#if data.updateAvailable}
              <span data-testid="managed-update-available" class="rounded bg-cau-100 dark:bg-cau-400/20 px-1.5 py-0.5 text-[11px] font-medium text-cau-400">update available {data.latest}</span>
            {/if}
          </dd>
        {/if}
      </dl>
    {:else}
      <div class="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs">
        {#if data.current}
          <span data-testid="managed-current" class="text-black-900 dark:text-white-100">Active <span class="font-mono font-medium">v{data.current}</span></span>
        {:else}
          <span data-testid="managed-not-installed" class="text-neg-400 font-medium">Binary not installed</span>
        {/if}
        {#if data.latest}
          <span class="text-black-800 dark:text-black-600" title={data.latestCheckedAt ? `checked ${new Date(data.latestCheckedAt).toLocaleString()}` : ""}>Latest on GitHub <span class="font-mono">{data.latest}</span></span>
        {/if}
        {#if data.updateAvailable}
          <span data-testid="managed-update-available" class="rounded bg-cau-100 dark:bg-cau-400/20 px-1.5 py-0.5 text-[11px] font-medium text-cau-400">update available {data.latest}</span>
        {/if}
        {#if note}
          <span data-testid="managed-sessions-old" class="text-black-800 dark:text-black-600">{note}</span>
        {/if}
      </div>
    {/if}
    {#if data.currentPath && !compact && !embedded}
      <p class="font-mono text-[11px] text-black-700 dark:text-black-600 break-all">{data.currentPath}</p>
    {/if}
    {#if data.latestErr && !compact}
      <p data-testid="managed-latest-err" class="text-[11px] text-black-700 dark:text-black-600">Last GitHub check failed: {data.latestErr}</p>
    {/if}

    {#if job}
      <ProgressBar class="max-w-md" testid="managed-job" pct={jobPct(job)} label={jobLabel(job)} />
    {:else if justDone}
      <ProgressBar class="max-w-md" testid="managed-job-done" pct={100} label={"✓ " + jobSummary(justDone)} />
    {:else if failed}
      <p data-testid="managed-job-error" class="rounded-lg border border-neg-400 px-3 py-2 text-xs text-neg-400">Last download failed ({failed.tag || "latest"}): {failed.error}. The active version was not changed.</p>
    {/if}

    {#if isAdmin && data.enabled}
      <div class="flex flex-wrap items-center gap-2">
        {#if !data.current}
          <Button variant="primary" testid="managed-download-first" disabled={locked} onclick={() => download()}>{job ? jobShort(job) : "Download from GitHub"}</Button>
        {:else if latestAct === "download"}
          <Button variant="primary" testid="managed-download-latest" disabled={locked} onclick={() => download(data!.latest)}>{job ? jobShort(job) : `Download ${data.latest}`}</Button>
        {:else if latestAct === "activate"}
          <Button variant="primary" testid="managed-activate-latest" disabled={locked} onclick={() => activate(data!.latestVersion)}>{job ? jobShort(job) : `Activate ${data.latest}`}</Button>
        {/if}
        {#if !compact}
          <Button variant="secondary" testid="managed-check" disabled={locked} onclick={check}>{busy === "check" ? "Checking…" : "Check for update"}</Button>
          {#if data.current}
            <Button variant="secondary" disabled={locked} onclick={verify}>Re-check --version</Button>
          {/if}
        {/if}
      </div>
    {/if}

    {#if verifyOut}
      <p data-testid="managed-verify" class="font-mono text-[11px] {verifyOut.error ? 'text-neg-400' : 'text-black-800 dark:text-black-600'}">
        wick ran --version → {verifyOut.output || "(no output)"}{verifyOut.error ? ` — ${verifyOut.error}` : ""}
      </p>
    {/if}

    {#if !compact && rows.length > 0}
      <!-- Downloaded versions: a short table (active + the kept backups).
           Every other release sits behind one searchable picker instead of
           a row each — GitHub lists dozens and a long list buried the
           versions that are actually on disk. -->
      <div class="space-y-2">
        <p class="text-[11px] font-semibold tracking-wide text-black-700 dark:text-black-600">DOWNLOADED VERSIONS</p>
        {#if onDisk.length === 0}
          <p data-testid="managed-none-downloaded" class="text-xs text-black-700 dark:text-black-600">Nothing downloaded yet.</p>
        {:else}
          <div class="overflow-x-auto rounded-lg border border-white-300 dark:border-navy-600">
            <table data-testid="managed-version-list" class="w-full text-xs">
              <thead class="bg-white-200 dark:bg-navy-800 text-left text-[11px] text-black-700 dark:text-black-600">
                <tr>
                  <th class="px-3 py-2 font-medium">Version</th>
                  <th class="px-3 py-2 font-medium">Status</th>
                  <th class="px-3 py-2 font-medium">Downloaded</th>
                  <th class="px-3 py-2 font-medium">sha256</th>
                  <th class="px-3 py-2"></th>
                </tr>
              </thead>
              <tbody class="divide-y divide-white-300 dark:divide-navy-600">
                {#each onDisk as r (r.version)}
                  <tr data-testid="managed-version-row" data-version={r.version} data-status={r.status}>
                    <td class="px-3 py-2 font-mono font-medium text-black-900 dark:text-white-100 whitespace-nowrap">
                      v{r.version}
                      {#if r.latest}<span class="ml-1 rounded bg-white-300 dark:bg-navy-600 px-1.5 py-0.5 font-sans text-[11px] font-medium text-black-800 dark:text-black-600">latest</span>{/if}
                    </td>
                    <td class="px-3 py-2 whitespace-nowrap">
                      {#if r.status === "active"}
                        <span class="rounded bg-pos-100 dark:bg-pos-400/20 px-1.5 py-0.5 text-[11px] font-medium text-pos-400">active</span>
                      {:else}
                        <span class="rounded bg-white-300 dark:bg-navy-600 px-1.5 py-0.5 text-[11px] font-medium text-black-800 dark:text-black-600">downloaded</span>
                      {/if}
                      {#if (r.installed?.inUse ?? 0) > 0}
                        <span data-testid="managed-row-inuse" class="ml-1 text-black-800 dark:text-black-600">{r.installed?.inUse} {r.installed?.inUse === 1 ? "session" : "sessions"} using it</span>
                      {/if}
                    </td>
                    <td class="px-3 py-2 text-black-700 dark:text-black-600 whitespace-nowrap">{r.installed?.installedAt ? new Date(r.installed.installedAt).toLocaleString() : "—"}</td>
                    <td class="px-3 py-2 font-mono text-black-700 dark:text-black-600" title={r.installed?.sha256}>{r.installed?.sha256.slice(0, 12)}</td>
                    <td class="px-3 py-2">
                      {#if isAdmin && data.enabled}
                        <span class="flex justify-end gap-2">
                          {#if r.status === "downloaded"}
                            <Button variant="secondary" testid="managed-row-activate" disabled={locked} onclick={() => activate(r.version)}>Activate</Button>
                          {/if}
                          <Button
                            variant="secondary"
                            testid="managed-row-remove"
                            disabled={!r.installed?.removable || locked}
                            title={r.status === "active" ? "The active version cannot be removed" : (r.installed?.inUse ?? 0) > 0 ? "Still used by a running session" : ""}
                            onclick={() => remove(r.version)}
                          >Remove</Button>
                        </span>
                      {/if}
                    </td>
                  </tr>
                {/each}
              </tbody>
            </table>
          </div>
        {/if}

        {#if isAdmin && data.enabled && available.length > 0}
          <div data-testid="managed-other-version" class="flex flex-wrap items-end gap-2 pt-1">
            <div class="min-w-0 basis-64 flex-1 max-w-sm">
              <label for="managed-pick-{type}" class="mb-1 block text-[11px] font-medium text-black-700 dark:text-black-600">Download another version</label>
              <Select
                id="managed-pick-{type}"
                size="sm"
                placeholder="Pick a release…"
                searchable
                value={pickVer}
                options={available.map((r) => ({
                  value: r.tag,
                  label: `v${r.version}`,
                  description: r.published ? `released ${new Date(r.published).toLocaleDateString()}` : undefined,
                  badge: r.latest ? "latest" : r.prerelease ? "pre" : undefined,
                }))}
                onChange={(v) => { pickVer = v; }}
                disabled={locked}
              />
            </div>
            <Button variant="secondary" testid="managed-row-download" disabled={locked || !pickVer} onclick={() => { const t = pickVer; pickVer = ""; download(t); }}>
              {pickJob ? jobShort(pickJob) : "Download"}
            </Button>
          </div>
        {/if}
      </div>
    {/if}
  {/if}
</div>
