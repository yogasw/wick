<script lang="ts">
  import type { RemoteRecheck } from "../api/team.js";
  import { recheckNote } from "../remoteRecheck.js";
  import { renderMarkdown } from "../markdown.js";

  /* "Check again" under a remote turn that timed out or ended without its
     marker: reads the reply again (nothing is sent to the remote) and
     shows it as it stands now. A settled reply is kept by the server in
     place of the turn — the thread reloads and shows it there. Clicking
     again re-reads. */
  type Props = {
    onRecheck: () => Promise<RemoteRecheck>;
    /** What the turn already shows — a re-read equal to it is "nothing new". */
    shown?: string;
  };
  let { onRecheck, shown = "" }: Props = $props();

  let loading = $state(false);
  let result = $state<RemoteRecheck | null>(null);
  let error = $state("");
  const fresh = $derived(!!result && !result.replaced && !!result.text.trim() && result.text.trim() !== shown.trim());

  async function recheck() {
    if (loading) return;
    loading = true;
    error = "";
    try {
      result = await onRecheck();
    } catch (e) {
      error = e instanceof Error ? e.message : String((e as { message?: string })?.message ?? e);
    } finally {
      loading = false;
    }
  }
</script>

<div class="mt-1.5 flex flex-col gap-1.5 max-w-full" data-testid="remote-recheck">
  <div class="flex items-center gap-2">
    <button
      type="button"
      class="inline-flex items-center gap-1.5 rounded-full border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-2.5 py-0.5 text-xs text-black-800 dark:text-white-200 hover:bg-white-200 dark:hover:bg-navy-700 disabled:opacity-60"
      disabled={loading}
      onclick={recheck}
    >
      <svg viewBox="0 0 16 16" class={`h-3 w-3 shrink-0 ${loading ? "animate-spin" : ""}`} fill="none" stroke="currentColor" stroke-width="1.5">
        <path d="M13.5 8a5.5 5.5 0 1 1-1.6-3.9" stroke-linecap="round"></path>
        <path d="M13.5 2.5v2.5H11" stroke-linecap="round" stroke-linejoin="round"></path>
      </svg>
      {loading ? "Checking…" : "Check again"}
    </button>
    {#if result && !loading}
      <span class="text-xs text-black-600 dark:text-black-700" data-testid="remote-recheck-note">{recheckNote(result, shown)}</span>
    {:else if error && !loading}
      <span class="text-xs text-neg-400" data-testid="remote-recheck-error">{error}</span>
    {/if}
  </div>
  {#if result && fresh && !loading}
    <div class="rounded-2xl rounded-tl-sm border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-4 py-3 shadow-sm text-sm text-black-900 dark:text-white-100 wick-prose" data-testid="remote-recheck-reply">
      {@html renderMarkdown(result.text)}
    </div>
  {/if}
</div>
