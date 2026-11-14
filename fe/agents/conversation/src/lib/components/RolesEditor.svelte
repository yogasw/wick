<!--
  RolesEditor — the owner's manual editor for an agent's role document
  (Settings › Access › Roles › Edit). It edits a draft copy: people and
  tags to roles, the owner's roles, and the lend table (guest included).
  Nothing is saved until Save, which asks first and writes a new
  revision from the rev it was read at (a change made meanwhile, e.g. by
  chatting, answers 409 instead of being overwritten).
-->
<script lang="ts">
  import type { AgentConnector, RBACDoc, RBACRule, RoleTag, ShareUser } from "../api/team.js";
  import { putRBAC, runApi } from "../api/team.js";
  import {
    addRole, addRule, cellValue, lendConnector, moveRule, removeRole, removeRule, roleNameError,
    setCell, setRole, setRuleRole, unlendConnector, type CellLevel,
  } from "../rbacEdit.js";
  import { roleOrder, roleTitle } from "../rbacView.js";

  type Props = {
    base: string; agentId: string; doc: RBACDoc; rev: number;
    users: ShareUser[]; tags: RoleTag[]; catalog: AgentConnector[]; onAgent: Set<string>;
    onSaved: (rev: number) => void; onCancel: () => void;
  };
  let { base, agentId, doc, rev, users, tags, catalog, onAgent, onSaved, onCancel }: Props = $props();

  let draft = $state<RBACDoc>(JSON.parse(JSON.stringify(doc)));
  let reason = $state("");
  let confirming = $state(false);
  let saving = $state(false);
  let error = $state("");

  const roles = $derived(roleOrder(draft));
  const custom = $derived(roles.filter((r) => r !== "owner" && r !== "guest"));
  const assignable = $derived(roles); // owner (co-owner) and guest included
  const labelOf = (id: string) => catalog.find((c) => c.id === id)?.label ?? id;
  const nameOf = (id: string) => users.find((u) => u.id === id)?.name || id;
  const tagOf = (id: string) => tags.find((t) => t.id === id)?.name || id;
  const whoOf = (r: RBACRule) => (r.match === "shared" ? "Everyone it's shared with" : r.match === "tag" ? `Everyone tagged ${tagOf(r.value ?? "")}` : nameOf(r.value ?? ""));
  const lentIDs = $derived(Object.keys(draft.lend ?? {}).filter((id) => onAgent.has(id)).sort((a, b) => labelOf(a).localeCompare(labelOf(b))));
  const lendable = $derived(
    [...onAgent].filter((id) => !draft.lend?.[id] && catalog.some((c) => c.id === id && !c.tool && (c.tier ?? "") === "")).sort((a, b) => labelOf(a).localeCompare(labelOf(b))),
  );
  const changed = $derived(JSON.stringify(draft) !== JSON.stringify(doc));

  // + Add person / tag / everyone shared
  let newMatch = $state<"user" | "tag" | "shared">("user");
  let newValue = $state("");
  let newRuleRole = $state("member");
  function addNewRule() {
    if (newMatch !== "shared" && !newValue) return;
    draft = addRule(draft, { match: newMatch, value: newMatch === "shared" ? undefined : newValue, role: newRuleRole });
    newValue = "";
  }

  // + Add role
  let newRole = $state("");
  let newRank = $state(50);
  let newLend = $state<CellLevel>("read");
  const newRoleError = $derived(newRole ? roleNameError(draft, newRole) : "");
  function addNewRole() {
    if (!newRole || newRoleError) return;
    draft = addRole(draft, newRole, newRank, newLend);
    newRole = "";
  }

  let newLent = $state("");

  async function save() {
    saving = true;
    error = "";
    try {
      const r = await runApi(putRBAC(base, agentId, $state.snapshot(draft) as RBACDoc, rev, reason.trim() || "edited in Settings"));
      confirming = false;
      onSaved(r.rev);
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      error = /changed since|409/i.test(msg) ? "The roles changed meanwhile (maybe by chatting). Cancel and edit again from the latest version." : msg;
      confirming = false;
    } finally {
      saving = false;
    }
  }

  const muted = "text-xs text-black-800 dark:text-black-600";
  const input = "rounded-md border border-white-300 bg-white-100 px-2 py-1 text-xs text-black-900 dark:border-navy-600 dark:bg-navy-800 dark:text-white-100";
  const btn = "rounded-md border border-white-300 px-2 py-1 text-xs text-black-900 hover:bg-white-200 dark:border-navy-600 dark:text-white-100 dark:hover:bg-navy-600";
  const x = "rounded px-1.5 text-xs text-black-700 hover:text-neg-400 dark:text-black-600";
