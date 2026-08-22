<script lang="ts">
  import { onMount, onDestroy } from "svelte";
  import { get } from "svelte/store";
  import { Effect } from "effect";
  import { WickClientLayer, listAgentProfiles } from "@wick-fe/common-api";
  import { toastError, toastOk, toastWarn } from "@wick-fe/common-stores";
  import { ConfirmDialog, Composer } from "@wick-fe/common-ui";
  import { NOTIFY_KEY } from "../notify-pref.js";

  import { createThreadStore } from "../stores/thread.js";
  import type { ThreadMeta, LifecycleState } from "../stores/thread.js";
  import { connectSession } from "../stores/sse.js";
  import type { SSEStatus } from "../types/agents.js";
  import { currentAsk, showAsk, hideAsk } from "../stores/asks.js";
  import { currentDetail, showDetail, hideDetail } from "../stores/detail.js";
  import DetailModal from "./DetailModal.svelte";
  import { currentApproval, showApproval, hideApproval, isExpiredApprovalError } from "../stores/approvals.js";
  import { notify } from "../notify.js";
  import { push } from "../router.js";
  import { bareToolName } from "../todoGroups.js";
  import { readScmWidth, writeScmWidth, clampScmWidth } from "../scmWidth.js";
  import { isValidFileName } from "../fileName.js";

  import { getConversation, getSessionMeta, deleteSession, getTurnTrace, cancelRun } from "../api/sessions.js";
  import { getProviderOptions, getProviderOptionModels, getProjectOptions, switchProvider, moveProject } from "../api/options.js";
  import { getAsks, answerAsk } from "../api/asks.js";
  import { getApprovals, sendApprovalDecision, revokeApproval } from "../api/approvals.js";
  import { sendMessage } from "../api/messages.js";
  import { listFiles, searchFiles, readFile, saveFile, createFile, deleteFile, downloadURL } from "../api/files.js";
  import { listComposerCommands, type ComposerApiCommand } from "../api/composer.js";
  import { getProcesses, killProcess, dequeueProcess, liveProcesses as filterLiveProcesses } from "../api/processes.js";
  import {
    getSubAgentPanel,
    interruptSubAgent,
    interruptAllSubAgents,
    continueSubAgent,
    liveSubAgents,
    getMessages,
    bumpHops,
  } from "../api/subagents.js";
  import { isSubAgentWorking } from "../lifecycleCls.js";
  import SubAgentPanel from "./SubAgentPanel.svelte";
  import SubAgentModal from "./SubAgentModal.svelte";
  import type { AgentMessageItem, IncidentSummary, SubAgentItem } from "../types/agents.js";
  import {
    listWorkspace, addWorkspace, saveWorkspaceConfig, testWorkspace,
    duplicateWorkspace, renameWorkspace, removeWorkspace,
  } from "../api/workspace.js";
  import { listSchedules, createSchedule, cancelSchedule, pauseSchedule, resumeSchedule, rescheduleSchedule, runScheduleNow } from "../api/schedules.js";
  import { getSessionTicket, type SessionTicket } from "../api/tickets.js";
  import TicketPanel from "./TicketPanel.svelte";
  import BrowserPanel from "./BrowserPanel.svelte";
  import { listInstances as listBrowserInstances } from "../api/browser.js";

  import ConversationHeader from "./ConversationHeader.svelte";
  import ConversationThread from "./ConversationThread.svelte";
  import JsonTree from "./JsonTree.svelte";
  import ContextPanel from "./ContextPanel.svelte";
  import FileViewerModal from "./FileViewerModal.svelte";
  import SwitchModal from "./SwitchModal.svelte";
  import OverridePopover from "./OverridePopover.svelte";
  import { getSessionOverrides, setSessionOverride } from "../api/overrides.js";
  import type { ConfigField } from "@wick-fe/common-ui";
  import { setFileContext, setWidgetPolicy } from "../richRender.js";
  import ProcessPanel from "./ProcessPanel.svelte";
  import WorkspacePanel from "./WorkspacePanel.svelte";
  import SchedulePanel from "./SchedulePanel.svelte";
  import AskUserModal from "./AskUserModal.svelte";
  import ApprovalsModal from "./ApprovalsModal.svelte";
  import ApprovedPanel from "./ApprovedPanel.svelte";
  import type { ActiveView } from "./ConversationHeader.svelte";

  import type {
    ConversationTurn, LiveTurn, TypingState,
    ContextFileEntry, AskAnswer, ApprovalDecision,
    ApprovedItem, ComposerCommand,
    WsInstance, WsBase, WsTombstone, ProcessInfo, FileContent,
    ProviderOption, ProjectOption, Schedule,
  } from "../types/agents.js";

  type Props = {
    base: string;
    sessionId: string;
  };

  let { base, sessionId }: Props = $props();

  /* ── thread store ──────────────────────────────────────────────── */
  const thread = createThreadStore();
  let turns = $state<ConversationTurn[]>([]);
  let live = $state<LiveTurn | null>(null);
  let typing = $state<TypingState>({ active: false });
  let agentLifecycle = $state<LifecycleState>({ state: "", pid: 0, substate: "", at: 0 });
  let threadMeta = $state<ThreadMeta>({});

  const unsubTurns = thread.turns.subscribe((v) => { turns = v; });
  const unsubLive = thread.live.subscribe((v) => { live = v; });
  const unsubTyping = thread.typing.subscribe((v) => { typing = v; });
  const unsubLifecycle = thread.lifecycle.subscribe((v) => { agentLifecycle = v; });
  const unsubMeta = thread.meta.subscribe((v) => { threadMeta = v; });

  /* ── raw trace view ────────────────────────────────────────────── */
  let traceMap = $state<Record<string, unknown>>({});

  const rawData = $derived(
    turns.map((t) => {
      if (t.has_trace !== true) return t;
      const tr = traceMap[t.turn_id];
      return { ...t, trace: tr === undefined ? "(loading…)" : tr };
    }),
  );
  const rawJson = $derived(JSON.stringify(rawData, null, 2));

  async function loadRawTraces() {
    const pending = turns.filter((t) => t.has_trace === true && t.turn_id && traceMap[t.turn_id] === undefined);
    if (pending.length === 0) return;
    await Promise.all(
      pending.map(async (t) => {
        try {
          const tr = await Effect.runPromise(getTurnTrace(base, sessionId, t.turn_id).pipe(Effect.provide(WickClientLayer)));
          traceMap = { ...traceMap, [t.turn_id]: tr };
        } catch {
          traceMap = { ...traceMap, [t.turn_id]: { error: "failed to load trace" } };
        }
      }),
    );
  }

  $effect(() => {
    if (activeView !== "raw") return;
    void turns.length;
    void loadRawTraces();
  });

  async function copyRaw() {
    try {
      await navigator.clipboard.writeText(rawJson);
      toastOk("Raw trace copied");
    } catch {
      toastError("Copy failed", "Clipboard unavailable in this browser.");
    }
  }

  /* ── session title + meta ──────────────────────────────────────── */
  let title = $state("");
  let agentLabel = $state("");

  /* Reflect the live session title in the browser tab; restore on leave. */
  $effect(() => {
    if (typeof document === "undefined") return;
    const shown = (threadMeta.title || title).trim();
    if (!shown) return;
    const prev = document.title;
    document.title = `${shown} · Agents`;
    return () => { document.title = prev; };
  });

  /* ── SSE ───────────────────────────────────────────────────────── */
  let closeSSE: (() => void) | null = null;
  let sseStream: ReturnType<typeof connectSession> | null = null;
  let sseStatus = $state<SSEStatus>("connecting");

  /* ── vertical rail tabs ────────────────────────────────────────── */
  type RailTab = "context" | "process" | "workspace" | "scheduled" | "browser" | "source" | "subagents" | "ticket";
  let railTab = $state<RailTab | null>(null);

  /* ── thread scroll ref ─────────────────────────────────────────── */
  let threadEl: HTMLElement | undefined = $state();

  /* ── context panel state ──────────────────────────────────────── */
  let cwdVal = $state("");
  let filesVal = $state<ContextFileEntry[]>([]);
  let filesLoading = $state(false);
  let filesLoadError = $state("");

  /* Composer autocomplete data. `@` searches the session's files via the
     backend (whole tree, ranked, fresh per keystroke); `/` lists commands from
     GET /api/composer/commands (built-in actions + skills). */
  function searchMentionFiles(query: string): Promise<string[]> {
    return run(searchFiles(base, sessionId, query).pipe(Effect.provide(WickClientLayer)))
      .catch(() => [] as string[]);
  }
  // The `/` command list comes from the backend (GET /api/composer/commands) so
  // it's extensible without an FE rebuild. Built-in commands carry an `action`
  // id that this map resolves to a local handler (UI actions must live in the
  // FE); skills carry `insert` and drop in `/name` as text. A new backend
  // command reusing an existing action id needs no FE change.
  let projectPickerOpen = $state(false);
  // /thinking (and any future per-session override) opens this popover instead
  // of sending a message. schema + values come from the provider's override
  // endpoint; each edit auto-saves via the same endpoint (no chat turn).
  let overridePopoverOpen = $state(false);
  let overrideSchema = $state<ConfigField[]>([]);
  let overrideValues = $state<Record<string, string>>({});
  // Imperative handle to the composer so `/provider` opens ITS provider drill
  // (4-level, live-set aware) — the same picker as the toolbar chip, not a
  // separate flat modal.
  let composerRef = $state<{ openProvider: () => void } | undefined>();
  const ACTION_HANDLERS: Record<string, () => void> = {
    "switch:provider": () => { composerRef?.openProvider(); },
    "switch:project": () => { projectPickerOpen = true; },
    "panel:process": () => toggleRail("process"),
    "panel:workspace": () => toggleRail("workspace"),
    "panel:source": () => toggleRail("source"),
    "panel:context": () => toggleRail("context"),
    "panel:subagents": () => toggleRail("subagents"),
    "panel:thinking": () => openOverridePopover(),
    "view:commands": () => handleTabChange("commands"),
    "view:approvals": () => handleTabChange("approvals"),
    "view:raw": () => handleTabChange("raw"),
  };

  // Load the session's override schema + current values, then open the popover.
  function openOverridePopover() {
    const providerType = activeProvider ? activeProvider.split("/")[0] : "";
    run(getSessionOverrides(base, sessionId, providerType).pipe(Effect.provide(WickClientLayer)))
      .then((res) => {
        overrideSchema = res.schema ?? [];
        overrideValues = res.values ?? {};
        overridePopoverOpen = true;
      })
      .catch(() => { /* toast handled by run() */ });
  }

  // Save one override field (auto-save on change); refresh values from the
  // server so visible_when-gated fields react to the new state.
  function saveOverride(key: string, value: string) {
    overrideValues = { ...overrideValues, [key]: value }; // optimistic
    const providerType = activeProvider ? activeProvider.split("/")[0] : "";
    run(setSessionOverride(base, sessionId, key, value, providerType).pipe(Effect.provide(WickClientLayer)))
      .then((res) => { if (res?.values) overrideValues = res.values; })
      .catch(() => { /* toast handled by run() */ });
  }
  function toComposerCommand(c: ComposerApiCommand): ComposerCommand {
    if (c.action) {
      // "send:<text>" actions (e.g. /compact) submit <text> as a message —
      // the provider engine interprets it (wick runs compaction in-process;
      // CLI providers pass their own /compact through). Generic so new
      // send-commands need no per-command FE handler.
      let run = ACTION_HANDLERS[c.action];
      if (!run && c.action.startsWith("send:")) {
        const text = c.action.slice("send:".length);
        run = () => void handleSend({ text, files: [] });
      }
      return { value: c.id, label: c.label, hint: c.hint, category: c.category, run };
    }
    // insert-type (skills): value is placed after `/`
    return { value: c.insert ?? c.id, label: c.label, hint: c.hint, category: c.category };
  }
  let composerCommands = $state<ComposerCommand[]>([]);

  // Items for the /provider and /project picker modals. Provider key mirrors
  // ComposerToolbar: a named instance is "type/name", a default collapses to
  // the bare type.
  function normKey(k: string): string { return k.includes("/") ? k : `${k}/${k}`; }
  const projectItems = $derived([
    { id: null as string | null, label: "— no project —", current: activeProjectId == null },
    ...projectOptions.map((p) => ({ id: p.id as string | null, label: p.name, current: p.id === activeProjectId })),
  ]);

  // Live model loader for the composer's model drill-in. `optionValue` is a
  // "type/name" key; split it and ask the server for that instance's current
  // vendor models. Errors bubble up to the composer, which keeps the static
  // list — so this never blocks selection.
  function loadProviderModels(optionValue: string, opts?: { entry?: string }) {
    const slash = optionValue.indexOf("/");
    const type = slash < 0 ? optionValue : optionValue.slice(0, slash);
    const name = slash < 0 ? optionValue : optionValue.slice(slash + 1);
    return Effect.runPromise(getProviderOptionModels(base, type, name, opts).pipe(Effect.provide(WickClientLayer)));
  }

  // Capability-chip prefs (wick-scoped global) ride on the wick option row.
  const capsPrefs = $derived.by(() => {
    const wick = providerOptions.find((p) => p.type === "wick");
    const m = wick?.capability_display_mode;
    return {
      show: wick?.show_capabilities ?? true,
      mode: (m === "name" ? "name" : m === "icon" ? "icon" : "list") as "list" | "name" | "icon",
    };
  });
  // Toolbar dropdowns for the shared Composer (same look as the new-session page).
  const providerSelect = $derived({
    options: providerOptions.map((p) => ({
      label: p.name && p.name !== p.type ? `${p.type} · ${p.name}` : p.type,
      value: `${p.type}/${p.name}`,
      badge: p.usesAIRouter ? "AI Router" : undefined,
      models: p.models,
    })),
    value: activeProvider
      ? normKey(activeProvider) + (activeModelID ? `::${activeModelID}` : "")
      : "",
    onChange: (v: string) => handleProviderChange(v),
    loadModels: loadProviderModels,
    showCapabilities: capsPrefs.show,
    capabilityMode: capsPrefs.mode,
  });
  const projectSelect = $derived({
    options: [
      { label: "— no project —", value: "" },
      ...projectOptions.map((p) => ({ label: `📁 ${p.name}`, value: p.id })),
    ],
    value: activeProjectId ?? "",
    onChange: (v: string) => handleProjectChange(v || null),
  });
  let fileSearch = $state("");
  let openDirs = $state<Record<string, boolean>>({});
  let viewerFile = $state<FileContent | null>(null);
  let viewerDirty = $state(false);

  /* ── process panel state ──────────────────────────────────────── */
  let processes = $state<ProcessInfo[]>([]);
  let confirmKill = $state<{ sid: string; queued: boolean } | null>(null);
  // Guard against overlapping /processes requests: a burst of SSE `lifecycle`
  // events would otherwise stack into a pile of pending fetches. Skip while
  // one is already in flight.
  let processesInFlight = false;
  // Set when a refresh is requested while one is already in flight, so the
  // load re-runs once on completion (see loadProcesses).
  let processReloadPending = false;

  /* ── sub-agents panel state ───────────────────────────────────── */
  let subAgents = $state<SubAgentItem[]>([]);
  // The tree's investigation record, when it has one. null for an
  // ordinary conversation, which is most of them.
  let incident = $state<IncidentSummary | null>(null);
  // Which child's transcript the inspector modal is showing. null = closed.
  let selectedSubAgent = $state<string | null>(null);
  let subAgentsInFlight = false;
  let subAgentReloadPending = false;

  // The selected row itself, which the modal needs (status, task, turn
  // budget) rather than just the session id. Resolves to undefined while a
  // `?sub=` deep link waits for the roster to load, and the modal stays shut
  // until it does.
  const selectedSubAgentRow = $derived(
    selectedSubAgent === null
      ? undefined
      : subAgents.find((s) => s.child_session_id === selectedSubAgent),
  );

  /* ── workspace panel state ────────────────────────────────────── */
  let wsInstances = $state<WsInstance[]>([]);
  let wsBases = $state<WsBase[]>([]);
  let wsDeleted = $state<WsTombstone[]>([]);
  let wsOpenCards = $state<Record<string, boolean>>({});

  /* ── scheduled-messages panel state ───────────────────────────── */
  let schedules = $state<Schedule[]>([]);

  /* ── provider / project options ──────────────────────────────── */
  let providerOptions = $state<ProviderOption[]>([]);
  let projectOptions = $state<ProjectOption[]>([]);
  let activeProvider = $state<string | null>(null);
  // Pinned wick model id on the active agent, if any — "" = that
  // provider instance's own default model applies.
  let activeModelID = $state("");
  let activeProjectId = $state<string | null>(null);

  /* ── approval error state ─────────────────────────────────────── */
  let approvalError = $state("");

  /* ── idle timeout ─────────────────────────────────────────────── */
  const idleTimeoutMs = parseInt(document.getElementById("app")?.dataset.idleTimeoutMs ?? "", 10) || 120_000;

  /* ── SCM dock ─────────────────────────────────────────────────── */
  const scmAssetUrl =
    document.getElementById("app")?.dataset.scmAsset ??
    document.querySelector<HTMLElement>("[data-scm-asset]")?.dataset.scmAsset ??
    "";
  let scmChangeCount = $state(0);
  let scmHostEl: HTMLElement | undefined = $state(undefined);
  let scmHostMobileEl: HTMLElement | undefined = $state(undefined);
  let scmMounted = false;

  /* ── SCM sidebar resizable width (desktop only, persisted) ────── */
  let scmWidth = $state(readScmWidth());
  let scmSideEl: HTMLElement | undefined = $state(undefined);

  function startScmResize(e: PointerEvent) {
    e.preventDefault();
    const handle = e.currentTarget as HTMLElement;
    handle.setPointerCapture?.(e.pointerId);
    document.body.style.userSelect = "none";

    function onMove(ev: PointerEvent) {
      if (!scmSideEl) return;
      const right = scmSideEl.getBoundingClientRect().right;
      scmWidth = clampScmWidth(right - ev.clientX);
    }
    function onUp(ev: PointerEvent) {
      handle.releasePointerCapture?.(ev.pointerId);
      document.body.style.userSelect = "";
      scmWidth = writeScmWidth(scmWidth);
      window.removeEventListener("pointermove", onMove);
      window.removeEventListener("pointerup", onUp);
    }
    window.addEventListener("pointermove", onMove);
    window.addEventListener("pointerup", onUp);
  }

  /* ── header tab view ──────────────────────────────────────────── */
  let activeView = $state<ActiveView>("conversation");

  /* ── approvals tab state ──────────────────────────────────────── */
  let approvalsTabPending = $state<import("../types/agents.js").ApprovalRequest[]>([]);
  let approvalsTabSession = $state<ApprovedItem[]>([]);
  let approvalsTabAlways = $state<ApprovedItem[]>([]);

  function loadApprovalsTab(showPending = false) {
    run(getApprovals(base, sessionId).pipe(Effect.provide(WickClientLayer)))
      .then((res) => {
        approvalsTabPending = res.pending;
        approvalsTabSession = res.session_approved;
        approvalsTabAlways = res.always_approved;
        // On a fresh page the approval_request event has already come and
        // gone, but the daemon is still holding the prompt open — so the
        // agent sits there with no visible way to answer. Reopen the modal
        // from server state rather than relying on an event we missed.
        if (showPending && res.pending.length > 0 && !get(currentApproval)) {
          showApproval(res.pending[0]);
        }
      })
      .catch((e: unknown) => toastError(`Approvals: ${e instanceof Error ? e.message : String(e)}`));
  }

  // Rehydrate whenever the session changes, including first render.
  $effect(() => {
    void sessionId;
    loadApprovalsTab(true);
  });

  async function handleRevokeApproval(matchKey: string, scope: "session" | "always") {
    try {
      await run(revokeApproval(base, sessionId, matchKey, scope).pipe(Effect.provide(WickClientLayer)));
      loadApprovalsTab();
    } catch (e: unknown) {
      toastError(`Revoke: ${e instanceof Error ? e.message : String(e)}`);
    }
  }

  function handleTabChange(view: ActiveView) {
    activeView = view;
    if (view === "approvals") loadApprovalsTab();
  }

  /* ── Effect runner ────────────────────────────────────────────── */
  function run<T>(eff: Effect.Effect<T, unknown, never>): Promise<T> {
    return Effect.runPromise(eff);
  }

  /* ── data loaders ─────────────────────────────────────────────── */
  function loadFiles() {
    filesLoading = true;
    filesLoadError = "";
    run(listFiles(base, sessionId).pipe(Effect.provide(WickClientLayer)))
      .then((res) => { cwdVal = res.cwd; filesVal = res.files; filesLoading = false; })
      .catch((e: unknown) => {
        filesLoading = false;
        const msg = e instanceof Error ? e.message : String(e);
        filesLoadError = msg;
        toastError(`Files: ${msg}`);
      });
  }

  // Populate the composer's `/` menu from the backend registry (built-in
  // actions + skills). Re-runs when the active provider changes so the skill
  // list matches that provider. Best-effort: on failure the menu is empty.
  $effect(() => {
    const providerType = activeProvider ? activeProvider.split("/")[0] : "";
    run(listComposerCommands(base, undefined, providerType).pipe(Effect.provide(WickClientLayer)))
      .then((res) => { composerCommands = (res.commands ?? []).map(toComposerCommand); })
      .catch(() => { /* commands are optional */ });
  });

  /* Workspace files change as the agent works (it writes artifacts into the
     session cwd). Reload the tree — silently + debounced — whenever the agent
     reports activity over SSE, so generated files appear without a manual
     refresh. Only fetch while the file tree is actually visible: `filesVal`
     feeds the context panel (the `@` mention now uses the backend search), so
     reloading on every idle SSE heartbeat while the panel is closed just
     hammered GET /files for nothing. */
  let fileReloadTimer: ReturnType<typeof setTimeout> | null = null;
  function reloadFilesSilently() {
    if (railTab !== "context") return;
    run(listFiles(base, sessionId).pipe(Effect.provide(WickClientLayer)))
      .then((res) => { cwdVal = res.cwd; filesVal = res.files; })
      .catch(() => { /* keep the current tree on a transient failure */ });
  }
  function scheduleFileReload() {
    if (railTab !== "context") return; // nothing renders the tree — skip the fetch
    if (fileReloadTimer !== null) clearTimeout(fileReloadTimer);
    fileReloadTimer = setTimeout(reloadFilesSilently, 400);
  }

  function loadProcesses() {
    // In-flight guard: if a fetch is already running, don't fire a second one
    // — but remember that a refresh was asked for, so we run once more when
    // the current one lands. Without the re-arm, a lifecycle event arriving
    // mid-flight would be dropped and the panel could settle on a stale state.
    if (processesInFlight) {
      processReloadPending = true;
      return;
    }
    processesInFlight = true;
    run(getProcesses(base, sessionId).pipe(Effect.provide(WickClientLayer)))
      .then((res) => {
        processes = res;
        // Derive active provider from processes when meta.active_agent is unset
        if (!activeProvider && res && res.length > 0) {
          activeProvider = res[0].provider || null;
          agentLabel = res[0].provider || "";
        }
      })
      .catch((e: unknown) => toastError(`Processes: ${e instanceof Error ? e.message : String(e)}`))
      .finally(() => {
        processesInFlight = false;
        if (processReloadPending) {
          processReloadPending = false;
          loadProcesses();
        }
      });
  }

  /* /processes is driven by SSE `lifecycle` events, not a timer. A single
     turn fires several transitions in quick succession (spawning → working →
     idle), and re-fetching on each one is the request burst seen in the
     network panel. Coalesce them: one fetch ~200ms after the last transition
     in a burst. The mount + post-kill loads stay immediate so the panel is
     populated the instant it opens. */
  let processReloadTimer: ReturnType<typeof setTimeout> | null = null;
  function scheduleProcessReload() {
    if (processReloadTimer !== null) clearTimeout(processReloadTimer);
    processReloadTimer = setTimeout(() => {
      processReloadTimer = null;
      loadProcesses();
    }, 200);
  }

  function loadSubAgents() {
    // Same in-flight guard + re-arm as loadProcesses: a delegation burst
    // fires many lifecycle events, and a refresh requested mid-flight must
    // not be dropped or the panel settles on a stale state.
    if (subAgentsInFlight) {
      subAgentReloadPending = true;
      return;
    }
    subAgentsInFlight = true;
    run(getSubAgentPanel(base, sessionId).pipe(Effect.provide(WickClientLayer)))
      .then((res) => { subAgents = res.subAgents; incident = res.incident; })
      .catch((e: unknown) => toastError(`Sub-agents: ${e instanceof Error ? e.message : String(e)}`))
      .finally(() => {
        subAgentsInFlight = false;
        if (subAgentReloadPending) {
          subAgentReloadPending = false;
          loadSubAgents();
        }
      });
  }

  // Agent-to-agent thread, loaded alongside the sub-agent list so the
  // panel never shows a roster without the conversation that goes with it.
  let agentMessages = $state<AgentMessageItem[]>([]);
  let hopsLeft = $state(0);

  function loadAgentMessages() {
    run(getMessages(base, sessionId).pipe(Effect.provide(WickClientLayer)))
      .then((res) => { agentMessages = res.messages; hopsLeft = res.hopsLeft; })
      // Quiet on failure: the thread is secondary to the sub-agent list,
      // and a toast per poll would bury the panel it decorates.
      .catch(() => {});
  }

  // Roles this conversation may delegate to, for the composer's @ menu.
  // Loaded once: the roster changes when someone edits a profile, not
  // during a conversation.
  let agentRoles = $state<{ key: string; name: string; description: string }[]>([]);

  function loadAgentRoles() {
    // Scoped to this session's project so the menu offers the same roles
    // delegate would actually resolve — a project role shadows a global
    // one, and offering the shadowed name would be a lie.
    listAgentProfiles(base, activeProjectId || undefined)
      .then((res) => {
        agentRoles = res.profiles.map((p) => ({
          key: p.key,
          name: p.name,
          description: p.description,
        }));
      })
      // Silent: the @ menu still lists files, and a toast on every load
      // would nag on installs where sub-agents are switched off.
      .catch(() => {});
  }

  // Live instances first, then roles. Mentioning a running agent talks to
  // the one that already has context; mentioning a role starts a new one,
  // so the cheaper, better-informed target is offered first.
  const mentionableAgents = $derived.by(() => {
    const out: { handle: string; label: string; hint?: string }[] = [];
    const seen = new Set<string>();
    for (const s of subAgents) {
      if (!s.handle || seen.has(s.handle)) continue;
      seen.add(s.handle);
      out.push({ handle: s.handle, label: s.handle, hint: `${s.profile_key} · running here` });
    }
    for (const r of agentRoles) {
      if (seen.has(r.key)) continue;
      seen.add(r.key);
      out.push({ handle: r.key, label: r.key, hint: r.description || r.name });
    }
    return out;
  });

  function bumpAgentHops() {
    run(bumpHops(base, sessionId).pipe(Effect.provide(WickClientLayer)))
      .then(() => loadAgentMessages())
      .catch((e: unknown) => toastError(`Allow more hops: ${e instanceof Error ? e.message : String(e)}`));
  }

  // Coalesced refetch, mirroring scheduleProcessReload: one fetch ~200ms
  // after the last transition in a burst, reusing the existing /stream SSE
  // rather than opening a second event source.
  let subAgentReloadTimer: ReturnType<typeof setTimeout> | null = null;
  function scheduleSubAgentReload() {
    if (subAgentReloadTimer !== null) clearTimeout(subAgentReloadTimer);
    subAgentReloadTimer = setTimeout(() => {
      subAgentReloadTimer = null;
      loadSubAgents();
      loadAgentMessages();
    }, 200);
  }

  /* Tools whose call means this session's sub-agent roster just changed.
     Matched on the BARE name so an MCP-namespaced call
     (mcp__wick__wick_delegate) counts too. */
  const DELEGATION_TOOLS = new Set(["wick_delegate", "wick_delegate_collect"]);
  function isDelegationTool(name?: string): boolean {
    return !!name && DELEGATION_TOOLS.has(bareToolName(name));
  }

  /* While a sub-agent is live, poll its row.

     Everything else in this panel rides the leader's SSE stream, but a
     sub-agent publishes its lifecycle on the CHILD's session id, which
     this stream is not subscribed to. Between the delegation call and the
     leader's end-of-turn the leader emits nothing at all — which is
     exactly the stretch where a running sub-agent's spinner and turn
     count need to move. Polling stops the moment none are live, so an
     idle conversation issues no requests. */
  const SUB_AGENT_POLL_MS = 3000;
  let subAgentPollTimer: ReturnType<typeof setInterval> | null = null;

  $effect(() => {
    const anyLive = liveSubAgents(subAgents).length > 0;
    if (!anyLive) {
      if (subAgentPollTimer !== null) {
        clearInterval(subAgentPollTimer);
        subAgentPollTimer = null;
      }
      return;
    }
    if (subAgentPollTimer !== null) return;
    subAgentPollTimer = setInterval(loadSubAgents, SUB_AGENT_POLL_MS);
    return () => {
      if (subAgentPollTimer !== null) {
        clearInterval(subAgentPollTimer);
        subAgentPollTimer = null;
      }
    };
  });

  function stopSubAgent(delegationId: string) {
    run(interruptSubAgent(base, delegationId).pipe(Effect.provide(WickClientLayer)))
      // A 409 is mapped to outcome "already_done" by the API layer, so it
      // lands here as a normal result: refresh and show the real outcome
      // instead of an error toast for a race the user cannot avoid.
      .then(() => loadSubAgents())
      .catch((e: unknown) => toastError(`Stop sub-agent: ${e instanceof Error ? e.message : String(e)}`));
  }

  // Opens the rail on a delegation's child. The thread card knows the
  // delegation id; the panel selects by child session, so resolve through
  // the already-loaded rows and fall back to just opening the panel when
  // the list has not caught up yet.
  function openSubAgent(delegationId: string) {
    railTab = "subagents";
    const row = subAgents.find((s) => s.delegation_id === delegationId);
    if (row) selectedSubAgent = row.child_session_id;
    loadSubAgents();
  }

  // Sends a finished sub-agent back to work in its own session.
  //
  // `resumed: false` is surfaced as a warning rather than swallowed: the
  // sub-agent woke inside its old session but WITHOUT its transcript, so
  // the instruction just sent has to stand on its own. A plain success
  // toast there would tell the user their follow-up landed on an agent
  // that remembers the work, when it does not.
  function continueSubAgentRow(delegationId: string, task: string) {
    run(continueSubAgent(base, delegationId, task).pipe(Effect.provide(WickClientLayer)))
      .then((res) => {
        if (res.resumed) toastOk("Sub-agent continued");
        else toastWarn("Continued, but it could not resume its earlier work — it is starting fresh");
        loadSubAgents();
      })
      .catch((e: unknown) =>
        toastError(`Continue sub-agent: ${e instanceof Error ? e.message : String(e)}`),
      );
  }

  function stopAllSubAgents() {
    run(interruptAllSubAgents(base, sessionId).pipe(Effect.provide(WickClientLayer)))
      .then(() => loadSubAgents())
      .catch((e: unknown) => toastError(`Stop all: ${e instanceof Error ? e.message : String(e)}`));
  }

  function loadWorkspace() {
    run(listWorkspace(base, sessionId).pipe(Effect.provide(WickClientLayer)))
      .then((res) => { wsInstances = res.instances; wsBases = res.bases; wsDeleted = res.deleted; })
      .catch((e: unknown) => toastError(`Workspace: ${e instanceof Error ? e.message : String(e)}`));
  }

  function loadSchedules() {
    run(listSchedules(base, sessionId).pipe(Effect.provide(WickClientLayer)))
      .then((res) => { schedules = res; })
      .catch((e: unknown) => toastError(`Schedules: ${e instanceof Error ? e.message : String(e)}`));
  }

  /* Ticket panel data. config.enabled=false (or a failed load on an old
     server) keeps the rail tab hidden — nothing else to clean up. */
  let ticketInfo = $state<SessionTicket | null>(null);
  function loadTicket() {
    run(getSessionTicket(base, sessionId).pipe(Effect.provide(WickClientLayer)))
      .then((res) => { ticketInfo = res; })
      .catch(() => { ticketInfo = null; });
  }

  /* Rehydrate a question that arrived while the tab was closed; never
   * clobber an ask already shown by a live SSE event. */
  function loadPendingAsk() {
    run(getAsks(base, sessionId).pipe(Effect.provide(WickClientLayer)))
      .then((res) => {
        const pending = res.pending[0];
        if (pending && !get(currentAsk)) showAsk(pending);
      })
      .catch(() => { /* non-fatal — live SSE still delivers new asks */ });
  }

  function loadProviderOptions() {
    run(getProviderOptions(base).pipe(Effect.provide(WickClientLayer)))
      .then((res) => { providerOptions = res; })
      .catch(() => { providerOptions = []; });
  }

  function loadProjectOptions() {
    run(getProjectOptions(base).pipe(Effect.provide(WickClientLayer)))
      .then((res) => { projectOptions = res; })
      .catch(() => { projectOptions = []; });
  }

  async function handleProviderChange(providerValue: string) {
    // Composer encodes a 3rd-level model pick as "type/name::modelID" (see
    // Composer.svelte's splitModelPin) — split it back out before sending.
    const sepIdx = providerValue.indexOf("::");
    const provider = sepIdx < 0 ? providerValue : providerValue.slice(0, sepIdx);
    const modelId = sepIdx < 0 ? undefined : providerValue.slice(sepIdx + 2);
    try {
      // In-place switch (same path as a "#codex" chat message): persists to
      // agents.json + kills the subprocess so the next message respawns with
      // the new provider. Session id is unchanged — no navigation. A single
      // system turn arrives over SSE (no assistant bubble).
      await run(
        switchProvider(base, sessionId, provider, modelId).pipe(Effect.provide(WickClientLayer)),
      );
      // Reflect the switch in both the composer selector and the header pill.
      activeProvider = provider;
      activeModelID = modelId ?? "";
      agentLabel = provider;
      // Refetch the authoritative history: the backend collapses back-to-back
      // switches on disk, so this replaces any stale switch bubbles still shown
      // from earlier live SSE turns with the pruned, canonical transcript.
      void loadConversation();
    } catch (e: unknown) {
      toastError(`Provider: ${e instanceof Error ? e.message : String(e)}`);
    }
  }

  async function handleProjectChange(projectId: string | null) {
    try {
      await run(
        moveProject(base, sessionId, projectId).pipe(Effect.provide(WickClientLayer)),
      );
      activeProjectId = projectId;
    } catch (e: unknown) {
      toastError(`Project: ${e instanceof Error ? e.message : String(e)}`);
    }
  }

  /* ── auto-scroll thread to bottom ─────────────────────────────── */
  let userScrolledUp = $state(false);
  let showJumpBtn = $state(false);
  let suppressScrollCheck = false;
  /* While true, the thread stays pinned to the bottom as content grows — the
     natural chat behaviour. Starts true (a fresh open should land at the
     latest turn) and, crucially, stays true through the post-mount settle when
     HTML-artifact iframes resize to their content: each growth re-pins instead
     of stranding the user above the fold. Any manual scroll up releases it. */
  let stickToBottom = $state(true);

  function scrollToBottom() {
    if (threadEl) {
      suppressScrollCheck = true;
      userScrolledUp = false;
      showJumpBtn = false;
      stickToBottom = true;
      threadEl.scrollTop = threadEl.scrollHeight;
      requestAnimationFrame(() => { suppressScrollCheck = false; });
    }
  }

  $effect(() => {
    function onKeydown(e: KeyboardEvent) {
      if (e.ctrlKey && e.key === "ArrowDown") {
        e.preventDefault();
        scrollToBottom();
      } else if ((e.ctrlKey || e.metaKey) && (e.key === "b" || e.key === "B")) {
        e.preventDefault();
        toggleRail("context");
      }
    }
    window.addEventListener("keydown", onKeydown);
    return () => window.removeEventListener("keydown", onKeydown);
  });

  // Generic detail chips (rendered by richRender) bubble a `wick-detail-open`
  // event to the window; open the shared modal with its title + body. One
  // listener serves every chip in the thread — reusable by any feature that
  // emits a `detail` fence, not just compaction.
  $effect(() => {
    function onDetailOpen(e: Event) {
      const d = (e as CustomEvent).detail as { title?: string; body?: string } | undefined;
      if (d) showDetail({ title: d.title ?? "Details", body: d.body ?? "" });
    }
    window.addEventListener("wick-detail-open", onDetailOpen);
    return () => window.removeEventListener("wick-detail-open", onDetailOpen);
  });

  $effect(() => {
    if (!threadEl) return;
    const el = threadEl;

    // A real user scroll: release the bottom-pin the moment they move up, and
    // re-pin once they return to the bottom. Drives the Jump button.
    function onScroll() {
      if (suppressScrollCheck) return;
      const distFromBottom = el.scrollHeight - el.scrollTop - el.clientHeight;
      userScrolledUp = distFromBottom > 80;
      stickToBottom = !userScrolledUp;
      showJumpBtn = userScrolledUp;
    }

    // Content can grow AFTER the initial layout with no scroll event — an HTML
    // artifact iframe auto-resizes to its content a beat after mount. While
    // pinned, follow that growth down (smooth, no overlay, no timing guess) so
    // a refresh lands at the latest turn even though the height wasn't known at
    // mount. Once the user has scrolled up (stickToBottom false), don't yank
    // them — just surface the Jump button.
    function onResize() {
      if (suppressScrollCheck) return;
      if (stickToBottom) {
        el.scrollTop = el.scrollHeight;
        showJumpBtn = false;
        userScrolledUp = false;
      } else {
        const distFromBottom = el.scrollHeight - el.scrollTop - el.clientHeight;
        userScrolledUp = distFromBottom > 80;
        showJumpBtn = userScrolledUp;
      }
    }

    el.addEventListener("scroll", onScroll, { passive: true });
    let ro: ResizeObserver | null = null;
    if (typeof ResizeObserver !== "undefined") {
      ro = new ResizeObserver(() => onResize());
      ro.observe(el);
      for (const child of Array.from(el.children)) ro.observe(child);
    }
    return () => {
      el.removeEventListener("scroll", onScroll);
      ro?.disconnect();
    };
  });

  $effect(() => {
    const _dep1 = turns.length;
    const _dep2 = live?.text?.length;
    const _dep3 = live?.blocks?.length;
    if (threadEl && stickToBottom) {
      threadEl.scrollTop = threadEl.scrollHeight;
    }
  });

  /* ── SCM island mount when source tab opens ───────────────────── */
  type WickSCMApi = {
    WickSCM?: {
      mount: (h: HTMLElement, o: { sessionID: string; mode: "sidebar"; onClose?: () => void }) => void;
      unmount: (h: HTMLElement) => void;
    };
  };

  function mountScmHost(host: HTMLElement) {
    const w = window as unknown as WickSCMApi;
    const capturedHost = host;
    const capturedSessionId = sessionId;
    let active = true;

    const doMount = async () => {
      try {
        if (scmAssetUrl && !w.WickSCM) {
          await new Promise<void>((resolve, reject) => {
            const s = document.createElement("script");
            s.type = "module";
            s.src = scmAssetUrl;
            s.onload = () => {
              if (w.WickSCM) resolve();
              else reject(new Error("WickSCM not installed"));
            };
            s.onerror = () => reject(new Error("failed to load scm bundle"));
            document.head.appendChild(s);
          });
        }
        if (active) {
          w.WickSCM?.mount(capturedHost, {
            sessionID: capturedSessionId,
            mode: "sidebar",
            onClose: () => { railTab = null; },
          });
          scmMounted = true;
        }
      } catch (_) { /* bundle load failure — island stays blank */ }
    };

    doMount();

    return () => {
      active = false;
      w.WickSCM?.unmount(capturedHost);
      scmMounted = false;
    };
  }

  $effect(() => {
    if (railTab !== "source" || !scmHostEl || !sessionId) return;
    return mountScmHost(scmHostEl);
  });

  $effect(() => {
    if (railTab !== "source" || !scmHostMobileEl || !sessionId) return;
    return mountScmHost(scmHostMobileEl);
  });

  /* ── file viewer ──────────────────────────────────────────────── */
  function openFileByPath(path: string) {
    if (!path) return;
    run(readFile(base, sessionId, path).pipe(Effect.provide(WickClientLayer)))
      .then((res) => {
        viewerFile = res;
        viewerDirty = false;
      })
      .catch((e: unknown) => toastError(`Read: ${e instanceof Error ? e.message : String(e)}`));
  }

  function openFile(f: ContextFileEntry) {
    if (f.isDir) return;
    openFileByPath(f.path);
  }

  function handleViewerSave(content: string) {
    if (!viewerFile) return;
    const path = viewerFile.path;
    run(saveFile(base, sessionId, path, content).pipe(Effect.provide(WickClientLayer)))
      .then(() => {
        if (viewerFile) viewerFile = { ...viewerFile, content };
        viewerDirty = false;
      })
      .catch((e: unknown) => toastError(`Save: ${e instanceof Error ? e.message : String(e)}`));
  }

  /* Prompt for a name, validate it, create the entry, and expand the
   * parent dir on success (mirrors legacy context.js createEntry). */
  function createEntry(isDir: boolean, parentDir?: string) {
    const raw = prompt(isDir ? "Directory name:" : "File name:");
    if (raw === null) return;
    const name = raw.trim();
    if (!isValidFileName(name)) {
      toastError("Invalid name", "No slashes or '..'. Open the folder first to nest.");
      return;
    }
    const path = parentDir ? `${parentDir}/${name}` : name;
    run(createFile(base, sessionId, path, isDir).pipe(Effect.provide(WickClientLayer)))
      .then(() => {
        if (parentDir) openDirs = { ...openDirs, [parentDir]: true };
        loadFiles();
      })
      .catch((e: unknown) => toastError(`Create: ${e instanceof Error ? e.message : String(e)}`));
  }

  /* Refetch the persisted conversation so a just-completed turn picks up
     server-derived artifacts — the live SSE turn is built client-side and
     carries none. showError surfaces a toast only on the initial load. */
  function loadConversation(showError = false) {
    return run(getConversation(base, sessionId).pipe(Effect.provide(WickClientLayer)))
      .then((res) => thread.setHistory(res.turns))
      .catch((e: unknown) => {
        if (showError) toastError(`History: ${e instanceof Error ? e.message : String(e)}`);
      });
  }

  /* ── SSE fan-out ──────────────────────────────────────────────── */
  function startSSE() {
    const stream = connectSession(base, sessionId);
    sseStream = stream;
    closeSSE = () => { sseStream = null; stream.close(); };

    stream.status.subscribe((s) => { sseStatus = s; });

    stream.onEvent((ev) => {
      thread.handleEvent(ev);

      if (ev.type === "ask_user") {
        try {
          showAsk(JSON.parse(ev.data ?? "{}"));
          userScrolledUp = false;
          setTimeout(() => scrollToBottom(), 50);
        } catch (_) { /* skip */ }
      } else if (ev.type === "ask_user_resolved") {
        try { hideAsk(JSON.parse(ev.data ?? "{}")); } catch (_) { /* skip */ }
      } else if (ev.type === "approval_request") {
        try {
          const req = JSON.parse(ev.data ?? "{}");
          approvalError = "";
          showApproval(req);
          if (!approvalsTabPending.some((p) => p.id === req.id)) {
            approvalsTabPending = [...approvalsTabPending, req];
          }
          notify("Approval needed", req.cmd ?? "");
        } catch (_) { /* skip */ }
      } else if (ev.type === "approval_resolved") {
        try {
          const payload = JSON.parse(ev.data ?? "{}");
          hideApproval(payload);
          // Drop the row too, so the tab does not keep offering buttons
          // for a request the daemon has already decided.
          if (payload?.id) {
            approvalsTabPending = approvalsTabPending.filter((p) => p.id !== payload.id);
          }
        } catch (_) { /* skip */ }
      } else if (ev.type === "done" || ev.type === "error") {
        void loadConversation();
        // A sub-agent's own lifecycle events are published on the CHILD's
        // session id, which this stream is not subscribed to. The leader's
        // end-of-turn is the reliable moment its delegations changed state,
        // so refresh the panel here as well as on lifecycle.
        scheduleSubAgentReload();
      } else if (ev.type === "tool_use" || ev.type === "tool_result") {
        // A delegation is made MID-TURN, and the leader emits no lifecycle
        // event while it keeps working — so without this the rail tab and
        // its badge did not appear until the turn ended or the page was
        // reloaded, which is precisely when you most want to see that
        // sub-agents are running.
        if (isDelegationTool(ev.tool_name)) scheduleSubAgentReload();
      } else if (ev.type === "lifecycle") {
        scheduleProcessReload();
        scheduleSubAgentReload();
        scheduleFileReload();
      } else if (ev.type === "git_status") {
        try {
          const d = JSON.parse(ev.data ?? "{}") as { total_changed?: number };
          if (typeof d.total_changed === "number") scmChangeCount = d.total_changed;
        } catch (_) { /* skip */ }
        scheduleFileReload();
      }
    });
  }

  /* ── header actions ───────────────────────────────────────────── */
  function handleKill() {
    confirmKill = { sid: sessionId, queued: false };
  }

  // Cancel one in-flight connector run behind a running tool call (the ✕ on a
  // wick_execute card). The run finalizes "cancelled" server-side and the agent
  // gets an explicit cancelled tool result; the connector_run(finished) SSE
  // event clears the button.
  function handleCancelRun(runId: string) {
    run(cancelRun(base, sessionId, runId).pipe(Effect.provide(WickClientLayer)))
      .then(() => toastOk("Operation cancelled"))
      .catch((e: unknown) => toastError("Cancel failed", e instanceof Error ? e.message : String(e)));
  }

  function doKill() {
    const target = confirmKill;
    confirmKill = null;
    if (!target) return;
    const action = target.queued
      ? dequeueProcess(base, target.sid)
      : killProcess(base, target.sid);
    run(action.pipe(Effect.provide(WickClientLayer)))
      .then(() => {
        // Kill succeeded server-side, but the lifecycle:idle/killed SSE
        // event that would normally clear "thinking…" can be lost — the
        // process may have already died silently before this click (an
        // idle-timeout race), or the SSE stream itself can drop the
        // message. Clear the panel's live/typing state directly so Kill
        // always has a visible effect instead of looking like a no-op.
        if (target.sid === sessionId && !target.queued) {
          thread.handleKilledLocally();
        }
        return loadProcesses();
      })
      .catch((e: unknown) => toastError(`Kill: ${e instanceof Error ? e.message : String(e)}`));
  }

  async function handleDelete() {
    try {
      await run(deleteSession(base, sessionId).pipe(Effect.provide(WickClientLayer)));
      push("/");
    } catch (e: unknown) {
      toastError(`Delete: ${e instanceof Error ? e.message : String(e)}`);
    }
  }

  /* ── ask / approval handlers ──────────────────────────────────── */
  async function handleAskSubmit(answer: AskAnswer) {
    hideAsk();
    try {
      await run(answerAsk(base, sessionId, answer).pipe(Effect.provide(WickClientLayer)));
    } catch (e: unknown) {
      toastError(`Answer: ${e instanceof Error ? e.message : String(e)}`);
    }
  }

  // Which pending row (if any) has its note field open, and the text in
  // it. Keyed by request id so a note typed for one row can't be sent
  // against another.
  let tabNoteFor = $state("");
  let tabNote = $state("");

  function sendTabGuide(req: import("../types/agents.js").ApprovalRequest) {
    const trimmed = tabNote.trim();
    if (!trimmed) return;
    tabNoteFor = "";
    tabNote = "";
    void handleApprovalDecide("guide", trimmed, req);
  }

  // reason carries a "guide" decision's correction back to the agent.
  // req is explicit so the Approvals tab can decide a row that is not the
  // one in the modal; it defaults to the modal's own request.
  async function handleApprovalDecide(
    decision: ApprovalDecision,
    reason?: string,
    req?: import("../types/agents.js").ApprovalRequest,
  ) {
    const approval = req ?? get(currentApproval);
    if (!approval) return;
    approvalError = "";
    hideApproval({ id: approval.id });
    try {
      await run(
        sendApprovalDecision(base, sessionId, {
          id: approval.id,
          decision,
          match_key: approval.match_key,
          ...(reason ? { reason } : {}),
        }).pipe(Effect.provide(WickClientLayer))
      );
      approvalsTabPending = approvalsTabPending.filter((p) => p.id !== approval.id);
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : String(e);
      // Expired requests stay closed. Reopening one would leave a modal
      // whose every button re-POSTs a dead id for another 410, escapable
      // only by reloading the page.
      if (isExpiredApprovalError(e)) {
        approvalsTabPending = approvalsTabPending.filter((p) => p.id !== approval.id);
        toastWarn("Approval expired — the command was blocked. Ask the agent to retry it.");
        return;
      }
      approvalError = msg;
      // Only the modal reopens on a retryable failure; a tab row keeps
      // its own buttons and needs no modal.
      if (!req) showApproval(approval);
      else toastError(`Approval: ${msg}`);
    }
  }

  /* ── send message ─────────────────────────────────────────────── */
  async function handleSend(msg: { text: string; files: File[] }) {
    const optimisticAttachments = msg.files.map((f) => ({
      name: f.name,
      stored_name: f.name,
      url: URL.createObjectURL(f),
      mime: f.type || "application/octet-stream",
      size: f.size,
    }));
    thread.appendUserTurn(msg.text, optimisticAttachments);
    scrollToBottom();
    try {
      await run(sendMessage(base, sessionId, msg).pipe(Effect.provide(WickClientLayer)));
    } catch (e: unknown) {
      toastError(`Send: ${e instanceof Error ? e.message : String(e)}`);
    }
  }

  /* ── rail toggle ──────────────────────────────────────────────── */
  function toggleRail(tab: RailTab) {
    railTab = railTab === tab ? null : tab;
  }

  // When a panel opens (via a `/` command or a tab click), move focus into it so
  // it's immediately keyboard-navigable and Esc feels natural.
  $effect(() => {
    if (railTab === null) return;
    queueMicrotask(() => scmSideEl?.focus());
  });

  // Refresh the file tree when the context panel opens — SSE-driven reloads are
  // skipped while it's closed, so pick up any files written meanwhile.
  $effect(() => {
    if (railTab === "context") reloadFilesSilently();
  });

  // Esc closes the open side panel — "sat set". Guarded on defaultPrevented so
  // an Esc already consumed by a higher overlay (command menu, picker, file
  // viewer) doesn't also close the panel.
  $effect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key !== "Escape" || e.defaultPrevented) return;
      if (railTab !== null) { e.preventDefault(); railTab = null; }
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  });

  /* ── mount/unmount ────────────────────────────────────────────── */
  onMount(() => {
    // htmlfile fences in the transcript resolve their path against this base +
    // session; set it before the first enrich pass runs (loadConversation).
    setFileContext(base, sessionId);
    loadConversation(true);

    run(getSessionMeta(base, sessionId).pipe(Effect.provide(WickClientLayer)))
      .then((res) => {
        title = res.label || res.id;
        agentLabel = res.active_agent || "";
        activeProvider = res.provider || res.active_agent || null;
        activeModelID = res.model_id || "";
        activeProjectId = res.project_id || null;
        // The widget CSP arrives with meta, which can land AFTER the first
        // artifacts have mounted under the blocked fallback. Artifacts
        // subscribe to this policy and rebuild their srcdoc when it changes,
        // so the transcript still renders immediately and widgets pick up the
        // real policy as soon as it is known.
        setWidgetPolicy(res.widget);
      })
      .catch(() => { title = sessionId; });

    // A bookmarked sub-agent URL redirects here as
    // ?rail=subagents&sub=<childSessionId>. Honour it so the link lands on
    // that child's row rather than a plain conversation view.
    try {
      const q = new URLSearchParams(window.location.search);
      if (q.get("rail") === "subagents") {
        railTab = "subagents";
        selectedSubAgent = q.get("sub");
      }
    } catch (_) { /* no query string — nothing to restore */ }

    startSSE();
    loadFiles();
    loadProcesses();
    loadSubAgents();
    loadAgentMessages();
    loadAgentRoles();
    loadWorkspace();
    loadSchedules();
    loadTicket();
    loadProviderOptions();
    loadProjectOptions();
    loadPendingAsk();
    // The Browser rail tab only appears if there's an enabled playwright_browser
    // instance to drive. Best-effort: on any error, leave it hidden.
    run(listBrowserInstances().pipe(Effect.provide(WickClientLayer)))
      .then((rows) => { hasBrowserInstance = rows.some((r) => !r.disabled); })
      .catch(() => { hasBrowserInstance = false; });
    // No interval polling for /processes: the SSE `lifecycle` event already
    // fires whenever a process starts/stops, and triggers a (debounced)
    // loadProcesses(). Polling on top of that just stacked redundant fetches.

    // A backgrounded tab (or a downed/restarted server) can leave the stream
    // stalled. On return-to-foreground or network-restore, nudge it to
    // reconnect + replay, and re-pull the authoritative conversation so the
    // panel un-sticks without a manual reload.
    document.addEventListener("visibilitychange", handleResync);
    window.addEventListener("online", handleResync);
  });

  function handleResync() {
    if (document.visibilityState !== "visible") return;
    sseStream?.resync();
    void loadConversation();
    scheduleProcessReload();
    scheduleSubAgentReload();
  }

  onDestroy(() => {
    if (fileReloadTimer !== null) clearTimeout(fileReloadTimer);
    if (processReloadTimer !== null) clearTimeout(processReloadTimer);
    document.removeEventListener("visibilitychange", handleResync);
    window.removeEventListener("online", handleResync);
    closeSSE?.();
    unsubTurns();
    unsubLive();
    unsubTyping();
    unsubLifecycle();
    unsubMeta();
  });

  // True once we confirm an enabled playwright_browser instance exists; gates
  // the Browser rail tab (hidden when there's nothing to drive).
  let hasBrowserInstance = $state(false);

  const railTabsAll: { id: RailTab; label: string; icon: string }[] = [
    {
      id: "subagents",
      label: "Sub-agents",
      icon: '<circle cx="8" cy="4" r="2.5"></circle><circle cx="3.5" cy="12" r="2"></circle><circle cx="12.5" cy="12" r="2"></circle><path d="M8 6.5v2M8 8.5H4.5a1 1 0 00-1 1v.5M8 8.5h3.5a1 1 0 011 1v.5" stroke-linecap="round" stroke-linejoin="round"></path>',
    },
    {
      id: "ticket",
      label: "Ticket",
      icon: '<path d="M2 6a1 1 0 011-1h10a1 1 0 011 1v1.5a1.5 1.5 0 000 3V12a1 1 0 01-1 1H3a1 1 0 01-1-1v-1.5a1.5 1.5 0 000-3V6z" stroke-linejoin="round"></path><path d="M9.5 5v1.2M9.5 7.7v1.2M9.5 10.4V12" stroke-linecap="round"></path>',
    },
    {
      id: "context",
      label: "Context",
      icon: '<path d="M2 4a1 1 0 011-1h3l2 2h5a1 1 0 011 1v6a1 1 0 01-1 1H3a1 1 0 01-1-1V4z" stroke-linejoin="round"></path>',
    },
    {
      id: "process",
      label: "Process",
      icon: '<rect x="2" y="2" width="12" height="12" rx="1.5" stroke-linejoin="round"/><path d="M5 8h6M5 5.5h4M5 10.5h3" stroke-linecap="round"/>',
    },
    {
      id: "workspace",
      label: "Workspace",
      icon: '<circle cx="8" cy="8" r="2"></circle><path d="M8 1.5v2M8 12.5v2M1.5 8h2M12.5 8h2M3.4 3.4l1.4 1.4M11.2 11.2l1.4 1.4M12.6 3.4l-1.4 1.4M4.8 11.2l-1.4 1.4" stroke-linecap="round"></path>',
    },
    {
      id: "scheduled",
      label: "Scheduled",
      icon: '<circle cx="8" cy="9" r="5.5"></circle><path d="M8 6v3l2 1.5M8 1.5h0M3 2.5L1.5 4M13 2.5L14.5 4" stroke-linecap="round" stroke-linejoin="round"></path>',
    },
    {
      id: "browser",
      label: "Browser",
      icon: '<circle cx="8" cy="8" r="6.5"></circle><path d="M1.5 8h13M8 1.5c1.8 1.7 2.8 4 2.8 6.5S9.8 12.8 8 14.5C6.2 12.8 5.2 10.5 5.2 8S6.2 3.2 8 1.5z" stroke-linecap="round" stroke-linejoin="round"></path>',
    },
    {
      id: "source",
      label: "Source",
      icon: '<circle cx="4" cy="4" r="1.5"></circle><circle cx="4" cy="12" r="1.5"></circle><circle cx="12" cy="5" r="1.5"></circle><path d="M4 5.5v5M5.5 4H9a2 2 0 012 2v0" stroke-linecap="round"></path>',
    },
  ];

  // Drop the Browser tab until an enabled playwright_browser instance is known,
  // and the Sub-agents tab until this session has actually delegated. The
  // Sub-agents filter uses the TOTAL count, not the live one, so finished
  // results stay reachable after every sub-agent has returned.
  const railTabs = $derived(
    railTabsAll.filter(
      (t) =>
        (t.id !== "browser" || hasBrowserInstance) &&
        (t.id !== "subagents" || subAgents.length > 0) &&
        (t.id !== "ticket" || ticketInfo?.config?.enabled === true),
    ),
  );
  // Close the Ticket panel if the project turns ticket mode off mid-session.
  $effect(() => {
    if (railTab === "ticket" && ticketInfo?.config?.enabled !== true) railTab = null;
  });
  // If the Browser panel is open but its tab just disappeared, close the panel.
  $effect(() => {
    if (railTab === "browser" && !hasBrowserInstance) railTab = null;
  });
  // Same for Sub-agents: without this the panel is orphaned when the last
  // sub-agent row goes away.
  $effect(() => {
    if (railTab === "subagents" && subAgents.length === 0) railTab = null;
  });

  const sideOpen = $derived(railTab !== null);

  const contextCount = $derived(filesVal.filter((f) => !f.isDir).length);
  // Idle-fallback rows (kind === "idle") carry only the provider/agent name
  // for the composer toolbar — they are not real processes, so exclude them
  // from the process panel and the "Process N" rail badge. A session whose
  // subprocess was killed/exited falls back to an idle row, which drops both
  // the card and the count instead of lingering as a stale "dead" entry.
  const liveProcesses = $derived(filterLiveProcesses(processes));
  const processCount = $derived(liveProcesses.length);
  const workspaceCount = $derived(wsInstances.length);
  const scheduledCount = $derived(schedules.filter((s) => (s.status === "pending" || s.status === "active") && !s.paused).length);
  // Badge counts LIVE sub-agents only. Using the total would leave the
  // badge stuck at "3" after everything finished; the tab itself stays
  // visible on the total so results remain readable.
  const subAgentCount = $derived(liveSubAgents(subAgents).length);

  /* Whether a rail has work running behind a closed panel.

     The badge alone cannot say this: "2" reads the same whether both
     sub-agents are grinding away or both are queued behind a slot. A
     spinning ring on the tab is the only thing that tells you something
     is happening on a rail you are not looking at. */
  // How many are actually producing output, not merely unfinished. Drives
  // both the rail's spinning ring and the header badge, so the two can
  // never disagree about whether anything is running.
  const busySubAgentCount = $derived(
    subAgents.filter((s) => isSubAgentWorking(s.status, s.lifecycle)).length,
  );
  const subAgentsBusy = $derived(busySubAgentCount > 0);
  function railBusy(id: RailTab): boolean {
    return id === "subagents" && subAgentsBusy;
  }

  function railCount(id: RailTab): number {
    if (id === "context") return contextCount;
    if (id === "process") return processCount;
    if (id === "workspace") return workspaceCount;
    if (id === "scheduled") return scheduledCount;
    if (id === "subagents") return subAgentCount;
    return 0;
  }
