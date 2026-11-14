<!--
  RolesViewer — Settings › Access when "By role" is on: the role document,
  read-only. Who lands in which role, what each role borrows of the owner's
  connectors (one table, connector column pinned, roles scroll sideways),
  a preview as one person computed by the server, and how to change it:
  by chatting (agents.roles_set). Activity, Raw and History fold away.
-->
<script lang="ts">
  import { untrack } from "svelte";
  import type { AgentConnector, AccessTrailItem, EffectiveRow, RBACDoc, RBACRevision, ShareUser } from "../api/team.js";
  import { getAccessEffective, getAccessTrail, getRBAC, listShareUsers, runApi } from "../api/team.js";
  import { cellOf, roleOrder, roleTitle, whoIs } from "../rbacView.js";
  import { timeAgo } from "../timeFormat.js";

  type Props = { base: string; agentId: string; catalog: AgentConnector[]; captainHandle?: string };
  let { base, agentId, catalog, captainHandle = "captain" }: Props = $props();

  let doc = $state<RBACDoc | null>(null);
  let rev = $state(0);
  let history = $state<RBACRevision[]>([]);
  let error = $state("");
  let users = $state<ShareUser[]>([]);
  $effect(() => {
    const id = agentId;
    error = "";
    runApi(getRBAC(base, id))
      .then((r) => {
        doc = r.doc;
        rev = r.rev;
        history = r.history ?? [];
      })
      .catch((e) => (error = e instanceof Error ? e.message : String(e)));
    void runApi(listShareUsers(base)).then((r) => (users = r.users ?? [])).catch(() => (users = []));
  });

  const nameOf = (id: string) => users.find((u) => u.id === id)?.name || id;
  const labelOf = (id: string) => catalog.find((c) => c.id === id)?.label ?? id;
  const roles = $derived(doc ? roleOrder(doc) : []);
  const who = $derived(doc ? whoIs(doc, nameOf) : {});
  const lentIDs = $derived(doc ? Object.keys(doc.lend ?? {}).sort((a, b) => labelOf(a).localeCompare(labelOf(b))) : []);
  const others = $derived(Math.max(0, catalog.filter((c) => !c.tool && (c.tier ?? "") === "").length - lentIDs.length));

  /* Preview as one person: the server applies the same role rules a run
     does. Nothing is asked until someone is picked: a preview walks every
     connector, and the viewer must not load one on open. */
  let previewAs = $state("");
  let preview = $state<EffectiveRow[] | null>(null);
  let previewError = $state("");
  /* One request per pick: the key below is plain (not $state), and the
     request runs untracked, so a re-render or a parent refresh can never
     turn the preview into a reload loop. A late answer for an older pick
     is dropped. */
  let previewKey = "";
  $effect(() => {
    const p = previewAs;
    const id = agentId;
    if (!p) return;
    const key = `${id}|${p}`;
    if (key === previewKey) return;
    previewKey = key;
    untrack(() => {
      const [as, uid] = p === "owner" || p === "guest" ? [p, ""] : ["user", p];
      preview = null;
      previewError = "";
      runApi(getAccessEffective(base, id, as as "owner" | "user" | "guest", uid))
        .then((r) => {
          if (previewKey === key) preview = (r.items ?? []).filter((x) => x.runs_as !== "none");
        })
        .catch((e) => {
          if (previewKey === key) previewError = e instanceof Error ? e.message : String(e);
        });
    });
  });

  let trail = $state<AccessTrailItem[] | null>(null);
  const loadTrail = () => {
    if (trail) return;
    runApi(getAccessTrail(base, agentId)).then((r) => (trail = r.items ?? [])).catch(() => (trail = []));
  };

  const card = "rounded-xl border border-white-300 dark:border-navy-600";
  const muted = "text-xs text-black-800 dark:text-black-600";
  const fold = "rounded-lg border border-white-300 dark:border-navy-600";
  const summary = "cursor-pointer select-none px-3 py-2 text-xs font-medium text-black-900 dark:text-white-100";
  const pill = (tone: string) =>
    "inline-block whitespace-nowrap rounded-full border px-2 py-0.5 text-[11px] " +
    (tone === "on" ? "border-green-700 text-green-600 dark:text-green-400" : tone === "read" ? "border-sky-700 text-sky-500 dark:text-sky-300" : "border-white-300 text-black-700 dark:border-navy-600 dark:text-black-600");
