<script lang="ts">
  /* Detail + edit for one schedule, shared by the conversation SPA's
     Scheduled tab and the global Scheduled page — both open it by clicking a
     row, so it lives here rather than being written twice.

     Read-only facts (id, provenance, run counts, last error) sit above the
     editable fields, so the modal doubles as the "detail" view: clicking a
     row should answer "what is this?" before it offers to change it.

     Timing edits reuse the server's own grammar (run_at / every / cron) via
     the reschedule endpoint; the caller supplies onSave and does the request. */
  import Modal from "./Modal.svelte";
  import type {
    EditableSchedule,
    SchedulePatchInput,
    ScheduleProjectOption,
    ScheduleSessionMode,
  } from "./schedule-edit-types.js";
  import {
    formatIntervalMs,
    formatScheduleTime,
    isLegalSessionID,
    isProjectScopedSchedule,
    renderSessionTemplate,
    scheduleCadence,
  } from "./schedule-edit-types.js";
  import { untrack } from "svelte";

  type Props = {
    open: boolean;
    schedule: EditableSchedule;
    /* Projects the caller may target. Empty hides the project picker, so a
       user with no reachable project can still edit timing and message. */
    projects?: ScheduleProjectOption[];
    /* Link to the session a fire landed in; omitted when the host has no
       session routing to offer. */
    sessionHref?: (sessionID: string) => string;
    saving?: boolean;
    onSave: (patch: SchedulePatchInput) => void;
    onClose: () => void;
  };

  let {
    open,
    schedule,
    projects = [],
    sessionHref,
    saving = false,
    onSave,
    onClose,
  }: Props = $props();

  const INPUT =
    "w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-sm text-black-900 dark:text-white-100 placeholder-black-600 dark:placeholder-black-700 focus:border-green-500 focus:ring-2 focus:ring-green-200 dark:focus:ring-green-800 focus:outline-none";

  const recurring = $derived(schedule.kind === "recurring");
  const projectScoped = $derived(isProjectScopedSchedule(schedule));
  const live = $derived(
    schedule.status === "pending" ||
      schedule.status === "active" ||
      schedule.status === "paused",
  );

  /* Form state is SEEDED from the row once per open, then owned by the modal
     so a background refresh can't overwrite what is being typed. `seeded`
     re-seeds when a different schedule is opened in the same mounted modal. */
  let seeded = $state("");
  let message = $state("");
  let timing = $state("");
  let timingKind = $state<"run_at" | "every" | "cron">("run_at");
  let maxRuns = $state("");
  // What the timing field was seeded with, so an untouched field is not
  // re-sent as an "edit" (which would make every save look like a change).
  let seededTiming = $state("");
  let seededKind = $state<"run_at" | "every" | "cron">("run_at");
  let projectId = $state("");
  let mode = $state<ScheduleSessionMode>("new");
  let template = $state("");
  let error = $state("");

  $effect(() => {
    if (!open || seeded === schedule.id) return;
    untrack(() => {
      seeded = schedule.id;
      error = "";
      message = schedule.message ?? "";
      maxRuns = schedule.max_runs ? String(schedule.max_runs) : "";
      projectId = schedule.project_id ?? "";
      mode = schedule.session_mode === "template" ? "template" : "new";
      template = schedule.session_template ?? "";
      // Seed the timing field with the row's CURRENT cadence, so leaving it
      // untouched is a no-op rather than a silent reset. seededTiming records
      // what was seeded, so build() can tell "unchanged" from "edited".
      if (schedule.cron) {
        timingKind = "cron";
        timing = schedule.cron;
      } else if (schedule.interval_ms) {
        timingKind = "every";
        timing = formatIntervalMs(schedule.interval_ms);
      } else {
        timingKind = "run_at";
        timing = "";
      }
      seededTiming = timing;
      seededKind = timingKind;
    });
  });

  const preview = $derived(
    projectScoped && mode === "template" && template.trim()
      ? renderSessionTemplate(template.trim(), (schedule.run_count ?? 0) + 1)
      : "",
  );
  const previewInvalid = $derived(preview !== "" && !isLegalSessionID(preview));

  /* Moving a job to another project moves it out of the project the user is
     looking at — it will disappear from this list. Say so before saving,
     rather than letting it vanish unexplained. */
  const movingProject = $derived(
    projectScoped && projectId !== "" && projectId !== (schedule.project_id ?? ""),
  );

  const lastRunHref = $derived(
    schedule.last_session_id && sessionHref ? sessionHref(schedule.last_session_id) : "",
  );

  function build(): SchedulePatchInput | null {
    const patch: SchedulePatchInput = {};

    const t = timing.trim();
    if (t && (t !== seededTiming || timingKind !== seededKind)) {
      if (timingKind === "cron") patch.cron = t;
      else if (timingKind === "every") patch.every = t;
      else patch.run_at = t;
    }

    const msg = message.trim();
    if (msg && msg !== schedule.message) patch.message = msg;

    if (recurring) {
      const raw = maxRuns.trim();
      const n = raw ? Number(raw) : 0;
      if (raw && (!Number.isFinite(n) || n < 0)) {
        error = "Max runs must be a positive number (or blank for no cap).";
        return null;
      }
      if (n !== (schedule.max_runs ?? 0)) patch.max_runs = n;
    }

    if (projectScoped) {
      if (!projectId) {
        error = "Pick a project.";
        return null;
      }
      if (mode === "template" && !template.trim()) {
        error = "Enter a session name pattern.";
        return null;
      }
      if (previewInvalid) {
        error = "Pattern makes an invalid session name (use letters, digits, . _ -).";
        return null;
      }
      const nextTemplate = mode === "template" ? template.trim() : "";
      const scopeChanged =
        projectId !== (schedule.project_id ?? "") ||
        mode !== schedule.session_mode ||
        nextTemplate !== (schedule.session_template ?? "");
      if (scopeChanged) {
        // Sent as a set: the server reads the three together to re-resolve the
        // target, and sending session_template unconditionally is what clears
        // a stale pattern when switching back to "new".
        patch.project_id = projectId;
        patch.session_mode = mode;
        patch.session_template = nextTemplate;
      }
    }

    if (Object.keys(patch).length === 0) {
      error = "Nothing changed.";
      return null;
    }
    return patch;
  }

  function save() {
    error = "";
    const patch = build();
    if (patch) onSave(patch);
  }

  const TIMING_KINDS: { id: "run_at" | "every" | "cron"; label: string; ph: string }[] = [
    { id: "run_at", label: "At", ph: "90m, 2h, 1d — or 2026-07-09T12:40:00Z" },
    { id: "every", label: "Every", ph: "5m, 90s, 1h30m, 1d" },
    { id: "cron", label: "Cron", ph: "0 9 * * 1  (min hour dom mon dow)" },
  ];
  const activePlaceholder = $derived(
    TIMING_KINDS.find((k) => k.id === timingKind)?.ph ?? "",
  );

  const MODES: { id: ScheduleSessionMode; label: string; hint: string }[] = [
    { id: "new", label: "New session each run", hint: "Every run starts with clean context." },
    { id: "template", label: "Named session", hint: "Runs sharing a name share one session." },
  ];
