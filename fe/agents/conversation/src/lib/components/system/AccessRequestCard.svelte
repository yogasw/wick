<script lang="ts">
  /* An agent's access request, drawn in the thread: one row per action with
     its status, and while anything waits, Review & decide / Deny. Both open
     a modal built from the server's view FOR THE SIGNED-IN PERSON: the
     actions they may decide get a mode picker, the rest stay visible with
     the reason and who can. A viewer with no access gets a "No access"
     panel instead. Every check happens on the server; the same decision
     from Slack or the web page updates this card too. */
  import { Effect, Either } from "effect";
  import { WickClientLayer } from "@wick-fe/common-api";
  import type { ConversationTurn } from "../../types/agents.js";
  import {
    accessCardOf, accessStatusIcon, itemLine, kindHint, noAccess, MODE_LABEL,
    type AccessRequestView,
  } from "../../accessRequests.js";
  import { getAccessRequestView, sendAccessDecisions, type AccessDecideResult } from "../../api/accessRequests.js";

  type Props = { turn: ConversationTurn };
  let { turn }: Props = $props();
  const card = $derived(accessCardOf(turn.text, turn.extras));

  let modal = $state<"" | "review" | "deny">("");
  let loading = $state(false);
  let saving = $state(false);
  let mine = $state<AccessRequestView | null>(null);
  let picks = $state<Record<string, string>>({});
  let note = $state("");
  let error = $state("");
  let failed = $state<string[]>([]);

  async function open(kind: "review" | "deny") {
    if (!card.api) return;
    modal = kind;
    loading = true;
    error = "";
    failed = [];
    picks = {};
    note = "";
    const res = await Effect.runPromise(Effect.either(getAccessRequestView(card.api).pipe(Effect.provide(WickClientLayer))));
    loading = false;
    if (Either.isLeft(res)) {
      error = res.left.message || "Could not load the request.";
      mine = null;
      return;
    }
    mine = res.right;
  }

  function close() {
    modal = "";
    mine = null;
  }

  async function save() {
    if (!mine || saving) return;
    const body = modal === "deny"
      ? { deny_all: true, note }
      : { decisions: Object.entries(picks).filter(([, m]) => !!m).map(([item_id, mode]) => ({ item_id, mode })), note };
    if (modal === "review" && (body.decisions?.length ?? 0) === 0) {
      error = "Pick a decision for at least one action.";
      return;
    }
    saving = true;
    error = "";
    const res = await Effect.runPromise(Effect.either(sendAccessDecisions(card.api, body).pipe(Effect.provide(WickClientLayer))));
    saving = false;
    if (Either.isLeft(res)) {
      error = res.left.message || "Could not save your decisions.";
      return;
    }
    const bad = (res.right.results as AccessDecideResult[]).filter((r) => !r.ok).map((r) => r.error ?? r.code ?? "refused");
    if (bad.length) {
      failed = bad;
      mine = res.right.view;
      return;
    }
    close();
  }

  const decidable = $derived(mine ? mine.items.filter((i) => i.can_decide) : []);
</script>

