<script lang="ts">
  import type { Snippet } from "svelte";

  type Props = {
    leftText: string;
    rightText: string;
    leftLabel: string;
    rightLabel: string;
    rightEmptyMsg?: string;
    note?: Snippet;
  };
  let { leftText, rightText, leftLabel, rightLabel, rightEmptyMsg, note }: Props = $props();

  let view = $state<"all" | "diff">("all");

  type Row = { text: string; changed: boolean };

  const model = $derived.by(() => {
    const ll = leftText.split("\n");
    const rl = rightText.split("\n");
    const lSet = new Set(ll);
    const rSet = new Set(rl);
    const left: Row[] = ll.map((line) => ({ text: line, changed: !rSet.has(line) }));
    const right: Row[] = rl.map((line) => ({ text: line, changed: !lSet.has(line) }));
    return { left, right };
  });

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

  const leftView = $derived(view === "diff" ? collapse(model.left) : model.left);
  const rightView = $derived(view === "diff" ? collapse(model.right) : model.right);
  const hasRight = $derived(rightText.length > 0);

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
    class="flex items-center gap-3 px-3 py-1.5 text-[11px] border-b border-slate-200 dark:border-navy-600"
  >
    <span class="font-semibold tracking-wide uppercase text-emerald-600 dark:text-emerald-400">
      {leftLabel}
    </span>
    {#if note}{@render note()}{/if}
    <div class="ml-auto inline-flex rounded border border-slate-300 dark:border-navy-500 overflow-hidden text-[10px] uppercase tracking-wide">
      {#each ["all", "diff"] as v}
        <button
          type="button"
          class="px-2 py-0.5 transition-colors"
          class:bg-emerald-500={view === v}
          class:text-white-100={view === v}
          class:text-black-700={view !== v}
          class:text-black-600={view !== v}
          onclick={() => (view = v as typeof view)}
        >{v === "all" ? "All" : "Diff only"}</button>
      {/each}
    </div>
    <span class="font-semibold tracking-wide uppercase text-black-700 dark:text-black-600">
      {rightLabel}
    </span>
  </header>
  <div class="flex-1 grid grid-cols-2 divide-x divide-white-300 dark:divide-navy-600 min-h-0">
    <section
      bind:this={leftEl}
      onscroll={() => onScroll("left")}
      class="overflow-auto p-2"
    >
      <pre class="font-mono text-[11px] leading-tight">{#each leftView as row}{#if "ellipsis" in row}<div class="text-black-700 dark:text-black-600 select-none">  … unchanged …</div>{:else}<div
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
      {#if !hasRight && rightEmptyMsg}
        <div class="h-full flex items-center justify-center text-black-700 dark:text-black-600 text-xs italic px-4 text-center">
          {rightEmptyMsg}
        </div>
      {:else}
        <pre class="font-mono text-[11px] leading-tight">{#each rightView as row}{#if "ellipsis" in row}<div class="text-black-700 dark:text-black-600 select-none">  … unchanged …</div>{:else}<div
              class={row.changed
                ? "whitespace-pre px-1 -mx-1 rounded-sm bg-rose-500/25 text-rose-700 dark:text-rose-300"
                : "whitespace-pre px-1 -mx-1 rounded-sm"}
            >{row.text || " "}</div>{/if}{/each}</pre>
      {/if}
    </section>
  </div>
</div>