</script>

<!-- Full-height flex row: main area + vertical rail -->
<div class="flex h-full min-w-0 overflow-hidden">

  <!-- Centre column: header + thread + ask + composer -->
  <div class="relative flex flex-col flex-1 min-w-0" data-session-id={sessionId}>

    <!-- Zone 1: header bar -->
    <ConversationHeader
      title={threadMeta.title || title}
      {agentLabel}
      {sseStatus}
      lifecycle={agentLifecycle}
      {idleTimeoutMs}
      subAgentsBusy={busySubAgentCount}
      {activeView}
      onKill={handleKill}
      onDelete={handleDelete}
      onTabChange={handleTabChange}
    />

    <!-- Zone 2: main content area — switches by activeView -->
    {#if activeView === "conversation"}
      <div
        class="flex-1 min-h-0 overflow-y-auto bg-white-200 dark:bg-navy-800"
        bind:this={threadEl}
        data-chat-panel
      >
        <div class="max-w-4xl mx-auto w-full px-6 pt-14 pb-6 md:pt-6">
          <ConversationThread {turns} {live} {typing} loadTrace={(turnId) => Effect.runPromise(getTurnTrace(base, sessionId, turnId).pipe(Effect.provide(WickClientLayer)))} onOpenPath={openFileByPath} onCancelRun={handleCancelRun} onDismissTool={(toolUseId) => thread.dismissToolBlock(toolUseId)} onOpenSubAgent={openSubAgent} />
        </div>
      </div>

      <!-- Zone 3: ask inline -->
      <div class="shrink-0 px-4 md:px-6 bg-white-200 dark:bg-navy-800">
        <div class="max-w-4xl mx-auto">
          <AskUserModal
            request={$currentAsk}
            onSubmit={handleAskSubmit}
            onDismiss={hideAsk}
          />
        </div>
      </div>

      <!-- Zone 4: composer with leading toolbar actions in one row -->
      <div class="relative shrink-0 px-6 bg-white-200 dark:bg-navy-800">
        {#if showJumpBtn}
          <button
            type="button"
            onclick={scrollToBottom}
            class="absolute left-1/2 -translate-x-1/2 -top-3 z-30 inline-flex items-center gap-1.5 rounded-full border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-3 py-1 text-[11px] font-medium text-black-700 dark:text-black-600 shadow-sm hover:bg-white-200 dark:hover:bg-navy-800 hover:text-black-900 dark:hover:text-white-100 transition-colors"
            title="Scroll to latest (Ctrl+↓)"
          >
            <svg viewBox="0 0 16 16" class="h-3 w-3" fill="none" stroke="currentColor" stroke-width="1.75">
              <path d="M8 3v9M4 9l4 4 4-4" stroke-linecap="round" stroke-linejoin="round"></path>
            </svg>
            <span>Jump to latest</span>
            <kbd class="rounded border border-white-400 dark:border-navy-600 bg-white-200 dark:bg-navy-800 px-1 text-[10px] font-mono text-black-600 dark:text-black-700">Ctrl+↓</kbd>
          </button>
        {/if}
        <div class="relative max-w-4xl mx-auto pb-6">
          <!-- /project picker floats above the composer. /provider now opens
               the composer's own provider drill (see composerRef), so no
               separate provider modal. -->
          <SwitchModal
            open={projectPickerOpen}
            title="Switch project"
            items={projectItems}
            onSelect={(id) => handleProjectChange(id)}
            onClose={() => (projectPickerOpen = false)}
          />
          <!-- /thinking (and future per-session knobs) — a popover that SAVES
               to the session override store, never sends a message. -->
          <OverridePopover
            open={overridePopoverOpen}
            title="🧠 Thinking"
            schema={overrideSchema}
            values={overrideValues}
            onChange={saveOverride}
            onClose={() => (overridePopoverOpen = false)}
          />
          <Composer
            bind:this={composerRef}
            onSend={handleSend}
            placeholder="Ask anything…   / commands · @ files"
            notifyKey={NOTIFY_KEY}
            provider={providerSelect}
            project={projectSelect}
            onSearchFiles={searchMentionFiles}
            mentionAgents={mentionableAgents}
            commands={composerCommands}
          />
        </div>
      </div>
    {:else if activeView === "approvals"}
      <div class="flex-1 min-h-0 overflow-y-auto bg-white-200 dark:bg-navy-800">
        <div class="max-w-4xl mx-auto w-full px-4 md:px-6 pt-14 pb-6 md:pt-16 flex flex-col gap-4">
          {#if approvalsTabPending.length > 0}
            <div>
              <h3 class="text-sm font-semibold text-black-900 dark:text-white-100 mb-3">Pending approvals</h3>
              <div class="flex flex-col gap-3">
                {#each approvalsTabPending as req (req.id)}
                  <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-sm px-5 py-4 space-y-3">
                    <dl class="space-y-1.5 text-xs">
                      <div class="flex gap-3">
                        <dt class="w-20 shrink-0 text-black-700 dark:text-black-600">Agent</dt>
                        <dd class="font-mono text-black-900 dark:text-white-100">{req.agent_name || "—"}</dd>
                      </div>
                      <div class="flex gap-3">
                        <dt class="w-20 shrink-0 text-black-700 dark:text-black-600">Tool</dt>
                        <dd class="font-mono text-black-900 dark:text-white-100">{req.tool || "—"}</dd>
                      </div>
                      <div class="flex gap-3">
                        <dt class="w-20 shrink-0 text-black-700 dark:text-black-600">Command</dt>
                        <dd class="font-mono text-black-900 dark:text-white-100 break-all">{req.cmd || "—"}</dd>
                      </div>
                    </dl>
                    <div class="flex flex-col gap-2">
                      <div class="grid grid-cols-1 sm:grid-cols-3 gap-2">
                        <button
                          type="button"
                          class="rounded-lg bg-green-500 px-3 py-2 text-xs font-medium text-white-100 hover:bg-green-600 transition-colors"
                          onclick={() => handleApprovalDecide("approve_once", undefined, req)}
                        >Approve once</button>
                        <button
                          type="button"
                          class="rounded-lg border border-green-500 dark:border-green-600 px-3 py-2 text-xs font-medium text-green-700 dark:text-green-400 hover:bg-green-50 dark:hover:bg-green-900/20 transition-colors"
                          onclick={() => handleApprovalDecide("approve_session", undefined, req)}
                        >Allow session</button>
                        <button
                          type="button"
                          class="rounded-lg border border-green-500 dark:border-green-600 px-3 py-2 text-xs font-medium text-green-700 dark:text-green-400 hover:bg-green-50 dark:hover:bg-green-900/20 transition-colors"
                          onclick={() => handleApprovalDecide("approve_always", undefined, req)}
                        >Always allow</button>
                      </div>
                      {#if tabNoteFor === req.id}
                        <div class="flex flex-col gap-2">
                          <textarea
                            rows="2"
                            bind:value={tabNote}
                            onkeydown={(e) => {
                              if (e.key === "Enter" && !e.shiftKey && !e.ctrlKey && !e.metaKey) {
                                e.preventDefault();
                                sendTabGuide(req);
                              } else if (e.key === "Escape") {
                                e.preventDefault();
                                tabNoteFor = "";
                              }
                            }}
                            placeholder="Why not, and what should it do instead?"
                            class="w-full rounded-lg border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-xs text-black-900 dark:text-white-100 placeholder:text-black-700 dark:placeholder:text-black-600 focus:outline-none focus:border-green-500 resize-none"
                          ></textarea>
                          <div class="grid grid-cols-2 gap-2">
                            <button
                              type="button"
                              class="rounded-lg border border-white-400 dark:border-navy-600 px-3 py-2 text-xs font-medium text-black-800 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-600 transition-colors"
                              onclick={() => { tabNoteFor = ""; tabNote = ""; }}
                            >Cancel</button>
                            <button
                              type="button"
                              disabled={tabNote.trim() === ""}
                              class="rounded-lg bg-cau-400 px-3 py-2 text-xs font-medium text-white-100 hover:bg-cau-500 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
                              onclick={() => sendTabGuide(req)}
                            >Send</button>
                          </div>
                        </div>
                      {:else}
                        <div class="grid grid-cols-2 gap-2">
                          <button
                            type="button"
                            class="rounded-lg border border-cau-400 px-3 py-2 text-xs font-medium text-cau-400 hover:bg-cau-50 dark:hover:bg-cau-900/20 transition-colors"
                            onclick={() => { tabNoteFor = req.id; tabNote = ""; }}
                          >Block with note</button>
                          <button
                            type="button"
                            class="rounded-lg bg-red-600 px-3 py-2 text-xs font-medium text-white-100 hover:bg-red-700 transition-colors"
                            onclick={() => handleApprovalDecide("block", undefined, req)}
                          >Block</button>
                        </div>
                      {/if}
                    </div>
                  </div>
                {/each}
              </div>
            </div>
          {:else}
            <div class="text-sm text-black-700 dark:text-black-600 italic">No pending approvals.</div>
          {/if}
          <ApprovedPanel
            sessionApproved={approvalsTabSession}
            alwaysApproved={approvalsTabAlways}
            onRevoke={handleRevokeApproval}
          />
        </div>
      </div>
    {:else if activeView === "commands"}
      <div data-placeholder-view class="flex-1 min-h-0 flex items-center justify-center bg-white-200 dark:bg-navy-800 pt-14">
        <p class="text-sm text-black-600 dark:text-black-700">Commands view — coming soon to the new UI</p>
      </div>
    {:else if activeView === "raw"}
      <div class="flex-1 min-h-0 flex flex-col bg-white-200 dark:bg-navy-800 pt-14">
        <div class="flex items-center justify-between gap-2 px-4 py-2 border-b border-white-300 dark:border-navy-600 shrink-0">
          <span class="text-xs font-medium text-black-700 dark:text-black-600">Raw trace · {turns.length} turn{turns.length === 1 ? "" : "s"}</span>
          <button
            type="button"
            onclick={copyRaw}
            disabled={turns.length === 0}
            class="rounded-lg border border-white-400 dark:border-navy-600 px-2.5 py-1 text-xs font-medium text-black-800 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800 transition-colors disabled:opacity-50 disabled:cursor-default"
          >Copy</button>
        </div>
        {#if turns.length === 0}
          <div class="flex-1 flex items-center justify-center">
            <p class="text-sm text-black-600 dark:text-black-700">No trace yet.</p>
          </div>
        {:else}
          <div class="flex-1 min-h-0 overflow-auto px-3 py-3">
            <JsonTree value={rawData} />
          </div>
        {/if}
      </div>
    {/if}
  </div>

  <!-- Side panel: slides in when a rail tab is active -->
  {#if sideOpen}
    <div
      bind:this={scmSideEl}
      tabindex="-1"
      class={`relative hidden lg:flex flex-col outline-none ${railTab === "source" ? "" : "w-80"} shrink-0 border-l border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 overflow-hidden`}
      style={railTab === "source" ? `width:${scmWidth}px` : ""}
    >
      {#if railTab === "source"}
        <button
          type="button"
          aria-label="Resize source panel"
          title="Drag to resize"
          data-scm-resize
          onpointerdown={startScmResize}
          class="absolute left-0 top-0 z-10 h-full w-1.5 -translate-x-1/2 cursor-col-resize bg-transparent hover:bg-green-500/40 focus-visible:bg-green-500/40 transition-colors"
        ></button>
        <div class="flex-1 overflow-hidden dark:bg-navy-700" data-scm-host bind:this={scmHostEl}></div>
      {:else if railTab === "ticket" && ticketInfo}
        <TicketPanel {base} {sessionId} info={ticketInfo} onSaved={loadTicket} />
      {:else if railTab === "context"}
        <ContextPanel
          cwd={cwdVal}
          files={filesVal}
          search={fileSearch}
          {openDirs}
          loading={filesLoading}
          loadError={filesLoadError}
          onSearch={(s) => { fileSearch = s; }}
          onToggleDir={(p) => { openDirs = { ...openDirs, [p]: !openDirs[p] }; }}
          onOpen={openFile}
          onRefresh={loadFiles}
          onNewFile={() => createEntry(false)}
          onNewDir={() => createEntry(true)}
          onDownload={(p) => { window.open(downloadURL(base, sessionId, p), "_blank"); }}
          onDelete={(p) => {
            run(deleteFile(base, sessionId, p).pipe(Effect.provide(WickClientLayer)))
              .then(loadFiles)
              .catch((e: unknown) => toastError(`Delete: ${e instanceof Error ? e.message : String(e)}`));
          }}
          onNewHere={(dir) => createEntry(false, dir)}
        />
      {:else if railTab === "process"}
        <ProcessPanel
          processes={liveProcesses}
          onKill={(sid) => { confirmKill = { sid, queued: false }; }}
          onDequeue={(sid) => { confirmKill = { sid, queued: true }; }}
        />
      {:else if railTab === "subagents"}
        <SubAgentPanel
          {incident}
          {subAgents}
          selectedId={selectedSubAgent}
          onSelect={(cid) => { selectedSubAgent = selectedSubAgent === cid ? null : cid; }}
          onInterrupt={stopSubAgent}
          onInterruptAll={stopAllSubAgents}
          onContinue={continueSubAgentRow}
          messages={agentMessages}
          {hopsLeft}
          onBumpHops={bumpAgentHops}
        />
      {:else if railTab === "workspace"}
        <WorkspacePanel
          instances={wsInstances}
          bases={wsBases}
          deleted={wsDeleted}
          openCards={wsOpenCards}
          onAdd={(baseKey) => {
            run(addWorkspace(base, sessionId, baseKey).pipe(Effect.provide(WickClientLayer)))
              .then(loadWorkspace)
              .catch((e: unknown) => toastError(`Add: ${e instanceof Error ? e.message : String(e)}`));
          }}
          onSave={(cid, values) => {
            run(saveWorkspaceConfig(base, sessionId, cid, values).pipe(Effect.provide(WickClientLayer)))
              .then(loadWorkspace)
              .catch((e: unknown) => toastError(`Save: ${e instanceof Error ? e.message : String(e)}`));
          }}
          onTest={(cid, config) =>
            run(testWorkspace(base, sessionId, cid, config).pipe(Effect.provide(WickClientLayer)))
              .catch((e: unknown) => {
                toastError(`Test: ${e instanceof Error ? e.message : String(e)}`);
                return null;
              })
          }
          onRename={(cid, label) => {
            run(renameWorkspace(base, sessionId, cid, label).pipe(Effect.provide(WickClientLayer)))
              .then(loadWorkspace)
              .catch((e: unknown) => toastError(`Rename: ${e instanceof Error ? e.message : String(e)}`));
          }}
          onDuplicate={(cid) => {
            run(duplicateWorkspace(base, sessionId, cid).pipe(Effect.provide(WickClientLayer)))
              .then(loadWorkspace)
              .catch((e: unknown) => toastError(`Duplicate: ${e instanceof Error ? e.message : String(e)}`));
          }}
          onDelete={(cid) => {
            run(removeWorkspace(base, sessionId, cid).pipe(Effect.provide(WickClientLayer)))
              .then(loadWorkspace)
              .catch((e: unknown) => toastError(`Remove: ${e instanceof Error ? e.message : String(e)}`));
          }}
        />
      {:else if railTab === "scheduled"}
        <SchedulePanel
          {schedules}
          projects={projectOptions}
          currentProjectId={activeProjectId ?? ""}
          onReschedule={(id, patch) => {
            run(rescheduleSchedule(base, sessionId, id, {
              runAt: patch.run_at, every: patch.every, cron: patch.cron,
              message: patch.message, maxRuns: patch.max_runs,
              projectId: patch.project_id, sessionMode: patch.session_mode,
              sessionTemplate: patch.session_template,
            }).pipe(Effect.provide(WickClientLayer)))
              .then(loadSchedules)
              .catch((e: unknown) => toastError(`Edit: ${e instanceof Error ? e.message : String(e)}`));
          }}
          onRunNow={(id) => {
            run(runScheduleNow(base, sessionId, id).pipe(Effect.provide(WickClientLayer)))
              .then(loadSchedules)
              .catch((e: unknown) => toastError(`Run now: ${e instanceof Error ? e.message : String(e)}`));
          }}
          onCreate={(args) =>
            run(createSchedule(base, sessionId, args).pipe(Effect.provide(WickClientLayer)))
              .then(() => { loadSchedules(); return true; })
              .catch((e: unknown) => { toastError(`Schedule: ${e instanceof Error ? e.message : String(e)}`); return false; })
          }
          onCancel={(id) => {
            run(cancelSchedule(base, sessionId, id).pipe(Effect.provide(WickClientLayer)))
              .then(loadSchedules)
              .catch((e: unknown) => toastError(`Cancel: ${e instanceof Error ? e.message : String(e)}`));
          }}
          onPause={(id) => {
            run(pauseSchedule(base, sessionId, id).pipe(Effect.provide(WickClientLayer)))
              .then(loadSchedules)
              .catch((e: unknown) => toastError(`Pause: ${e instanceof Error ? e.message : String(e)}`));
          }}
          onResume={(id) => {
            run(resumeSchedule(base, sessionId, id).pipe(Effect.provide(WickClientLayer)))
              .then(loadSchedules)
              .catch((e: unknown) => toastError(`Resume: ${e instanceof Error ? e.message : String(e)}`));
          }}
        />
      {:else if railTab === "browser"}
        <BrowserPanel onError={(m) => toastError(m)} />
      {/if}
    </div>

    <!-- Mobile slide-over for context/process/workspace (below lg) -->
    <div
      class="lg:hidden fixed inset-0 z-40 flex"
    >
      <button
        type="button"
        aria-label="Close panel"
        class="absolute inset-0 bg-black/40 backdrop-blur-sm"
        onclick={() => { railTab = null; }}
      ></button>
      <div
        class="relative ml-auto flex flex-col w-full sm:w-[420px] bg-white-100 dark:bg-navy-700 border-l border-white-300 dark:border-navy-600 shadow-xl overflow-hidden"
      >
        <div class="flex items-center justify-between px-4 py-3 border-b border-white-300 dark:border-navy-600 shrink-0">
          <h2 class="text-sm font-semibold text-black-900 dark:text-white-100 capitalize">{railTab}</h2>
          <button
            type="button"
            aria-label="Close"
            onclick={() => { railTab = null; }}
            class="inline-flex h-7 w-7 items-center justify-center rounded-lg text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800 transition-colors"
          >
            <svg viewBox="0 0 16 16" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="2">
              <path d="M4 4l8 8M12 4l-8 8" stroke-linecap="round"></path>
            </svg>
          </button>
        </div>
        <div class="flex-1 overflow-hidden flex flex-col">
          {#if railTab === "source"}
            <div class="flex-1 overflow-hidden dark:bg-navy-700" data-scm-host-mobile bind:this={scmHostMobileEl}></div>
          {:else if railTab === "ticket" && ticketInfo}
            <TicketPanel {base} {sessionId} info={ticketInfo} onSaved={loadTicket} />
          {:else if railTab === "context"}
            <ContextPanel
              cwd={cwdVal}
              files={filesVal}
              search={fileSearch}
              {openDirs}
              loading={filesLoading}
              loadError={filesLoadError}
              onSearch={(s) => { fileSearch = s; }}
              onToggleDir={(p) => { openDirs = { ...openDirs, [p]: !openDirs[p] }; }}
              onOpen={openFile}
              onRefresh={loadFiles}
              onNewFile={() => createEntry(false)}
              onNewDir={() => createEntry(true)}
              onDownload={(p) => { window.open(downloadURL(base, sessionId, p), "_blank"); }}
              onDelete={(p) => {
                run(deleteFile(base, sessionId, p).pipe(Effect.provide(WickClientLayer)))
                  .then(loadFiles)
                  .catch((e: unknown) => toastError(`Delete: ${e instanceof Error ? e.message : String(e)}`));
              }}
              onNewHere={(dir) => createEntry(false, dir)}
            />
          {:else if railTab === "process"}
            <ProcessPanel
              processes={liveProcesses}
              onKill={(sid) => { confirmKill = { sid, queued: false }; }}
              onDequeue={(sid) => { confirmKill = { sid, queued: true }; }}
            />
          {:else if railTab === "subagents"}
            <SubAgentPanel
              {incident}
              {subAgents}
              selectedId={selectedSubAgent}
              onSelect={(cid) => { selectedSubAgent = selectedSubAgent === cid ? null : cid; }}
              onInterrupt={stopSubAgent}
              onInterruptAll={stopAllSubAgents}
              onContinue={continueSubAgentRow}
              messages={agentMessages}
              {hopsLeft}
              onBumpHops={bumpAgentHops}
            />
          {:else if railTab === "workspace"}
            <WorkspacePanel
              instances={wsInstances}
              bases={wsBases}
              deleted={wsDeleted}
              openCards={wsOpenCards}
              onAdd={(baseKey) => {
                run(addWorkspace(base, sessionId, baseKey).pipe(Effect.provide(WickClientLayer)))
                  .then(loadWorkspace)
                  .catch((e: unknown) => toastError(`Add: ${e instanceof Error ? e.message : String(e)}`));
              }}
              onSave={(cid, values) => {
                run(saveWorkspaceConfig(base, sessionId, cid, values).pipe(Effect.provide(WickClientLayer)))
                  .then(loadWorkspace)
                  .catch((e: unknown) => toastError(`Save: ${e instanceof Error ? e.message : String(e)}`));
              }}
              onTest={(cid, config) =>
                run(testWorkspace(base, sessionId, cid, config).pipe(Effect.provide(WickClientLayer)))
                  .catch((e: unknown) => {
                    toastError(`Test: ${e instanceof Error ? e.message : String(e)}`);
                    return null;
                  })
              }
              onRename={(cid, label) => {
                run(renameWorkspace(base, sessionId, cid, label).pipe(Effect.provide(WickClientLayer)))
                  .then(loadWorkspace)
                  .catch((e: unknown) => toastError(`Rename: ${e instanceof Error ? e.message : String(e)}`));
              }}
              onDuplicate={(cid) => {
                run(duplicateWorkspace(base, sessionId, cid).pipe(Effect.provide(WickClientLayer)))
                  .then(loadWorkspace)
                  .catch((e: unknown) => toastError(`Duplicate: ${e instanceof Error ? e.message : String(e)}`));
              }}
              onDelete={(cid) => {
                run(removeWorkspace(base, sessionId, cid).pipe(Effect.provide(WickClientLayer)))
                  .then(loadWorkspace)
                  .catch((e: unknown) => toastError(`Remove: ${e instanceof Error ? e.message : String(e)}`));
              }}
            />
          {:else if railTab === "scheduled"}
            <SchedulePanel
              {schedules}
              projects={projectOptions}
              currentProjectId={activeProjectId ?? ""}
              onReschedule={(id, patch) => {
                run(rescheduleSchedule(base, sessionId, id, {
                  runAt: patch.run_at, every: patch.every, cron: patch.cron,
                  message: patch.message, maxRuns: patch.max_runs,
                  projectId: patch.project_id, sessionMode: patch.session_mode,
                  sessionTemplate: patch.session_template,
                }).pipe(Effect.provide(WickClientLayer)))
                  .then(loadSchedules)
                  .catch((e: unknown) => toastError(`Edit: ${e instanceof Error ? e.message : String(e)}`));
              }}
              onRunNow={(id) => {
                run(runScheduleNow(base, sessionId, id).pipe(Effect.provide(WickClientLayer)))
                  .then(loadSchedules)
                  .catch((e: unknown) => toastError(`Run now: ${e instanceof Error ? e.message : String(e)}`));
              }}
              onCreate={(args) =>
                run(createSchedule(base, sessionId, args).pipe(Effect.provide(WickClientLayer)))
                  .then(() => { loadSchedules(); return true; })
                  .catch((e: unknown) => { toastError(`Schedule: ${e instanceof Error ? e.message : String(e)}`); return false; })
              }
              onCancel={(id) => {
                run(cancelSchedule(base, sessionId, id).pipe(Effect.provide(WickClientLayer)))
                  .then(loadSchedules)
                  .catch((e: unknown) => toastError(`Cancel: ${e instanceof Error ? e.message : String(e)}`));
              }}
              onPause={(id) => {
                run(pauseSchedule(base, sessionId, id).pipe(Effect.provide(WickClientLayer)))
                  .then(loadSchedules)
                  .catch((e: unknown) => toastError(`Pause: ${e instanceof Error ? e.message : String(e)}`));
              }}
              onResume={(id) => {
                run(resumeSchedule(base, sessionId, id).pipe(Effect.provide(WickClientLayer)))
                  .then(loadSchedules)
                  .catch((e: unknown) => toastError(`Resume: ${e instanceof Error ? e.message : String(e)}`));
              }}
            />
          {:else if railTab === "browser"}
            <BrowserPanel onError={(m) => toastError(m)} />
          {/if}
        </div>
      </div>
    </div>
  {/if}

  <!-- Vertical rail strip — fixed on right edge -->
  <div
    class="fixed top-1/2 right-0 z-20 -translate-y-1/2 flex flex-col rounded-l-xl border border-r-0 border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-md overflow-hidden"
  >
    {#each railTabs as tab, i}
      <button
        type="button"
        title={tab.label}
        aria-label={tab.label}
        onclick={() => toggleRail(tab.id)}
        class={[
          "group inline-flex flex-col items-center justify-center gap-1 px-1.5 py-2.5 transition-colors",
          i > 0 ? "border-t border-white-300 dark:border-navy-600" : "",
          railTab === tab.id
            ? "bg-green-50 dark:bg-green-900/20"
            : "hover:bg-white-200 dark:hover:bg-navy-800",
        ].join(" ")}
      >
        {#if railBusy(tab.id)}
          <!-- Ring around the icon rather than a badge replacement: the
               count still matters (how many), the ring adds the part a
               number cannot carry (right now). -->
          <span class="relative inline-flex h-4 w-4 items-center justify-center">
            <span
              class="absolute inset-[-3px] rounded-full border-2 border-green-500 border-t-transparent animate-spin"
              aria-label="Working"
            ></span>
            <svg
              viewBox="0 0 16 16"
              class="h-4 w-4 text-green-500"
              fill="none"
              stroke="currentColor"
              stroke-width="1.5"
            >
              {@html tab.icon}
            </svg>
            {#if railCount(tab.id) > 0}
              <span
                class="absolute -top-1.5 -right-1.5 inline-flex h-3.5 min-w-3.5 items-center justify-center rounded-full bg-green-500 px-0.5 text-[9px] font-semibold text-white-100"
              >{railCount(tab.id) > 99 ? "99+" : railCount(tab.id)}</span>
            {/if}
          </span>
        {:else if tab.id === "source" && scmChangeCount > 0}
          <span class="relative">
            <svg
              viewBox="0 0 16 16"
              class="h-4 w-4 text-green-500"
              fill="none"
              stroke="currentColor"
              stroke-width="1.5"
            >
              {@html tab.icon}
            </svg>
            <span
              class="absolute -top-1 -right-1 inline-flex h-3.5 min-w-3.5 items-center justify-center rounded-full bg-green-500 px-0.5 text-[9px] font-semibold text-white-100"
            >{scmChangeCount > 99 ? "99+" : scmChangeCount}</span>
          </span>
        {:else if railCount(tab.id) > 0}
          <span class="relative">
            <svg
              viewBox="0 0 16 16"
              class="h-4 w-4 text-green-500"
              fill="none"
              stroke="currentColor"
              stroke-width="1.5"
            >
              {@html tab.icon}
            </svg>
            <span
              class="absolute -top-1 -right-1 inline-flex h-3.5 min-w-3.5 items-center justify-center rounded-full bg-green-500 px-0.5 text-[9px] font-semibold text-white-100"
            >{railCount(tab.id) > 99 ? "99+" : railCount(tab.id)}</span>
          </span>
        {:else}
          <svg
            viewBox="0 0 16 16"
            class="h-4 w-4 text-green-500"
            fill="none"
            stroke="currentColor"
            stroke-width="1.5"
          >
            {@html tab.icon}
          </svg>
        {/if}
        <span
          class={[
            "text-[9px] font-medium [writing-mode:vertical-rl] [transform:rotate(180deg)] tracking-wide",
            railTab === tab.id
              ? "text-green-600 dark:text-green-400"
              : "text-black-700 dark:text-black-600",
          ].join(" ")}
        >{tab.label}</span>
      </button>
    {/each}
  </div>
</div>

<!-- Full-screen overlays -->
<ApprovalsModal
  request={$currentApproval}
  onDecide={handleApprovalDecide}
  onClose={() => { approvalError = ""; hideApproval(); }}
  error={approvalError}
/>

<DetailModal content={$currentDetail} onClose={hideDetail} />

<!-- Inspector for the sub-agent selected in the rail. Rendered only once the
     row is known: the selection can arrive from the `?sub=` query parameter
     before the roster has loaded. -->
{#if selectedSubAgentRow}
  <SubAgentModal
    {base}
    {sessionId}
    row={selectedSubAgentRow}
    onClose={() => { selectedSubAgent = null; }}
    onChanged={loadSubAgents}
  />
{/if}

<ConfirmDialog
  open={confirmKill !== null}
  title={confirmKill?.queued ? "Cancel queued agent?" : "Stop this agent?"}
  body={confirmKill?.queued ? "The queued spawn will be dropped." : "The running agent process will be terminated."}
  confirmLabel={confirmKill?.queued ? "Cancel spawn" : "Stop agent"}
  destructive={true}
  onConfirm={doKill}
  onCancel={() => { confirmKill = null; }}
/>

{#if viewerFile !== null}
  <FileViewerModal
    file={viewerFile}
    dirty={viewerDirty}
    onSave={handleViewerSave}
    onClose={() => { viewerFile = null; viewerDirty = false; }}
    downloadHref={downloadURL(base, sessionId, viewerFile.path)}
  />
{/if}
