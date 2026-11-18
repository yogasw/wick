<script lang="ts">
  /* A project's access requests: the history of what its agents asked for
     and who decided (kept in each session's files), the project's rules,
     live "this session" grants and a simulator. Shown in the project's
     settings and in a Team agent's Access tab (its project). Approving
     happens on the card in the conversation, never here. */
  import {
    accessRequestLine,
    accessStatusIcon,
    loadProjectAccess,
    postProjectAccess,
    type ProjectAccess,
    type SimulateReply,
  } from "./project-access.js";

  type Props = { base: string; projectId: string };
  let { base, projectId }: Props = $props();

  let data = $state<ProjectAccess | null>(null);
  let error = $state("");
  let notice = $state("");
  let busy = $state(false);
  let editing = $state<Record<string, string>>({});
  let newDoc = $state("");
  let showExamples = $state(false);

  let sim = $state({ connector: "", op: "", agent: "", session: "", params: "", rule: "", viewer: "" });
  let simReply = $state<SimulateReply | null>(null);

  async function reload() {
    error = "";
    try {
      data = await loadProjectAccess(base, projectId);
      if (!newDoc) newDoc = data.rule_example;
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    }
  }

  async function act(path: string, body: unknown, ok: string) {
    busy = true;
    error = "";
    notice = "";
    try {
      await postProjectAccess(base, projectId, path, body);
      notice = ok;
      await reload();
      return true;
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
      return false;
    } finally {
      busy = false;
    }
  }

  async function saveRule(id: string, doc: string) {
    if (await act("rules", { id, doc }, id ? "Rule saved." : "Rule created.")) {
      if (!id) newDoc = data?.rule_example ?? "";
      delete editing[id];
    }
  }

  async function setEnabled(on: boolean) {
    await act("enabled", { enabled: on }, on ? "Access requests are on for this project." : "Access requests are off for this project.");
  }

  async function simulate() {
    busy = true;
    error = "";
    simReply = null;
    const params: Record<string, string> = {};
    for (const line of sim.params.split("\n")) {
      const i = line.indexOf("=");
      if (i > 0) params[line.slice(0, i).trim()] = line.slice(i + 1).trim();
    }
    try {
      simReply = (await postProjectAccess(base, projectId, "simulate", { ...sim, params })) as SimulateReply;
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      busy = false;
    }
  }

  $effect(() => {
    void projectId;
    reload();
  });

  const input = "w-full rounded-lg border border-white-300 bg-white-100 px-2 py-1 text-xs text-black-900 dark:border-navy-600 dark:bg-navy-800 dark:text-white-100";
  const code = "w-full rounded-lg border border-white-300 bg-white-100 p-2 font-mono text-xs text-black-900 dark:border-navy-600 dark:bg-navy-800 dark:text-white-100";
  const btn = "rounded-lg border border-white-300 px-2.5 py-1 text-xs font-medium text-black-900 hover:bg-white-200 disabled:opacity-50 dark:border-navy-600 dark:text-white-100 dark:hover:bg-navy-600";
  const primary = "rounded-lg bg-green-500 px-2.5 py-1 text-xs font-medium text-white-100 hover:bg-green-600 disabled:opacity-50";
  const h = "mb-1.5 mt-4 text-xs font-semibold text-black-900 dark:text-white-100";
  const muted = "text-xs text-black-800 dark:text-black-600";
</script>

