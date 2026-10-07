<script lang="ts">
  /* Settings of an A2A remote agent (plan §6.2b), two halves of one
     drawer: section "remote" is the Remote A2A tab (card, Refresh agent
     card, Test, auth, who may use it) and "advanced" the per-call limits.
     Each edit saves on its own through PATCH …/a2a-remote, like the rest
     of Settings. The auth secret is write-only: the form starts empty and
     only `auth_set` says one is stored. */
  import { onMount, untrack } from "svelte";
  import RemoteAuthFields from "./RemoteAuthFields.svelte";
  import {
    getRemoteAgent, updateRemoteAgent, refreshRemoteCard, testRemoteAgent, runApi,
    type AgentItem, type RemoteAgentInfo, type RemoteAuthType, type RemoteTestResult, type RemoteUpdate,
  } from "../../api/team.js";
  import { rosterTime } from "../../timeFormat.js";
  import {
    AUTH_OPTIONS, TIMEOUT_MIN, TIMEOUT_MAX, MAX_BYTES_DEFAULT, TIMEOUT_DEFAULT,
    authReq, bytesToKb, kbToBytes, limitsError, formatBytes, testSummary,
  } from "../../remoteAgent.js";

  type Props = {
    base: string;
    agent: AgentItem;
    section: "remote" | "advanced";
    /** The server's latest settings, so the roster's copy follows. */
    onChanged: (info: RemoteAgentInfo) => void;
  };
  let { base, agent, section, onChanged }: Props = $props();

  let info = $state<RemoteAgentInfo | null>(untrack(() => agent.remote ?? null));
  let loadError = $state("");
  let busy = $state<"" | "refresh" | "test" | "auth" | "limits">("");
  let error = $state("");
  let note = $state("");
  let testResult = $state<RemoteTestResult | null>(null);

  let authType = $state<RemoteAuthType>(untrack(() => agent.remote?.auth_type ?? "none"));
  let secret = $state("");
  let header = $state(untrack(() => agent.remote?.auth_header ?? ""));

  let timeoutSec = $state(untrack(() => agent.remote?.timeout_sec ?? TIMEOUT_DEFAULT));
  let maxKb = $state(untrack(() => bytesToKb(agent.remote?.max_response_bytes ?? MAX_BYTES_DEFAULT)));
  const limitsWhy = $derived(limitsError(timeoutSec, kbToBytes(maxKb)));

  function take(r: RemoteAgentInfo) {
    info = r;
    authType = r.auth_type;
    header = r.auth_header ?? "";
    timeoutSec = r.timeout_sec;
    maxKb = bytesToKb(r.max_response_bytes);
  }

  onMount(() => {
    runApi(getRemoteAgent(base, agent.id))
      .then(take)
      .catch((e) => { loadError = e instanceof Error ? e.message : String(e); });
  });

  async function run(kind: typeof busy, fn: () => Promise<RemoteAgentInfo>, done: string) {
    if (busy) return;
    busy = kind;
    error = "";
    note = "";
    try {
      const r = await fn();
      take(r);
      onChanged(r);
      note = done;
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      busy = "";
    }
  }

  const patch = (body: RemoteUpdate) => runApi(updateRemoteAgent(base, agent.id, body));

  function refresh() {
    void run("refresh", () => runApi(refreshRemoteCard(base, agent.id)), "Agent card refreshed.");
  }

  async function test() {
    if (busy) return;
    busy = "test";
    testResult = null;
    error = "";
    try {
      testResult = await runApi(testRemoteAgent(base, { agent_id: agent.id }));
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      busy = "";
    }
  }

  const authBody = $derived(authReq(authType, secret, header));
  // Switching to None needs no secret; a bearer/api_key change needs one.
  const authDirty = $derived(!!info && authBody !== undefined && (authType !== info.auth_type || secret.trim() !== "" || (authType === "api_key" && (header.trim() || "X-API-Key") !== (info.auth_header || "X-API-Key"))));
  function saveAuth() {
    const body = authBody;
    if (!body || !authDirty) return;
    void run("auth", () => patch({ auth: body }), body.type === "none" ? "Auth removed." : "Auth saved.").then(() => { secret = ""; });
  }


  function saveLimits() {
    if (!info || limitsWhy) return;
    const bytes = kbToBytes(maxKb);
    if (timeoutSec === info.timeout_sec && bytes === info.max_response_bytes) return;
    void run("limits", () => patch({ timeout_sec: timeoutSec, max_response_bytes: bytes }), "Saved ✓");
  }

  const input =
    "w-full rounded-lg border border-white-300 bg-white-100 px-3 py-2 text-sm text-black-900 focus:border-green-500 focus:outline-none dark:border-navy-600 dark:bg-navy-800 dark:text-white-100";
  const label = "mb-1 block text-xs font-medium text-black-800 dark:text-black-600";
  const outline =
    "rounded-lg border border-white-300 px-3 py-1.5 text-sm text-black-900 hover:bg-white-200 disabled:opacity-50 dark:border-navy-600 dark:text-white-100 dark:hover:bg-navy-600";
  const authLabel = (t: RemoteAuthType) => AUTH_OPTIONS.find((o) => o.value === t)?.label ?? t;
</script>

