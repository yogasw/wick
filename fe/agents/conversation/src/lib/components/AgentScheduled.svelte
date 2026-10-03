<script lang="ts">
  /* Scheduled drawer (⋯ → Scheduled): the schedules that fire into this
     agent — the ones made here and the ones the agent made for itself. A
     toggle pauses/resumes, ⋯ runs now / edits / deletes. Create uses a
     button; editing an existing schedule autosaves. ⋯ → History lists the
     last runs, each opening the chat it answered in. */
  import { Button, Toggle } from "@wick-fe/common-ui";
  import { toastOk } from "@wick-fe/common-stores";
  import DrawerHeader from "./DrawerHeader.svelte";
  import {
    getAgentScheduled, createAgentSchedule, updateAgentSchedule, deleteAgentSchedule, agentScheduleAction, getAgentScheduleRuns, runApi,
    type AgentItem, type AgentSchedule, type AgentScheduledList, type AgentScheduleRuns,
  } from "../api/team.js";
  import {
    whenLabel, fmtTime, statusOf, isLive, emptyDraft, draftOf, draftError, bodyOf, kindOf, destLabel, runStatus,
    type ScheduleDraft,
  } from "../scheduledForm.js";

  type Props = {
    base: string;
    agent: AgentItem;
    onClose: () => void;
    onOpenTools?: () => void;
    /** Opens the chat a run answered in. */
    onOpenSession?: (sessionId: string) => void;
  };
  let { base, agent, onClose, onOpenTools, onOpenSession }: Props = $props();

  let data = $state<AgentScheduledList | null>(null);
  let loadError = $state("");
  /** null = list; "new" = create form; an id = editing that row. */
  let editing = $state<string | null>(null);
  let draft = $state<ScheduleDraft>(emptyDraft());
  let formError = $state("");
  let busy = $state(false);
  let saveState = $state<"" | "saving" | "saved" | "error">("");
  let menuFor = $state<string | null>(null);
  let confirmDelete = $state<AgentSchedule | null>(null);
  let saveTimer: ReturnType<typeof setTimeout> | undefined;
  /** The row whose run history is open, and what it loaded. */
  let historyFor = $state<string | null>(null);
  let runs = $state<AgentScheduleRuns | null>(null);
  let runsError = $state("");

  const input =
    "w-full rounded-lg border border-white-300 bg-white-100 px-3 py-2 text-sm text-black-900 focus:border-green-500 focus:outline-none dark:border-navy-600 dark:bg-navy-800 dark:text-white-100";
  const label = "mb-1 block text-xs font-medium text-black-800 dark:text-black-600";
  const muted = "text-xs text-black-800 dark:text-black-600";
  const TONE: Record<string, string> = {
    ok: "bg-green-100 text-green-700 dark:bg-green-900 dark:text-green-300",
    muted: "bg-white-200 text-black-800 dark:bg-navy-600 dark:text-black-600",
    warn: "bg-yellow-100 text-yellow-800",
    error: "bg-neg-100 text-neg-400",
  };

  async function load() {
    try {
      data = await runApi(getAgentScheduled(base, agent.id));
      loadError = "";
    } catch (e) {
      loadError = e instanceof Error ? e.message : String(e);
    }
  }
  $effect(() => {
    void agent.id;
    void load();
  });

  const editingRow = $derived(editing && editing !== "new" ? data?.items.find((s) => s.id === editing) ?? null : null);
  const problem = $derived(draftError(draft));
  const tgChats = $derived(data?.telegram_chats ?? []);
  const slackChannels = $derived(data?.slack_channels ?? []);

  function pickDest(dest: ScheduleDraft["dest"]) {
    draft.dest = dest;
    if (dest === "telegram" && !draft.tgSession && tgChats.length > 0) draft.tgSession = tgChats[0].session_id;
    if (dest === "slack" && !draft.slackChannel && slackChannels.length > 0) draft.slackChannel = slackChannels[0].id;
    queueSave();
  }

  /** "3 on · 1 paused": a paused schedule is not counted as on. */
  function activeSummary(items: AgentSchedule[]): string {
    const live = items.filter(isLive);
    const paused = live.filter((s) => s.paused).length;
    return paused ? `${live.length - paused} on · ${paused} paused` : `${live.length} on`;
  }

  async function toggleHistory(s: AgentSchedule) {
    menuFor = null;
    if (historyFor === s.id) {
      historyFor = null;
      return;
    }
    historyFor = s.id;
    runs = null;
    runsError = "";
    try {
      const r = await runApi(getAgentScheduleRuns(base, agent.id, s.id));
      if (historyFor === s.id) runs = r;
    } catch (e) {
      if (historyFor === s.id) runsError = e instanceof Error ? e.message : String(e);
    }
  }

  function openNew() {
    draft = emptyDraft();
    formError = saveState = "";
    editing = "new";
  }
  function openEdit(s: AgentSchedule) {
    menuFor = null;
    draft = draftOf(s);
    formError = saveState = "";
    editing = s.id;
  }
  function closeForm() {
    clearTimeout(saveTimer);
    editing = null;
    void load();
  }

  function replace(row: AgentSchedule) {
    if (!data) return;
    data.items = data.items.map((s) => (s.id === row.id ? row : s));
  }

  async function create() {
    if (problem) return;
    busy = true;
    formError = "";
    try {
      await runApi(createAgentSchedule(base, agent.id, bodyOf(draft)));
      toastOk("Scheduled");
      closeForm();
    } catch (e) {
      formError = e instanceof Error ? e.message : String(e);
    } finally {
      busy = false;
    }
  }

  /** Autosave of the edit form, debounced like the Settings tabs. */
  function queueSave() {
    if (editing === "new" || !editingRow) return;
    clearTimeout(saveTimer);
    if (problem) return;
    saveTimer = setTimeout(save, 600);
  }
  async function save() {
    if (!editingRow || problem) return;
    saveState = "saving";
    formError = "";
    try {
      replace(await runApi(updateAgentSchedule(base, agent.id, editingRow.id, bodyOf(draft))));
      saveState = "saved";
    } catch (e) {
      saveState = "error";
      formError = e instanceof Error ? e.message : String(e);
    }
  }

  async function toggle(s: AgentSchedule, on: boolean) {
    try {
      replace(await runApi(agentScheduleAction(base, agent.id, s.id, on ? "resume" : "pause")));
    } catch (e) {
      loadError = e instanceof Error ? e.message : String(e);
    }
  }
  async function runNow(s: AgentSchedule) {
    menuFor = null;
    try {
      replace(await runApi(agentScheduleAction(base, agent.id, s.id, "run")));
      toastOk("Running now");
    } catch (e) {
      loadError = e instanceof Error ? e.message : String(e);
    }
  }
  async function remove(s: AgentSchedule) {
    confirmDelete = null;
    try {
      await runApi(deleteAgentSchedule(base, agent.id, s.id));
      if (data) data.items = data.items.filter((x) => x.id !== s.id);
    } catch (e) {
      loadError = e instanceof Error ? e.message : String(e);
    }
  }