<div data-testid="access-request" data-state={card.open ? "pending" : "settled"} class="flex w-full max-w-md flex-col gap-1.5 rounded-xl border border-amber-400/40 bg-white-100 px-3 py-2 text-left text-xs shadow-sm dark:bg-navy-800">
  <div class="flex items-center gap-1.5 text-[11px] text-amber-700 dark:text-amber-300">
    <span aria-hidden="true">🔐</span>
    <span>{card.view?.agent ?? "The agent"} asks to run {card.view?.items.length ?? 0} action(s){card.view?.status === "cancelled" ? " · cancelled" : ""}</span>
  </div>
  {#if card.view?.reason}
    <p class="rounded-md border-l-2 border-amber-400 bg-white-200 px-2 py-1 text-[11px] text-black-800 dark:bg-navy-900 dark:text-black-600"><span class="text-black-700 dark:text-black-500">Agent's claim (unverified):</span> {card.view.reason}</p>
  {/if}
  {#each card.view?.items ?? [] as it (it.id)}
    <div class="flex flex-col gap-0.5" data-testid="access-item" data-status={it.status}>
      <div class="flex min-w-0 items-center gap-1.5">
        <span aria-hidden="true">{accessStatusIcon(it.status)}</span>
        <code class="truncate font-mono text-[11px] text-black-900 dark:text-white-100">{it.n}. {it.connector_key}.{it.op_key}</code>
        <span class="rounded-full border border-white-300 px-1.5 text-[10px] text-black-700 dark:border-navy-600 dark:text-black-500">{it.kind}</span>
      </div>
      {#if it.target}<span class="truncate pl-5 text-[11px] text-black-800 dark:text-black-600" title={it.target}>{it.target}</span>{/if}
      <span class={"pl-5 text-[11px] " + (it.status === "error" || it.status === "denied" ? "text-neg-400" : "text-black-700 dark:text-black-500")}>{itemLine(it)}</span>
    </div>
  {/each}
  {#if card.open && card.api}
    <div class="flex flex-wrap gap-1.5 pt-0.5">
      <button type="button" data-testid="access-review" onclick={() => open("review")} class="rounded-lg bg-green-500 px-3 py-1 text-xs font-medium text-white-100 hover:bg-green-600">Review &amp; decide</button>
      <button type="button" data-testid="access-deny" onclick={() => open("deny")} class="rounded-lg border border-neg-400/40 px-3 py-1 text-xs font-medium text-neg-400 hover:bg-neg-400/10">Deny</button>
    </div>
  {/if}
</div>

{#if modal}
  <div class="fixed inset-0 z-50 flex items-center justify-center bg-black-900/50 p-4" role="presentation" onclick={(e) => { if (e.target === e.currentTarget) close(); }}>
    <div role="dialog" aria-modal="true" data-testid="access-modal" class="flex max-h-[85vh] w-full max-w-xl flex-col gap-3 overflow-y-auto rounded-xl border border-white-300 bg-white-100 p-4 text-sm text-black-900 shadow-lg dark:border-navy-600 dark:bg-navy-800 dark:text-white-100">
      {#if loading}
        <p class="text-black-700 dark:text-black-500">Loading the request…</p>
      {:else if !mine}
        <p class="text-neg-400">{error}</p>
        <div class="flex justify-end"><button type="button" onclick={close} class="rounded-lg border border-white-300 px-3 py-1 text-xs dark:border-navy-600">Close</button></div>
      {:else if noAccess(mine)}
        <div data-testid="access-no-access" class="flex flex-col gap-2">
          <strong>⛔ You do not have access</strong>
          <p class="text-xs">{mine.viewer_reason}</p>
          <ul class="flex flex-col gap-1 text-xs">
            {#each mine.items as it (it.id)}
              <li><span aria-hidden="true">{accessStatusIcon(it.status)}</span> <code class="font-mono text-[11px]">{it.connector_key}.{it.op_key}</code> {it.target}
                {#if it.status === "pending"}<div class="pl-5 text-[11px] text-black-700 dark:text-black-500">Who can: {it.approvers.join(", ") || "nobody yet"}</div>{/if}</li>
            {/each}
          </ul>
          <div class="flex justify-end"><button type="button" onclick={close} class="rounded-lg border border-white-300 px-3 py-1 text-xs dark:border-navy-600">Close</button></div>
        </div>
      {:else if modal === "deny"}
        <div class="flex flex-col gap-2" data-testid="access-deny-modal">
          <strong>Deny these actions {mine.agent} asked for?</strong>
          <ul class="flex flex-col gap-1 text-xs">
            {#each decidable as it (it.id)}<li><code class="font-mono text-[11px]">{it.connector_key}.{it.op_key}</code> {it.target}</li>{/each}
          </ul>
          {#if mine.items.some((i) => !i.can_decide && i.status === "pending")}
            <p class="text-[11px] text-black-700 dark:text-black-500">Actions outside your access stay waiting for another approver.</p>
          {/if}
          {#if decidable.length === 0}<p class="text-xs">{mine.viewer_reason || "Nothing left for you to deny."}</p>{/if}
          <label class="flex flex-col gap-1 text-xs">Reason (sent to the agent)<textarea bind:value={note} rows="3" class="rounded-lg border border-white-300 bg-white-200 p-2 text-xs dark:border-navy-600 dark:bg-navy-900"></textarea></label>
          {#if error}<p class="text-xs text-neg-400">{error}</p>{/if}
          {#each failed as f}<p class="text-xs text-neg-400">{f}</p>{/each}
          <div class="flex justify-end gap-2">
            <button type="button" onclick={close} class="rounded-lg border border-white-300 px-3 py-1 text-xs dark:border-navy-600">Cancel</button>
            <button type="button" data-testid="access-deny-confirm" disabled={saving || decidable.length === 0} onclick={save} class="rounded-lg border border-neg-400 px-3 py-1 text-xs font-medium text-neg-400 hover:bg-neg-400/10 disabled:opacity-50">Deny</button>
          </div>
        </div>
      {:else}
        <div class="flex flex-col gap-3" data-testid="access-review-modal">
          <div>
            <strong>{mine.agent} asks to run {mine.items.length} action(s)</strong>
            {#if mine.asked_by}<div class="text-[11px] text-black-700 dark:text-black-500">Asked while answering {mine.asked_by}</div>{/if}
            {#if mine.requester}<div class="text-[11px] text-black-700 dark:text-black-500">Asked in a conversation of {mine.requester}</div>{/if}
            {#if mine.reason}<p class="mt-1 rounded-md border-l-2 border-amber-400 bg-white-200 px-2 py-1 text-xs dark:bg-navy-900"><span class="text-black-700 dark:text-black-500">Agent's claim (unverified):</span> {mine.reason}</p>{/if}
          </div>
          {#each mine.items as it (it.id)}
            <div class="flex flex-col gap-1 rounded-lg border border-white-300 p-2 dark:border-navy-600" data-testid="access-modal-item">
              <div class="flex flex-wrap items-center gap-1.5 text-xs">
                <span aria-hidden="true">{accessStatusIcon(it.status)}</span>
                <code class="font-mono text-[11px]">{it.n}. {it.connector_key}.{it.op_key}</code>
                {#if it.connector_label}<span class="text-black-700 dark:text-black-500">{it.connector_label}</span>{/if}
                <span class="rounded-full border border-white-300 px-1.5 text-[10px] dark:border-navy-600">{it.kind}</span>
              </div>
              {#if it.target}<div class="text-xs"><span class="text-black-700 dark:text-black-500">Target:</span> {it.target}</div>{/if}
              {#if it.can_decide}<div class="text-xs"><span class="text-black-700 dark:text-black-500">Runs as:</span> your own account ✅</div>{/if}
              <details class="text-xs"><summary class="cursor-pointer text-black-700 dark:text-black-500">Parameters (secrets masked)</summary>
                <pre class="mt-1 max-h-60 overflow-auto whitespace-pre-wrap break-all rounded-md bg-white-200 p-2 font-mono text-[11px] dark:bg-navy-900">{it.params_json}</pre>
              </details>
              {#if it.can_decide}
                <div class="flex flex-wrap gap-3 text-xs" role="radiogroup">
                  {#each [...it.modes, "deny"] as m}
                    <label class="inline-flex cursor-pointer items-center gap-1"><input type="radio" name={"mode-" + it.id} value={m} bind:group={picks[it.id]} /> {MODE_LABEL[m] ?? m}</label>
                  {/each}
                </div>
                {#if kindHint(it.kind)}<div class="text-[11px] text-black-700 dark:text-black-500">{kindHint(it.kind)}</div>{/if}
              {:else if it.status === "pending"}
                <div class="rounded-md border-l-2 border-neg-400 bg-white-200 px-2 py-1 text-[11px] dark:bg-navy-900" data-testid="access-locked">⛔ {it.locked_reason}</div>
              {:else}
                <div class="text-[11px] text-black-700 dark:text-black-500">{itemLine(it)}</div>
              {/if}
            </div>
          {/each}
          {#if mine.viewer_can_decide}
            <label class="flex flex-col gap-1 text-xs">Note for the agent (sent with a deny)<textarea bind:value={note} rows="2" class="rounded-lg border border-white-300 bg-white-200 p-2 text-xs dark:border-navy-600 dark:bg-navy-900"></textarea></label>
            <p class="text-[11px] text-black-700 dark:text-black-500">Leave an action unselected to keep it waiting. Approved actions run right away with your access. The timed choices (1, 8, 24 hours, This session) cover the same operation on the same target, with the same config override, in this session only; an action with no clear target, or one a require rule matches, is approved once. Standing access is set with rules.</p>
          {:else if mine.viewer_reason}
            <p class="text-xs text-black-700 dark:text-black-500">{mine.viewer_reason}</p>
          {/if}
          {#if error}<p class="text-xs text-neg-400">{error}</p>{/if}
          {#each failed as f}<p class="text-xs text-neg-400">{f}</p>{/each}
          <div class="flex justify-end gap-2">
            <button type="button" onclick={close} class="rounded-lg border border-white-300 px-3 py-1 text-xs dark:border-navy-600">{mine.viewer_can_decide ? "Cancel" : "Close"}</button>
            {#if mine.viewer_can_decide}
              <button type="button" data-testid="access-save" disabled={saving} onclick={save} class="rounded-lg bg-green-500 px-3 py-1 text-xs font-medium text-white-100 hover:bg-green-600 disabled:opacity-50">Save decisions</button>
            {/if}
          </div>
        </div>
      {/if}
    </div>
  </div>
{/if}
