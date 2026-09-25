<script lang="ts">
  /* Settings tab — the eight blocks of PLAN §13.4, block H excepted: the
     per-instance toggle lives on the Provider page, because it is a property
     of an agent, not of the daemon.

     Two rules run through the whole screen.

     One: a setting whose cost is invisible carries that cost as a sentence
     next to it. Assistant capture, a non-loopback allowlist, a retention age,
     a cloud model provider — each of those is a decision about client data,
     and none of them looks like one at the moment you flip it.

     Two: nothing here is applied by rewriting the backend's config.toml. Every
     field rides the daemon's launch line, and an UNSET field is not sent at
     all — so "" and 0 mean "whatever your config file says", which is why the
     retention block has to spell out that 0 is itself a real setting. */
  import { Button, LabeledInput, NumberInput, Select, TextArea, TextInput, Toggle } from "@wick-fe/common-ui";
  import { Section } from "@wick-fe/common-agentmemory";
  import SettingRow from "./SettingRow.svelte";
  import {
    accessWarning,
    autostartNote,
    backfillCapNote,
    CAPTURE_ASSISTANT_WARNING,
    CAPTURE_MODE_SCOPE_NOTE,
    CAPTURE_MODES,
    EXTERNAL_WARNING,
    externalExample,
    externalPathsNote,
    externalStatus,
    isDirty,
    PROJECT_STRATEGIES,
    PROMPT_CAPTURE_NOTE,
    PROVIDER_MODES,
    portNote,
    providerMode,
    RERANKER_NOTE,
    restartNote,
    rejectionLine,
    retentionSummary,
    TOKEN_SHOWN_ONCE,
    ZERO_LLM_NOTE,
  } from "./settings.js";
  import { MANAGE_ADMIN_ONLY } from "./format.js";
  import type { AutostartLock, ExternalState, Settings, SweepReport, TestResult } from "./types.js";

  type Props = {
    form: Settings | null;
    stored: Settings | null;
    lock: AutostartLock | null;
    defaultPort: number;
    running: boolean;
    restartPending: boolean;
    saving: boolean;
    busy: boolean;
    test: TestResult | null;
    sweep: SweepReport | null;
    sweepError: string;
    advanced: boolean;
    // canManage false = a viewer: the fields are shown (the server masks the
    // one secret for everyone) but nothing here can be submitted, so the save
    // bar and every write button are left out and the controls are inert
    // (PLAN §23.2, §23.3).
    canManage: boolean;
    // ── external access ──────────────────────────────────────────────
    // Optional, with closed defaults: a page that has not read the state
    // yet renders the block as shut rather than as open.
    external?: ExternalState | null;
    // mintedToken is non-empty ONLY for as long as a freshly created token
    // is on screen. Nothing stores it — the server never sends it again.
    mintedToken?: string;
    externalBusy?: boolean;
    onSetExternal?: (next: boolean) => void;
    onMintToken?: () => void;
    onRevokeToken?: () => void;
    onDismissToken?: () => void;
    onField: <K extends keyof Settings>(key: K, value: Settings[K]) => void;
    onSave: () => void;
    onReset: () => void;
    onTest: () => void;
    onCaptureAssistant: (next: boolean) => void;
    onPreviewSweep: () => void;
    onRunSweep: () => void;
    onToggleAdvanced: () => void;
  };
  let {
    form,
    stored,
    lock,
    defaultPort,
    running,
    restartPending,
    saving,
    busy,
    test,
    sweep,
    sweepError,
    advanced,
    canManage,
    external = null,
    mintedToken = "",
    externalBusy = false,
    onSetExternal = () => {},
    onMintToken = () => {},
    onRevokeToken = () => {},
    onDismissToken = () => {},
    onField,
    onSave,
    onReset,
    onTest,
    onCaptureAssistant,
    onPreviewSweep,
    onRunSweep,
    onToggleAdvanced,
  }: Props = $props();

  // readOnly is the inverse of canManage, named for what it does to a field.
  const readOnly = $derived(!canManage);
  const dirty = $derived(isDirty(form, stored));
  const access = $derived(form ? accessWarning(form) : null);
  const retention = $derived(retentionSummary(form?.observation_retention_days ?? 0));
  const mode = $derived(form ? providerMode(form) : "zero");
  const captureModeBody = $derived(
    CAPTURE_MODES.find((m) => m.value === (form?.capture_mode || "denylist"))?.body ?? "",
  );
  const strategyBody = $derived(PROJECT_STRATEGIES.find((s) => s.value === (form?.project_strategy ?? ""))?.body ?? "");

  const LOG_LEVELS = [
    { label: "Backend default", value: "" },
    { label: "error", value: "error" },
    { label: "warn", value: "warn" },
    { label: "info", value: "info" },
    { label: "debug", value: "debug" },
  ];
