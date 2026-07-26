<script lang="ts">
  import { apiGetWickInteractions, apiGetWickInteractionCurl, apiGetWickLiveCurl, apiCancelWickModelCall, type WickInteraction, type WickCurlForms } from "$lib/api";
  import { toastError, toastOk } from "@wick-fe/common-stores";
  import CurlBuilderModal from "./CurlBuilderModal.svelte";

  // Session log for the built-in wick provider: every model call's
  // request → response, so an operator can see WHY the model answered a
  // given way (replaces the CLI Reproduce/argv box, which is meaningless
  // for the in-process provider).
  // live = a spawn for this session is still running, so a model call may be
  // in flight right now. When live we auto-refresh and show an in-progress
  // row, so a long call (e.g. the 10m shell window) reads as "working" here
  // instead of the list looking frozen with only finished calls.
  let { base, session, live = false }: { base: string; session: string; live?: boolean } = $props();

  let interactions = $state<WickInteraction[]>([]);
  let total = $state(0);
  let loading = $state(true);
  let err = $state("");
  // Live phase, straight from the server: is a model call in flight right now,
  // and for how long. Authoritative — no guessing from the log.
  let modelInFlight = $state(false);
  let modelElapsedMs = $state(0);
  let modelAttempt = $state(1);
  let modelRetryReason = $state("");
  let expanded = $state<Set<number>>(new Set());
  let curlBusy = $state<number | null>(null);

  // ── search / sort / pagination — all server-side so a big session's log
  // isn't shipped whole to the browser. Changing any of these reloads. ──
  let query = $state("");
  let sortDir = $state<"newest" | "oldest">("newest");
  let pageSize = $state(10);
  let page = $state(1);

  const totalPages = $derived(Math.max(1, Math.ceil(total / pageSize)));
  const pageRows = $derived(interactions); // server already sliced this page

  // The newest record on page 1 (respecting sort), used to name the tool that's
  // running when the server says NO model call is in flight.
  const newestInteraction = $derived(
    page !== 1 || interactions.length === 0
      ? null
      : sortDir === "newest"
        ? interactions[0]
        : interactions[interactions.length - 1],
  );

  // Human "0m 12s" from ms.
  function fmtDur(ms: number): string {
    const s = Math.floor(ms / 1000);
    return s < 60 ? `${s}s` : `${Math.floor(s / 60)}m ${s % 60}s`;
  }

  // Live label — accurate now, not guessed. The server tells us whether a model
  // call is in flight (HTTP out, no record yet); if so it's a MODEL call, with a
  // running timer so a stuck call (elapsed keeps climbing) is obvious. Otherwise
  // a turn is in progress but no model call is out → a TOOL is running (the
  // newest record's tool_calls name it). The timer is what surfaces "stuck".
  const liveLabel = $derived.by(() => {
    if (modelInFlight) {
      if (modelAttempt > 1) {
        const why = modelRetryReason ? ` — ${modelRetryReason}` : "";
        return `retrying model call (attempt ${modelAttempt}${why})… (${fmtDur(liveElapsedMs)})`;
      }
      return `model call in progress… (${fmtDur(liveElapsedMs)})`;
    }
    const tc = newestInteraction?.tool_calls;
    if (tc && tc.length) return `running tool: ${tc.join(", ")}…`;
    return "working…";
  });

  // Local clock so the timer advances smoothly between 3s polls: seed from the
  // server's elapsed on each load, then tick locally.
  let liveElapsedMs = $state(0);
  $effect(() => {
    if (!live || !modelInFlight) return;
    liveElapsedMs = modelElapsedMs;
    const t = setInterval(() => (liveElapsedMs += 1000), 1000);
    return () => clearInterval(t);
  });

  async function load(showSpinner = true) {
    if (showSpinner) loading = true;
    err = "";
    try {
      const res = await apiGetWickInteractions(base, session, {
        q: query.trim() || undefined,
        sort: sortDir,
        page,
        pageSize,
      });
      interactions = res.interactions;
      total = res.total;
      modelInFlight = res.model_call_in_flight ?? false;
      modelElapsedMs = res.model_call_elapsed_ms ?? 0;
      modelAttempt = res.model_call?.attempt ?? 1;
      modelRetryReason = res.model_call?.retry_reason ?? "";
      // Server clamps page to range; mirror it so the footer stays honest.
      if (res.page) page = res.page;
    } catch (e) {
      err = e instanceof Error ? e.message : "Failed to load interactions";
    } finally {
      loading = false;
    }
  }

  // Search runs on submit (Enter or the Search button), NOT per keystroke —
  // typing shouldn't fire a request for every character. `applied` tracks
  // the query the current results reflect, so the Search button can show
  // whether there's an unapplied change.
  let applied = $state("");
  function runSearch() {
    if (query.trim() === applied) return;
    applied = query.trim();
    page = 1;
    void load();
  }
  function onSearchKey(e: KeyboardEvent) {
    if (e.key === "Enter") {
      e.preventDefault();
      runSearch();
    }
  }
  function setSort(dir: "newest" | "oldest") {
    if (sortDir === dir) return;
    sortDir = dir;
    page = 1;
    void load();
  }
  function setPageSize(n: number) {
    if (pageSize === n) return;
    pageSize = n;
    page = 1;
    void load();
  }
  function goPage(p: number) {
    if (p < 1 || p > totalPages || p === page) return;
    page = p;
    void load();
  }

  // Open the curl builder for one call: fetch every render form, then let
  // the modal pick format / token / edits before copying. Replaces the old
  // one-shot copy that produced a multi-line command Postman truncated.
  let curlForms = $state<WickCurlForms | null>(null);
  async function openCurl(seq: number) {
    curlBusy = seq;
    try {
      curlForms = await apiGetWickInteractionCurl(base, session, seq);
    } catch (e) {
      toastError(e instanceof Error ? e.message : "Could not build the request for this call");
    } finally {
      curlBusy = null;
    }
  }

  // View/copy the request being sent RIGHT NOW (the in-flight call), before any
  // record lands — for debugging a stuck "model call in progress".
  let liveCurlBusy = $state(false);
  async function openLiveCurl() {
    liveCurlBusy = true;
    try {
      curlForms = await apiGetWickLiveCurl(base, session);
    } catch (e) {
      toastError(e instanceof Error ? e.message : "No in-flight request to show");
    } finally {
      liveCurlBusy = false;
    }
  }

  // Cancel just the in-flight model call (the turn keeps going).
  let cancelBusy = $state(false);
  async function cancelModelCall() {
    cancelBusy = true;
    try {
      await apiCancelWickModelCall(base, session);
      toastOk("Model call cancelled");
      await load(false);
    } catch (e) {
      toastError(e instanceof Error ? e.message : "Could not cancel the model call");
    } finally {
      cancelBusy = false;
    }
  }

  // Keyed by seq (stable across filter/sort/page) so an expanded row stays
  // expanded when the list reorders — an index key would jump.
  function toggle(seq: number) {
    const next = new Set(expanded);
    if (next.has(seq)) next.delete(seq);
    else next.add(seq);
    expanded = next;
  }

  $effect(() => {
    if (session) void load();
  });

  // While a spawn is live, poll for new interactions (background refresh, no
  // spinner) so a call that finishes mid-view appears without a manual
  // Refresh. Stops as soon as the session is no longer live.
  $effect(() => {
    if (!session || !live) return;
    const t = setInterval(() => void load(false), 3000);
    return () => clearInterval(t);
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
      onclick={() => load()}
      disabled={loading}
      class="inline-flex items-center gap-1.5 rounded-lg border border-white-400 dark:border-navy-600 px-3 py-1 text-xs font-medium text-black-800 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800 disabled:opacity-60"
    >
      {#if loading}
        <svg viewBox="0 0 16 16" class="h-3 w-3 animate-spin" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><path d="M8 2a6 6 0 1 1-6 6" stroke-linecap="round"></path></svg>
        Loading
      {:else}
        Refresh
      {/if}
    </button>
  </div>

  <!-- Toolbar: search + sort + page size. Stays visible during search
       reloads (a loading spinner shows in the search field instead). -->
  {#if total > 0 || query || err}
    <div class="flex flex-wrap items-center gap-2 border-b border-white-300 dark:border-navy-600 px-5 py-2">
      <div class="relative min-w-0 flex-1">
        {#if loading}
          <svg viewBox="0 0 16 16" class="pointer-events-none absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 animate-spin text-green-500" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><path d="M8 2a6 6 0 1 1-6 6" stroke-linecap="round"></path></svg>
        {:else}
          <svg viewBox="0 0 16 16" class="pointer-events-none absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-black-600 dark:text-black-700" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
            <circle cx="7" cy="7" r="4.5"></circle><path d="M11 11l3 3" stroke-linecap="round"></path>
          </svg>
        {/if}
        <input
          bind:value={query}
          onkeydown={onSearchKey}
          placeholder="Search tool calls, response, model… (Enter)"
          class="w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 py-1 pl-7 pr-7 text-xs text-black-900 dark:text-white-100 placeholder-black-600 dark:placeholder-black-700 focus:border-green-500 focus:outline-none"
        />
        {#if query}
          <button
            onclick={() => { query = ""; runSearch(); }}
            aria-label="Clear search"
            class="absolute right-1.5 top-1/2 -translate-y-1/2 rounded p-0.5 text-black-600 hover:text-black-800 dark:hover:text-black-400"
          >
            <svg viewBox="0 0 16 16" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M4 4l8 8M12 4l-8 8" stroke-linecap="round"></path></svg>
          </button>
        {/if}
      </div>
      <button
        onclick={runSearch}
        disabled={loading || query.trim() === applied}
        class="rounded-lg bg-green-500 px-3 py-1 text-xs font-medium text-white-100 hover:bg-green-600 disabled:opacity-40"
      >Search</button>
      <div class="inline-flex overflow-hidden rounded-lg border border-white-400 dark:border-navy-600">
        <button onclick={() => setSort("newest")} class={`px-2.5 py-1 text-xs font-medium ${sortDir === "newest" ? "bg-green-500 text-white-100" : "text-black-800 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800"}`}>Newest</button>
        <button onclick={() => setSort("oldest")} class={`px-2.5 py-1 text-xs font-medium ${sortDir === "oldest" ? "bg-green-500 text-white-100" : "text-black-800 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800"}`}>Oldest</button>
      </div>
      <div class="inline-flex items-center gap-1">
        <span class="text-[11px] text-black-700 dark:text-black-600">Per page</span>
        <div class="inline-flex overflow-hidden rounded-lg border border-white-400 dark:border-navy-600">
          {#each [5, 10] as n (n)}
            <button onclick={() => setPageSize(n)} class={`px-2.5 py-1 text-xs font-medium ${pageSize === n ? "bg-green-500 text-white-100" : "text-black-800 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800"}`}>{n}</button>
          {/each}
        </div>
      </div>
    </div>
  {/if}

  {#if loading && interactions.length === 0}
    <!-- First load only — a search/page reload keeps the list visible with
         the small spinner in the search field. -->
    <div class="flex items-center gap-2 px-5 py-4 text-xs text-black-700 dark:text-black-600">
      <svg viewBox="0 0 16 16" class="h-3.5 w-3.5 animate-spin" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><path d="M8 2a6 6 0 1 1-6 6" stroke-linecap="round"></path></svg>
      Loading…
    </div>
  {:else if err}
    <div class="px-5 py-4 text-xs text-error-400">{err}</div>
  {:else if total === 0 && !applied && !live}
    <div class="px-5 py-4 text-xs text-black-700 dark:text-black-600">No model interactions recorded for this session yet.</div>
  {:else}
    <ul class="divide-y divide-white-300 dark:divide-navy-600">
      {#if live}
        <!-- A spawn is running: a model call may be in flight now. Records
             land only when a call FINISHES, so show a synthetic in-progress
             row at the top so the list doesn't look frozen mid-call. -->
        <li class="flex items-center gap-3 px-5 py-2.5 bg-white-200/50 dark:bg-navy-800/50">
          <span class="w-8 shrink-0"></span>
          <span class="flex items-center gap-1.5 rounded px-1.5 py-0.5 text-[10px] font-medium bg-pos-100 text-pos-400">
            <svg viewBox="0 0 16 16" class="h-3 w-3 animate-spin" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
              <path d="M8 2a6 6 0 1 1-6 6" stroke-linecap="round"></path>
            </svg>
            running
          </span>
          <span class="min-w-0 flex-1 truncate text-xs text-black-700 dark:text-black-600 italic">
            {liveLabel}
          </span>
          {#if modelInFlight}
            <button
              type="button"
              onclick={openLiveCurl}
              disabled={liveCurlBusy}
              class="shrink-0 rounded px-1.5 py-0.5 text-[10px] font-medium text-black-700 dark:text-black-600 hover:bg-white-300 dark:hover:bg-navy-600 disabled:opacity-40"
              title="View / copy the request being sent right now"
            >View request</button>
            <button
              type="button"
              onclick={cancelModelCall}
              disabled={cancelBusy}
              class="shrink-0 rounded px-1.5 py-0.5 text-[10px] font-medium text-error-400 hover:bg-error-100 disabled:opacity-40"
              title="Cancel just this model call (the turn keeps going)"
            >Cancel call</button>
          {/if}
        </li>
      {/if}
      {#if total === 0 && applied}
        <li class="px-5 py-4 text-xs text-black-700 dark:text-black-600">No interactions match “{applied}”.</li>
      {/if}
      {#each pageRows as it (it.seq)}
        <li>
          <button
            onclick={() => toggle(it.seq)}
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

          {#if expanded.has(it.seq)}
            <div class="border-t border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800 px-5 py-3 space-y-3 text-xs">
              <div class="flex flex-wrap items-center gap-x-4 gap-y-1 text-[11px] text-black-800 dark:text-black-600">
                <span>model: <span class="font-mono text-black-900 dark:text-white-100">{it.model}</span></span>
                {#if it.tools && it.tools.length}
                  <span>tools: <span class="font-mono">{it.tools.join(", ")}</span></span>
                {/if}
                <button
                  onclick={() => openCurl(it.seq)}
                  disabled={curlBusy === it.seq}
                  class="ml-auto rounded-lg border border-white-400 dark:border-navy-600 px-2 py-0.5 text-[11px] font-medium text-black-800 dark:text-black-600 hover:bg-white-100 dark:hover:bg-navy-700 disabled:opacity-50"
                >{curlBusy === it.seq ? "Building…" : "Copy request"}</button>
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

    {#if totalPages > 1}
      <div class="flex items-center justify-between gap-3 border-t border-white-300 dark:border-navy-600 px-5 py-2">
        <span class="text-[11px] text-black-700 dark:text-black-600">
          {total} call{total === 1 ? "" : "s"} · page {page} of {totalPages}
        </span>
        <div class="inline-flex items-center gap-1">
          <button
            onclick={() => goPage(page - 1)}
            disabled={page <= 1 || loading}
            class="rounded-lg border border-white-400 dark:border-navy-600 px-2 py-1 text-xs font-medium text-black-800 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800 disabled:opacity-40"
          >Prev</button>
          <button
            onclick={() => goPage(page + 1)}
            disabled={page >= totalPages || loading}
            class="rounded-lg border border-white-400 dark:border-navy-600 px-2 py-1 text-xs font-medium text-black-800 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800 disabled:opacity-40"
          >Next</button>
        </div>
      </div>
    {/if}
  {/if}
</div>

<CurlBuilderModal {base} forms={curlForms} onClose={() => (curlForms = null)} />