</script>

<DrawerHeader title="Scheduled" subtitle={`@${agent.handle} — runs on its own, results land in the chat`} avatar={agent.avatar} {onClose} />

<div class="min-h-0 flex-1 overflow-y-auto px-6 py-4" data-testid="scheduled-drawer">
  {#if loadError}<p class="mb-3 text-xs text-neg-400" data-testid="scheduled-error">{loadError}</p>{/if}

  {#if data && !data.feature_on}
    <div class="rounded-lg border border-white-300 p-4 dark:border-navy-600" data-testid="scheduled-off">
      <p class="text-sm font-medium text-black-900 dark:text-white-100">Scheduling is off for this agent</p>
      <p class="mt-1 {muted}">Turn on “Schedule” in Settings › Tools &amp; features so the agent (and you) can schedule work here.</p>
      {#if onOpenTools}<div class="mt-3"><Button size="sm" variant="ghost" onclick={onOpenTools}>Open Tools &amp; features</Button></div>{/if}
    </div>
  {:else if data && editing}
    <!-- Create / edit form -->
    <div class="space-y-4" data-testid="scheduled-form">
      <div>
        <label class={label} for="sch-msg">Message to the agent</label>
        <textarea id="sch-msg" rows="4" class={input} bind:value={draft.message} oninput={queueSave} placeholder="e.g. Summarise yesterday's open tickets and flag anything urgent."></textarea>
      </div>
      <div>
        <span class={label}>When</span>
        <div class="inline-flex rounded-lg border border-white-300 p-0.5 dark:border-navy-600" role="group" aria-label="When">
          {#each [["once", "Once at"], ["every", "Every"], ["cron", "Cron"]] as [m, lbl] (m)}
            <button
              type="button"
              class="rounded-md px-3 py-1 text-xs {draft.mode === m ? 'bg-green-500 text-white-100' : 'text-black-800 dark:text-black-600'} disabled:opacity-40"
              aria-pressed={draft.mode === m}
              disabled={!!editingRow && kindOf(m as ScheduleDraft["mode"]) !== editingRow.kind}
              onclick={() => { draft.mode = m as ScheduleDraft["mode"]; queueSave(); }}
            >{lbl}</button>
          {/each}
        </div>
        <div class="mt-2">
          {#if draft.mode === "once"}
            <input type="datetime-local" class={input} bind:value={draft.at} oninput={queueSave} aria-label="Run at" />
          {:else if draft.mode === "every"}
            <div class="flex gap-2">
              <input type="number" min="1" class="{input} w-24" bind:value={draft.every} oninput={queueSave} aria-label="Every" />
              <select class={input} bind:value={draft.unit} onchange={queueSave} aria-label="Unit">
                <option value="m">minutes</option>
                <option value="h">hours</option>
                <option value="d">days</option>
              </select>
            </div>
          {:else}
            <input class="{input} font-mono" bind:value={draft.cron} oninput={queueSave} aria-label="Cron" placeholder="0 9 * * 1-5" />
            <p class="mt-1 {muted}">minute hour day month weekday</p>
          {/if}
          <p class="mt-1 {muted}" data-testid="server-tz">Server time zone: {data.server_timezone}</p>
          {#if editingRow}<p class="mt-1 {muted}">To switch between once and repeating, delete this one and create a new schedule.</p>{/if}
        </div>
      </div>
      <div>
        <span class={label}>Send the result to</span>
        {#if editingRow && editingRow.destination !== "main" && editingRow.destination !== "telegram" && editingRow.destination !== "slack"}
          <label class="flex items-center gap-2 text-sm text-black-900 dark:text-white-100">
            <input type="radio" name="sch-dest" checked={draft.dest === "other"} onchange={() => pickDest("other")} /> {destLabel(editingRow)} <span class={muted}>(where it runs now)</span>
          </label>
        {/if}
        <label class="flex items-center gap-2 text-sm text-black-900 dark:text-white-100">
          <input type="radio" name="sch-dest" checked={draft.dest === "main"} onchange={() => pickDest("main")} /> Main chat
        </label>
        {#if data.telegram_connected}
          <label class="mt-1 flex items-center gap-2 text-sm text-black-900 dark:text-white-100" data-testid="dest-telegram">
            <input type="radio" name="sch-dest" checked={draft.dest === "telegram"} disabled={tgChats.length === 0} onchange={() => pickDest("telegram")} /> Telegram chat
          </label>
          {#if tgChats.length === 0}
            <p class="pl-5 {muted}">Nobody has messaged the agent's Telegram bot yet — a bot can only post to a chat that wrote to it.</p>
          {:else if draft.dest === "telegram"}
            <div class="mt-1 pl-5">
              <select class={input} bind:value={draft.tgSession} onchange={queueSave} aria-label="Telegram chat">
                {#each tgChats as c (c.session_id)}<option value={c.session_id}>{c.title}</option>{/each}
              </select>
            </div>
          {/if}
        {/if}
        {#if data.slack_ready}
          <label class="mt-1 flex items-center gap-2 text-sm text-black-900 dark:text-white-100" data-testid="dest-slack">
            <input type="radio" name="sch-dest" checked={draft.dest === "slack"} onchange={() => pickDest("slack")} /> Slack channel
          </label>
          {#if draft.dest === "slack"}
            <div class="mt-1 pl-5">
              <!-- Saved on change, not per keystroke: each new channel opens a thread there. -->
              <input
                class="{input} font-mono"
                list="sch-slack-channels"
                bind:value={draft.slackChannel}
                onchange={queueSave}
                aria-label="Slack channel"
                placeholder="C0123ABCD or a channel link"
              />
              <datalist id="sch-slack-channels">
                {#each slackChannels as c (c.id)}<option value={c.id}></option>{/each}
              </datalist>
              <p class="mt-1 {muted}">The bot posts “⏰ Scheduled …” there and answers every run in that thread — invite the bot to the channel first.</p>
            </div>
          {/if}
        {/if}
      </div>
      {#if problem}<p class={muted} data-testid="form-problem">{problem}</p>{/if}
      {#if formError}<p class="text-xs text-neg-400" data-testid="form-error">{formError}</p>{/if}
      <div class="flex items-center gap-2">
        {#if editing === "new"}
          <Button size="sm" disabled={busy || !!problem} onclick={create}>{busy ? "Saving…" : "Create"}</Button>
          <Button size="sm" variant="ghost" onclick={closeForm}>Cancel</Button>
        {:else}
          <Button size="sm" variant="ghost" onclick={closeForm}>Done</Button>
          <span class={muted} data-testid="save-state">{saveState === "saving" ? "Saving…" : saveState === "saved" ? "Saved" : saveState === "error" ? "Not saved" : ""}</span>
        {/if}
      </div>
    </div>
  {:else if data}
    {#if data.agent_disabled}
      <p class="mb-3 rounded-lg bg-white-200 px-3 py-2 {muted} dark:bg-navy-600">This agent is off, so its schedules are on hold. They resume when you turn it back on.</p>
    {/if}
    <div class="mb-3 flex items-center justify-between">
      <p class={muted}>{data.items.length === 0 ? "Nothing scheduled yet." : activeSummary(data.items)}</p>
      <Button size="sm" onclick={openNew}>New schedule</Button>
    </div>
    <ul class="space-y-2" data-testid="scheduled-list">
      {#each data.items as s (s.id)}
        {@const st = statusOf(s)}
        <li class="rounded-lg border border-white-300 p-3 dark:border-navy-600" data-testid="scheduled-row">
          <div class="flex items-start gap-3">
            <div class="min-w-0 flex-1">
              <p class="truncate text-sm font-medium text-black-900 dark:text-white-100">{s.title || "(no message)"}</p>
              <p class="mt-0.5 {muted}">{whenLabel(s)} · {destLabel(s, tgChats)}</p>
              <p class="mt-0.5 {muted}">
                {#if isLive(s) && !s.paused && s.next_run_at}Next {fmtTime(s.next_run_at)}{/if}
                {#if s.last_run_at}{isLive(s) && !s.paused && s.next_run_at ? " · " : ""}Last {fmtTime(s.last_run_at)}{/if}
              </p>
              {#if s.last_error}<p class="mt-0.5 truncate text-xs text-neg-400">{s.last_error}</p>{/if}
            </div>
            <span class="shrink-0 rounded-full px-2 py-0.5 text-xs {TONE[st.tone]}">{st.label}</span>
            {#if isLive(s)}
              <Toggle checked={!s.paused} disabled={s.kind !== "recurring"} onChange={(v: boolean) => toggle(s, v)} label="On" />
            {/if}
            <div class="relative">
              <button type="button" class="rounded px-1.5 text-black-800 hover:bg-white-200 dark:text-black-600 dark:hover:bg-navy-600" aria-label="More" onclick={() => (menuFor = menuFor === s.id ? null : s.id)}>⋯</button>
              {#if menuFor === s.id}
                <div class="absolute right-0 z-10 mt-1 w-36 rounded-lg border border-white-300 bg-white-100 py-1 shadow-lg dark:border-navy-600 dark:bg-navy-700" role="menu">
                  {#if isLive(s)}
                    <button type="button" role="menuitem" class="block w-full px-3 py-1.5 text-left text-sm hover:bg-white-200 dark:hover:bg-navy-600" onclick={() => runNow(s)}>Run now</button>
                    <button type="button" role="menuitem" class="block w-full px-3 py-1.5 text-left text-sm hover:bg-white-200 dark:hover:bg-navy-600" onclick={() => openEdit(s)}>Edit</button>
                  {/if}
                  <button type="button" role="menuitem" class="block w-full px-3 py-1.5 text-left text-sm hover:bg-white-200 dark:hover:bg-navy-600" onclick={() => toggleHistory(s)}>{historyFor === s.id ? "Hide history" : "History"}</button>
                  <button type="button" role="menuitem" class="block w-full px-3 py-1.5 text-left text-sm text-neg-400 hover:bg-white-200 dark:hover:bg-navy-600" onclick={() => { menuFor = null; confirmDelete = s; }}>Delete</button>
                </div>
              {/if}
            </div>
          </div>
          {#if historyFor === s.id}
            <div class="mt-2 border-t border-white-300 pt-2 dark:border-navy-600" data-testid="scheduled-history">
              <p class="mb-1 text-xs font-medium text-black-900 dark:text-white-100">Last runs</p>
              {#if runsError}
                <p class="text-xs text-neg-400">{runsError}</p>
              {:else if !runs}
                <p class={muted}>Loading…</p>
              {:else if runs.items.length === 0}
                <p class={muted}>{runs.last_error ? `No run reached the chat. Last error: ${runs.last_error}` : "It hasn't run yet."}</p>
              {:else}
                <ul class="space-y-1">
                  {#each runs.items as r (r.turn_id || r.at)}
                    {@const rs = runStatus(r)}
                    <li class="flex items-center gap-2 text-xs" data-testid="run-row">
                      <span class="w-36 shrink-0 text-black-900 dark:text-white-100">{fmtTime(r.at)}</span>
                      <span class="shrink-0 rounded-full px-2 py-0.5 {TONE[rs.tone]}">{rs.label}</span>
                      <span class="min-w-0 flex-1 truncate text-neg-400" title={r.error}>{r.error ?? ""}</span>
                      {#if onOpenSession}
                        <button type="button" class="shrink-0 text-green-600 hover:underline dark:text-green-400" onclick={() => onOpenSession(r.session_id)}>Open message</button>
                      {/if}
                    </li>
                  {/each}
                </ul>
              {/if}
            </div>
          {/if}
          {#if confirmDelete?.id === s.id}
            <div class="mt-2 flex items-center gap-2 rounded-lg bg-white-200 px-3 py-2 dark:bg-navy-600" data-testid="confirm-delete">
              <span class="flex-1 text-xs text-black-900 dark:text-white-100">Delete this schedule?</span>
              <Button size="sm" variant="danger" onclick={() => remove(s)}>Delete</Button>
              <Button size="sm" variant="ghost" onclick={() => (confirmDelete = null)}>Cancel</Button>
            </div>
          {/if}
        </li>
      {/each}
    </ul>
  {:else if !loadError}
    <p class={muted}>Loading…</p>
  {/if}
</div>