</script>

{#if !form}
  <div class="mx-auto w-full max-w-3xl px-6 py-12 text-center text-sm text-black-800 dark:text-black-600">
    Loading settings…
  </div>
{:else}
  <div class="mx-auto w-full max-w-4xl space-y-5 px-4 py-5 sm:px-6 sm:py-6">
    <!-- Save bar. Sticky because the blocks below are long and a change made
         at the bottom must not need a scroll to commit. -->
    <div
      class="sticky top-0 z-10 flex flex-wrap items-center justify-between gap-3 rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-5 py-3"
    >
      <p class="min-w-0 text-xs text-black-700 dark:text-black-600">
        {#if readOnly}
          {MANAGE_ADMIN_ONLY}
        {:else if dirty}
          Unsaved changes.
        {:else}
          {restartNote(restartPending, running)}
        {/if}
      </p>
      {#if !readOnly}
        <div class="flex flex-wrap items-center gap-2">
          <Button variant="ghost" size="md" disabled={!dirty || saving} onclick={onReset}>Discard</Button>
          <Button variant="primary" size="md" disabled={!dirty || saving} onclick={onSave}>
            {saving ? "Saving…" : "Save"}
          </Button>
        </div>
      {/if}
    </div>

    <!-- ── A. daemon ──────────────────────────────────────────────── -->
    <Section title="Daemon" scope="this backend">
      <div class="divide-y divide-white-300 dark:divide-navy-600">
        <SettingRow label="Start with wick" note={autostartNote(form.autostart_locked, lock?.reason)}>
          <div class="flex justify-start sm:justify-end">
            <Toggle
              checked={form.autostart || form.autostart_locked}
              disabled={readOnly || form.autostart_locked}
              label="Start the daemon when wick boots"
              onChange={(v) => onField("autostart", v)}
            />
          </div>
        </SettingRow>

        <SettingRow label="Port" note={portNote(form.port, defaultPort, undefined)}>
          <NumberInput disabled={readOnly} value={form.port} min={0} max={65535} onChange={(v) => onField("port", v)} ariaLabel="Port" />
        </SettingRow>

        <SettingRow
          label="Store location"
          note="Absolute path. One daemon holds every workspace and project in one store — the separation that matters happens on the project axis, not by splitting stores."
        >
          <TextInput disabled={readOnly}
            value={form.data_dir}
            onChange={(v) => onField("data_dir", v)}
            placeholder="Backend default"
            ariaLabel="Store location"
          />
        </SettingRow>

        <SettingRow
          label="Web API"
          note="Serves the project list and search this panel reads. With it off, those two report that they are unavailable rather than showing an empty store."
        >
          <div class="flex justify-start sm:justify-end">
            <Toggle disabled={readOnly} checked={form.enable_web} label="Web API" onChange={(v) => onField("enable_web", v)} />
          </div>
        </SettingRow>

        <SettingRow
          label="Base path"
          note="Mounts everything — MCP, the API, the hook endpoint and the web UI — under one prefix, for running behind a reverse proxy."
        >
          <TextInput disabled={readOnly}
            value={form.base_path}
            onChange={(v) => onField("base_path", v)}
            placeholder="/"
            ariaLabel="Base path"
          />
        </SettingRow>

        <SettingRow label="Log level" note="Raise it only while diagnosing: debug logs every hook event.">
          <Select disabled={readOnly} value={form.log_level} options={LOG_LEVELS} onChange={(v) => onField("log_level", v)} />
        </SettingRow>
      </div>
    </Section>

    <!-- ── B. access & security ───────────────────────────────────── -->
    <Section
      title="Access & security"
      scope="this daemon"
      note="The daemon always binds loopback. This block is about who is allowed to reach it by name."
    >
      {#if access}
        <div
          class={`border-b px-5 py-3 ${
            access.level === "danger"
              ? "border-neg-400 bg-neg-100 dark:bg-navy-800"
              : "border-cau-400 bg-cau-100 dark:bg-navy-800"
          }`}
        >
          <p
            class={`text-xs font-medium ${access.level === "danger" ? "text-neg-400" : "text-cau-400"}`}
          >
            {access.title}
          </p>
          <p class="mt-1 text-[0.6875rem] leading-relaxed text-black-800 dark:text-black-600">{access.body}</p>
        </div>
      {/if}
      <div class="divide-y divide-white-300 dark:divide-navy-600">
        <SettingRow
          label="Allowed hosts"
          note="Comma-separated Host-header allowlist. Leave it on the loopback default unless something off this machine genuinely has to read the store."
        >
          <TextInput disabled={readOnly}
            value={form.allowed_hosts}
            onChange={(v) => onField("allowed_hosts", v)}
            placeholder="localhost, 127.0.0.1, ::1"
            ariaLabel="Allowed hosts"
          />
        </SettingRow>

        <SettingRow
          label="Bearer token"
          note="Required on every request when set. A stored token is shown as dots and is left alone unless you type a new one; clearing the field removes it."
        >
          <TextInput disabled={readOnly}
            value={form.auth_token}
            onChange={(v) => onField("auth_token", v)}
            type="password"
            placeholder="No token — loopback only"
            ariaLabel="Bearer token"
          />
        </SettingRow>

        <SettingRow label="Connection" note="Checks the daemon answers its health path, and reads the store so the result carries a version and real counts.">
          <div class="flex flex-col items-start gap-2 sm:items-end">
            {#if !readOnly}
              <Button variant="secondary" size="md" disabled={busy} onclick={onTest}>Test connection</Button>
            {/if}
            {#if test}
              <p class={`text-[0.6875rem] ${test.ok ? "text-pos-400" : "text-neg-400"}`}>
                {test.ok
                  ? `${test.version ?? "answered"} · ${test.counts?.sessions ?? 0} sessions`
                  : (test.error ?? "no answer")}
              </p>
            {/if}
          </div>
        </SettingRow>
      </div>
    </Section>

    <!-- ── B2. reaching the store from outside wick ───────────────── -->
    <!-- Admin only, and LEFT OUT for everyone else rather than disabled:
         the sticky bar above already carries the one line explaining who
         may manage this (PLAN §23.3). -->
    {#if !readOnly}
      <Section title="External access" scope="this daemon" note={EXTERNAL_WARNING}>
        <div class="divide-y divide-white-300 dark:divide-navy-600">
          <SettingRow label="Reachable from outside wick" note={externalStatus(external)} id="external-note">
            <div class="flex justify-start sm:justify-end">
              <Toggle
                checked={external?.enabled ?? false}
                disabled={externalBusy || !(external?.has_token ?? false)}
                label="Let callers outside wick reach this store with an access token"
                describedBy="external-note"
                onChange={(v) => onSetExternal(v)}
              />
            </div>
          </SettingRow>

          <SettingRow
            label="Access token"
            note={(external?.has_token ?? false)
              ? "A token exists. It is stored encrypted and is never shown again — create a new one to replace it, or revoke to close this off entirely."
              : "No token yet. Create one to be able to turn the switch on; every external request is refused until then."}
          >
            <div class="flex flex-wrap items-center gap-2 sm:justify-end">
              <Button variant="secondary" size="md" disabled={externalBusy} onclick={onMintToken}>
                {(external?.has_token ?? false) ? "Create new token" : "Create token"}
              </Button>
              {#if external?.has_token}
                <Button variant="ghost" size="md" disabled={externalBusy} onclick={onRevokeToken}>Revoke</Button>
              {/if}
            </div>
          </SettingRow>

          {#if mintedToken}
            <!-- The one moment the plaintext token exists on screen. -->
            <div class="bg-cau-100 px-5 py-3 dark:bg-navy-800">
              <p class="text-xs font-medium text-cau-400">Your new access token</p>
              <code
                class="mt-2 block break-all rounded-lg border border-cau-400 bg-white-100 px-3 py-2 font-mono text-xs text-black-900 dark:bg-navy-700 dark:text-white-100"
                data-testid="minted-token">{mintedToken}</code
              >
              <p class="mt-2 text-[0.6875rem] leading-relaxed text-black-800 dark:text-black-600">{TOKEN_SHOWN_ONCE}</p>
              <div class="mt-2">
                <Button variant="ghost" size="sm" onclick={onDismissToken}>I have copied it</Button>
              </div>
            </div>
          {/if}

          {#if external?.url}
            <SettingRow label="Address" note={externalPathsNote(external)}>
              <code class="block break-all font-mono text-[0.6875rem] text-black-800 dark:text-black-600">
                {externalExample(external)}
              </code>
            </SettingRow>
          {/if}

          {#if external && (external.rejected_total > 0 || external.allowed_total > 0)}
            <SettingRow
              label="Refused requests"
              note={`${external.allowed_total} forwarded, ${external.rejected_total} refused since wick started. A refusal is shown here so "why can't my script reach it?" has an answer.`}
            >
              <div class="flex flex-col items-start gap-1 sm:items-end">
                {#each external.recent.slice(0, 5) as r (r.time_ms + r.path + r.reason)}
                  <p class="text-[0.6875rem] text-neg-400">{rejectionLine(r)}</p>
                {/each}
                {#if external.recent.length === 0}
                  <p class="text-[0.6875rem] text-black-700 dark:text-black-600">Nothing refused yet.</p>
                {/if}
              </div>
            </SettingRow>
          {/if}
        </div>
      </Section>
    {/if}

    <!-- ── C. capture & privacy ───────────────────────────────────── -->
    <Section
      title="Capture & privacy"
      scope="sessions wick starts"
      note={CAPTURE_MODE_SCOPE_NOTE}
    >
      <div class="divide-y divide-white-300 dark:divide-navy-600">
        <SettingRow label="Capture mode" note={captureModeBody}>
          <Select disabled={readOnly}
            value={form.capture_mode || "denylist"}
            options={CAPTURE_MODES.map((m) => ({ label: m.label, value: m.value }))}
            onChange={(v) => onField("capture_mode", v)}
          />
        </SettingRow>

        <SettingRow
          label="Store the assistant's replies"
          note={CAPTURE_ASSISTANT_WARNING}
          danger={true}
          id="capture-assistant-note"
        >
          <div class="flex justify-start sm:justify-end">
            <!-- Routed through the parent so it can raise a confirmation
                 before turning ON. Off needs no ceremony; on does. -->
            <Toggle disabled={readOnly}
              checked={form.capture_assistant}
              label="Store the assistant's final message on every session stop"
              describedBy="capture-assistant-note"
              onChange={onCaptureAssistant}
            />
          </div>
        </SettingRow>

        <SettingRow label="Capture prompts" note={PROMPT_CAPTURE_NOTE}>
          <div class="flex justify-start sm:justify-end">
            <Toggle disabled={readOnly}
              checked={!form.no_capture_prompts}
              label="Capture prompt text"
              onChange={(v) => onField("no_capture_prompts", !v)}
            />
          </div>
        </SettingRow>

        <SettingRow label="Project strategy" note={strategyBody}>
          <Select disabled={readOnly}
            value={form.project_strategy}
            options={PROJECT_STRATEGIES.map((s) => ({ label: s.label, value: s.value }))}
            onChange={(v) => onField("project_strategy", v)}
          />
        </SettingRow>

        <div class="px-5 py-4">
          <LabeledInput
            label="Extra redaction patterns"
            helper="One regex per line, redacted on top of the built-in patterns. An invalid one stops the daemon from starting."
          >
            <TextArea disabled={readOnly}
              value={form.sanitize_extra_patterns}
              onChange={(v) => onField("sanitize_extra_patterns", v)}
              rows={3}
              placeholder={"PROJ-[0-9a-f]{32}"}
              ariaLabel="Extra redaction patterns"
            />
          </LabeledInput>
        </div>

        <div class="px-5 py-4">
          <LabeledInput
            label="Never redact"
            helper="One substring per line. For a public identifier that the generic *_KEY catch-all would otherwise hide."
          >
            <TextArea disabled={readOnly}
              value={form.sanitize_allowlist}
              onChange={(v) => onField("sanitize_allowlist", v)}
              rows={2}
              placeholder="PUBLIC_VERIFY_KEY"
              ariaLabel="Never redact"
            />
          </LabeledInput>
        </div>

        <SettingRow
          label="Hook rate limit"
          note="Events per second per session. 0 is no limit — raise it off zero only if a busy host is flooding the spool."
        >
          <div class="grid grid-cols-2 gap-2">
            <NumberInput disabled={readOnly}
              value={form.hook_rate_per_sec}
              min={0}
              step={0.5}
              onChange={(v) => onField("hook_rate_per_sec", v)}
              ariaLabel="Hook events per second"
            />
            <NumberInput disabled={readOnly}
              value={form.hook_rate_burst}
              min={0}
              step={1}
              onChange={(v) => onField("hook_rate_burst", v)}
              ariaLabel="Hook burst"
            />
          </div>
        </SettingRow>
      </div>
    </Section>

    <!-- ── D. model providers ─────────────────────────────────────── -->
    <Section
      title="Model providers"
      scope="this daemon"
      note="No option here is free on all three axes. Pick one deliberately."
    >
      <!-- The trade-off table, on screen rather than behind a dropdown: the
           cost of each mode is the decision, and a select box hides it. -->
      <div class="overflow-x-auto border-b border-white-300 dark:border-navy-600">
        <table class="w-full text-left text-[0.6875rem]">
          <thead class="border-b border-white-300 dark:border-navy-600">
            <tr class="uppercase tracking-wider text-black-700 dark:text-black-600">
              <th class="px-5 py-2 font-medium">Mode</th>
              <th class="px-5 py-2 font-medium">Stored facts</th>
              <th class="px-5 py-2 font-medium">Client data</th>
              <th class="px-5 py-2 font-medium">Memory</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-white-300 dark:divide-navy-600">
            {#each PROVIDER_MODES as m (m.id)}
              <tr class={m.id === mode ? "bg-green-200 dark:bg-green-800" : ""}>
                <td class="px-5 py-2 font-medium text-black-900 dark:text-white-100">
                  {m.label}{#if m.id === mode}<span class="ml-2 text-[0.625rem] uppercase tracking-wider">current</span>{/if}
                </td>
                <td class="px-5 py-2 text-black-800 dark:text-black-600">{m.facts}</td>
                <td class={`px-5 py-2 ${m.danger ? "text-cau-400" : "text-black-800 dark:text-black-600"}`}>
                  {m.clientData}
                </td>
                <td class="px-5 py-2 text-black-800 dark:text-black-600">{m.ram}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>

      <div class="divide-y divide-white-300 dark:divide-navy-600">
        <SettingRow label="LLM provider" note={mode === "zero" ? ZERO_LLM_NOTE : undefined} danger={mode === "cloud"}>
          <div class="grid grid-cols-2 gap-2">
            <TextInput disabled={readOnly}
              value={form.llm_provider}
              onChange={(v) => onField("llm_provider", v)}
              placeholder="none"
              ariaLabel="LLM provider"
            />
            <TextInput disabled={readOnly}
              value={form.llm_model}
              onChange={(v) => onField("llm_model", v)}
              placeholder="model"
              ariaLabel="LLM model"
            />
          </div>
        </SettingRow>

        <SettingRow
          label="Embedding provider"
          note="Drives semantic search. Unset uses the backend's in-process local model — no key, no egress. “none” leaves full-text search only."
        >
          <div class="grid grid-cols-2 gap-2">
            <TextInput disabled={readOnly}
              value={form.embedding_provider}
              onChange={(v) => onField("embedding_provider", v)}
              placeholder="local"
              ariaLabel="Embedding provider"
            />
            <TextInput disabled={readOnly}
              value={form.embedding_model}
              onChange={(v) => onField("embedding_model", v)}
              placeholder="model"
              ariaLabel="Embedding model"
            />
          </div>
        </SettingRow>

        <SettingRow
          label="Consolidation token limits"
          note="Input and output ceilings for the summarising prompt. A small local model needs both lowered together."
        >
          <div class="grid grid-cols-2 gap-2">
            <NumberInput disabled={readOnly}
              value={form.max_input_tokens}
              min={0}
              onChange={(v) => onField("max_input_tokens", v)}
              ariaLabel="Max input tokens"
            />
            <NumberInput disabled={readOnly}
              value={form.max_output_tokens}
              min={0}
              onChange={(v) => onField("max_output_tokens", v)}
              ariaLabel="Max output tokens"
            />
          </div>
        </SettingRow>

        <SettingRow
          label="Review proposals before they are written"
          note="On, the auto-improvement reviewer stages its edits for approval instead of writing them into the wiki."
        >
          <div class="flex justify-start sm:justify-end">
            <Toggle disabled={readOnly}
              checked={form.auto_improve_require_approval}
              label="Require approval for auto-improvement proposals"
              onChange={(v) => onField("auto_improve_require_approval", v)}
            />
          </div>
        </SettingRow>
      </div>
    </Section>

    <!-- ── E. retention ───────────────────────────────────────────── -->
    <Section title="Retention" scope="whole store">
      <div
        class={`border-b px-5 py-3 ${
          retention.danger ? "border-cau-400 bg-cau-100 dark:bg-navy-800" : "border-white-300 dark:border-navy-600"
        }`}
      >
        <p class={`text-xs font-medium ${retention.danger ? "text-cau-400" : "text-black-900 dark:text-white-100"}`}>
          {retention.headline}
        </p>
        <p class="mt-1 text-[0.6875rem] leading-relaxed text-black-800 dark:text-black-600">{retention.body}</p>
      </div>
      <div class="divide-y divide-white-300 dark:divide-navy-600">
        <SettingRow
          label="Prune raw observations after"
          note="Days. 0 means never — the backend's default, and a real setting rather than an empty field."
        >
          <NumberInput disabled={readOnly}
            value={form.observation_retention_days}
            min={0}
            onChange={(v) => onField("observation_retention_days", v)}
            ariaLabel="Observation retention days"
          />
        </SettingRow>

        <SettingRow label="Hard-delete evicted pages after" note="Days. Until then an evicted page can still be recovered.">
          <NumberInput disabled={readOnly}
            value={form.hard_delete_after_days}
            min={0}
            onChange={(v) => onField("hard_delete_after_days", v)}
            ariaLabel="Hard delete after days"
          />
        </SettingRow>

        <SettingRow label="Prune batch size" note="Rows per transaction. Lower it on a slow disk.">
          <NumberInput disabled={readOnly}
            value={form.observation_prune_batch}
            min={0}
            onChange={(v) => onField("observation_prune_batch", v)}
            ariaLabel="Prune batch size"
          />
        </SettingRow>

        <SettingRow
          label="Run the sweep"
          note="Preview first. The real run evicts cold pages and, if an age is set above, permanently deletes older raw observations."
          danger={true}
        >
          <div class="flex flex-wrap items-center gap-2 sm:justify-end">
            {#if readOnly}
              <p class="text-xs leading-relaxed text-black-700 dark:text-black-600">{MANAGE_ADMIN_ONLY}</p>
            {:else}
              <Button variant="secondary" size="md" disabled={busy} onclick={onPreviewSweep}>Preview</Button>
              <Button variant="danger" size="md" disabled={busy} onclick={onRunSweep}>Run sweep</Button>
            {/if}
          </div>
        </SettingRow>
      </div>
      {#if sweepError}
        <p class="border-t border-white-300 dark:border-navy-600 px-5 py-3 text-xs text-neg-400">{sweepError}</p>
      {:else if sweep}
        <div class="border-t border-white-300 dark:border-navy-600 px-5 py-3">
          <p class="text-[0.6875rem] uppercase tracking-wider text-black-700 dark:text-black-600">
            {sweep.dry_run ? "Preview — nothing was changed" : "Sweep result"}
          </p>
          <pre
            class="mt-1 overflow-x-auto whitespace-pre-wrap font-mono text-[0.6875rem] text-black-900 dark:text-white-100">{sweep.output}</pre>
        </div>
      {/if}
    </Section>

    <!-- ── G. backfill ────────────────────────────────────────────── -->
    <Section title="Backfill" scope="per import">
      <div class="divide-y divide-white-300 dark:divide-navy-600">
        <SettingRow label="Sessions per import" note={backfillCapNote(form.backfill_max_sessions)}>
          <NumberInput disabled={readOnly}
            value={form.backfill_max_sessions}
            min={0}
            onChange={(v) => onField("backfill_max_sessions", v)}
            ariaLabel="Backfill max sessions"
          />
        </SettingRow>

        <SettingRow
          label="Import history automatically"
          note="Off by default. An import reads every local harness transcript for the project, which is a lot of history to absorb without being asked — the Projects tab has a per-project import with a preview."
        >
          <div class="flex justify-start sm:justify-end">
            <Toggle disabled={readOnly}
              checked={form.backfill_auto}
              label="Import a project's history automatically"
              onChange={(v) => onField("backfill_auto", v)}
            />
          </div>
        </SettingRow>
      </div>
    </Section>

    <!-- ── F. recall ranking ──────────────────────────────────────── -->
    <Section title="Recall ranking">
      {#snippet actions()}
        <Button variant="ghost" size="sm" onclick={onToggleAdvanced}>
          {advanced ? "Hide advanced" : "Show advanced"}
        </Button>
      {/snippet}
      {#if !advanced}
        <p class="px-5 py-4 text-xs leading-relaxed text-black-700 dark:text-black-600">
          The decay curve behind which pages survive and which are ranked first. The defaults are tuned; changing them
          shifts what agents recall, which is hard to notice and harder to attribute.
        </p>
      {:else}
        <div class="divide-y divide-white-300 dark:divide-navy-600">
          <SettingRow label="LLM reranker" note={RERANKER_NOTE} danger={(form.reranker ?? "") === "llm"}>
            <Select disabled={readOnly}
              value={form.reranker}
              options={[
                { label: "Off — local ranking only", value: "" },
                { label: "llm", value: "llm" },
              ]}
              onChange={(v) => onField("reranker", v)}
            />
          </SettingRow>
          <SettingRow label="Decay λ" note="Age decay. Higher forgets faster; 0.02 puts an unused page below the cold threshold at about 80 days.">
            <NumberInput disabled={readOnly} value={form.decay_lambda} min={0} step={0.01} onChange={(v) => onField("decay_lambda", v)} ariaLabel="Decay lambda" />
          </SettingRow>
          <SettingRow label="Access boost σ" note="How much being read keeps a page alive.">
            <NumberInput disabled={readOnly} value={form.decay_sigma} min={0} step={0.1} onChange={(v) => onField("decay_sigma", v)} ariaLabel="Decay sigma" />
          </SettingRow>
          <SettingRow label="Access decay μ" note="How fast that boost fades once nobody reads the page.">
            <NumberInput disabled={readOnly} value={form.decay_mu} min={0} step={0.01} onChange={(v) => onField("decay_mu", v)} ariaLabel="Decay mu" />
          </SettingRow>
          <SettingRow label="Default salience" note="The starting score every page gets.">
            <NumberInput disabled={readOnly} value={form.salience_default} min={0} step={0.1} onChange={(v) => onField("salience_default", v)} ariaLabel="Default salience" />
          </SettingRow>
          <SettingRow label="Breadth weight" note="Rewards a page several distinct operators have used.">
            <NumberInput disabled={readOnly} value={form.breadth_weight} min={0} step={0.1} onChange={(v) => onField("breadth_weight", v)} ariaLabel="Breadth weight" />
          </SettingRow>
          <SettingRow label="Cold threshold" note="A page scoring below this is evicted by the sweep.">
            <NumberInput disabled={readOnly} value={form.cold_threshold} min={0} step={0.05} onChange={(v) => onField("cold_threshold", v)} ariaLabel="Cold threshold" />
          </SettingRow>
        </div>
      {/if}
    </Section>

    <!-- H lives on the Provider page. Saying so beats leaving a reader
         hunting for a toggle that is deliberately somewhere else. -->
    <p class="px-1 pb-2 text-[0.6875rem] leading-relaxed text-black-700 dark:text-black-600">
      Whether an individual agent uses Agent Memory — and whether it records or only reads — is set per instance on the
      Provider page, not here. This tab configures the daemon they all share.
    </p>
  </div>
{/if}
