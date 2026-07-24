<script lang="ts">
  import { apiGetWickInteractions, type WickInteraction } from "$lib/api";

  // Session log for the built-in wick provider: every model call's
  // request → response, so an operator can see WHY the model answered a
  // given way (replaces the CLI Reproduce/argv box, which is meaningless
  // for the in-process provider).
  let { base, session }: { base: string; session: string } = $props();

  let interactions = $state<WickInteraction[]>([]);
  let loading = $state(true);
  let err = $state("");
  let expanded = $state<Set<number>>(new Set());

  async function load() {
    loading = true;
    err = "";
    try {
      interactions = await apiGetWickInteractions(base, session);
    } catch (e) {
      err = e instanceof Error ? e.message : "Failed to load interactions";
    } finally {
      loading = false;
    }
  }

  function toggle(index: number) {
    const next = new Set(expanded);
    if (next.has(index)) next.delete(index);
    else next.add(index);
    expanded = next;
  }

  $effect(() => {
    if (session) void load();
  });

  function roleLabel(r: string): string {
    if (r === "model") return "assistant";
    return r || "user";
  }
</script>

<div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 overflow-hidden">
  <div class="flex items-center justify-between gap-3 px-5 py-3 border-b border-white-300 dark:border-navy-600">
    <div>
      <h3 class="text-sm font-semibold text-black-900 dark:text-white-100">Model interactions</h3>
      <p class="text-[11px] text-black-700 dark:text-black-600">
        Every call this session made to the model — the exact request and response, so you can see why it answered as it did.
      </p>
    </div>
    <button
      onclick={load}
      class="rounded-lg border border-white-400 dark:border-navy-600 px-3 py-1 text-xs font-medium text-black-800 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800"
    >Refresh</button>
  </div>

  {#if loading}
    <div class="px-5 py-4 text-xs text-black-700 dark:text-black-600">Loading…</div>
  {:else if err}
    <div class="px-5 py-4 text-xs text-error-400">{err}</div>
  {:else if interactions.length === 0}
    <div class="px-5 py-4 text-xs text-black-700 dark:text-black-600">No model interactions recorded for this session yet.</div>
  {:else}
    <ul class="divide-y divide-white-300 dark:divide-navy-600">
      {#each interactions as it, i (i)}
        <li>
          <button
            onclick={() => toggle(i)}
            class="flex w-full items-center gap-3 px-5 py-2.5 text-left hover:bg-white-200 dark:hover:bg-navy-800"
          >
            <span class="font-mono text-[11px] text-black-700 dark:text-black-600 w-8 shrink-0">#{it.seq}</span>
            <span class="rounded px-1.5 py-0.5 text-[10px] font-medium {it.error ? 'bg-error-100 text-error-800' : it.kind === 'compaction' ? 'bg-cau-100 text-cau-400' : 'bg-pos-100 text-pos-400'}">
              {it.error ? "error" : it.kind}
            </span>
            <span class="min-w-0 flex-1 truncate text-xs text-black-900 dark:text-white-100">
              {#if it.error}{it.error}{:else if it.tool_calls && it.tool_calls.length}→ {it.tool_calls.join(", ")}{:else}{it.response || "(no text)"}{/if}
            </span>
            <span class="shrink-0 font-mono text-[10px] text-black-700 dark:text-black-600">
              {it.latency_ms}ms
              {#if it.prompt_tokens || it.output_tokens}· {it.prompt_tokens ?? 0}→{it.output_tokens ?? 0}tok{/if}
              {#if it.cached_tokens}· {it.cached_tokens} cached{/if}
            </span>
          </button>

          {#if expanded.has(i)}
            <div class="border-t border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800 px-5 py-3 space-y-3 text-xs">
              <div class="flex flex-wrap gap-x-4 gap-y-1 text-[11px] text-black-800 dark:text-black-600">
                <span>model: <span class="font-mono text-black-900 dark:text-white-100">{it.model}</span></span>
                {#if it.tools && it.tools.length}
                  <span>tools: <span class="font-mono">{it.tools.join(", ")}</span></span>
                {/if}
              </div>

              {#if it.system}
                <div>
                  <div class="mb-1 font-medium text-black-800 dark:text-black-600">System prompt</div>
                  <pre class="max-h-40 overflow-auto whitespace-pre-wrap rounded bg-white-100 dark:bg-navy-700 p-2 font-mono text-[11px] text-black-900 dark:text-white-100">{it.system}</pre>
                </div>
              {/if}

              <div>
                <div class="mb-1 font-medium text-black-800 dark:text-black-600">Request ({it.request?.length ?? 0} messages)</div>
                <div class="max-h-56 overflow-auto rounded bg-white-100 dark:bg-navy-700 p-2 space-y-1.5">
                  {#each it.request ?? [] as m}
                    <div class="font-mono text-[11px]">
                      <span class="text-green-600 dark:text-green-400">{roleLabel(m.role)}:</span>
                      {#if m.tool_call}<span class="text-cau-400"> [tool_call {m.tool_call}]</span>{/if}
                      {#if m.tool_resp}<span class="text-prog-400"> [tool_result]</span> <span class="text-black-900 dark:text-white-100 whitespace-pre-wrap">{m.tool_resp}</span>{/if}
                      {#if m.text}<span class="text-black-900 dark:text-white-100 whitespace-pre-wrap"> {m.text}</span>{/if}
                    </div>
                  {/each}
                </div>
              </div>

              {#if it.response}
                <div>
                  <div class="mb-1 font-medium text-black-800 dark:text-black-600">Response</div>
                  <pre class="max-h-56 overflow-auto whitespace-pre-wrap rounded bg-white-100 dark:bg-navy-700 p-2 font-mono text-[11px] text-black-900 dark:text-white-100">{it.response}</pre>
                </div>
              {/if}

              {#if it.tool_calls && it.tool_calls.length}
                <div class="text-[11px] text-black-800 dark:text-black-600">
                  Tool calls: <span class="font-mono text-black-900 dark:text-white-100">{it.tool_calls.join(", ")}</span>
                </div>
              {/if}

              {#if it.error}
                <div class="rounded bg-error-100 p-2 font-mono text-[11px] text-error-800">{it.error}</div>
              {/if}
            </div>
          {/if}
        </li>
      {/each}
    </ul>
  {/if}
</div>
