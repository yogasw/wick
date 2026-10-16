<script lang="ts">
  import { Effect } from "effect";
  import { WickClientLayer } from "@wick-fe/common-api";
  import { Modal } from "@wick-fe/common-ui";
  import type { Schedule, WatchRun, WatchRunSummary, WatchStep } from "./api.js";
  import { listRuns, getRun, testWatch, saveSteps, stepSummary, stopLabel, ruleText, ago } from "./api.js";

  type Props = {
    s: Schedule;
    base: string;
    onClose: () => void;
    /* The schedule changed (steps saved) — the page reloads its list. */
    onChanged?: () => void;
    /* Bring a failed/done watch back (status page action). */
    onResume?: (id: string) => void;
    /* Delete the watch and its history (the page confirms first). */
    onDelete?: (s: Schedule) => void;
  };
  let { s, base, onClose, onChanged, onResume, onDelete }: Props = $props();

  const run = <A,>(eff: Effect.Effect<A, unknown, never>) =>
    Effect.runPromise(eff.pipe(Effect.provide(WickClientLayer)) as Effect.Effect<A, unknown, never>);
  const msg = (e: unknown) => (e instanceof Error ? e.message : String(e));

  let runs = $state<WatchRunSummary[]>([]);
  let runsLoading = $state(true);
  let runsError = $state("");
  let onlyErrors = $state(false);
  let openRun = $state<WatchRun | null>(null);
  let openRunID = $state("");

  function loadRuns() {
    run(listRuns(base, s.id, onlyErrors ? "error" : ""))
      .then((r) => {
        runs = r;
        runsError = "";
      })
      .catch((e: unknown) => {
        runsError = msg(e);
      })
      .finally(() => {
        runsLoading = false;
      });
  }

  function toggleRun(id: string) {
    if (openRunID === id) {
      openRunID = "";
      openRun = null;
      return;
    }
    openRunID = id;
    openRun = null;
    run(getRun(base, s.id, id))
      .then((r) => {
        if (openRunID === id) openRun = r;
      })
      .catch((e: unknown) => {
        runsError = msg(e);
      });
  }

  // Reload the history when the schedule, its latest run, or the filter changes.
  $effect(() => {
    void s.id;
    void s.last_run_at;
    void onlyErrors;
    loadRuns();
  });

  /* Test now: one dry run with the saved steps — or the editor's unsaved ones. */
  let testing = $state(false);
  let testResult = $state<WatchRun | null>(null);
  let testError = $state("");

  function parseDraft(): WatchStep[] | undefined {
    if (!editing || !draft.trim()) return undefined;
    return JSON.parse(draft) as WatchStep[];
  }

  function testNow() {
    testing = true;
    testError = "";
    testResult = null;
    let steps: WatchStep[] | undefined;
    try {
      steps = parseDraft();
    } catch (e) {
      testError = "Steps JSON: " + msg(e);
      testing = false;
      return;
    }
    run(testWatch(base, s.id, steps))
      .then((r) => {
        testResult = r;
        loadRuns();
      })
      .catch((e: unknown) => {
        testError = msg(e);
      })
      .finally(() => {
        testing = false;
      });
  }

  /* Steps editor: JSON, validated by the server (shape, limits, Bash). */
  let editing = $state(false);
  let draft = $state("");
  let saving = $state(false);
  let saveError = $state("");

  function startEdit() {
    draft = JSON.stringify(s.steps ?? [], null, 2);
    saveError = "";
    editing = true;
  }

  function save() {
    let steps: WatchStep[];
    try {
      steps = JSON.parse(draft) as WatchStep[];
    } catch (e) {
      saveError = "Steps JSON: " + msg(e);
      return;
    }
    saving = true;
    run(saveSteps(base, s.id, steps))
      .then(() => {
        editing = false;
        onChanged?.();
      })
      .catch((e: unknown) => {
        saveError = msg(e);
      })
      .finally(() => {
        saving = false;
      });
  }

  /* Where the latest real run stopped: the timeline marks that step. */
  const latest = $derived(runs.find((r) => !r.dry_run));
  function stepTone(i: number): "error" | "pending" | "matched" | "" {
    const r = latest;
    if (!r?.stopped_at || r.stopped_at.index !== i) return "";
    if (r.result === "error" || r.outcome === "fail") return "error";
    return r.result === "matched" ? "matched" : "pending";
  }
  const toneCls: Record<string, string> = {
    error: "border-neg-400 bg-neg-100/60 dark:bg-neg-400/10",
    pending: "border-white-300 dark:border-navy-600",
    matched: "border-green-500 bg-green-50 dark:border-green-700 dark:bg-green-900/20",
    "": "border-white-300 dark:border-navy-600",
  };
  const kindIcon: Record<string, string> = { connector: "🔌", bash: "⌨", check: "✓" };

  function fmtDur(ms: number): string {
    if (!ms || ms < 1000) return `${ms ?? 0} ms`;
    if (ms < 60_000) return `${(ms / 1000).toFixed(1)} s`;
    return `${Math.floor(ms / 60_000)}m ${Math.round((ms % 60_000) / 1000)}s`;
  }

  const placeholder = `[
  {"kind": "connector", "name": "pipeline", "tool_id": "conn:<connector_id>/get_pipeline", "params": {"uuid": "{...}"}},
  {"kind": "check", "name": "done?", "rules": [{"path": "state.name", "op": "equals", "value": "COMPLETED"}],
   "fail_rules": [{"path": "state.result.name", "op": "in", "value": ["FAILED", "ERROR"]}]}
]`;

  function fmtTime(iso: string): string {
    const d = new Date(iso);
    return isNaN(d.getTime()) ? iso : d.toLocaleString(undefined, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit", second: "2-digit" });
  }

  function resultCls(r: string): string {
    switch (r) {
      case "matched":
        return "bg-green-100 text-green-700 dark:bg-green-900 dark:text-green-300";
      case "error":
        return "bg-neg-100 text-neg-400 dark:bg-neg-400/20 dark:text-neg-300";
      case "pending":
        // Pending is the normal waiting state, not a warning: an outlined
        // neutral badge, so a long run of ticks reads calm.
        return "border border-white-400 text-black-700 dark:border-navy-500 dark:text-white-300";
      default:
        return "bg-white-300 text-black-600 dark:bg-navy-700 dark:text-black-600";
    }
  }

  const canEdit = $derived(s.can_edit !== false);
  const revivable = $derived(s.status === "failed" || s.status === "done");
  const btn =
    "rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-3 py-1.5 text-xs font-medium text-black-800 dark:text-white-200 hover:bg-white-200 dark:hover:bg-navy-600 disabled:opacity-50";
</script>

{#snippet runSteps(r: WatchRun)}
  {#if r.reason}<p class="mb-1 text-black-800 dark:text-white-200"><span class="font-medium">{stopLabel(r.stopped_at)}</span> — {r.reason}</p>{/if}
  {#if r.extract}<pre class="mb-1 whitespace-pre-wrap break-all font-mono text-[11px] text-black-800 dark:text-white-200">extract {JSON.stringify(r.extract)}</pre>{/if}
  <div class="space-y-1.5" data-testid="run-steps">
    {#each r.steps as sr, j (j)}
      <details class="rounded-lg border border-white-300 dark:border-navy-600" open={j === r.steps.length - 1}>
        <summary class="cursor-pointer px-2 py-1 text-black-800 dark:text-white-200">
          {sr.name} · {sr.kind}{sr.decision ? " · " + sr.decision : ""} · exit {sr.exit_code} · {sr.duration_ms} ms{sr.error ? " · " + sr.error : ""}
        </summary>
        {#if sr.tool_id || sr.params}
          <pre class="whitespace-pre-wrap break-all px-2 py-1 font-mono text-[11px] text-black-700 dark:text-black-600">{sr.tool_id ?? ""} {sr.params ? JSON.stringify(sr.params) : ""}</pre>
        {/if}
        {#if sr.rules?.length}
          <ul class="px-2 py-1 font-mono text-[11px]" data-testid="rule-results">
            {#each sr.rules as rr, k (k)}
              <li class={rr.ok ? "text-green-700 dark:text-green-400" : "text-black-700 dark:text-black-600"}>
                {rr.ok ? "✓" : "✗"} {rr.fail ? "fail: " : ""}{ruleText({ path: rr.path, op: rr.op, value: rr.want })} — got {rr.found ? JSON.stringify(rr.got) : "(missing)"}
              </li>
            {/each}
          </ul>
        {/if}
        <pre class="max-h-64 overflow-auto whitespace-pre-wrap break-all bg-white-200 dark:bg-navy-800 px-2 py-1.5 font-mono text-[11px] text-black-800 dark:text-white-200">{sr.output || "(no output)"}</pre>
        {#if sr.stderr}
          <pre class="max-h-40 overflow-auto whitespace-pre-wrap break-all bg-white-200 dark:bg-navy-800 px-2 py-1.5 font-mono text-[11px] text-neg-400">{sr.stderr}</pre>
        {/if}
      </details>
    {/each}
  </div>
{/snippet}

<Modal open={true} {onClose} title="Watch" size="xl">
  <div class="space-y-4" data-testid="watch-detail">
    <div class="min-w-0 space-y-1">
      <p class="text-xs text-black-800 dark:text-white-200 whitespace-pre-wrap break-words">{s.message}</p>
      <p class="text-[11px] text-black-700 dark:text-black-600">
        {s.id} · {s.status}{#if s.last_result} · last {s.last_result}{/if}{#if s.last_run_at} · {ago(s.last_run_at)}{/if}{#if s.steps_rev} · rev {s.steps_rev}{/if}
      </p>
    </div>

    {#if canEdit}
      <div class="flex flex-wrap items-center gap-2">
        <button type="button" class={btn} onclick={testNow} disabled={testing} data-testid="test-now">
          {testing ? "Testing…" : editing && draft ? "Test draft" : "Test now"}
        </button>
        {#if revivable && onResume}
          <button type="button" class={btn} onclick={() => onResume?.(s.id)} data-testid="resume">Resume</button>
        {/if}
        {#if onDelete}
          <button type="button" class={btn + " text-neg-400 dark:text-neg-300"} onclick={() => onDelete?.(s)} data-testid="delete-watch">Delete</button>
        {/if}
        <span class="text-[11px] text-black-700 dark:text-black-600">Test = one dry run: nothing is delivered, the schedule does not change.</span>
      </div>
      {#if testError}<p class="text-[11px] text-neg-400" data-testid="test-error">{testError}</p>{/if}
      {#if testResult}
        <div class="rounded-lg border border-white-300 dark:border-navy-600 p-2 text-[11px]" data-testid="test-result">
          <p class="mb-1"><span class={"rounded-full px-1.5 py-px text-[10px] font-medium " + resultCls(testResult.result)}>{testResult.result}</span> dry run · {fmtDur(testResult.duration_ms)}</p>
          {@render runSteps(testResult)}
        </div>
      {/if}
    {/if}

    <section class="space-y-2">
      <h3 class="text-xs font-semibold uppercase tracking-wide text-black-700 dark:text-black-600">Steps</h3>
      {#if (s.steps ?? []).length === 0}
        <p class="text-[11px] text-black-700 dark:text-black-600">No steps.</p>
      {:else}
        <ol class="flex flex-wrap items-stretch gap-1.5" data-testid="watch-steps">
          {#each s.steps ?? [] as st, i (i)}
            {#if i > 0}<li class="flex items-center text-black-600" aria-hidden="true">→</li>{/if}
            <li class={"min-w-[10rem] max-w-xs flex-1 rounded-lg border px-3 py-2 " + toneCls[stepTone(i)]} data-testid="watch-step" data-tone={stepTone(i)}>
              <div class="flex items-center gap-2">
                <span class="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-white-300 dark:bg-navy-600 text-[10px] font-semibold tabular-nums text-black-800 dark:text-white-200">{i + 1}</span>
                <span class="text-xs" title={st.kind} aria-label={st.kind}>{kindIcon[st.kind] ?? "•"}</span>
                <span class="truncate text-xs font-medium text-black-900 dark:text-white-100">{st.name || st.kind}</span>
              </div>
              <p class="mt-1 break-all font-mono text-[11px] text-black-800 dark:text-white-200">{stepSummary(st)}</p>
              {#if st.on_ok === "done" || st.on_fail === "pending"}
                <p class="mt-0.5 text-[10px] text-black-700 dark:text-black-600">
                  {st.on_ok === "done" ? "ok → finish here" : ""}{st.on_ok === "done" && st.on_fail === "pending" ? " · " : ""}{st.on_fail === "pending" ? "fail → retry" : ""}
                </p>
              {/if}
              {#if stepTone(i) && latest?.reason}
                <p class={"mt-1 text-[11px] " + (stepTone(i) === "error" ? "text-neg-400" : "text-black-800 dark:text-white-200")} data-testid="step-reason">
                  {latest.reason}
                </p>
              {/if}
            </li>
          {/each}
        </ol>
      {/if}
      {#if canEdit}
        <details class="rounded-lg border border-white-300 dark:border-navy-600" bind:open={editing} ontoggle={(e) => { if ((e.currentTarget as HTMLDetailsElement).open && !draft) startEdit(); }}>
          <summary class="cursor-pointer px-3 py-1.5 text-[11px] font-medium text-black-700 dark:text-black-600" data-testid="edit-steps">Advanced: edit steps JSON</summary>
          <div class="space-y-2 p-3 pt-1">
            <textarea
              class="h-64 w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 p-2 font-mono text-[11px] text-black-900 dark:text-white-100"
              bind:value={draft}
              {placeholder}
              spellcheck="false"
              aria-label="Steps JSON"
              data-testid="steps-editor"
            ></textarea>
            {#if saveError}<p class="whitespace-pre-wrap rounded-lg bg-neg-100 dark:bg-neg-400/10 px-2 py-1 font-mono text-[11px] text-neg-400" data-testid="save-error">{saveError}</p>{/if}
            <div class="flex items-center gap-2">
              <button type="button" class={btn} onclick={save} disabled={saving} data-testid="save-steps">{saving ? "Saving…" : "Save steps"}</button>
              <button type="button" class={btn} onclick={() => { editing = false; draft = ""; saveError = ""; }}>Discard</button>
              <span class="text-[11px] text-black-700 dark:text-black-600">kinds: connector · bash · check — saving bumps the rev.</span>
            </div>
          </div>
        </details>
      {/if}
    </section>

    <section class="space-y-2">
      <div class="flex items-center justify-between gap-2">
        <h3 class="text-xs font-semibold uppercase tracking-wide text-black-700 dark:text-black-600">Run history</h3>
        <div class="flex items-center gap-3">
          <label class="flex items-center gap-1 text-[11px] text-black-800 dark:text-white-200">
            <input type="checkbox" bind:checked={onlyErrors} data-testid="filter-errors" /> errors only
          </label>
          <button type="button" class="text-[11px] font-medium text-black-700 dark:text-black-600 hover:underline" onclick={loadRuns}>Refresh</button>
        </div>
      </div>
      {#if runsError}
        <p class="rounded-lg bg-neg-100 dark:bg-neg-400/10 px-2 py-1 text-[11px] text-neg-400">{runsError}</p>
      {:else if runsLoading}
        <p class="animate-pulse text-[11px] text-black-700 dark:text-black-600" data-testid="runs-loading">Loading runs…</p>
      {:else if runs.length === 0}
        <p class="rounded-lg border border-dashed border-white-400 dark:border-navy-600 px-3 py-4 text-center text-[11px] text-black-700 dark:text-black-600" data-testid="no-runs">
          {onlyErrors ? "No failed runs." : "No runs yet — the first check shows up here. Use Test now to try the steps."}
        </p>
      {:else}
        <div class="overflow-x-auto">
          <table class="w-full text-left text-[11px]" data-testid="watch-runs">
            <thead class="text-black-700 dark:text-black-600">
              <tr>
                <th class="py-1 pr-3 font-medium">Run</th>
                <th class="py-1 pr-3 font-medium">Time</th>
                <th class="py-1 pr-3 font-medium">Result</th>
                <th class="py-1 pr-3 font-medium">Stopped at</th>
                <th class="py-1 pr-3 font-medium">Reason</th>
                <th class="py-1 pr-3 font-medium">Rev</th>
                <th class="py-1 font-medium">Duration</th>
              </tr>
            </thead>
            <tbody class="divide-y divide-white-300 dark:divide-navy-600">
              {#each runs as r (r.id)}
                <tr class="cursor-pointer hover:bg-white-200 dark:hover:bg-navy-600" onclick={() => toggleRun(r.id)} data-testid="run-row">
                  <td class="py-1.5 pr-3 tabular-nums text-black-800 dark:text-white-200">#{r.run}{r.dry_run ? " (test)" : r.manual ? " (manual)" : ""}</td>
                  <td class="py-1.5 pr-3 whitespace-nowrap text-black-800 dark:text-white-200" title={fmtTime(r.started_at)}>{ago(r.started_at)}</td>
                  <td class="py-1.5 pr-3">
                    <span class={"rounded-full px-1.5 py-px text-[10px] font-medium " + resultCls(r.result)}>{r.result}{r.outcome === "fail" ? " · fail" : ""}</span>
                  </td>
                  <td class="py-1.5 pr-3 text-black-800 dark:text-white-200" data-testid="stopped-at">{stopLabel(r.stopped_at)}</td>
                  <td class="py-1.5 pr-3 max-w-xs truncate text-black-800 dark:text-white-200" title={r.reason}>{r.reason ?? r.error ?? ""}</td>
                  <td class="py-1.5 pr-3 tabular-nums text-black-800 dark:text-white-200">{r.steps_rev ?? ""}</td>
                  <td class="py-1.5 whitespace-nowrap tabular-nums text-black-800 dark:text-white-200">{fmtDur(r.duration_ms)}</td>
                </tr>
                {#if openRunID === r.id}
                  <tr>
                    <td colspan="7" class="py-2">
                      {#if !openRun}
                        <p class="text-black-700 dark:text-black-600">Loading…</p>
                      {:else}
                        {#if openRun.error}<p class="mb-1 text-neg-400">{openRun.error}</p>{/if}
                        {@render runSteps(openRun)}
                      {/if}
                    </td>
                  </tr>
                {/if}
              {/each}
            </tbody>
          </table>
        </div>
      {/if}
    </section>
  </div>
</Modal>