{#if loadError && !info}
  <p class="text-sm text-neg-400">{loadError}</p>
{:else if !info}
  <p class="text-sm text-black-800 dark:text-black-600">Loading…</p>
{:else if section === "remote"}
  <div data-testid="remote-settings">
    <p class="text-sm font-semibold text-black-900 dark:text-white-100">Remote A2A</p>
    <p class="mt-1 text-xs text-black-800 dark:text-black-600">Persona, access and tools belong to the remote agent; wick only reads its card and keeps the transcript.</p>
  </div>
  <div class="rounded-xl border border-white-300 p-3 dark:border-navy-600" data-testid="remote-card">
    <div class="flex items-center gap-3">
      {#if info.card.icon_url}<img src={info.card.icon_url} alt="" class="h-10 w-10 rounded-xl object-cover" />{/if}
      <div class="min-w-0 flex-1">
        <p class="truncate text-sm font-semibold text-black-900 dark:text-white-100">{info.card.name}</p>
        <p class="truncate text-xs text-black-800 dark:text-black-600">
          {info.host}{#if info.card.version} · v{info.card.version}{/if} · {info.card.streaming ? "streaming" : "no streaming"}{#if info.card.provider} · {info.card.provider}{/if}
        </p>
      </div>
    </div>
    {#if (info.card.skills ?? []).length > 0}
      <ul class="mt-2 flex flex-wrap gap-1.5" aria-label="Skills">
        {#each info.card.skills ?? [] as sk (sk.id)}
          <li class="rounded-full bg-white-200 px-2 py-0.5 text-[11px] text-black-900 dark:bg-navy-800 dark:text-white-100" title={sk.description ?? ""}>{sk.name || sk.id}</li>
        {/each}
      </ul>
    {/if}
    <p class="mt-2 text-[11px] text-black-700">Card fetched {rosterTime(info.refreshed_at)}</p>
    <div class="mt-2 flex flex-wrap gap-2">
      <button type="button" class={outline} disabled={!!busy} data-testid="remote-refresh" onclick={refresh}>{busy === "refresh" ? "Refreshing…" : "Refresh agent card"}</button>
      <button type="button" class={outline} disabled={!!busy} data-testid="remote-test" onclick={test}>{busy === "test" ? "Testing…" : "Test"}</button>
    </div>
    <p class="mt-1 text-xs text-black-800 dark:text-black-600">Refresh updates skills, version and streaming; the handle and avatar here stay.</p>
    {#if testResult}
      <p class="mt-1 text-xs {testResult.ok ? 'text-green-600 dark:text-green-400' : 'text-neg-400'}" data-testid="remote-test-result">{testSummary(testResult)}</p>
    {/if}
  </div>
  <div>
    <span class={label}>Agent card URL</span>
    <p class="break-all font-mono text-xs text-black-900 dark:text-white-100" data-testid="remote-url">{info.card_url}</p>
    <p class="mt-1 text-xs text-black-800 dark:text-black-600">Endpoint {info.card.endpoint} ({info.card.transport}). To point at another URL, add a new A2A remote agent.</p>
  </div>
  <div class="space-y-2 border-t border-white-300 pt-4 dark:border-navy-600">
    <p class="text-xs text-black-800 dark:text-black-600" data-testid="remote-auth-current">
      Current: {authLabel(info.auth_type)}{#if info.auth_type === "api_key"} in {info.auth_header || "X-API-Key"}{/if}{#if info.auth_type !== "none"}{` · ${info.auth_set ? "secret saved" : "no secret"}`}{/if}
    </p>
    <RemoteAuthFields bind:type={authType} bind:secret bind:header saved={info.auth_set && authType === info.auth_type} idPrefix="rs" />
    <button type="button" class={outline} disabled={!authDirty || !!busy} data-testid="remote-auth-save" onclick={saveAuth}>{busy === "auth" ? "Saving…" : "Save auth"}</button>
  </div>
{:else}
  <div>
    <p class="text-sm font-semibold text-black-900 dark:text-white-100">Advanced</p>
    <p class="mt-1 text-xs text-black-800 dark:text-black-600">Limits on every call to {info.host}. No project or provider: the remote runs itself.</p>
  </div>
  <div class="grid grid-cols-1 gap-4 sm:grid-cols-2" data-testid="remote-limits">
    <div>
      <label class={label} for="rs-timeout">Timeout (seconds)</label>
      <input id="rs-timeout" class={input} type="number" min={TIMEOUT_MIN} max={TIMEOUT_MAX} bind:value={timeoutSec} onchange={saveLimits} />
      <p class="mt-1 text-xs text-black-800 dark:text-black-600">Default 120.</p>
    </div>
    <div>
      <label class={label} for="rs-maxkb">Max response (KB)</label>
      <input id="rs-maxkb" class={input} type="number" min="1" max="32768" bind:value={maxKb} onchange={saveLimits} />
      <p class="mt-1 text-xs text-black-800 dark:text-black-600">Now {formatBytes(info.max_response_bytes)}; default 2 MB.</p>
    </div>
  </div>
  {#if limitsWhy}<p class="text-sm text-neg-400" data-testid="remote-limits-error">{limitsWhy}</p>{/if}
{/if}
{#if error}<p class="text-sm text-neg-400">{error}</p>{/if}
{#if note}<p class="text-xs text-black-800 dark:text-black-600" aria-live="polite" data-testid="remote-note">{note}</p>{/if}
