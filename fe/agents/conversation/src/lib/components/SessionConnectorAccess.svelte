<script lang="ts">
  /* Workspace › Connector access: what this session's agent did with each
     connector (calls, errors, config overrides, approved runs, last call),
     and the session's own say over each one — Normal or Blocked — for this
     session only. Also the session's access requests and the approvals
     still live in it ("1 hour" / "this session"), each revocable. */
  type Usage = {
    connector_id: string;
    key: string;
    label: string;
    kind: "saved" | "session";
    calls: number;
    errors: number;
    overrides: number;
    approved: number;
    last_at: string;
    last_op: string;
    last_status: string;
    last_error?: string;
    last_note?: string;
    mode: "allow" | "block";
  };
  type Req = {
    id: string;
    status: string;
    created_at: string;
    items: { id: string; connector_key: string; op_key: string; target: string; status: string; decided_by: string; mode: string; error: string; note: string }[];
  };
  type Grant = { id: string; connector_id: string; op_key: string; target: string; approver: string; expires_at?: string | null };
  type Data = { enabled: boolean; connectors: Usage[]; requests: Req[]; grants: Grant[] };

  type Props = { base: string; sessionId: string };
  let { base, sessionId }: Props = $props();

  let data = $state<Data | null>(null);
  let error = $state("");
  let busy = $state(false);

  const url = (p = "") => `${base}/sessions/${encodeURIComponent(sessionId)}/access${p}`;

  async function load() {
    error = "";
    try {
      const r = await fetch(url(), { credentials: "same-origin", headers: { Accept: "application/json" } });
      if (!r.ok) throw new Error((await r.text()) || `HTTP ${r.status}`);
      data = (await r.json()) as Data;
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    }
  }

  async function post(path: string, body: unknown) {
    busy = true;
    error = "";
    try {
      const r = await fetch(url(path), {
        method: "POST",
        credentials: "same-origin",
        headers: { Accept: "application/json", "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
      if (!r.ok) {
        const t = await r.text();
        let msg = t || `HTTP ${r.status}`;
        try {
          msg = (JSON.parse(t) as { error?: string }).error || msg;
        } catch {
          /* not JSON */
        }
        throw new Error(msg);
      }
      await load();
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      busy = false;
    }
  }

  $effect(() => {
    void sessionId;
    load();
  });

  const MODES: { value: Usage["mode"]; label: string; on: string }[] = [
    { value: "allow", label: "Normal", on: "bg-green-500 text-white-100" },
    { value: "block", label: "Blocked", on: "bg-neg-400 text-white-100" },
  ];

  function when(s: string): string {
    if (!s || s.startsWith("0001")) return "";
    return new Date(s).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  }

  /** left is how long an approval still runs: "42 min left", or "this session". */
  function left(exp?: string | null): string {
    if (!exp) return "this session";
    const ms = new Date(exp).getTime() - Date.now();
    if (ms <= 0) return "expired";
    const min = Math.ceil(ms / 60000);
    return min >= 60 ? `${Math.floor(min / 60)} h ${min % 60} min left` : `${min} min left`;
  }

  function icon(status: string): string {
    switch (status) {
      case "ok": return "✅";
      case "error": return "❌";
      case "denied": return "🚫";
      case "running": return "🔄";
      default: return "⏳";
    }
  }

  const muted = "text-[11px] text-black-700 dark:text-black-600";
  const chip = "ml-1 rounded-full px-1.5 py-px text-[10px]";
  const h = "mb-1.5 mt-3 text-[11px] font-semibold uppercase tracking-wide text-black-700 dark:text-black-600";
</script>

<div class="space-y-2" data-testid="session-connector-access">
  {#if error}<p class="text-xs text-neg-400">{error}</p>{/if}
  {#if !data && !error}
    <p class={muted}>Loading…</p>
  {:else if data}
    <div class="rounded-lg border border-white-300 dark:border-navy-600 px-2.5 py-1.5 text-[11px] text-black-800 dark:text-white-200">
      Access requests: <b class={data.enabled ? "text-green-500" : ""}>{data.enabled ? "ON" : "OFF"}</b> for this session's project
      {#if !data.enabled}· writes outside the agent's access are refused; turn them on in the project settings for cards{/if}
    </div>
    <p class={muted}>
      Every connector this session used. Normal: the agent works as its access allows, and a write beyond it asks on a card. Blocked: every call refused. For this session only; the connector itself is untouched.
    </p>

    {#if data.connectors.length === 0}
      <p class={muted}>No connector used yet.</p>
    {/if}
    {#each data.connectors as u (u.connector_id)}
      <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2" data-testid="sca-connector">
        <div class="text-sm font-medium text-black-900 dark:text-white-100">
          {u.label || u.key}
          <span class="{chip} bg-white-200 dark:bg-navy-700 text-black-800 dark:text-white-200">{u.kind === "session" ? "session connector" : "saved"}</span>
        </div>
        <div class={muted}>
          {u.calls} call{u.calls === 1 ? "" : "s"}
          {#if u.errors}<span class="{chip} bg-neg-100 text-neg-400 dark:bg-navy-700">{u.errors} error{u.errors === 1 ? "" : "s"}</span>{/if}
          {#if u.overrides}<span class="{chip} bg-amber-50 text-amber-700 dark:bg-navy-700 dark:text-amber-300">{u.overrides} override{u.overrides === 1 ? "" : "s"}</span>{/if}
          {#if u.approved}<span class="{chip} bg-green-100 text-green-600 dark:bg-navy-700 dark:text-green-400">{u.approved} approved</span>{/if}
        </div>
        {#if u.last_op}
          <div class="mt-1 break-words font-mono text-[11px] text-black-800 dark:text-white-200">
            {when(u.last_at)} {u.last_op}{u.last_note ? ` · ${u.last_note}` : ""} · {u.last_status}{u.last_error ? ` · ${u.last_error}` : ""}
          </div>
        {/if}
        <div class="mt-2 flex overflow-hidden rounded-lg border border-white-300 dark:border-navy-600" role="radiogroup" aria-label="Access for this session">
          {#each MODES as m (m.value)}
            <button
              type="button"
              role="radio"
              aria-checked={u.mode === m.value}
              disabled={busy}
              class="flex-1 px-1 py-1 text-[11px] {u.mode === m.value ? m.on : 'text-black-800 dark:text-white-200 hover:bg-white-200 dark:hover:bg-navy-700'}"
              onclick={() => u.mode !== m.value && post("/policy", { connector_id: u.connector_id, mode: m.value })}
            >{m.label}</button>
          {/each}
        </div>
      </div>
    {/each}

    <div class={h}>Requests in this session</div>
    {#if data.requests.length === 0}
      <p class={muted}>None.</p>
    {:else}
      {#each data.requests as r (r.id)}
        {#each r.items as it (it.id)}
          <div class="border-b border-dashed border-white-300 dark:border-navy-600 py-1 text-xs text-black-900 dark:text-white-100">
            {icon(it.status)} <code>{it.connector_key}.{it.op_key}</code> {it.target}
            <span class={muted}> — {it.status}{it.decided_by ? ` by ${it.decided_by}` : ""}{it.error ? `: ${it.error}` : ""}{it.note ? `: ${it.note}` : ""} · {when(r.created_at)}</span>
          </div>
        {/each}
      {/each}
    {/if}

    <div class={h}>Live approvals</div>
    {#if data.grants.length === 0}
      <p class={muted}>None active.</p>
    {:else}
      {#each data.grants as g (g.id)}
        <div class="flex items-center justify-between gap-2 text-xs text-black-900 dark:text-white-100">
          <span class="break-words"><code>{g.op_key}</code> {g.target} · by {g.approver} · {left(g.expires_at)}</span>
          <button type="button" disabled={busy}
            class="shrink-0 rounded-md border border-white-300 dark:border-navy-600 px-2 py-0.5 text-[11px] hover:bg-white-200 dark:hover:bg-navy-700"
            onclick={() => post(`/grants/${encodeURIComponent(g.id)}/revoke`, {})}>Revoke</button>
        </div>
      {/each}
    {/if}
  {/if}
</div>
