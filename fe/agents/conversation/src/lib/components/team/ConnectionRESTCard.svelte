<script lang="ts">
  /* Connections › REST: the agent on the OpenAI-compatible endpoint, as
     model "agent:<handle>". The toggle autosaves; callers authenticate with
     their own Personal Access Token, so the card never holds a token —
     snippets use the $WICK_TOKEN placeholder. */
  import { Button, Toggle } from "@wick-fe/common-ui";
  import { runApi, type AgentItem } from "../../api/team.js";
  import { connState, type ConnState } from "../../connectionTabs.js";
  import {
    getAgentREST, updateAgentREST, testAgentREST, restCurlExample, restSDKExample,
    type AgentRESTStatus, type RESTTestResult,
  } from "../../restConnection.js";

  /* onStatus feeds the drawer's tab mark (✓ / ⚠). */
  type Props = { base: string; agent: AgentItem; onStatus?: (s: ConnState) => void };
  let { base, agent, onStatus }: Props = $props();

  let status = $state<AgentRESTStatus | null>(null);
  let error = $state("");
  let saveState = $state<"" | "saving" | "saved" | "error">("");
  let busy = $state(false);
  let result = $state<RESTTestResult | null>(null);
  let copied = $state("");

  const muted = "text-xs text-black-800 dark:text-black-600";
  const field =
    "min-w-0 flex-1 truncate rounded-lg border border-white-300 bg-white-100 px-3 py-1.5 font-mono text-xs text-black-900 dark:border-navy-600 dark:bg-navy-800 dark:text-white-100";
  const pre =
    "mt-2 overflow-x-auto rounded-lg bg-white-200 p-3 font-mono text-xs text-black-900 dark:bg-navy-800 dark:text-white-100";

  async function load() {
    try {
      status = await runApi(getAgentREST(base, agent.id));
      error = "";
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    }
  }

  async function save(enabled: boolean) {
    if (!status) return;
    const prev = status;
    status = { ...status, enabled };
    saveState = "saving";
    result = null;
    try {
      status = await runApi(updateAgentREST(base, agent.id, enabled));
      saveState = "saved";
    } catch {
      status = prev;
      saveState = "error";
    }
  }

  async function runTest() {
    busy = true;
    result = null;
    try {
      result = await runApi(testAgentREST(base, agent.id));
    } catch (e) {
      result = { ok: false, model: status?.model ?? "", detail: e instanceof Error ? e.message : String(e) };
    } finally {
      busy = false;
    }
  }

  async function copy(what: string, text: string) {
    try {
      await navigator.clipboard?.writeText(text);
    } catch {
      return;
    }
    copied = what;
    setTimeout(() => (copied = ""), 1500);
  }

  $effect(() => {
    void agent.id;
    result = null;
    void load();
  });

  $effect(() => onStatus?.(connState(!!status?.enabled, true, !!error)));
</script>

<section class="rounded-xl border border-white-300 p-4 dark:border-navy-600" data-testid="rest-card">
  <div class="flex items-center gap-3">
    <span class="text-sm font-semibold text-black-900 dark:text-white-100">REST (OpenAI-compatible)</span>
    <span class="rounded-full px-2 py-0.5 text-xs {status?.enabled ? 'bg-green-50 text-green-700' : 'bg-white-200 text-black-800 dark:bg-navy-600 dark:text-black-600'}">
      {status?.enabled ? "Enabled" : "Off"}
    </span>
    {#if saveState}<span class="ml-auto {muted}" aria-live="polite">{saveState === "saving" ? "Saving…" : saveState === "saved" ? "Saved" : "Couldn't save"}</span>{/if}
  </div>
  <p class="mt-1 {muted}">Let scripts, n8n and any OpenAI SDK call @{agent.handle}. Each call runs with this agent's persona and access; approvals are blocked automatically.</p>
  {#if error}<p class="mt-2 text-xs text-neg-400" data-testid="rest-error">{error}</p>{/if}

  {#if status}
    <div class="mt-4 space-y-4">
      <div class="flex items-start gap-3">
        <Toggle checked={status.enabled} onChange={(v: boolean) => save(v)} label="Enable REST" describedBy="rest-enable-hint" />
        <span class="min-w-0">
          <span class="block text-sm text-black-900 dark:text-white-100">Enable REST</span>
          <span id="rest-enable-hint" class="block {muted}">Off: calls with this model are refused with 403.</span>
        </span>
      </div>

      <div class="space-y-2">
        {#each [["base", "Base URL", status.base_url], ["model", "Model", status.model]] as [k, lbl, val] (k)}
          <div>
            <span class="mb-1 block {muted}">{lbl}</span>
            <div class="flex items-center gap-2">
              <code class={field} data-testid="rest-{k}">{val}</code>
              <Button size="sm" variant="ghost" onclick={() => copy(k, val)}>{copied === k ? "Copied" : "Copy"}</Button>
            </div>
          </div>
        {/each}
      </div>

      <p class={muted}>
        Authenticate with your own Personal Access Token as <span class="font-mono">Authorization: Bearer …</span>.
        <a href={status.tokens_url} target="_blank" rel="noopener" class="font-medium underline hover:no-underline" data-testid="rest-tokens-link">Create a token</a>
      </p>

      <div class="flex flex-wrap items-center gap-2">
        <Button size="sm" variant="ghost" disabled={busy} onclick={runTest}>Test</Button>
        {#if result}<span class="text-xs {result.ok ? 'text-green-700' : 'text-neg-400'}" data-testid="rest-test">{result.ok ? "OK" : "Not reachable"} — {result.detail}</span>{/if}
      </div>

      <details>
        <summary class="cursor-pointer {muted}">Example: curl</summary>
        <p class="mt-2 {muted}">Set <span class="font-mono">WICK_TOKEN</span> to your Personal Access Token first.</p>
        <pre class={pre} data-testid="rest-curl">{restCurlExample(status.base_url, status.model)}</pre>
      </details>
      <details>
        <summary class="cursor-pointer {muted}">Example: OpenAI SDK (Python)</summary>
        <pre class={pre} data-testid="rest-sdk">{restSDKExample(status.base_url, status.model)}</pre>
      </details>
    </div>
  {/if}
</section>
