<script lang="ts">
  import { onMount } from "svelte";
  import { Breadcrumb, CodeEditor, type BreadcrumbItem } from "@wick-fe/common-ui";
  import { toastError } from "@wick-fe/common-stores";
  import { apiGetSpawnDetail, apiRevealSpawn } from "$lib/api.js";
  import type { SpawnDetailResponse } from "$lib/types.js";

  type Props = {
    base: string;
    file: string;
    onBack: () => void;
  };
  let { base, file, onBack }: Props = $props();

  let data = $state<SpawnDetailResponse | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);

  let crumbs = $derived<BreadcrumbItem[]>([
    { label: "Providers", onClick: onBack },
    { label: "Spawn Log", truncate: true },
  ]);

  function shortID(id: string): string {
    return id.length > 8 ? id.slice(0, 8) : id;
  }

  onMount(() => {
    apiGetSpawnDetail(base, file)
      .then((d) => { data = d; })
      .catch((e: unknown) => { error = e instanceof Error ? e.message : String(e); })
      .finally(() => { loading = false; });
  });

  /* ── Reproduce card state ─────────────────────────────────────── */
  type Axis = "mode" | "env" | "shell" | "path" | "resume";
  let sel = $state<Record<Axis, string>>({ mode: "headless", env: "masked", shell: "bash", path: "full", resume: "res" });
  let live = $state<Record<string, string> | null>(null); // unmasked, fetched once
  let liveLoading = $state(false);
  let liveError = $state("");
  // per-variant user edits, keyed "<env>:<variant>" so switching keeps them
  let edited = $state<Record<string, string>>({});
  let copied = $state(false);

  // Mirrors Go view.ReproKey: "<shell>-<h|i>-<full|short>-<res|new>".
  function variantKey(): string {
    const mode = sel.mode === "interactive" ? "i" : "h";
    return `${sel.shell}-${mode}-${sel.path}-${sel.resume}`;
  }

  let command = $derived.by(() => {
    const k = variantKey();
    const editKey = `${sel.env}:${k}`;
    if (edited[editKey] != null) return edited[editKey];
    if (sel.env === "live") return live ? (live[k] ?? "") : "";
    return data?.Repro?.[k] ?? "";
  });

  let showCmdNote = $derived(sel.shell === "cmd");

  function pick(axis: Axis, val: string) {
    sel = { ...sel, [axis]: val };
    if (axis === "env" && val === "live" && !live && !liveLoading) void loadLive();
  }

  async function loadLive() {
    liveLoading = true;
    liveError = "";
    try {
      live = await apiRevealSpawn(base, file);
    } catch (e: unknown) {
      liveError = `Failed to load live env: ${e instanceof Error ? e.message : String(e)}`;
      sel = { ...sel, env: "masked" }; // fall back so the editor isn't blank
    } finally {
      liveLoading = false;
    }
  }

  function onEditorChange(v: string) {
    edited = { ...edited, [`${sel.env}:${variantKey()}`]: v };
  }

  async function copyCommand() {
    try {
      await navigator.clipboard.writeText(command);
      copied = true;
      setTimeout(() => { copied = false; }, 1400);
    } catch (e: unknown) {
      toastError(`Copy failed: ${e instanceof Error ? e.message : String(e)}`);
    }
  }

  type SegGroup = { axis: Axis; label: string; opts: [string, string][] };
  // The Resume control only appears when the spawn actually had a resume id;
  // otherwise Keep and Fresh render identically, so it would just confuse.
  let segGroups = $derived<SegGroup[]>([
    { axis: "mode", label: "Mode", opts: [["headless", "Headless"], ["interactive", "Interactive"]] },
    { axis: "env", label: "Env", opts: [["masked", "Masked"], ["live", "Live"]] },
    { axis: "shell", label: "Shell", opts: [["bash", "bash"], ["powershell", "PowerShell"], ["cmd", "cmd.exe"]] },
    { axis: "path", label: "Path", opts: [["full", "Full"], ["short", "Short"]] },
    ...(data?.HasResume ? [{ axis: "resume" as Axis, label: "Resume", opts: [["res", "Keep"], ["new", "Fresh"]] as [string, string][] }] : []),
  ]);
</script>