</script>

<Modal {open} {onClose} title="Schedule" size="lg">
  <div class="space-y-4" data-testid="schedule-edit">
    <!-- Facts: what this schedule IS, before offering to change it -->
    <dl class="grid grid-cols-2 gap-x-4 gap-y-2 text-xs">
      <div>
        <dt class="text-black-800 dark:text-black-600">Status</dt>
        <dd class="font-medium text-black-900 dark:text-white-100">
          {schedule.paused ? "paused" : schedule.status}
        </dd>
      </div>
      <div>
        <dt class="text-black-800 dark:text-black-600">Cadence</dt>
        <dd class="font-medium text-black-900 dark:text-white-100">
          {recurring ? scheduleCadence(schedule) : "once"}
          {#if schedule.cron && schedule.cron_timezone}
            <!-- Say which zone the cron was read in: "9am" in the wrong zone
                 is hours off, and it isn't guessable from the expression. -->
            <span class="block font-normal text-[11px] text-black-800 dark:text-black-600" data-testid="detail-timezone">
              {schedule.cron_timezone}
            </span>
          {/if}
        </dd>
      </div>
      <div>
        <dt class="text-black-800 dark:text-black-600">{live ? "Next run" : "Was due"}</dt>
        <dd class="font-medium text-black-900 dark:text-white-100">{formatScheduleTime(schedule.run_at)}</dd>
      </div>
      <div>
        <dt class="text-black-800 dark:text-black-600">Runs</dt>
        <dd class="font-medium text-black-900 dark:text-white-100 tabular-nums">
          {schedule.run_count}{#if schedule.max_runs}/{schedule.max_runs}{/if}
          {#if schedule.manual_runs}
            <!-- Manual runs are listed apart from the scheduled count, since
                 only the latter counts against max_runs. -->
            <span class="font-normal text-black-800 dark:text-black-600" data-testid="detail-manual-runs">
              (+{schedule.manual_runs} manual)
            </span>
          {/if}
          {#if schedule.last_run_at}<span class="font-normal text-black-800 dark:text-black-600"> · last {formatScheduleTime(schedule.last_run_at)}</span>{/if}
        </dd>
      </div>
      <div>
        <dt class="text-black-800 dark:text-black-600">Created by</dt>
        <dd class="font-medium text-black-900 dark:text-white-100">{schedule.created_by || "—"}</dd>
      </div>
      <div>
        <dt class="text-black-800 dark:text-black-600">Delivers to</dt>
        <dd class="font-medium text-black-900 dark:text-white-100" data-testid="detail-target">
          {#if !projectScoped}
            this session
          {:else if schedule.session_mode === "template"}
            {schedule.session_template} · {schedule.project_name || schedule.project_id}
          {:else}
            new session each run · {schedule.project_name || schedule.project_id}
          {/if}
        </dd>
      </div>
    </dl>

    {#if lastRunHref}
      <p class="text-xs text-black-800 dark:text-black-600">
        Last run landed in
        <a href={lastRunHref} class="font-medium text-black-900 dark:text-white-100 hover:text-green-700 dark:hover:text-green-400" data-testid="detail-last-run">
          {schedule.last_session_label || schedule.last_session_id}
        </a>
      </p>
    {/if}

    {#if schedule.last_error}
      <p class="rounded-lg bg-neg-100 dark:bg-neg-400/20 px-3 py-2 text-xs text-neg-400" data-testid="detail-error">
        {schedule.last_error}
      </p>
    {/if}

    <p class="font-mono text-[11px] text-black-700 dark:text-black-600 break-all">{schedule.id}</p>

    {#if live}
      <hr class="border-white-300 dark:border-navy-600" />

      <!-- Timing -->
      <div class="space-y-1.5">
        <span class="block text-xs font-medium text-black-800 dark:text-black-600">Timing</span>
        <div class="inline-flex overflow-hidden rounded-lg border border-white-400 dark:border-navy-600 text-xs font-medium">
          {#each TIMING_KINDS as k (k.id)}
            <!-- A one-shot can't become recurring (and vice versa) — the
                 server refuses the kind flip — so only offer the kinds that
                 match this schedule. -->
            {#if recurring ? k.id !== "run_at" : k.id === "run_at"}
              <button
                type="button"
                class={"px-3 py-1.5 transition-colors " +
                  (timingKind === k.id
                    ? "bg-green-500 text-white-100"
                    : "bg-white-100 dark:bg-navy-800 text-black-700 dark:text-white-200")}
                onclick={() => (timingKind = k.id)}
                data-testid={"edit-kind-" + k.id}
              >{k.label}</button>
            {/if}
          {/each}
        </div>
        <input class={INPUT} bind:value={timing} placeholder={activePlaceholder} data-testid="edit-timing" />
        <p class="text-[11px] text-black-800 dark:text-black-600">Leave blank to keep the current timing.</p>
      </div>

      <!-- Message -->
      <div class="space-y-1.5">
        <label class="block text-xs font-medium text-black-800 dark:text-black-600" for={"msg-" + schedule.id}>Message</label>
        <textarea id={"msg-" + schedule.id} class={INPUT} rows="4" bind:value={message} data-testid="edit-message"></textarea>
      </div>

      {#if recurring}
        <div class="space-y-1.5">
          <label class="block text-xs font-medium text-black-800 dark:text-black-600" for={"max-" + schedule.id}>Max runs</label>
          <input
            id={"max-" + schedule.id}
            class={INPUT}
            bind:value={maxRuns}
            inputmode="numeric"
            placeholder="blank = no cap"
            data-testid="edit-maxruns"
          />
        </div>
      {/if}

      {#if projectScoped}
        <!-- Scope: only project jobs have a target to configure. A session
             nudge's target was fixed at create time and can't move scope. -->
        <div class="space-y-1.5">
          <span class="block text-xs font-medium text-black-800 dark:text-black-600">Runs in</span>
          {#if projects.length > 0}
            <select class={INPUT} bind:value={projectId} data-testid="edit-project">
              {#if projectId && !projects.some((p) => p.id === projectId)}
                <!-- Current project not in the caller's options (renamed, or
                     access changed): keep it selectable so saving another
                     field doesn't silently repoint the job. -->
                <option value={projectId}>{schedule.project_name || projectId}</option>
              {/if}
              <option value="">Select a project…</option>
              {#each projects as p (p.id)}
                <option value={p.id}>{p.name}</option>
              {/each}
            </select>
          {/if}
          <div class="flex flex-col gap-1.5 pt-1">
            {#each MODES as m (m.id)}
              <label class="flex items-start gap-2 text-xs text-black-900 dark:text-white-100">
                <input
                  type="radio"
                  class="mt-0.5 accent-green-500"
                  checked={mode === m.id}
                  onchange={() => (mode = m.id)}
                  data-testid={"edit-mode-" + m.id}
                />
                <span class="space-y-0.5">
                  <span class="block font-medium">{m.label}</span>
                  <span class="block text-[11px] text-black-800 dark:text-black-600">{m.hint}</span>
                </span>
              </label>
            {/each}
          </div>

          {#if mode === "template"}
            <input
              class={INPUT}
              bind:value={template}
              placeholder="daily-report-&#123;date&#125;"
              data-testid="edit-template"
            />
            <p class="text-[11px] text-black-800 dark:text-black-600">
              Use &#123;date&#125;, &#123;datetime&#125;, &#123;ym&#125;, &#123;run&#125; or &#123;id&#125;. No placeholder = always the same session.
            </p>
            {#if preview}
              <p
                class={"text-[11px] " + (previewInvalid ? "text-neg-400" : "text-black-800 dark:text-black-600")}
                data-testid="edit-preview"
              >
                Next run lands in <span class="font-medium">{preview}</span>
              </p>
            {/if}
          {/if}

          {#if movingProject}
            <p class="rounded-lg bg-cau-400/10 px-3 py-2 text-[11px] text-cau-400" data-testid="edit-move-warning">
              Moving this job to another project — it will no longer show up here.
            </p>
          {/if}
        </div>
      {/if}

      {#if error}
        <p class="text-xs font-medium text-neg-400" data-testid="edit-error">{error}</p>
      {/if}
    {/if}
  </div>

  {#snippet footer()}
    <button
      type="button"
      class="rounded-lg px-3 py-2 text-sm font-medium text-black-800 dark:text-black-600 hover:text-black-900 dark:hover:text-white-100"
      onclick={onClose}
      data-testid="edit-cancel"
    >{live ? "Cancel" : "Close"}</button>
    {#if live}
      <button
        type="button"
        class="rounded-lg bg-green-500 px-4 py-2 text-sm font-medium text-white-100 hover:bg-green-600 active:bg-green-700 transition-colors disabled:opacity-50"
        disabled={saving}
        onclick={save}
        data-testid="edit-save"
      >{saving ? "Saving…" : "Save"}</button>
    {/if}
  {/snippet}
</Modal>
