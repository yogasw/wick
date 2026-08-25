<script lang="ts">
  import { onMount, tick } from "svelte";
  import { Breadcrumb, type BreadcrumbItem } from "@wick-fe/common-ui";
  import { toastError } from "@wick-fe/common-stores";
  import { apiTailLog, logDownloadURL } from "$lib/api.js";
  import type { LogTail } from "$lib/types.js";

  type Props = {
    base: string;
    file: string;
    onBack: () => void;
    /** Spawn window (ISO) to highlight/scroll to, if the viewer was opened
        from a spawn's Logs block. */
    from?: string;
    to?: string;
  };
  let { base, file, onBack, from, to }: Props = $props();

  let data = $state<LogTail | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let copied = $state(false);

  let crumbs = $derived<BreadcrumbItem[]>([
    { label: "Providers", onClick: onBack },
    { label: file, truncate: true },
  ]);

  function fmtTime(iso: string): string {
    return iso ? new Date(iso).toLocaleTimeString() : "";
  }
  let windowLabel = $derived(
    from ? `${fmtTime(from)}${to ? ` → ${fmtTime(to)}` : " → running"}` : "",
  );
  let humanSize = $derived.by(() => {
    const n = data?.Size ?? 0;
    if (n < 1024) return `${n} B`;
    if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
    return `${(n / 1024 / 1024).toFixed(1)} MB`;
  });

  async function copyContent() {
    try {
      await navigator.clipboard.writeText(data?.Content ?? "");
      copied = true;
      setTimeout(() => { copied = false; }, 1400);
    } catch (e: unknown) {
      toastError(`Copy failed: ${e instanceof Error ? e.message : String(e)}`);
    }
  }

  let pre: HTMLPreElement | undefined = $state();

  onMount(() => {
    apiTailLog(base, file)
      .then(async (d) => {
        data = d;
        // The tail shows the most recent output; the spawn window is near the
        // end, so scroll to the bottom once rendered.
        await tick();
        if (pre) pre.scrollTop = pre.scrollHeight;
      })
      .catch((e: unknown) => { error = e instanceof Error ? e.message : String(e); })
      .finally(() => { loading = false; });
  });
</script>

<div class="space-y-6">
  <Breadcrumb items={crumbs} />

  {#if loading}
    <div class="px-5 py-16 text-center text-sm text-black-700 dark:text-black-600">Loading…</div>
  {:else if error}
    <div class="rounded-xl border border-error-400 bg-error-100 px-4 py-3 text-sm text-error-800">{error}</div>
  {:else if data}
    <div class="flex flex-wrap items-start justify-between gap-3">
      <div>
        <h1 class="text-lg font-semibold text-black-900 dark:text-white-100">{data.Name}</h1>
        <p class="mt-0.5 font-mono text-xs text-black-700 dark:text-black-600" title={data.Path}>{data.Path}</p>
        <p class="mt-0.5 text-xs text-black-700 dark:text-black-600">
          {humanSize} · modified {new Date(data.Modified).toLocaleString()}
          {#if data.Truncated}· <span class="text-cau-400">showing last 256 KB</span>{/if}
          {#if windowLabel}· <span class="font-medium">spawn window</span> <span class="font-mono">{windowLabel}</span>{/if}
        </p>
      </div>
      <div class="flex items-center gap-2 shrink-0">
        <button type="button" onclick={copyContent} class="rounded-lg border border-white-400 dark:border-navy-600 px-2.5 py-1 text-xs font-medium text-black-800 dark:text-white-100 hover:bg-white-200 dark:hover:bg-navy-800 transition-colors">{copied ? "Copied!" : "Copy"}</button>
        <a href={logDownloadURL(base, file)} class="rounded-lg border border-white-400 dark:border-navy-600 px-2.5 py-1 text-xs font-medium text-black-800 dark:text-white-100 hover:bg-white-200 dark:hover:bg-navy-800 transition-colors">Download</a>
      </div>
    </div>

    <pre bind:this={pre} class="max-h-[70vh] overflow-auto whitespace-pre-wrap rounded-xl border border-white-300 dark:border-navy-600 bg-navy-900 dark:bg-navy-800 px-3 py-2 font-mono text-xs text-black-800 dark:text-black-600">{data.Content || "(empty)"}</pre>
  {/if}
</div>