<div class="space-y-6">
  <Breadcrumb items={crumbs} />

  {#if loading}
    <div class="px-5 py-16 text-center text-sm text-black-700 dark:text-black-600">Loading…</div>
  {:else if error}
    <div class="rounded-xl border border-error-400 bg-error-100 px-4 py-3 text-sm text-error-800">{error}</div>
  {:else if data}
    <!-- Header -->
    <div>
      <h1 class="text-lg font-semibold text-black-900 dark:text-white-100">Spawn Log</h1>
      <p class="mt-0.5 font-mono text-xs text-black-700 dark:text-black-600">
        {data.File.ProviderType}/{data.File.ProviderName} · session {shortID(data.File.SessionID)} · {new Date(data.File.StartedAt).toLocaleString()}
      </p>
    </div>

    {#if data.SessionDeleted}
      <div class="flex items-start gap-2 rounded-xl border border-cau-400 bg-cau-100 px-4 py-3">
        <span class="text-sm text-cau-400">⚠</span>
        <p class="text-xs text-cau-800">This session has been <strong>deleted</strong>. This is a historical spawn log — its session and the <code class="font-mono">cwd</code> path below may no longer exist.</p>
      </div>
    {/if}

    <!-- Reproduce -->
    <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-sm p-4 space-y-4">
      <div class="flex items-start justify-between gap-4">
        <div>
          <h2 class="text-sm font-semibold text-black-900 dark:text-white-100">Reproduce</h2>
          <p class="mt-0.5 text-xs text-black-700 dark:text-black-600">Editable — tweak a flag, then copy. Runs the spawn outside wick.</p>
        </div>
        <button
          type="button"
          onclick={copyCommand}
          class="shrink-0 inline-flex items-center gap-1.5 rounded-lg bg-green-500 px-3 py-1.5 text-xs font-medium text-white-100 hover:bg-green-600 active:bg-green-700 transition-colors"
        >
          <svg viewBox="0 0 16 16" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
            <rect x="5" y="5" width="8" height="8" rx="1.5"></rect>
            <path d="M11 5V3.5A1.5 1.5 0 0 0 9.5 2h-6A1.5 1.5 0 0 0 2 3.5v6A1.5 1.5 0 0 0 3.5 11H5" stroke-linecap="round"></path>
          </svg>
          <span>{copied ? "Copied!" : "Copy"}</span>
        </button>
      </div>

      <div class="flex flex-wrap items-center gap-x-6 gap-y-3">
        {#each segGroups as g}
          <div class="flex items-center gap-2">
            <span class="text-xs text-black-700 dark:text-black-600">{g.label}</span>
            <div class="inline-flex gap-0.5 rounded-lg bg-white-200 dark:bg-navy-800 p-0.5">
              {#each g.opts as [val, label]}
                {@const on = sel[g.axis] === val}
                <button
                  type="button"
                  aria-pressed={on}
                  onclick={() => pick(g.axis, val)}
                  class="px-3 py-1 text-xs font-medium rounded-md transition-colors {on
                    ? 'bg-white-100 dark:bg-navy-700 text-black-900 dark:text-white-100 shadow-sm'
                    : 'text-black-700 dark:text-black-600 hover:text-black-900 dark:hover:text-white-100'}"
                >{label}</button>
              {/each}
            </div>
          </div>
        {/each}
      </div>

      {#if liveError}
        <p class="text-xs text-error-400">{liveError}</p>
      {/if}

      <div class="h-56">
        {#if sel.env === "live" && liveLoading}
          <div class="flex h-full items-center justify-center rounded-lg border border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800 text-xs text-black-700 dark:text-black-600">Loading live env…</div>
        {:else}
          <CodeEditor
            language="sh"
            value={command}
            onChange={onEditorChange}
            theme={{ light: "chrome", dark: "twilight" }}
          />
        {/if}
      </div>

      {#if showCmdNote}
        <p class="text-xs text-cau-400">cmd.exe: the <code class="font-mono">--mcp-config</code> JSON arg has doubled quotes (<code class="font-mono">""</code>) — PowerShell or bash reproduce more reliably.</p>
      {/if}
    </div>

    <!-- Event timeline -->
    <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-sm overflow-hidden">
      {#if data.Events.length === 0}
        <div class="px-5 py-12 text-center text-sm text-black-700 dark:text-black-600">No events.</div>
      {:else}
        <ul class="divide-y divide-white-300 dark:divide-navy-600">
          {#each data.Events as ev}
            <li class="px-5 py-3">
              <div class="flex items-center gap-3">
                <span class="font-mono text-xs text-black-700 dark:text-black-600">{new Date(ev.At).toLocaleTimeString()}</span>
                <span class="rounded-full bg-white-300 dark:bg-navy-600 px-2 py-0.5 text-xs font-medium text-black-900 dark:text-white-100">{ev.Type}</span>
                {#if ev.ExitReason}<span class="text-xs text-black-800 dark:text-black-600">{ev.ExitReason}</span>{/if}
                {#if ev.DurationMs > 0}<span class="text-xs text-black-700 dark:text-black-600">{ev.DurationMs}ms</span>{/if}
              </div>
              {#if ev.Error}<p class="mt-1 font-mono text-xs text-error-400 break-all">{ev.Error}</p>{/if}
              {#if ev.Workspace}<p class="mt-1 font-mono text-xs text-black-800 dark:text-black-600 break-all">cwd: {ev.Workspace}</p>{/if}
              {#if ev.ResumeID}<p class="mt-1 font-mono text-xs text-black-800 dark:text-black-600 break-all">resume: {ev.ResumeID}</p>{/if}
              {#if ev.PID !== 0}<p class="mt-1 font-mono text-xs text-black-800 dark:text-black-600">pid: {ev.PID}</p>{/if}
              {#if ev.Binary}<p class="mt-1 font-mono text-xs text-black-800 dark:text-black-600 break-all">bin: {ev.Binary}</p>{/if}
              {#if ev.Args.length > 0}<p class="mt-1 font-mono text-xs text-black-800 dark:text-black-600 break-all">args: {ev.Args.join(" ")}</p>{/if}
              {#if ev.Env.length > 0}<p class="mt-1 font-mono text-xs text-black-800 dark:text-black-600 break-all">env: {ev.Env.join(" ")}</p>{/if}
              {#if ev.FirstUserMessage}<p class="mt-1 font-mono text-xs text-black-800 dark:text-black-600 break-all">msg: {ev.FirstUserMessage}</p>{/if}
            </li>
          {/each}
        </ul>
      {/if}
    </div>
  {/if}
</div>
