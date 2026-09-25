<script lang="ts">
  /* Handoffs tab — what "two providers working the same project" looks like
     (PLAN §13.3).

     A handoff is a baton: the summary, open questions and next steps one
     session left for whoever comes next. An old one means nobody picked it
     up, which is why the age is the most prominent thing on a row.

     Cancelling is per id and nothing else. The backend's only CLI form is
     --expire-all, which discards every open baton including the ones other
     agents are waiting on — it is not exposed here at all (PLAN §20.2). */
  import { Button, Select } from "@wick-fe/common-ui";
  import Section from "./Section.svelte";
  import BlockedState from "./BlockedState.svelte";
  import { blockedBy } from "./format.js";
  import { handoffAge, handoffParties, messageSender, messageStateLabel, messageSummary, SENDER_NOTE } from "./handoffs.js";
  import type { Handoff, HandoffsResponse, MessagesResponse, ProjectRow } from "./types.js";

  type Props = {
    res: HandoffsResponse | null;
    messages: MessagesResponse | null;
    projects: ProjectRow[];
    scopeKey: string;
    loading: boolean;
    busy: boolean;
    cancelling: string;
    onScope: (key: string) => void;
    onRefresh: () => void;
    onCancel: (h: Handoff) => void;
    onGoOverview: () => void;
  };
  let {
    res,
    messages,
    projects,
    scopeKey,
    loading,
    busy,
    cancelling,
    onScope,
    onRefresh,
    onCancel,
    onGoOverview,
  }: Props = $props();

  const blocked = $derived(blockedBy(res));
  const rows = $derived(res?.handoffs ?? []);
  const mail = $derived(messages?.messages ?? []);

  // Both reads are project-scoped: the backend resolves an unscoped handoff
  // listing from a working directory, which would answer about whichever
  // project it fell back to. So this tab asks for one project at a time and
  // says which (PLAN §13.5 point 1, §14).
  const scopeOptions = $derived(projects.map((p) => ({ label: `${p.workspace}/${p.project}`, value: `${p.workspace}/${p.project}` })));
</script>

<div class="mx-auto w-full max-w-4xl space-y-5 px-4 py-5 sm:px-6 sm:py-6">
  <div class="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
    <div class="min-w-0">
      <p class="text-sm font-medium text-black-900 dark:text-white-100">Open batons and mail</p>
      <p class="mt-0.5 text-xs text-black-700 dark:text-black-600">
        Per project — both listings resolve from a scope, never store-wide.
      </p>
    </div>
    <div class="flex flex-wrap items-center gap-2">
      <div class="w-56">
        <Select value={scopeKey} options={scopeOptions} onChange={onScope} size="md" placeholder="Pick a project" />
      </div>
      <Button variant="secondary" size="md" disabled={loading || !scopeKey} onclick={onRefresh}>Refresh</Button>
    </div>
  </div>

  {#if !scopeKey}
    <div
      class="rounded-xl border border-dashed border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-6 py-10 text-center"
    >
      <p class="text-sm font-medium text-black-900 dark:text-white-100">Pick a project</p>
      <p class="mx-auto mt-1.5 max-w-md text-xs leading-relaxed text-black-700 dark:text-black-600">
        Handoffs and mail belong to one project. An unscoped listing would answer about whichever project the daemon
        resolved on its own, which is a wrong answer that looks right.
      </p>
    </div>
  {:else if blocked}
    <BlockedState {blocked} onAction={onGoOverview} />
  {:else}
    <Section
      title="Open handoffs"
      scope={scopeKey}
      note="A baton nobody picked up means the next session started without what the last one left behind."
    >
      {#if loading}
        <p class="px-5 py-8 text-center text-xs text-black-700 dark:text-black-600">Reading…</p>
      {:else if rows.length === 0}
        <p class="px-5 py-8 text-center text-xs leading-relaxed text-black-700 dark:text-black-600">
          No open handoffs. Either every baton was picked up, or no session in this project ended with one.
        </p>
      {:else}
        <ul class="divide-y divide-white-300 dark:divide-navy-600">
          {#each rows as h (h.id)}
            <li class="flex flex-wrap items-start justify-between gap-3 px-5 py-3">
              <div class="min-w-0">
                <p class="text-xs font-medium text-black-900 dark:text-white-100">{handoffParties(h)}</p>
                <p class="mt-0.5 flex flex-wrap items-center gap-2 text-[0.6875rem] text-black-700 dark:text-black-600">
                  <span>{handoffAge(h)}</span>
                  <span class="font-mono">{h.id}</span>
                </p>
                {#if h.cwd}
                  <p class="mt-0.5 truncate font-mono text-[0.6875rem] text-black-700 dark:text-black-600">{h.cwd}</p>
                {/if}
              </div>
              <Button
                variant="danger"
                size="sm"
                disabled={busy || cancelling === h.id}
                onclick={() => onCancel(h)}
              >
                {cancelling === h.id ? "Cancelling…" : "Cancel"}
              </Button>
            </li>
          {/each}
        </ul>
      {/if}
    </Section>

    <Section title="Cross-project mail" scope={`${scopeKey} · inbox`} note={SENDER_NOTE}>
      {#if messages?.error && !messages.reason}
        <p class="px-5 py-5 text-xs leading-relaxed text-neg-400 dark:text-neg-400">{messages.error}</p>
      {:else if mail.length === 0}
        <p class="px-5 py-8 text-center text-xs leading-relaxed text-black-700 dark:text-black-600">
          Nothing in this project's inbox. Mail is a directed, claim-once message from another project — it only exists
          when an agent sent one.
        </p>
      {:else}
        <ul class="divide-y divide-white-300 dark:divide-navy-600">
          {#each mail as m (m.id)}
            <li class="px-5 py-3">
              <div class="flex flex-wrap items-baseline justify-between gap-2">
                <p class="min-w-0 truncate text-xs font-medium text-black-900 dark:text-white-100">{messageSummary(m)}</p>
                <span class="text-[0.6875rem] uppercase tracking-wider text-black-700 dark:text-black-600">
                  {messageStateLabel(m)}
                </span>
              </div>
              <p class="mt-0.5 text-[0.6875rem] text-black-700 dark:text-black-600">
                from {messageSender(m)}{m.created_at ? ` · ${m.created_at}` : ""}
              </p>
              {#if m.body && m.subject}
                <p class="mt-1 whitespace-pre-wrap break-words text-[0.6875rem] leading-relaxed text-black-800 dark:text-black-600">
                  {m.body}
                </p>
              {/if}
            </li>
          {/each}
        </ul>
      {/if}
    </Section>
  {/if}
</div>