<div data-testid="project-access">
  <p class={muted}>
    What this project's agents asked to run beyond their own access, and who decided. Approving happens on the card in the
    conversation. Rules here apply to this project's sessions: <b>deny</b> beats <b>require</b> beats <b>auto_approve</b>.
  </p>
  {#if error}<p class="mt-2 text-xs text-neg-400" data-testid="project-access-error">{error}</p>{/if}
  {#if notice}<p class="mt-2 text-xs text-green-500">{notice}</p>{/if}

  {#if !data && !error}
    <p class={"mt-2 " + muted}>Loading…</p>
  {:else if data}
    <label class="mt-3 flex cursor-pointer items-center justify-between gap-4 rounded-lg border border-white-300 px-3 py-2 dark:border-navy-600" data-testid="project-access-enabled">
      <span class="min-w-0 flex-1">
        <span class="block text-sm font-medium text-black-900 dark:text-white-100">Turn on access requests</span>
        <span class={"block " + muted}>
          Off (default): nothing changes, an action outside an agent's access is simply refused. On: agents can ask
          for approval on a card, the rules below apply, and an op that is off by config can run once with a
          config override.
        </span>
      </span>
      <input type="checkbox" class="h-5 w-9 accent-green-500" checked={data.enabled} disabled={busy}
        onchange={(e) => setEnabled((e.currentTarget as HTMLInputElement).checked)} aria-label="Turn on access requests" />
    </label>
    <h3 class={h}>Requests</h3>
    {#if data.requests.length === 0}
      <p class={muted}>No requests yet.</p>
    {:else}
      <ul class="space-y-1.5">
        {#each data.requests as r (r.id)}
          <li class="rounded-lg border border-white-300 px-2 py-1.5 text-xs dark:border-navy-600" data-testid="project-access-request">
            <div class="text-black-900 dark:text-white-100"><b>{r.agent}</b> · {r.items.length} action(s) · {r.status}
              <span class={muted}> · {new Date(r.created_at).toLocaleString()}</span></div>
            {#if r.reason}<div class={muted}><i>Agent's claim:</i> {r.reason}</div>{/if}
            {#each r.items as it (it.id)}
              <div class="break-words text-black-900 dark:text-white-100">{accessStatusIcon(it.status)} <code>{it.connector_key}.{it.op_key}</code> {it.target} — <span class={muted}>{accessRequestLine(it)}</span></div>
            {/each}
          </li>
        {/each}
      </ul>
    {/if}

    <h3 class={h}>Rules</h3>
    {#each data.rules as r (r.id)}
      <div class="mb-2 rounded-lg border border-white-300 p-2 dark:border-navy-600" data-testid="project-access-rule">
        <div class="flex flex-wrap items-center gap-1.5 text-xs text-black-900 dark:text-white-100">
          <b>{r.name}</b><span class="rounded bg-white-200 px-1 dark:bg-navy-800">{r.effect}</span>
          {#if r.simulate}<span class="rounded bg-white-200 px-1 dark:bg-navy-800">simulate</span>{/if}
          {#if r.disabled}<span class="rounded bg-white-200 px-1 dark:bg-navy-800">disabled</span>{/if}
          <span class={muted}>by {r.owner || "someone"} · rev {r.rev}</span>
        </div>
        {#if r.can_edit}
          <textarea class={code + " mt-1.5"} rows="8" value={editing[r.id] ?? r.yaml} oninput={(e) => (editing[r.id] = (e.currentTarget as HTMLTextAreaElement).value)}></textarea>
          <div class="mt-1.5 flex gap-1.5">
            <button type="button" class={primary} disabled={busy} onclick={() => saveRule(r.id, editing[r.id] ?? r.yaml)}>Save</button>
            <button type="button" class={btn} disabled={busy} onclick={() => act(`rules/${encodeURIComponent(r.id)}/delete`, {}, "Rule deleted.")}>Delete</button>
          </div>
        {:else}
          <pre class={code + " mt-1.5 whitespace-pre-wrap"}>{r.yaml}</pre>
        {/if}
      </div>
    {/each}
    <div class="rounded-lg border border-dashed border-white-300 p-2 dark:border-navy-600">
      <div class="text-xs font-medium text-black-900 dark:text-white-100">New rule</div>
      <textarea class={code + " mt-1.5"} rows="10" bind:value={newDoc} data-testid="project-access-new-rule"></textarea>
      <div class="mt-1.5 flex flex-wrap items-center gap-2">
        <button type="button" class={primary} disabled={busy} onclick={() => saveRule("", newDoc)}>Create</button>
        <button type="button" class={btn} onclick={() => (showExamples = !showExamples)}>{showExamples ? "Hide" : "Show"} script examples</button>
      </div>
      {#if showExamples}<pre class={code + " mt-1.5 whitespace-pre-wrap"}>{data.script_examples}</pre>{/if}
      <p class={"mt-1 " + muted}>Facts a rule can test: {data.fact_names.join(", ")}</p>
    </div>

    <h3 class={h}>Simulate</h3>
    <div class="grid grid-cols-2 gap-1.5">
      <input class={input} placeholder="connector key or instance id" bind:value={sim.connector} />
      <input class={input} placeholder="op key, e.g. update_page" bind:value={sim.op} />
      <input class={input} placeholder="agent id (optional)" bind:value={sim.agent} />
      <input class={input} placeholder="session id (optional)" bind:value={sim.session} />
      <select class={input} bind:value={sim.rule}>
        <option value="">Run a rule's script too (optional)</option>
        {#each data.rules as r (r.id)}<option value={r.id}>{r.name}</option>{/each}
      </select>
      <input class={input} placeholder="viewer wick user id (approvers rules)" bind:value={sim.viewer} />
    </div>
    <textarea class={code + " mt-1.5"} rows="3" placeholder="params, one key=value per line" bind:value={sim.params}></textarea>
    <div class="mt-1.5 flex items-center gap-2">
      <button type="button" class={btn} disabled={busy} onclick={simulate}>Simulate</button>
      {#if simReply}
        <span class="text-xs text-black-900 dark:text-white-100" data-testid="project-access-sim">
          → <b>{simReply.result.effect}</b>{#if simReply.result.rule} by rule <b>{simReply.result.rule}</b>{/if} · kind {simReply.result.kind}
          {#if simReply.script}· script: {simReply.script.error ? `error: ${simReply.script.error}` : simReply.script.allow ? "allow" : "not allowed"}{simReply.script.reason ? ` (${simReply.script.reason})` : ""}{/if}
          {#if simReply.script_error}· script: {simReply.script_error}{/if}
        </span>
      {/if}
    </div>
    {#if simReply?.script?.logs?.length}<pre class={code + " mt-1.5 whitespace-pre-wrap"}>{simReply.script.logs.join("\n")}</pre>{/if}

    <h3 class={h}>"This session" grants</h3>
    {#if data.grants.length === 0}
      <p class={muted}>None active.</p>
    {:else}
      <ul class="space-y-1">
        {#each data.grants as g (g.id)}
          <li class="flex items-center justify-between gap-2 text-xs text-black-900 dark:text-white-100">
            <span class="break-words"><code>{g.op_key}</code> {g.target} · by {g.approver} · session {g.session_id.slice(-12)}</span>
            <button type="button" class={btn} disabled={busy} onclick={() => act(`grants/${encodeURIComponent(g.id)}/revoke`, {}, "Grant revoked.")}>Revoke</button>
          </li>
        {/each}
      </ul>
    {/if}

  {/if}
</div>
