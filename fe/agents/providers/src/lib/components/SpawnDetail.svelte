<script lang="ts">
  import { onMount } from "svelte";
  import { Breadcrumb, CodeEditor, type BreadcrumbItem } from "@wick-fe/common-ui";
  import { toastError } from "@wick-fe/common-stores";
  import { apiGetSpawnDetail, apiRevealSpawn } from "$lib/api.js";
  import type { SpawnDetailResponse } from "$lib/types.js";

  type Props = {
    base: string;
    file: string;
    /** Back link handler; omitted when embedded (inline expand). */
    onBack?: () => void;
    /** Embedded = rendered inline inside a Recent Spawns row: drop the
        breadcrumb + page header chrome, keep the detail cards. */
    embedded?: boolean;
    /** Open the log viewer for a runtime log file, optionally scoped to the
        spawn window. Omitted → the process-log rows are copy-only. */
    onOpenLog?: (logFile: string, from?: string, to?: string) => void;
  };
  let { base, file, onBack, embedded = false, onOpenLog }: Props = $props();

  // Copy-to-clipboard for the log-link buttons; shows a transient "Copied!".
  let copiedKey = $state("");
  async function copyText(key: string, text: string) {
    try {
      await navigator.clipboard.writeText(text);
      copiedKey = key;
      setTimeout(() => { if (copiedKey === key) copiedKey = ""; }, 1400);
    } catch (e: unknown) {
      toastError(`Copy failed: ${e instanceof Error ? e.message : String(e)}`);
    }
  }

  // Whole event timeline as pretty JSON — one click to paste into an AI.
  let rawJSON = $derived(data ? JSON.stringify(data.Events, null, 2) : "");

  function fmtTime(iso: string): string {
    return iso ? new Date(iso).toLocaleTimeString() : "";
  }
  function fmtDuration(ms: number): string {
    if (ms <= 0) return "";
    if (ms < 1000) return `${ms}ms`;
    const s = ms / 1000;
    if (s < 60) return `${s.toFixed(s < 10 ? 1 : 0)}s`;
    const m = Math.floor(s / 60);
    return `${m}m ${Math.round(s % 60)}s`;
  }
  // Log-scan hint. Clean: "21:55:07 → 21:55:39 (32s)". Still alive:
  // "21:55:07 → running". Crash/kill without an exit event:
  // "21:55:07 → ~21:55:40 (died, no exit)" — the end is approximate.
  let windowLabel = $derived.by(() => {
    const w = data?.Logs.Window;
    if (!w || !w.Start) return "";
    const start = fmtTime(w.Start);
    if (w.Running) return `${start} → running`;
    if (w.Unclean) {
      return w.End
        ? `${start} → ~${fmtTime(w.End)} (died, no exit event)`
        : `${start} → ended (died, no exit event)`;
    }
    const dur = fmtDuration(w.DurationMs);
    return `${start} → ${fmtTime(w.End)}${dur ? ` (${dur})` : ""}`;
  });

  // The final exit event, if the spawn ended in a crash/unclean state — used
  // to surface a "why it died" banner at the top instead of buried in the list.
  let crashEvent = $derived.by(() => {
    const evs = data?.Events ?? [];
    for (let i = evs.length - 1; i >= 0; i--) {
      const e = evs[i];
      if (e.Type === "exit" && (e.ExitReason === "error" || e.ExitReason === "unclean")) return e;
    }
    return null;
  });

  let data = $state<SpawnDetailResponse | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);

  let crumbs = $derived<BreadcrumbItem[]>([
    { label: "Providers", onClick: () => onBack?.() },
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

  // downloadBundle writes a single .txt with the spawn metadata, the
  // reproduce command, and the full event timeline (incl. crash stderr)
  // so it can be attached to an AI in one shot.
  function downloadBundle() {
    if (!data) return;
    const lines = [
      `# Spawn log — ${data.File.ProviderType}/${data.File.ProviderName}`,
      `session: ${data.File.SessionID}`,
      `started: ${data.File.StartedAt}`,
      `pid: ${data.File.PID}`,
      `exit_reason: ${data.File.ExitReason || "(alive)"}`,
      data.File.ReasonDetail ? `reason: ${data.File.ReasonDetail}` : "",
      data.File.ExitCode ? `exit_code: ${data.File.ExitCode}` : "",
      `spawn_file: ${data.Logs.SpawnPath || data.File.Path}`,
      "",
      "## Reproduce (bash, headless, masked)",
      data.Repro?.["bash-h-full-res"] ?? data.Repro?.["bash-h-full-new"] ?? "(none)",
      "",
      "## Events",
      JSON.stringify(data.Events, null, 2),
    ].filter((l) => l !== "");
    const blob = new Blob([lines.join("\n")], { type: "text/plain" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = `spawn-${shortID(data.File.SessionID)}-${data.File.PID || "x"}.txt`;
    document.body.appendChild(a);
    a.click();
    a.remove();
    URL.revokeObjectURL(url);
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

<div class={embedded ? "space-y-4" : "space-y-6"}>
  {#if !embedded}
    <Breadcrumb items={crumbs} />
  {/if}

  {#if loading}
    <div class="px-5 py-16 text-center text-sm text-black-700 dark:text-black-600">Loading…</div>
  {:else if error}
    <div class="rounded-xl border border-error-400 bg-error-100 px-4 py-3 text-sm text-error-800">{error}</div>
  {:else if data}
    {#if !embedded}
      <!-- Header (page mode only; the row already shows this in embedded mode) -->
      <div>
        <h1 class="text-lg font-semibold text-black-900 dark:text-white-100">Spawn Log</h1>
        <p class="mt-0.5 font-mono text-xs text-black-700 dark:text-black-600">
          {data.File.ProviderType}/{data.File.ProviderName} · session {shortID(data.File.SessionID)} · {new Date(data.File.StartedAt).toLocaleString()}
        </p>
      </div>
    {/if}

    {#if data.SessionDeleted}
      <div class="flex items-start gap-2 rounded-xl border border-cau-400 bg-cau-100 px-4 py-3">
        <span class="text-sm text-cau-400">⚠</span>
        <p class="text-xs text-cau-800">This session has been <strong>deleted</strong>. This is a historical spawn log — its session and the <code class="font-mono">cwd</code> path below may no longer exist.</p>
      </div>
    {/if}

    {#if crashEvent}
      <div class="rounded-xl border border-error-400 bg-error-100 dark:bg-error-400/10 px-4 py-3 space-y-1.5">
        <div class="flex items-center gap-2">
          <span class="text-sm text-error-400">✕</span>
          <h2 class="text-sm font-semibold text-error-800 dark:text-error-400">
            Process ended abnormally
            {#if crashEvent.ExitCode !== 0}<span class="font-mono">(exit {crashEvent.ExitCode})</span>{/if}
          </h2>
        </div>
        {#if crashEvent.ReasonDetail}<p class="text-xs text-error-800 dark:text-error-400">{crashEvent.ReasonDetail}</p>{/if}
        {#if crashEvent.StderrTail}
          <pre class="mt-1 max-h-56 overflow-auto whitespace-pre-wrap rounded-md bg-white-100 dark:bg-navy-800 px-2 py-1.5 font-mono text-xs text-error-400">{crashEvent.StderrTail}</pre>
        {/if}
        <p class="text-xs text-black-700 dark:text-black-600">Full timeline + stderr is in the event log below and in the spawn file linked under <strong>Logs</strong>.</p>
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
                {#if ev.ExitReason}<span class="text-xs {ev.ExitReason === 'error' || ev.ExitReason === 'unclean' ? 'text-error-400 font-medium' : 'text-black-800 dark:text-black-600'}">{ev.ExitReason}</span>{/if}
                {#if ev.ExitCode !== 0}<span class="text-xs text-error-400">exit {ev.ExitCode}</span>{/if}
                {#if ev.DurationMs > 0}<span class="text-xs text-black-700 dark:text-black-600">{ev.DurationMs}ms</span>{/if}
              </div>
              {#if ev.ReasonDetail}<p class="mt-1 text-xs {ev.ExitReason === 'error' || ev.ExitReason === 'unclean' ? 'text-error-400' : 'text-black-800 dark:text-black-600'}">{ev.ReasonDetail}</p>{/if}
              {#if ev.StderrTail}<pre class="mt-1 whitespace-pre-wrap rounded-md bg-error-100/50 dark:bg-error-400/10 px-2 py-1 font-mono text-xs text-error-400 break-all">{ev.StderrTail}</pre>{/if}
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

    <!-- Logs: copy the paths / raw JSON to paste into an AI -->
    <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-sm p-4 space-y-3">
      <div>
        <h2 class="text-sm font-semibold text-black-900 dark:text-white-100">Logs</h2>
        <p class="mt-0.5 text-xs text-black-700 dark:text-black-600">Grab the raw log for this spawn — copy a path or the JSON, or download a bundle to paste into an AI.</p>
        {#if windowLabel}
          <p class="mt-1 text-xs text-black-800 dark:text-black-600">
            <span class="font-medium">Spawn window:</span> <span class="font-mono">{windowLabel}</span> — scan the process logs around this range.
          </p>
        {/if}
      </div>

      <!-- Spawn jsonl file -->
      <div class="flex flex-wrap items-center gap-2">
        <span class="text-xs font-medium text-black-800 dark:text-black-600 w-24 shrink-0">Spawn file</span>
        <code class="flex-1 min-w-0 truncate rounded bg-white-200 dark:bg-navy-800 px-2 py-1 font-mono text-xs text-black-800 dark:text-black-600" title={data.Logs.SpawnPath || data.File.Path}>{data.Logs.SpawnPath || data.File.Path}</code>
        <button type="button" onclick={() => copyText("path", data.Logs.SpawnPath || data.File.Path)} class="shrink-0 rounded-lg border border-white-400 dark:border-navy-600 px-2.5 py-1 text-xs font-medium text-black-800 dark:text-white-100 hover:bg-white-200 dark:hover:bg-navy-800 transition-colors">{copiedKey === "path" ? "Copied!" : "Copy path"}</button>
        <button type="button" onclick={() => copyText("json", rawJSON)} class="shrink-0 rounded-lg border border-white-400 dark:border-navy-600 px-2.5 py-1 text-xs font-medium text-black-800 dark:text-white-100 hover:bg-white-200 dark:hover:bg-navy-800 transition-colors">{copiedKey === "json" ? "Copied!" : "Copy raw JSON"}</button>
        <button type="button" onclick={downloadBundle} class="shrink-0 rounded-lg border border-white-400 dark:border-navy-600 px-2.5 py-1 text-xs font-medium text-black-800 dark:text-white-100 hover:bg-white-200 dark:hover:bg-navy-800 transition-colors">Download bundle</button>
      </div>

      <!-- Process logs from the spawn's day(s): open in the viewer or copy path -->
      {#if data.Logs.Components.length > 0}
        <div class="space-y-2 border-t border-white-300 dark:border-navy-600 pt-3">
          <p class="text-xs text-black-700 dark:text-black-600">Process logs from this spawn's day — open to read the window above, or copy the path:</p>
          {#each data.Logs.Components as c}
            {@const logName = c.Path.slice(Math.max(c.Path.lastIndexOf("/"), c.Path.lastIndexOf("\\")) + 1)}
            <div class="flex flex-wrap items-center gap-2">
              <span class="text-xs font-medium text-black-800 dark:text-black-600 w-24 shrink-0 capitalize">{c.Prefix}</span>
              <code class="flex-1 min-w-0 truncate rounded bg-white-200 dark:bg-navy-800 px-2 py-1 font-mono text-xs text-black-800 dark:text-black-600" title={c.Path}>{c.Path}</code>
              {#if onOpenLog}
                <button type="button" onclick={() => onOpenLog?.(logName, data.Logs.Window.Start, data.Logs.Window.End)} class="shrink-0 rounded-lg bg-green-500 px-2.5 py-1 text-xs font-medium text-white-100 hover:bg-green-600 transition-colors">Open</button>
              {/if}
              <button type="button" onclick={() => copyText(`comp-${c.Path}`, c.Path)} class="shrink-0 rounded-lg border border-white-400 dark:border-navy-600 px-2.5 py-1 text-xs font-medium text-black-800 dark:text-white-100 hover:bg-white-200 dark:hover:bg-navy-800 transition-colors">{copiedKey === `comp-${c.Path}` ? "Copied!" : "Copy path"}</button>
            </div>
          {/each}
        </div>
      {:else}
        <div class="border-t border-white-300 dark:border-navy-600 pt-3">
          {#if data.Logs.LogsPresent === 0}
            <p class="text-xs text-black-700 dark:text-black-600">
              No process log files found in
              {#if data.Logs.LogsDir}<code class="font-mono">{data.Logs.LogsDir}</code>{:else}the logs directory{/if}.
              The server/worker/mcp logs are only written to files when running the
              packaged binary (<code class="font-mono">wick all</code> / tray); in dev
              (<code class="font-mono">wick-lab</code>) they go to the console instead.
            </p>
          {:else}
            <p class="text-xs text-black-700 dark:text-black-600">
              No process logs dated for this spawn's day
              {#if data.Logs.LogsDir}(other days exist in <code class="font-mono">{data.Logs.LogsDir}</code>){/if}.
            </p>
          {/if}
        </div>
      {/if}
    </div>
  {/if}
</div>