</script>

<section class="{card} border-green-500 dark:border-green-700" data-testid="roles-viewer">
  <div class="flex flex-wrap items-center justify-between gap-2 px-4 py-3">
    <p class="text-sm font-semibold text-black-900 dark:text-white-100">Roles</p>
    <span class={muted}>view only · change it by chatting{rev ? ` · rev ${rev}` : ""}</span>
  </div>
  <div class="space-y-4 border-t border-white-300 px-4 py-4 dark:border-navy-600">
    {#if error}
      <p class="text-xs text-neg-400">{error}</p>
    {:else if !doc}
      <p class={muted}>Loading roles…</p>
    {:else}
      <div>
        <p class="text-xs font-medium text-black-900 dark:text-white-100">Who is who</p>
        <div class="mt-2 space-y-1.5" data-testid="roles-who">
          {#each roles as r (r)}
            <div class="flex flex-wrap items-center gap-1.5 text-xs">
              <span class="w-16 shrink-0 {muted}">{roleTitle(r)}</span>
              {#each who[r] ?? ["nobody yet"] as w (w)}
                <span class="rounded-full border border-white-300 px-2 py-0.5 text-black-900 dark:border-navy-600 dark:text-white-100">{w}</span>
              {/each}
            </div>
          {/each}
        </div>
      </div>

      <div>
        <p class="text-xs font-medium text-black-900 dark:text-white-100">What each role borrows from you</p>
        <p class={muted}>People always keep their own access on top. ⚑ = writes wait for your OK, so they read only for now.</p>
        <div class="mt-2 overflow-x-auto rounded-lg border border-white-300 dark:border-navy-600" data-testid="roles-table">
          <table class="w-max min-w-full border-collapse text-xs">
            <thead>
              <tr class="text-left text-[11px] text-black-800 dark:text-black-600">
                <th class="sticky left-0 z-10 bg-white-100 px-3 py-2 font-medium dark:bg-navy-700">Connector</th>
                {#each roles as r (r)}<th class="whitespace-nowrap px-3 py-2 font-medium">{roleTitle(r)}</th>{/each}
              </tr>
            </thead>
            <tbody>
              {#each lentIDs as id (id)}
                <tr class="border-t border-white-300 dark:border-navy-600">
                  <th scope="row" class="sticky left-0 z-10 max-w-[12rem] truncate bg-white-100 px-3 py-2 text-left font-medium text-black-900 dark:bg-navy-700 dark:text-white-100" title={labelOf(id)}>{labelOf(id)}</th>
                  {#each roles as r (r)}
                    {@const c = cellOf(doc, r, id)}
                    <td class="px-3 py-2"><span class={pill(c.tone)}>{c.text}{c.approval ? " ⚑" : ""}</span></td>
                  {/each}
                </tr>
              {/each}
              <tr class="border-t border-white-300 dark:border-navy-600">
                <th scope="row" class="sticky left-0 z-10 bg-white-100 px-3 py-2 text-left font-normal {muted} dark:bg-navy-700">{lentIDs.length ? `${others} other connectors` : "Every connector"}</th>
                <td class="px-3 py-2"><span class={pill("on")}>Write</span></td>
                <td colspan={roles.length - 1} class="px-3 py-2 {muted}">not lent — own access only</td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>

      <div data-testid="roles-preview">
        <p class="text-xs font-medium text-black-900 dark:text-white-100">Preview as</p>
        <select class="mt-1 rounded-md border border-white-300 bg-white-100 px-2 py-1 text-xs text-black-900 dark:border-navy-600 dark:bg-navy-800 dark:text-white-100" bind:value={previewAs} aria-label="Preview as">
          <option value="">Choose a person…</option>
          <option value="owner">You</option>
          {#each users as u (u.id)}<option value={u.id}>{u.name || u.id}</option>{/each}
          <option value="guest">Someone unknown</option>
        </select>
        {#if !previewAs}
          <p class="mt-2 {muted}">Pick someone to see what this agent gives them.</p>
        {:else if previewError}
          <p class="mt-2 text-xs text-neg-400">{previewError}</p>
        {:else if preview === null}
          <p class="mt-2 {muted}">Loading…</p>
        {:else if preview.length === 0}
          <p class="mt-2 {muted}">Nothing — no connectors through this agent.</p>
        {:else}
          <ul class="mt-2 space-y-0.5 text-xs text-black-900 dark:text-white-100">
            {#each preview.slice(0, 12) as r (r.connector_id)}
              <li><b>{r.label}</b> · {r.runs_as === "owner" ? "on your account" : previewAs === "owner" ? "your own access" : "their own access"} · {r.ops.length} op{r.ops.length === 1 ? "" : "s"}</li>
            {/each}
            {#if preview.length > 12}<li class={muted}>+ {preview.length - 12} more</li>{/if}
          </ul>
        {/if}
      </div>

      <div class="rounded-lg border border-white-300 p-3 dark:border-navy-600">
        <p class="text-xs font-medium text-black-900 dark:text-white-100">Change it by chatting</p>
        <p class={muted}>Tell @{captainHandle} in your own chat, for example:</p>
        <ul class="mt-1 space-y-0.5 text-xs text-green-600 dark:text-green-400">
          <li>"make Person A trusted on this agent"</li>
          <li>"members can read Bitbucket here"</li>
          <li>"undo the last roles change"</li>
        </ul>
        <p class="mt-1 {muted}">Only your own request changes roles; every change is a new revision you can go back to.</p>
      </div>

      <div class="space-y-2">
        <details class={fold} ontoggle={(e) => { if ((e.currentTarget as HTMLDetailsElement).open) loadTrail(); }}>
          <summary class={summary}>Activity</summary>
          <div class="border-t border-white-300 px-3 py-2 dark:border-navy-600">
            {#if trail === null}<p class={muted}>Loading…</p>
            {:else if trail.length === 0}<p class={muted}>No calls have used a lend yet.</p>
            {:else}
              <ul class="space-y-1 text-xs">
                {#each trail as t (t.run_id)}
                  <li class="text-black-900 dark:text-white-100"><b>{t.caller_name || t.caller_id}</b> · {t.connector_label || t.connector_id} · {t.operation} <span class="text-black-800 dark:text-black-600">· {timeAgo(t.at)}</span></li>
                {/each}
              </ul>
            {/if}
          </div>
        </details>
        <details class={fold}>
          <summary class={summary}>Raw</summary>
          <pre class="max-h-64 overflow-auto border-t border-white-300 px-3 py-2 text-[11px] text-black-900 dark:border-navy-600 dark:text-white-100">{JSON.stringify(doc, null, 2)}</pre>
        </details>
        <details class={fold}>
          <summary class={summary}>Changes</summary>
          <div class="border-t border-white-300 px-3 py-2 dark:border-navy-600">
            {#if history.length === 0}<p class={muted}>The default roles; nothing changed yet.</p>
            {:else}
              <ul class="space-y-1 text-xs">
                {#each history as h (h.rev)}
                  <li class="text-black-900 dark:text-white-100">rev {h.rev} · {nameOf(h.updated_by)} <span class="text-black-800 dark:text-black-600">· {timeAgo(h.at)}{h.reason ? ` · ${h.reason}` : ""}</span></li>
                {/each}
              </ul>
            {/if}
          </div>
        </details>
      </div>
    {/if}
  </div>
</section>
