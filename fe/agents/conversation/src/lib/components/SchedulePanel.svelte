<script lang="ts">
  import { ScheduleEditModal, scheduleCadence as cadence } from "@wick-fe/common-ui";
  import type { SchedulePatchInput } from "@wick-fe/common-ui";
  import type { Schedule } from "../types/agents.js";

  type CreateArgs = {
    message: string;
    runAt?: string;
    every?: string;
    cron?: string;
    maxRuns?: number;
    projectId?: string;
    sessionMode?: "existing" | "new" | "template";
    sessionTemplate?: string;
  };

  type ProjectOption = { id: string; name: string };

  type Props = {
    schedules: Schedule[];
    /* onCreate returns a promise so the panel clears the form only on success. */
    onCreate: (args: CreateArgs) => Promise<boolean>;
    onCancel: (id: string) => void;
    onPause: (id: string) => void;
    onResume: (id: string) => void;
    /* Clicking a row opens the detail/edit modal; omit to keep rows inert. */
    onReschedule?: (id: string, patch: SchedulePatchInput) => void;
    onRunNow?: (id: string) => void;
    /* Projects the user may target, for the "runs in" picker. Empty hides the
       project options entirely — with nowhere to run, offering them is a
       dead end. */
    projects?: ProjectOption[];
    /* This session's own project, pre-selected when switching to project
       scope: scheduling a job from a conversation almost always means "in the
       project I'm already working in". */
    currentProjectId?: string;
  };

  let {
    schedules,
    onCreate,
    onCancel,
    onPause,
    onResume,
    onReschedule,
    onRunNow,
    projects = [],
    currentProjectId = "",
  }: Props = $props();

  /* Modal state lives here, keyed by id and re-resolved from `schedules`, so a
     background refresh replacing the row objects doesn't remount the modal
     (which would wipe a half-typed edit). */
  let openID = $state("");
  const openRow = $derived(schedules.find((s) => s.id === openID));

  /* Status filter. Without one, a session that has run a few schedules buries
     the live ones under every finished and cancelled row — the list is
     newest-fire-first, and a done row's fire time is in the past, so the
     things you can still act on sink to the bottom. Live is the default view;
     the counts let you see there IS history without showing it. */
  type ListFilter = "live" | "done" | "all";
  let listFilter = $state<ListFilter>("live");

  // "paused" is a live status — still resumable, so it keeps its actions.
  const rowIsLive = (s: Schedule) =>
    s.status === "pending" || s.status === "active" || s.status === "paused";
  const rowIsDone = (s: Schedule) =>
    s.status === "done" || s.status === "cancelled" || s.status === "failed";

  const liveCount = $derived(schedules.filter(rowIsLive).length);
  const doneCount = $derived(schedules.filter(rowIsDone).length);

  const visible = $derived(
    schedules.filter((s) =>
      listFilter === "live" ? rowIsLive(s) : listFilter === "done" ? rowIsDone(s) : true,
    ),
  );

  const LIST_FILTERS = $derived([
    { id: "live" as ListFilter, label: "Live", count: liveCount },
    { id: "done" as ListFilter, label: "Finished", count: doneCount },
    { id: "all" as ListFilter, label: "All", count: schedules.length },
  ]);

  /* The create form starts collapsed when there is already something to look
     at, and open on an empty panel (where the form IS the content). */
  let composerOpen = $state(schedules.length === 0);

  const INPUT_CLASS =
    "w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-sm text-black-900 dark:text-white-100 placeholder-black-600 dark:placeholder-black-700 focus:border-green-500 focus:ring-2 focus:ring-green-200 dark:focus:ring-green-800 focus:outline-none";

  type Mode = "once" | "repeat";
  let mode = $state<Mode>("once");

  /* one-shot presets + custom */
  const ONCE_PRESETS = [
    { label: "in 20 min", value: "20m" },
    { label: "in 1 hour", value: "1h" },
    { label: "in 5 hours", value: "5h" },
    { label: "tomorrow", value: "1d" },
    { label: "custom…", value: "custom" },
  ];
  let onceWhen = $state("1h");
  let onceCustom = $state("");

  /* recurring: interval preset OR cron */
  const EVERY_PRESETS = [
    { label: "every 5 min", value: "5m" },
    { label: "every 30 min", value: "30m" },
    { label: "every hour", value: "1h" },
    { label: "every day", value: "1d" },
    { label: "custom interval…", value: "custom" },
    { label: "cron…", value: "cron" },
  ];
  let repeatEvery = $state("5m");
  let repeatCustom = $state("");
  let repeatCron = $state("");
  let maxRuns = $state("");

  let message = $state("");
  let submitting = $state(false);
  let formError = $state("");

  /* Where each fire lands. "existing" = nudge this conversation (the default,
     and what the panel did before project scope existed). The other two run in
     a project: "new" opens a fresh session per fire (clean context), "template"
     reuses a session named from the fire time. */
  type Target = "existing" | "new" | "template";
  let target = $state<Target>("existing");
  let targetProject = $state("");
  let sessionTemplate = $state("");

  const TARGETS: { id: Target; label: string; hint: string }[] = [
    { id: "existing", label: "This session", hint: "Delivered into this conversation." },
    { id: "new", label: "New session each run", hint: "Every run starts with clean context." },
    { id: "template", label: "Named session", hint: "Runs sharing a name share one session." },
  ];

  /* Picking a project target defaults to this session's project — the common
     case — but the user can still choose another they can reach. */
  function selectTarget(t: Target) {
    target = t;
    if (t !== "existing" && !targetProject) {
      targetProject = currentProjectId || (projects.length === 1 ? projects[0].id : "");
    }
  }

  /* Mirror of the server's RenderTemplate, so the user sees the session id a
     fire would land in. The backend re-validates and is the authority. */
  function renderPreview(tpl: string): string {
    const now = new Date();
    const p = (n: number) => String(n).padStart(2, "0");
    const date = `${now.getUTCFullYear()}-${p(now.getUTCMonth() + 1)}-${p(now.getUTCDate())}`;
    return tpl
      .replaceAll("{datetime}", `${date}-${p(now.getUTCHours())}${p(now.getUTCMinutes())}`)
      .replaceAll("{date}", date)
      .replaceAll("{ym}", `${now.getUTCFullYear()}-${p(now.getUTCMonth() + 1)}`)
      .replaceAll("{run}", "1")
      .replaceAll("{id}", "abc12345");
  }

  const templatePreview = $derived(
    target === "template" && sessionTemplate.trim() ? renderPreview(sessionTemplate.trim()) : "",
  );

  const canSubmit = $derived(!submitting && message.trim().length > 0);

  /* Target fields, or null when the choice is incomplete (which buildArgs
     reports as a form error rather than silently scheduling into the wrong
     place). */
  function targetArgs(): Pick<CreateArgs, "projectId" | "sessionMode" | "sessionTemplate"> | null {
    if (target === "existing") return {};
    if (!targetProject) return null;
    if (target === "template" && !sessionTemplate.trim()) return null;
    return {
      projectId: targetProject,
      sessionMode: target,
      sessionTemplate: target === "template" ? sessionTemplate.trim() : undefined,
    };
  }

  function buildArgs(): CreateArgs | null {
    const msg = message.trim();
    const max = maxRuns.trim() ? Number(maxRuns.trim()) : undefined;
    const tgt = targetArgs();
    if (!tgt) return null;
    if (mode === "once") {
      const runAt = onceWhen === "custom" ? onceCustom.trim() : onceWhen;
      if (!runAt) return null;
      return { message: msg, runAt, ...tgt };
    }
    if (repeatEvery === "cron") {
      const cron = repeatCron.trim();
      if (!cron) return null;
      return { message: msg, cron, maxRuns: max, ...tgt };
    }
    const every = repeatEvery === "custom" ? repeatCustom.trim() : repeatEvery;
    if (!every) return null;
    return { message: msg, every, maxRuns: max, ...tgt };
  }

  async function submit() {
    if (!canSubmit) return;
    const args = buildArgs();
    if (!args) {
      // Distinguish the two ways the form can be incomplete, so the message
      // points at the field that's actually missing.
      formError = targetArgs()
        ? "Fill in the timing."
        : target === "template" && targetProject
          ? "Enter a session name pattern."
          : "Pick a project to run in.";
      return;
    }
    submitting = true;
    formError = "";
    try {
      const ok = await onCreate(args);
      if (ok) {
        message = "";
        onceCustom = "";
        repeatCustom = "";
        repeatCron = "";
        maxRuns = "";
      }
    } catch (e: unknown) {
      formError = e instanceof Error ? e.message : String(e);
    } finally {
      submitting = false;
    }
  }

  function statusBadgeCls(status: string): string {
    switch (status) {
      case "pending":
      case "active":
        return "bg-green-100 text-green-700 dark:bg-green-900 dark:text-green-300";
      case "done":
        return "bg-white-300 text-black-700 dark:bg-navy-700 dark:text-white-200";
      case "failed":
        return "bg-neg-100 text-neg-400 dark:bg-neg-400/20 dark:text-neg-300";
      default: /* cancelled */
        return "bg-white-300 text-black-600 dark:bg-navy-700 dark:text-black-600";
    }
  }

  function fmtWhen(iso: string): string {
    const d = new Date(iso);
    if (isNaN(d.getTime())) return iso;
    return d.toLocaleString(undefined, {
      month: "short",
      day: "numeric",
      hour: "2-digit",
      minute: "2-digit",
    });
  }

