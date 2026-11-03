<script lang="ts">
  /* A wick_run_code call. Inline code renders as it was sent. A script_path
     call carries only the path, so the file is read from the session folder
     through ctx.statPath/loadPath — the same endpoints, and the same viewer
     access check, every other trace file goes through. The card says when
     the file changed after the run or is gone, never shows it silently. */
  import CodeBlock from "./CodeBlock.svelte";
  import CopyButton from "./CopyButton.svelte";
  import { traceFileState } from "../fileState.js";
  import type { TraceContext, TraceDisplay, TraceFileStat } from "../types.js";
  type Props = { display: TraceDisplay; ctx?: TraceContext };
  let { display, ctx }: Props = $props();

  // Same cap code mode applies to a script_path file.
  const MAX_SCRIPT_BYTES = 256 << 10;

  let stat = $state<TraceFileStat | null>(null);
  let code = $state<string | null>(null);
  let failed = $state("");

  $effect(() => {
    const p = display.path;
    stat = null;
    code = null;
    failed = "";
    if (!p) return;
    const statPath = ctx?.statPath, loadPath = ctx?.loadPath;
    if (!statPath || !loadPath) {
      failed = "The script file can't be opened from here.";
      return;
    }
    let live = true;
    statPath(p)
      .then(async (s) => {
        if (!live) return;
        stat = s;
        if (s.status !== "present" || !s.rel || (s.size ?? 0) > MAX_SCRIPT_BYTES) return;
        const text = await (await loadPath(s.rel)).text();
        if (live) code = text;
      })
      .catch((e: unknown) => { if (live) failed = e instanceof Error ? e.message : String(e); });
    return () => { live = false; };
  });

  const fs = $derived(stat ? traceFileState(stat, undefined, ctx?.calledAt) : null);
  const tooLarge = $derived(stat?.status === "present" && (stat.size ?? 0) > MAX_SCRIPT_BYTES);
</script>

<div data-trace-kind="codemode">
  {#if display.path}
    <div class="flex items-center gap-2 px-3 pt-2">
      <span data-path class="min-w-0 flex-1 break-all font-mono text-[11px] text-black-800 dark:text-black-600">{display.path}</span>
      {#if fs && fs.state !== "same"}
        <span data-file-state={fs.state} title={fs.title} class="shrink-0 rounded border px-1.5 py-0.5 text-[10px] {fs.state === 'changed' ? 'border-amber-500 text-amber-700 dark:text-amber-300' : 'border-red-400 text-red-600 dark:text-red-300'}">{fs.state === "unknown" ? "can't open" : fs.label}</span>
      {/if}
      <CopyButton text={display.path} label="Copy path" />
    </div>
    {#if fs?.state === "changed"}
      <p data-script-note class="px-3 pt-1 text-[10px] italic text-amber-700 dark:text-amber-300">Changed after this run: showing the file as it is now, not the script that ran.</p>
    {:else if fs?.state === "missing"}
      <p data-script-note class="px-3 py-1.5 text-[11px] text-black-600 dark:text-black-500">The script file was deleted; what ran can't be shown.</p>
    {:else if fs?.state === "unknown"}
      <p data-script-note class="px-3 py-1.5 text-[11px] text-black-600 dark:text-black-500">Can't open this file: no access to the session folder, or it is outside it.</p>
    {:else if tooLarge}
      <p data-script-note class="px-3 py-1.5 text-[11px] text-black-600 dark:text-black-500">The script file is larger than code mode runs (256 KB).</p>
    {/if}
    {#if failed}
      <p data-script-note class="px-3 py-1.5 text-[11px] text-red-600 dark:text-red-300">{failed}</p>
    {:else if code !== null}
      <CodeBlock {display} {code} />
    {:else if !stat}
      <p class="px-3 py-1.5 text-[11px] text-black-500 dark:text-black-600">Loading script…</p>
    {/if}
  {:else}
    <CodeBlock {display} />
  {/if}
</div>