</script>

<div class="space-y-5" data-testid="roles-editor">
  <div>
    <p class="text-xs font-medium text-black-900 dark:text-white-100">Who is who</p>
    <p class={muted}>First match wins, top to bottom. You are always owner; anyone no rule names is a guest.</p>
    <ul class="mt-2 space-y-1.5">
      {#each draft.rules ?? [] as r, i (i)}
        <li class="flex flex-wrap items-center gap-1.5 text-xs" data-testid="rule-row">
          <span class="min-w-0 flex-1 truncate text-black-900 dark:text-white-100" title={whoOf(r)}>{whoOf(r)}</span>
          <select class={input} value={r.role} aria-label="Role" onchange={(e) => (draft = setRuleRole(draft, i, (e.currentTarget as HTMLSelectElement).value))}>
            {#each assignable as a (a)}<option value={a}>{roleTitle(a)}</option>{/each}
          </select>
          <button type="button" class={x} aria-label="Move up" disabled={i === 0} onclick={() => (draft = moveRule(draft, i, -1))}>↑</button>
          <button type="button" class={x} aria-label="Move down" disabled={i === (draft.rules?.length ?? 0) - 1} onclick={() => (draft = moveRule(draft, i, 1))}>↓</button>
          <button type="button" class={x} aria-label="Remove rule" onclick={() => (draft = removeRule(draft, i))}>✕</button>
        </li>
      {/each}
    </ul>
    <div class="mt-2 flex flex-wrap items-center gap-1.5" data-testid="add-rule">
      <select class={input} bind:value={newMatch} aria-label="Who">
        <option value="user">A person</option>
        <option value="tag">Everyone with a tag</option>
        <option value="shared">Everyone it's shared with</option>
      </select>
      {#if newMatch === "user"}
        <select class={input} bind:value={newValue} aria-label="Person">
          <option value="">Choose a person…</option>
          {#each users as u (u.id)}<option value={u.id}>{u.name || u.id}</option>{/each}
        </select>
      {:else if newMatch === "tag"}
        <select class={input} bind:value={newValue} aria-label="Tag">
          <option value="">Choose a tag…</option>
          {#each tags as t (t.id)}<option value={t.id}>{t.name}</option>{/each}
        </select>
      {/if}
      <span class={muted}>→</span>
      <select class={input} bind:value={newRuleRole} aria-label="New rule role">
        {#each assignable as a (a)}<option value={a}>{roleTitle(a)}</option>{/each}
      </select>
      <button type="button" class={btn} disabled={newMatch !== "shared" && !newValue} onclick={addNewRule}>+ Add</button>
    </div>
  </div>

  <div>
    <p class="text-xs font-medium text-black-900 dark:text-white-100">Roles</p>
    <p class={muted}>Rank orders them (owner 100, guest 0). The default is what a role borrows of a lent connector unless its cell says otherwise.</p>
    <ul class="mt-2 space-y-1.5">
      {#each custom as name (name)}
        {@const r = draft.roles[name]}
        <li class="flex flex-wrap items-center gap-1.5 text-xs" data-testid="role-row">
          <span class="w-24 truncate font-medium text-black-900 dark:text-white-100">{roleTitle(name)}</span>
          <label class={muted}>rank <input class="{input} w-14" type="number" min="1" max="99" value={r.rank} onchange={(e) => (draft = setRole(draft, name, { rank: +(e.currentTarget as HTMLInputElement).value }))} /></label>
          <label class={muted}>default
            <select class={input} value={r.lend} onchange={(e) => (draft = setRole(draft, name, { lend: (e.currentTarget as HTMLSelectElement).value as CellLevel }))}>
              <option value="off">Off</option><option value="read">Read</option><option value="all">Write</option>
            </select>
          </label>
          <label class="flex items-center gap-1 {muted}"><input type="checkbox" checked={!!r.write_needs_approval} onchange={(e) => (draft = setRole(draft, name, { write_needs_approval: (e.currentTarget as HTMLInputElement).checked }))} /> writes wait for OK ⚑</label>
          <button type="button" class={x} aria-label="Remove role" onclick={() => (draft = removeRole(draft, name))}>✕</button>
        </li>
      {/each}
    </ul>
    <div class="mt-2 flex flex-wrap items-center gap-1.5" data-testid="add-role">
      <input class={input} placeholder="new role, e.g. support" bind:value={newRole} aria-label="New role name" />
      <label class={muted}>rank <input class="{input} w-14" type="number" min="1" max="99" bind:value={newRank} /></label>
      <select class={input} bind:value={newLend} aria-label="New role default">
        <option value="off">Off</option><option value="read">Read</option><option value="all">Write</option>
      </select>
      <button type="button" class={btn} disabled={!newRole || !!newRoleError} onclick={addNewRole}>+ Add role</button>
      {#if newRoleError}<span class="text-xs text-neg-400">{newRoleError}</span>{/if}
    </div>
  </div>

  <div>
    <p class="text-xs font-medium text-black-900 dark:text-white-100">What each role borrows from you</p>
    <p class={muted}>Only connectors on this agent. "Default" follows the role's default; guest has none, so Default is off for guests.</p>
    <div class="mt-2 overflow-x-auto rounded-lg border border-white-300 dark:border-navy-600">
      <table class="w-max min-w-full border-collapse text-xs">
        <thead>
          <tr class="text-left text-[11px] text-black-800 dark:text-black-600">
            <th class="sticky left-0 z-10 bg-white-100 px-2 py-2 font-medium dark:bg-navy-700">Connector</th>
            {#each roles.filter((r) => r !== "owner") as r (r)}<th class="whitespace-nowrap px-2 py-2 font-medium">{roleTitle(r)}</th>{/each}
            <th></th>
          </tr>
        </thead>
        <tbody>
          {#each lentIDs as id (id)}
            <tr class="border-t border-white-300 dark:border-navy-600" data-testid="lend-row">
              <th scope="row" class="sticky left-0 z-10 max-w-[9rem] truncate bg-white-100 px-2 py-2 text-left font-medium text-black-900 dark:bg-navy-700 dark:text-white-100" title={labelOf(id)}>{labelOf(id)}</th>
              {#each roles.filter((r) => r !== "owner") as r (r)}
                {@const v = cellValue(draft, r, id)}
                <td class="px-2 py-1.5">
                  <select class={input} value={v} aria-label={`${roleTitle(r)} on ${labelOf(id)}`} onchange={(e) => (draft = setCell(draft, r, id, (e.currentTarget as HTMLSelectElement).value as CellLevel | "default"))}>
                    <option value="default">Default</option>
                    <option value="off">Off</option>
                    <option value="read">Read</option>
                    <option value="all">Write</option>
                    {#if v === "pick"}<option value="pick" disabled>Picked ops</option>{/if}
                  </select>
                </td>
              {/each}
              <td class="px-1"><button type="button" class={x} aria-label="Stop lending" onclick={() => (draft = unlendConnector(draft, id))}>✕</button></td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
    {#if lendable.length}
      <div class="mt-2 flex flex-wrap items-center gap-1.5" data-testid="add-lend">
        <select class={input} bind:value={newLent} aria-label="Connector to lend">
          <option value="">Lend another connector…</option>
          {#each lendable as id (id)}<option value={id}>{labelOf(id)}</option>{/each}
        </select>
        <button type="button" class={btn} disabled={!newLent} onclick={() => { draft = lendConnector(draft, newLent); newLent = ""; }}>+ Lend</button>
      </div>
    {/if}
  </div>

  {#if error}<p class="text-xs text-neg-400" role="alert">{error}</p>{/if}

  <div class="flex flex-wrap items-center gap-2 border-t border-white-300 pt-3 dark:border-navy-600">
    <input class="{input} min-w-0 flex-1" placeholder="Why (kept in Changes)" bind:value={reason} aria-label="Reason" />
    <button type="button" class={btn} onclick={onCancel} disabled={saving}>Cancel</button>
    <button type="button" class="rounded-md bg-green-600 px-3 py-1 text-xs text-white-100 hover:bg-green-700 disabled:opacity-50" disabled={!changed || saving} data-testid="roles-save" onclick={() => (confirming = true)}>Save</button>
  </div>

  {#if confirming}
    <div class="rounded-lg border border-cau-300 bg-cau-100 p-3 text-xs text-black-900 dark:bg-navy-800 dark:text-cau-300" role="alertdialog" data-testid="roles-save-confirm">
      <p>Save these roles? They apply on this agent's next call, for everyone chatting with it. You can undo from Changes.</p>
      <div class="mt-2 flex gap-2">
        <button type="button" class={btn} onclick={() => (confirming = false)} disabled={saving}>Back</button>
        <button type="button" class="rounded-md bg-green-600 px-3 py-1 text-xs text-white-100 hover:bg-green-700" data-testid="roles-save-ok" disabled={saving} onclick={save}>{saving ? "Saving…" : "Save roles"}</button>
      </div>
    </div>
  {/if}
</div>