</script>

<div class="flex-1 overflow-y-auto p-3 space-y-3">
  <!-- Create form. Collapsed by default: the list is what you return to, and
       an always-open form pushed it off-screen once a session had a few rows. -->
  <div class="rounded-xl border border-white-300 dark:border-navy-600 p-3 space-y-2">
    <button
      type="button"
      class="flex w-full items-center gap-1.5 text-left text-xs font-medium text-black-800 dark:text-white-200"
      onclick={() => (composerOpen = !composerOpen)}
      data-testid="toggle-composer"
    >
      <svg
        viewBox="0 0 16 16"
        class={"h-3 w-3 shrink-0 transition-transform " + (composerOpen ? "rotate-90" : "")}
        fill="none"
        stroke="currentColor"
        stroke-width="2"
      >
        <path d="M6 4l4 4-4 4" stroke-linecap="round" stroke-linejoin="round"></path>
      </svg>
      Schedule a message
    </button>
    {#if composerOpen}

    <!-- Mode toggle -->
    <div class="inline-flex rounded-lg border border-white-400 dark:border-navy-600 overflow-hidden text-xs font-medium">
      <button
        type="button"
        class={"px-3 py-1.5 transition-colors " + (mode === "once" ? "bg-green-500 text-white-100" : "bg-white-100 dark:bg-navy-800 text-black-700 dark:text-white-200")}
        onclick={() => (mode = "once")}
        data-testid="mode-once"
      >Once</button>
      <button
        type="button"
        class={"px-3 py-1.5 transition-colors " + (mode === "repeat" ? "bg-green-500 text-white-100" : "bg-white-100 dark:bg-navy-800 text-black-700 dark:text-white-200")}
        onclick={() => (mode = "repeat")}
        data-testid="mode-repeat"
      >Repeat</button>
    </div>

    {#if mode === "once"}
      <select class={INPUT_CLASS} bind:value={onceWhen} data-testid="once-when">
        {#each ONCE_PRESETS as p (p.value)}
          <option value={p.value}>{p.label}</option>
        {/each}
      </select>
      {#if onceWhen === "custom"}
        <input
          class={INPUT_CLASS}
          bind:value={onceCustom}
          placeholder="90m, 2h, 1d — or 2026-07-09T12:40:00Z"
          data-testid="once-custom"
        />
      {/if}
    {:else}
      <select class={INPUT_CLASS} bind:value={repeatEvery} data-testid="repeat-when">
        {#each EVERY_PRESETS as p (p.value)}
          <option value={p.value}>{p.label}</option>
        {/each}
      </select>
      {#if repeatEvery === "custom"}
        <input
          class={INPUT_CLASS}
          bind:value={repeatCustom}
          placeholder="5m, 90s, 1h30m, 1d"
          data-testid="repeat-custom"
        />
      {:else if repeatEvery === "cron"}
        <input
          class={INPUT_CLASS}
          bind:value={repeatCron}
          placeholder="0 9 * * 1  (min hour dom mon dow)"
          data-testid="repeat-cron"
        />
      {/if}
      <input
        class={INPUT_CLASS}
        bind:value={maxRuns}
        inputmode="numeric"
        placeholder="Max runs (optional — blank = forever)"
        data-testid="repeat-maxruns"
      />
    {/if}

    {#if projects.length > 0}
      <!-- Target: this conversation, or a standalone job in a project -->
      <div class="space-y-1.5 pt-1">
        <span class="block text-[11px] font-medium text-black-800 dark:text-black-600">Runs in</span>
        <div class="flex flex-col gap-1.5">
          {#each TARGETS as t (t.id)}
            <label class="flex items-start gap-2 text-xs text-black-900 dark:text-white-100">
              <input
                type="radio"
                class="mt-0.5 accent-green-500"
                checked={target === t.id}
                onchange={() => selectTarget(t.id)}
                data-testid={"target-" + t.id}
              />
              <span class="space-y-0.5">
                <span class="block font-medium">{t.label}</span>
                <span class="block text-[11px] text-black-800 dark:text-black-600">{t.hint}</span>
              </span>
            </label>
          {/each}
        </div>

        {#if target !== "existing"}
          <select class={INPUT_CLASS} bind:value={targetProject} data-testid="target-project">
            <option value="">Select a project…</option>
            {#each projects as p (p.id)}
              <option value={p.id}>{p.name}</option>
            {/each}
          </select>
        {/if}

        {#if target === "template"}
          <input
            class={INPUT_CLASS}
            bind:value={sessionTemplate}
            placeholder="daily-report-&#123;date&#125;"
            data-testid="target-template-pattern"
          />
          <p class="text-[11px] text-black-700 dark:text-black-600">
            Use &#123;date&#125;, &#123;datetime&#125;, &#123;ym&#125;, &#123;run&#125; or &#123;id&#125;. No placeholder = always the same session.
          </p>
          {#if templatePreview}
            <p class="text-[11px] text-black-700 dark:text-black-600" data-testid="target-preview">
              First run lands in <span class="font-medium">{templatePreview}</span>
            </p>
          {/if}
        {/if}
      </div>
    {/if}

    <textarea
      class={INPUT_CLASS}
      rows="3"
      bind:value={message}
      placeholder={target === "existing"
        ? "Message to deliver into this session when it fires…"
        : "Message to start each run with…"}
      data-testid="sched-message"
    ></textarea>

    {#if formError}
      <p class="text-xs font-medium text-neg-400" data-testid="sched-error">{formError}</p>
    {/if}

    <button
      type="button"
      class="rounded-lg bg-green-500 px-4 py-2 text-sm font-medium text-white-100 hover:bg-green-600 active:bg-green-700 transition-colors disabled:opacity-50"
      disabled={!canSubmit}
      onclick={submit}
    >{submitting ? "Scheduling…" : "Schedule"}</button>
    {/if}
  </div>

  <!-- List -->
  {#if schedules.length === 0}
    <p class="text-xs text-black-700 dark:text-black-600">No scheduled messages.</p>
  {:else}
    <!-- Status tabs: keep finished rows out of the way of live ones -->
    <div class="flex items-center gap-1 border-b border-white-300 dark:border-navy-600">
      {#each LIST_FILTERS as f (f.id)}
        <button
          type="button"
          class={"relative -mb-px px-2.5 py-1.5 text-xs font-medium transition-colors " +
            (listFilter === f.id
              ? "text-green-700 dark:text-green-400 border-b-2 border-green-500"
              : "text-black-800 dark:text-black-600 border-b-2 border-transparent hover:text-black-900 dark:hover:text-white-200")}
          onclick={() => (listFilter = f.id)}
          data-testid={"list-filter-" + f.id}
        >
          {f.label}
          <span class="ml-1 tabular-nums text-black-700 dark:text-black-600">{f.count}</span>
        </button>
      {/each}
    </div>

    {#if visible.length === 0}
      <p class="text-xs text-black-700 dark:text-black-600" data-testid="list-empty">
        {listFilter === "live" ? "Nothing scheduled right now." : "No finished schedules."}
      </p>
    {/if}

    {#each visible as s (s.id)}
      <div
        class="rounded-xl border border-white-300 dark:border-navy-600 p-3 space-y-1.5"
        data-sid={s.id}
      >
        <!-- The summary opens the detail/edit modal. A real button so keyboard
             and screen readers get it for free; the action row stays outside
             it, since buttons may not nest. -->
        <svelte:element
          this={onReschedule ? "button" : "div"}
          type={onReschedule ? "button" : undefined}
          class={"block w-full space-y-1.5 text-left " + (onReschedule ? "cursor-pointer" : "")}
          onclick={onReschedule ? () => (openID = s.id) : undefined}
          role={onReschedule ? "button" : undefined}
          data-testid={onReschedule ? "row-open" : undefined}
        >
        <div class="flex items-center gap-2 flex-wrap">
          {#if s.kind === "recurring"}
            <span class="text-xs font-medium text-black-900 dark:text-white-100">{cadence(s)}</span>
          {:else}
            <span class="text-xs font-medium text-black-900 dark:text-white-100">{s.run_at ? fmtWhen(s.run_at) : s.last_run_at ? fmtWhen(s.last_run_at) : "—"}</span>
          {/if}
          <span class={"shrink-0 rounded-full px-2 py-0.5 text-[10px] font-medium " + statusBadgeCls(s.status)}>
            {s.paused ? "paused" : s.status}
          </span>
          {#if s.created_by === "ai"}
            <span class="shrink-0 rounded-full px-2 py-0.5 text-[10px] font-medium bg-white-300 text-black-600 dark:bg-navy-700 dark:text-black-600">
              by agent
            </span>
          {/if}
          {#if s.session_mode && s.session_mode !== "existing"}
            <!-- A project job listed here was created from this conversation
                 but does NOT deliver into it, so say where it goes. -->
            <span
              class="shrink-0 rounded-full px-2 py-0.5 text-[10px] font-medium bg-green-100 text-green-700 dark:bg-green-900 dark:text-green-300"
              data-testid="row-scope"
            >
              {s.session_mode === "template" ? s.session_template || "named session" : "new session each run"}
            </span>
          {/if}
        </div>

        <p class="text-xs text-black-800 dark:text-white-200 whitespace-pre-wrap break-words">{s.message}</p>

        <!-- Meta line: next / last / count for recurring -->
        {#if s.kind === "recurring"}
          <p class="text-[11px] text-black-700 dark:text-black-600">
            {#if !s.paused && s.run_at}next {fmtWhen(s.run_at)} · {/if}
            {#if s.last_run_at}last {fmtWhen(s.last_run_at)} · {/if}
            ran {s.run_count}{#if s.max_runs}/{s.max_runs}{/if}×
          </p>
        {/if}

        {#if s.last_error}
          <p class="text-[11px] text-neg-400">{s.last_error}</p>
        {/if}
        </svelte:element>

        <!-- Actions -->
        {#if rowIsLive(s)}
          <div class="flex items-center gap-3 pt-0.5">
            {#if s.kind === "recurring"}
              {#if s.paused}
                <button type="button" class="text-[11px] font-medium text-green-600 dark:text-green-400 hover:underline" onclick={() => onResume(s.id)}>Resume</button>
              {:else}
                <button type="button" class="text-[11px] font-medium text-black-700 dark:text-black-600 hover:underline" onclick={() => onPause(s.id)}>Pause</button>
              {/if}
            {/if}
            {#if onRunNow}
              <button
                type="button"
                class="text-[11px] font-medium text-black-700 dark:text-black-600 hover:underline"
                onclick={() => onRunNow(s.id)}
                data-testid="run-now"
              >Run now</button>
            {/if}
            <button type="button" class="ml-auto text-[11px] font-medium text-neg-400 hover:underline" onclick={() => onCancel(s.id)}>Cancel</button>
          </div>
        {/if}
      </div>
    {/each}
  {/if}
</div>

{#if openRow && onReschedule}
  <ScheduleEditModal
    open={true}
    schedule={openRow}
    {projects}
    onSave={(patch) => {
      onReschedule(openRow.id, patch);
      openID = "";
    }}
    onClose={() => (openID = "")}
  />
{/if}
