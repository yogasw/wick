<script lang="ts">
  /* Paste entry of the custom-connector builder: two parser tabs (cURL /
     AI), one paste box, a Parse button, and an error box. Parse posts the
     box to the parse endpoint; on success the returned draft is stashed in
     sessionStorage and the SPA navigates to the review route. Mirrors
     custom_paste.templ + custom_paste.js. */
  import { Button, TextArea, Select } from "@wick-fe/common-ui";
  import { push } from "$lib/router.js";
  import { getCustomMeta, parseCustomPaste } from "$lib/api.js";
  import { DRAFT_STORAGE_KEY } from "./storage.js";

  let parser = $state<"curl" | "ai">("curl");
  let provider = $state("");
  let paste = $state("");
  let aiProviders = $state<string[]>([]);
  let error = $state("");
  let busy = $state(false);

  async function loadMeta() {
    try {
      const meta = await getCustomMeta();
      aiProviders = meta.ai_providers;
      if (aiProviders.length > 0) provider = aiProviders[0];
    } catch {
      /* meta failure leaves the AI tab hidden — cURL still works */
    }
  }

  function selectTab(name: "curl" | "ai") {
    parser = name;
    error = "";
  }

  async function parse() {
    const value = paste.trim();
    if (!value) {
      error = "Paste something first.";
      return;
    }
    error = "";
    busy = true;
    try {
      const draft = await parseCustomPaste(parser, parser === "ai" ? provider : "", value);
      sessionStorage.setItem(DRAFT_STORAGE_KEY, JSON.stringify(draft));
      push("/custom/review");
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      busy = false;
    }
  }

  function tabClass(active: boolean): string {
    return active
      ? "bg-white-100 dark:bg-navy-700 text-green-600 shadow-sm rounded-lg px-3 py-1.5 text-sm font-medium"
      : "rounded-lg px-3 py-1.5 text-sm font-medium text-black-800 dark:text-black-600 transition-colors";
  }

  $effect(() => { loadMeta(); });
</script>

<div class="space-y-6">
  <div>
    <h1 class="text-lg font-semibold text-black-900 dark:text-white-100">New connector from paste</h1>
    <p class="mt-1 text-sm text-black-800 dark:text-black-600">Paste an HTTP call and wick extracts the connector definition. Both parsers feed the same review step.</p>
  </div>

  <section class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-6">
    <div class="flex w-fit items-center gap-1 rounded-lg border border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800 p-1">
      <button type="button" class={tabClass(parser === "curl")} onclick={() => selectTab("curl")}>📋 cURL parser</button>
      {#if aiProviders.length > 0}
        <button type="button" class={tabClass(parser === "ai")} onclick={() => selectTab("ai")}>✨ AI parser</button>
      {/if}
    </div>

    {#if parser === "curl"}
      <p class="mt-3 text-sm text-black-800 dark:text-black-600">Deterministic, instant, no LLM call. Reads <code class="font-mono text-xs">-X</code>, <code class="font-mono text-xs">-H</code>, <code class="font-mono text-xs">-d</code>, <code class="font-mono text-xs">-u</code>, the URL, and the query string. Values that look like tokens are auto-flagged secret.</p>
    {:else}
      <p class="mt-3 text-sm text-black-800 dark:text-black-600">One LLM call. Accepts anything the cURL parser rejects: <code class="font-mono text-xs">fetch()</code> snippets, axios calls, raw API doc paragraphs, plain English. The raw paste is never persisted.</p>
      <div class="mt-2 flex max-w-xs items-center gap-2">
        <span class="text-xs font-medium text-black-800 dark:text-black-600">Provider</span>
        <Select value={provider} options={aiProviders} onChange={(v) => (provider = v)} size="sm" />
      </div>
    {/if}

    <label class="mt-4 block text-xs font-medium text-black-800 dark:text-black-600" for="cc-paste-box">
      {parser === "ai" ? "Paste anything — fetch(), axios, API docs, prose" : "cURL command"}
    </label>
    <div class="mt-1">
      <TextArea
        id="cc-paste-box"
        value={paste}
        onChange={(v) => (paste = v)}
        rows={10}
        placeholder="curl -X POST 'https://api.example.com/v1/items' -H 'Authorization: Bearer ...' -d 'name=demo'"
        ariaLabel="Paste box"
      />
    </div>
    <p class="mt-1 text-[11px] text-black-700 dark:text-black-600">Tip: copy from browser DevTools → Network → Copy as cURL. One endpoint per paste, up to 8 KB.</p>

    {#if error}
      <div class="mt-4 rounded-lg border border-neg-400 bg-neg-100 px-4 py-3">
        <p class="text-sm font-medium text-neg-400">✗ {error}</p>
      </div>
    {/if}

    <div class="mt-6 flex items-center justify-between">
      <Button variant="secondary" size="lg" onclick={() => push("/")}>← Cancel</Button>
      <Button variant="primary" size="lg" disabled={busy} onclick={parse}>
        {#if busy}{parser === "ai" ? "Extracting…" : "Parsing…"}{:else}Parse →{/if}
      </Button>
    </div>
  </section>
</div>
