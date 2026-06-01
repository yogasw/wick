<script lang="ts">
  // JSON preview — live draft (what the canvas is showing right now)
  // side-by-side with the last published copy. Reactive: any edit on
  // the canvas refreshes the left pane immediately; the right stays
  // pinned to the published version until the user hits Publish.
  //
  // View modes:
  //   All        — full JSON both sides, changed lines highlighted.
  //   Diff only  — collapse runs of unchanged lines into a "…" marker
  //                so the eye lands on edits without scrolling.
  import { draftWorkflow, publishedWorkflow, dirty } from "$lib/stores/editor";

  let view = $state<"all" | "diff">("all");

  // Strip editor-only `_canvas` metadata before printing — node
  // positions are persistence noise the YAML on disk doesn't care
  // about, and including them muddies the diff with rebalance churn.
  function clean(wf: unknown): unknown {
    if (wf === null || typeof wf !== "object") return wf;
    if (Array.isArray(wf)) return wf.map(clean);
    const out: Record<string, unknown> = {};
    for (const [k, v] of Object.entries(wf as Record<string, unknown>)) {
      if (k === "_canvas") continue;
      out[k] = clean(v);
    }
    return out;
  }

  const draftText = $derived(
    $draftWorkflow ? JSON.stringify(clean($draftWorkflow), null, 2) : "",
  );
  const publishedText = $derived(
    $publishedWorkflow ? JSON.stringify(clean($publishedWorkflow), null, 2) : "",
  );

  type Row = { text: string; changed: boolean };

  // Per-side line model + a Set of indices that differ between the
  // two sides. Cheap line-by-line set comparison — good enough for
  // workflow docs which sit in the low-hundreds-of-lines range.
  const model = $derived.by(() => {
    const dl = draftText.split("\n");
    const pl = publishedText.split("\n");
    const dSet = new Set(dl);
    const pSet = new Set(pl);
    const draft: Row[] = dl.map((line) => ({ text: line, changed: !pSet.has(line) }));
    const published: Row[] = pl.map((line) => ({ text: line, changed: !dSet.has(line) }));
    return { draft, published };
  });

  // Collapse stretches of unchanged lines into a single ellipsis row
  // when the user picks "Diff only". `pad` keeps one row of context
  // above and below each change so labels / brackets stay readable.
  function collapse(rows: Row[], pad = 1): (Row | { ellipsis: true })[] {
    const keep = rows.map((r) => r.changed);
    for (let i = 0; i < rows.length; i++) {
      if (rows[i].changed) {
        for (let j = Math.max(0, i - pad); j <= Math.min(rows.length - 1, i + pad); j++) {
          keep[j] = true;
        }
      }
    }
    const out: (Row | { ellipsis: true })[] = [];
    let elided = false;
    for (let i = 0; i < rows.length; i++) {
      if (keep[i]) {
        out.push(rows[i]);
        elided = false;
      } else if (!elided) {
        out.push({ ellipsis: true });
        elided = true;
      }
    }
    return out;
  }

  const draftView = $derived(view === "diff" ? collapse(model.draft) : model.draft);
  const publishedView = $derived(view === "diff" ? collapse(model.published) : model.published);

  const hasPublished = $derived(!!$publishedWorkflow);

  // Scroll sync — both panes follow each other vertically, git-diff
  // side-by-side style. Guard with `syncing` so the listener that
  // mirrors the scroll doesn't re-trigger the opposite listener.
  let leftEl: HTMLElement | undefined = $state();
  let rightEl: HTMLElement | undefined = $state();
  let syncing = false;
  function onScroll(from: "left" | "right") {
    if (syncing) return;
    if (from === "left" && leftEl && rightEl) {
      syncing = true;
      rightEl.scrollTop = leftEl.scrollTop;
      rightEl.scrollLeft = leftEl.scrollLeft;
      queueMicrotask(() => (syncing = false));
    } else if (from === "right" && leftEl && rightEl) {
      syncing = true;
      leftEl.scrollTop = rightEl.scrollTop;
      leftEl.scrollLeft = rightEl.scrollLeft;
      queueMicrotask(() => (syncing = false));
    }
  }
</script>

<div class="flex flex-col h-full">
  <header
    class="flex items-center gap-3 px-3 py-1.5 text-[11px] border-b border-slate-200 dark:border-slate-700"
  >
    <span class="font-semibold tracking-wide uppercase text-emerald-600 dark:text-emerald-400">
      Live (draft)
    </span>
    {#if $dirty}
      <span class="text-amber-600 dark:text-amber-400">●&nbsp;unpublished changes</span>
    {:else}
      <span class="text-slate-500 dark:text-slate-400">in sync</span>
    {/if}
    <div class="ml-auto inline-flex rounded border border-slate-300 dark:border-slate-600 overflow-hidden text-[10px] uppercase tracking-wide">
      {#each ["all", "diff"] as v}
        <button
          type="button"
          class="px-2 py-0.5 transition-colors"
          class:bg-emerald-500={view === v}
          class:text-white={view === v}
          class:text-slate-500={view !== v}
          class:dark:text-slate-400={view !== v}
          onclick={() => (view = v as typeof view)}
        >{v === "all" ? "All" : "Diff only"}</button>
      {/each}
    </div>
    <span class="font-semibold tracking-wide uppercase text-slate-500 dark:text-slate-400">
      Published
    </span>
  </header>
  <div class="flex-1 grid grid-cols-2 divide-x divide-slate-200 dark:divide-slate-800 min-h-0">
    <section
      bind:this={leftEl}
      onscroll={() => onScroll("left")}
      class="overflow-auto p-2"
    >
      <pre class="font-mono text-[11px] leading-tight">{#each draftView as row}{#if "ellipsis" in row}<div class="text-slate-400 dark:text-slate-500 select-none">  … unchanged …</div>{:else}<div
            class={row.changed
              ? "whitespace-pre px-1 -mx-1 rounded-sm bg-emerald-500/25 text-emerald-600 dark:text-emerald-300"
              : "whitespace-pre px-1 -mx-1 rounded-sm"}
          >{row.text || " "}</div>{/if}{/each}</pre>
    </section>
    <section
      bind:this={rightEl}
      onscroll={() => onScroll("right")}
      class="overflow-auto p-2"
    >
      {#if !hasPublished}
        <div class="h-full flex items-center justify-center text-slate-400 dark:text-slate-500 text-xs italic px-4 text-center">
          No published version yet. Publish the draft to populate this pane.
        </div>
      {:else}
        <pre class="font-mono text-[11px] leading-tight">{#each publishedView as row}{#if "ellipsis" in row}<div class="text-slate-400 dark:text-slate-500 select-none">  … unchanged …</div>{:else}<div
              class={row.changed
                ? "whitespace-pre px-1 -mx-1 rounded-sm bg-rose-500/25 text-rose-700 dark:text-rose-300"
                : "whitespace-pre px-1 -mx-1 rounded-sm"}
            >{row.text || " "}</div>{/if}{/each}</pre>
      {/if}
    </section>
  </div>
</div>
