<script lang="ts">
  /* Connections › A2A: the agent as an Agent-to-Agent server. Options
     autosave; the API key is shown once, right after Enable or Rotate, and
     is masked from then on — the server keeps only its hash. */
  import { Button, Toggle } from "@wick-fe/common-ui";
  import { runApi, type AgentItem } from "../../api/team.js";
  import {
    getAgentA2A, updateAgentA2A, rotateAgentA2A, revokeAgentA2A, testAgentA2A,
    maskedKey, probeLine, curlExample, type AgentA2AStatus, type AgentA2AUpdate, type A2AProbe,
  } from "../../a2aConnection.js";

  type Props = { base: string; agent: AgentItem };
  let { base, agent }: Props = $props();

  let status = $state<AgentA2AStatus | null>(null);
  let error = $state("");
  let saveState = $state<"" | "saving" | "saved" | "error">("");
  let freshKey = $state("");
  let busy = $state(false);
  let probe = $state<A2AProbe | null>(null);
  let copied = $state("");

  const muted = "text-xs text-black-800 dark:text-black-600";
  const field =
    "min-w-0 flex-1 truncate rounded-lg border border-white-300 bg-white-100 px-3 py-1.5 font-mono text-xs text-black-900 dark:border-navy-600 dark:bg-navy-800 dark:text-white-100";

  async function load() {
    try {
      status = await runApi(getAgentA2A(base, agent.id));
      error = "";
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    }
  }

  function took(next: AgentA2AStatus) {
    if (next.api_key) freshKey = next.api_key;
    status = { ...next, api_key: undefined };
  }

  async function save(patch: AgentA2AUpdate) {
    if (!status) return;
    const prev = status;
    status = { ...status, ...patch };
    saveState = "saving";
    try {
      took(await runApi(updateAgentA2A(base, agent.id, patch)));
      saveState = "saved";
    } catch {
      status = prev;
      saveState = "error";
    }
  }

  async function act(fn: typeof rotateAgentA2A) {
    busy = true;
    error = "";
    try {
      took(await runApi(fn(base, agent.id)));
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      busy = false;
    }
  }

  async function runTest() {
    busy = true;
    probe = null;
    try {
      probe = await runApi(testAgentA2A(base, agent.id));
    } catch (e) {
      probe = { ok: false, status: "error", card_ms: 0, send_ms: 0, error: e instanceof Error ? e.message : String(e) };
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
    freshKey = "";
    probe = null;
    void load();
  });
</script>

<section class="rounded-xl border border-white-300 p-4 dark:border-navy-600" data-testid="a2a-card">
  <div class="flex items-center gap-3">
    <span class="text-sm font-semibold text-black-900 dark:text-white-100">A2A</span>
    <span class="rounded-full px-2 py-0.5 text-xs {status?.enabled ? 'bg-green-50 text-green-700' : 'bg-white-200 text-black-800 dark:bg-navy-600 dark:text-black-600'}">
      {status?.enabled ? "Enabled" : "Off"}
    </span>
    {#if saveState}<span class="ml-auto {muted}" aria-live="polite">{saveState === "saving" ? "Saving…" : saveState === "saved" ? "Saved" : "Couldn't save"}</span>{/if}
  </div>
  <p class="mt-1 {muted}">Let other agents and systems call @{agent.handle} over the Agent-to-Agent protocol. Each A2A conversation is its own chat, run with this agent's access.</p>
  {#if error}<p class="mt-2 text-xs text-neg-400" data-testid="a2a-error">{error}</p>{/if}

  {#if status}
    <div class="mt-4 space-y-4">
      <div class="flex items-start gap-3">
        <Toggle checked={status.enabled} onChange={(v: boolean) => save({ enabled: v })} label="Enable A2A" describedBy="a2a-enable-hint" />
        <span class="min-w-0">
          <span class="block text-sm text-black-900 dark:text-white-100">Enable A2A</span>
          <span id="a2a-enable-hint" class="block {muted}">Off: the endpoint answers 404 and the agent card is hidden.</span>
        </span>
      </div>

      <div class="space-y-2">
        {#each [["endpoint", "Endpoint", status.endpoint_url], ["card", "Agent card", status.card_url]] as [k, lbl, url] (k)}
          <div>
            <span class="mb-1 block {muted}">{lbl}</span>
            <div class="flex items-center gap-2">
              <code class={field} data-testid="a2a-{k}-url">{url}</code>
              <Button size="sm" variant="ghost" onclick={() => copy(k, url)}>{copied === k ? "Copied" : "Copy"}</Button>
            </div>
          </div>
        {/each}
      </div>

      <div>
        <span class="mb-1 block {muted}">API key</span>
        {#if freshKey}
          <div class="rounded-lg border border-green-500 p-2" data-testid="a2a-fresh-key">
            <p class="mb-1 text-xs text-black-900 dark:text-white-100">Copy this key now — it won't be shown again.</p>
            <div class="flex items-center gap-2">
              <code class={field}>{freshKey}</code>
              <Button size="sm" onclick={() => copy("key", freshKey)}>{copied === "key" ? "Copied" : "Copy"}</Button>
            </div>
          </div>
        {:else}
          <code class="{field} block" data-testid="a2a-masked-key">{maskedKey(status)}</code>
        {/if}
        <div class="mt-2 flex flex-wrap gap-2">
          <Button size="sm" variant="ghost" disabled={busy} onclick={() => act(rotateAgentA2A)}>{status.key_set ? "Rotate key" : "Create key"}</Button>
          {#if status.key_set}<Button size="sm" variant="ghost" disabled={busy} onclick={() => { freshKey = ""; act(revokeAgentA2A); }}>Revoke key</Button>{/if}
        </div>
        {#if status.key_set && status.rotated_at}<p class="mt-1 {muted}" data-testid="a2a-key-date">Created {new Date(status.rotated_at).toLocaleString()}</p>{/if}
        <p class="mt-1 {muted}">Callers send it as <span class="font-mono">Authorization: Bearer …</span>. Your own wick Personal Access Token works too.</p>
      </div>

      <div class="flex items-start gap-3">
        <Toggle checked={status.public_card} onChange={(v: boolean) => save({ public_card: v })} label="Public agent card" describedBy="a2a-card-hint" />
        <span class="min-w-0">
          <span class="block text-sm text-black-900 dark:text-white-100">Public agent card</span>
          <span id="a2a-card-hint" class="block {muted}">Anyone can read the card (name, description, skills). Sending messages still needs the key.</span>
        </span>
      </div>

      <div class="flex flex-wrap items-center gap-2">
        <Button size="sm" variant="ghost" disabled={busy || !status.enabled} onclick={runTest}>Test</Button>
        {#if probe}<span class="text-xs {probe.ok ? 'text-green-700' : 'text-neg-400'}" data-testid="a2a-probe">{probeLine(probe)}</span>{/if}
      </div>

      <details>
        <summary class="cursor-pointer {muted}">Example: send a message with curl</summary>
        <p class="mt-2 {muted}">Set <span class="font-mono">A2A_KEY</span> to your API key first.</p>
        <pre class="mt-2 overflow-x-auto rounded-lg bg-white-200 p-3 font-mono text-xs text-black-900 dark:bg-navy-800 dark:text-white-100" data-testid="a2a-curl">{curlExample(status.endpoint_url)}</pre>
      </details>
    </div>
  {/if}
</section>
