<script lang="ts">
  import { onMount, onDestroy } from "svelte";
  import { get } from "svelte/store";
  import { Effect } from "effect";
  import { WickClientLayer } from "@wick-fe/common-api";
  import { toastError, toastOk } from "@wick-fe/common-stores";
  import { ConfirmDialog } from "@wick-fe/common-ui";

  import { createThreadStore } from "../stores/thread.js";
  import type { ThreadMeta, LifecycleState } from "../stores/thread.js";
  import { connectSession } from "../stores/sse.js";
  import type { SSEStatus } from "../types/agents.js";
  import { currentAsk, showAsk, hideAsk } from "../stores/asks.js";
  import { currentApproval, showApproval, hideApproval } from "../stores/approvals.js";
  import { notify } from "../notify.js";
  import { push } from "../router.js";
  import { readScmWidth, writeScmWidth, clampScmWidth } from "../scmWidth.js";

  import { getConversation, getSessionMeta, deleteSession, getTurnTrace } from "../api/sessions.js";
  import { getProviderOptions, getProjectOptions, switchProvider, moveProject } from "../api/options.js";
  import { answerAsk } from "../api/asks.js";
  import { getApprovals, sendApprovalDecision, revokeApproval } from "../api/approvals.js";
  import { sendMessage } from "../api/messages.js";
  import { listFiles, readFile, saveFile, createFile, deleteFile, downloadURL } from "../api/files.js";
  import { getProcesses, killProcess, dequeueProcess } from "../api/processes.js";
  import {
    listWorkspace, addWorkspace, saveWorkspaceConfig, testWorkspace,
    duplicateWorkspace, renameWorkspace, removeWorkspace,
  } from "../api/workspace.js";

  import ConversationHeader from "./ConversationHeader.svelte";
  import ConversationThread from "./ConversationThread.svelte";
  import JsonTree from "./JsonTree.svelte";
  import Composer from "./Composer.svelte";
  import ComposerToolbar from "./ComposerToolbar.svelte";
  import ContextPanel from "./ContextPanel.svelte";
  import FileViewerModal from "./FileViewerModal.svelte";
  import ProcessPanel from "./ProcessPanel.svelte";
  import WorkspacePanel from "./WorkspacePanel.svelte";
  import AskUserModal from "./AskUserModal.svelte";
  import ApprovalsModal from "./ApprovalsModal.svelte";
  import ApprovedPanel from "./ApprovedPanel.svelte";
  import type { ActiveView } from "./ConversationHeader.svelte";

  import type {
    ConversationTurn, LiveTurn, TypingState,
    ContextFileEntry, AskAnswer, ApprovalDecision,
    ApprovedItem,
    WsInstance, WsBase, ProcessInfo, FileContent,
    ProviderOption, ProjectOption,
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

  /* ── SSE ───────────────────────────────────────────────────────── */
  let closeSSE: (() => void) | null = null;
  let sseStatus = $state<SSEStatus>("connecting");

  /* ── vertical rail tabs ────────────────────────────────────────── */
  type RailTab = "context" | "process" | "workspace" | "source";
  let railTab = $state<RailTab | null>(null);

  /* ── thread scroll ref ─────────────────────────────────────────── */
  let threadEl: HTMLElement | undefined = $state();

  /* ── context panel state ──────────────────────────────────────── */
  let cwdVal = $state("");
  let filesVal = $state<ContextFileEntry[]>([]);
  let fileSearch = $state("");
  let openDirs = $state<Record<string, boolean>>({});
  let viewerFile = $state<FileContent | null>(null);
  let viewerDirty = $state(false);

  /* ── process panel state ──────────────────────────────────────── */
  let processes = $state<ProcessInfo[]>([]);
  let confirmKill = $state<{ sid: string; queued: boolean } | null>(null);

  /* ── workspace panel state ────────────────────────────────────── */
  let wsInstances = $state<WsInstance[]>([]);
  let wsBases = $state<WsBase[]>([]);
  let wsOpenCards = $state<Record<string, boolean>>({});

  /* ── provider / project options ──────────────────────────────── */
  let providerOptions = $state<ProviderOption[]>([]);
  let projectOptions = $state<ProjectOption[]>([]);
  let activeProvider = $state<string | null>(null);
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

  function loadApprovalsTab() {
    run(getApprovals(base, sessionId).pipe(Effect.provide(WickClientLayer)))
      .then((res) => {
        approvalsTabPending = res.pending;
        approvalsTabSession = res.session_approved;
        approvalsTabAlways = res.always_approved;
      })
      .catch((e: unknown) => toastError(`Approvals: ${e instanceof Error ? e.message : String(e)}`));
  }

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
    run(listFiles(base, sessionId).pipe(Effect.provide(WickClientLayer)))
      .then((res) => { cwdVal = res.cwd; filesVal = res.files; })
      .catch((e: unknown) => toastError(`Files: ${e instanceof Error ? e.message : String(e)}`));
  }

  function loadProcesses() {
    run(getProcesses(base, sessionId).pipe(Effect.provide(WickClientLayer)))
      .then((res) => {
        processes = res;
        // Derive active provider from processes when meta.active_agent is unset
        if (!activeProvider && res && res.length > 0) {
          activeProvider = res[0].provider || null;
          agentLabel = res[0].provider || "";
        }
      })
      .catch((e: unknown) => toastError(`Processes: ${e instanceof Error ? e.message : String(e)}`));
  }

  function loadWorkspace() {
    run(listWorkspace(base, sessionId).pipe(Effect.provide(WickClientLayer)))
      .then((res) => { wsInstances = res.instances; wsBases = res.bases; })
      .catch((e: unknown) => toastError(`Workspace: ${e instanceof Error ? e.message : String(e)}`));
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

  async function handleProviderChange(provider: string) {
    try {
      const res = await run(
        switchProvider(base, sessionId, provider).pipe(Effect.provide(WickClientLayer)),
      );
      if (res.redirect) {
        if (res.redirect.startsWith("/sessions/")) {
          push(res.redirect);
        } else {
          window.location.href = res.redirect;
        }
      } else {
        activeProvider = provider;
      }
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

  function scrollToBottom() {
    if (threadEl) {
      suppressScrollCheck = true;
      userScrolledUp = false;
      showJumpBtn = false;
      threadEl.scrollTop = threadEl.scrollHeight;
      requestAnimationFrame(() => { suppressScrollCheck = false; });
    }
  }

  $effect(() => {
    function onKeydown(e: KeyboardEvent) {
      if (e.ctrlKey && e.key === "ArrowDown") {
        e.preventDefault();
        scrollToBottom();
      }
    }
    window.addEventListener("keydown", onKeydown);
    return () => window.removeEventListener("keydown", onKeydown);
  });

  $effect(() => {
    if (!threadEl) return;
    const el = threadEl;

    function onScroll() {
      if (suppressScrollCheck) return;
      const distFromBottom = el.scrollHeight - el.scrollTop - el.clientHeight;
      userScrolledUp = distFromBottom > 80;
      showJumpBtn = userScrolledUp;
    }

    el.addEventListener("scroll", onScroll, { passive: true });
    return () => el.removeEventListener("scroll", onScroll);
  });

  $effect(() => {
    const _dep1 = turns.length;
    const _dep2 = live?.text?.length;
    const _dep3 = live?.blocks?.length;
    if (threadEl && !userScrolledUp) {
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
  function openFile(f: ContextFileEntry) {
    if (f.isDir) return;
    run(readFile(base, sessionId, f.path).pipe(Effect.provide(WickClientLayer)))
      .then((res) => {
        viewerFile = res;
        viewerDirty = false;
      })
      .catch((e: unknown) => toastError(`Read: ${e instanceof Error ? e.message : String(e)}`));
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

  /* ── SSE fan-out ──────────────────────────────────────────────── */
  function startSSE() {
    const stream = connectSession(base, sessionId);
    closeSSE = () => stream.close();

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
          notify("Approval needed", req.cmd ?? "");
        } catch (_) { /* skip */ }
      } else if (ev.type === "approval_resolved") {
        try { hideApproval(JSON.parse(ev.data ?? "{}")); } catch (_) { /* skip */ }
      } else if (ev.type === "lifecycle") {
        loadProcesses();
      } else if (ev.type === "git_status") {
        try {
          const d = JSON.parse(ev.data ?? "{}") as { changed?: number };
          if (typeof d.changed === "number") scmChangeCount = d.changed;
        } catch (_) { /* skip */ }
      }
    });
  }

  /* ── header actions ───────────────────────────────────────────── */
  function handleKill() {
    confirmKill = { sid: sessionId, queued: false };
  }

  function doKill() {
    const target = confirmKill;
    confirmKill = null;
    if (!target) return;
    const action = target.queued
      ? dequeueProcess(base, target.sid)
      : killProcess(base, target.sid);
    run(action.pipe(Effect.provide(WickClientLayer)))
      .then(loadProcesses)
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

  async function handleApprovalDecide(decision: ApprovalDecision) {
    const approval = get(currentApproval);
    if (!approval) return;
    approvalError = "";
    hideApproval();
    try {
      await run(
        sendApprovalDecision(base, sessionId, {
          id: approval.id,
          decision,
          match_key: approval.match_key,
        }).pipe(Effect.provide(WickClientLayer))
      );
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : String(e);
      approvalError = msg;
      showApproval(approval);
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

  /* ── mount/unmount ────────────────────────────────────────────── */
  onMount(() => {
    run(getConversation(base, sessionId).pipe(Effect.provide(WickClientLayer)))
      .then((res) => thread.setHistory(res.turns))
      .catch((e: unknown) => toastError(`History: ${e instanceof Error ? e.message : String(e)}`));

    run(getSessionMeta(base, sessionId).pipe(Effect.provide(WickClientLayer)))
      .then((res) => {
        title = res.label || res.id;
        agentLabel = res.active_agent || "";
        activeProvider = res.active_agent || null;
        activeProjectId = res.project_id || null;
      })
      .catch(() => { title = sessionId; });

    startSSE();
    loadFiles();
    loadProcesses();
    loadWorkspace();
    loadProviderOptions();
    loadProjectOptions();
  });

  onDestroy(() => {
    closeSSE?.();
    unsubTurns();
    unsubLive();
    unsubTyping();
    unsubLifecycle();
    unsubMeta();
  });

  const railTabs: { id: RailTab; label: string; icon: string }[] = [
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
      id: "source",
      label: "Source",
      icon: '<circle cx="4" cy="4" r="1.5"></circle><circle cx="4" cy="12" r="1.5"></circle><circle cx="12" cy="5" r="1.5"></circle><path d="M4 5.5v5M5.5 4H9a2 2 0 012 2v0" stroke-linecap="round"></path>',
    },
  ];

  const sideOpen = $derived(railTab !== null);

  const contextCount = $derived(filesVal.filter((f) => !f.isDir).length);
  const processCount = $derived(processes.length);
  const workspaceCount = $derived(wsInstances.length);
  function railCount(id: RailTab): number {
    if (id === "context") return contextCount;
    if (id === "process") return processCount;
    if (id === "workspace") return workspaceCount;
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
          <ConversationThread {turns} {live} {typing} loadTrace={(turnId) => Effect.runPromise(getTurnTrace(base, sessionId, turnId).pipe(Effect.provide(WickClientLayer)))} />
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
        <div class="max-w-4xl mx-auto pb-6">
          {#snippet toolbarLeading()}
            <ComposerToolbar
              providers={providerOptions}
              projects={projectOptions}
              activeProvider={activeProvider}
              activeProjectId={activeProjectId}
              onProviderChange={handleProviderChange}
              onProjectChange={handleProjectChange}
            />
          {/snippet}
          <Composer
            onSend={handleSend}
            placeholder="Message the agent…"
            showShiftEnterHint={true}
            leadingActions={toolbarLeading}
          />
        </div>
      </div>
    {:else if activeView === "approvals"}
      <div class="flex-1 min-h-0 overflow-y-auto bg-white-200 dark:bg-navy-800">
        <div class="max-w-4xl mx-auto w-full px-4 md:px-6 py-6 flex flex-col gap-4">
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
                    <div class="grid grid-cols-4 gap-2">
                      <button
                        type="button"
                        class="rounded-lg bg-green-500 px-3 py-2 text-xs font-medium text-white-100 hover:bg-green-600 transition-colors"
                        onclick={() => handleApprovalDecide("approve_once")}
                      >Approve once</button>
                      <button
                        type="button"
                        class="rounded-lg border border-green-500 dark:border-green-600 px-3 py-2 text-xs font-medium text-green-700 dark:text-green-400 hover:bg-green-50 dark:hover:bg-green-900/20 transition-colors"
                        onclick={() => handleApprovalDecide("approve_session")}
                      >Allow session</button>
                      <button
                        type="button"
                        class="rounded-lg border border-green-500 dark:border-green-600 px-3 py-2 text-xs font-medium text-green-700 dark:text-green-400 hover:bg-green-50 dark:hover:bg-green-900/20 transition-colors"
                        onclick={() => handleApprovalDecide("approve_always")}
                      >Always allow</button>
                      <button
                        type="button"
                        class="rounded-lg bg-red-600 px-3 py-2 text-xs font-medium text-white-100 hover:bg-red-700 transition-colors"
                        onclick={() => handleApprovalDecide("block")}
                      >Block</button>
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
      <div data-placeholder-view class="flex-1 min-h-0 flex items-center justify-center bg-white-200 dark:bg-navy-800">
        <p class="text-sm text-black-600 dark:text-black-700">Commands view — coming soon to the new UI</p>
      </div>
    {:else if activeView === "raw"}
      <div class="flex-1 min-h-0 flex flex-col bg-white-200 dark:bg-navy-800">
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
      class={`relative hidden lg:flex flex-col ${railTab === "source" ? "" : "w-80"} shrink-0 border-l border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 overflow-hidden`}
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
      {:else if railTab === "context"}
        <ContextPanel
          cwd={cwdVal}
          files={filesVal}
          search={fileSearch}
          {openDirs}
          onSearch={(s) => { fileSearch = s; }}
          onToggleDir={(p) => { openDirs = { ...openDirs, [p]: !openDirs[p] }; }}
          onOpen={openFile}
          onRefresh={loadFiles}
          onNewFile={() => {
            const name = prompt("File name:");
            if (name) {
              run(createFile(base, sessionId, name, false).pipe(Effect.provide(WickClientLayer)))
                .then(loadFiles)
                .catch((e: unknown) => toastError(`Create: ${e instanceof Error ? e.message : String(e)}`));
            }
          }}
          onNewDir={() => {
            const name = prompt("Directory name:");
            if (name) {
              run(createFile(base, sessionId, name, true).pipe(Effect.provide(WickClientLayer)))
                .then(loadFiles)
                .catch((e: unknown) => toastError(`Create: ${e instanceof Error ? e.message : String(e)}`));
            }
          }}
          onDownload={(p) => { window.open(downloadURL(base, sessionId, p), "_blank"); }}
          onDelete={(p) => {
            run(deleteFile(base, sessionId, p).pipe(Effect.provide(WickClientLayer)))
              .then(loadFiles)
              .catch((e: unknown) => toastError(`Delete: ${e instanceof Error ? e.message : String(e)}`));
          }}
          onNewHere={(dir) => {
            const name = prompt("File name:");
            if (name) {
              run(createFile(base, sessionId, `${dir}/${name}`, false).pipe(Effect.provide(WickClientLayer)))
                .then(loadFiles)
                .catch((e: unknown) => toastError(`Create: ${e instanceof Error ? e.message : String(e)}`));
            }
          }}
        />
      {:else if railTab === "process"}
        <ProcessPanel
          {processes}
          onKill={(sid) => { confirmKill = { sid, queued: false }; }}
          onDequeue={(sid) => { confirmKill = { sid, queued: true }; }}
        />
      {:else if railTab === "workspace"}
        <WorkspacePanel
          instances={wsInstances}
          bases={wsBases}
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
          {:else if railTab === "context"}
            <ContextPanel
              cwd={cwdVal}
              files={filesVal}
              search={fileSearch}
              {openDirs}
              onSearch={(s) => { fileSearch = s; }}
              onToggleDir={(p) => { openDirs = { ...openDirs, [p]: !openDirs[p] }; }}
              onOpen={openFile}
              onRefresh={loadFiles}
              onNewFile={() => {
                const name = prompt("File name:");
                if (name) {
                  run(createFile(base, sessionId, name, false).pipe(Effect.provide(WickClientLayer)))
                    .then(loadFiles)
                    .catch((e: unknown) => toastError(`Create: ${e instanceof Error ? e.message : String(e)}`));
                }
              }}
              onNewDir={() => {
                const name = prompt("Directory name:");
                if (name) {
                  run(createFile(base, sessionId, name, true).pipe(Effect.provide(WickClientLayer)))
                    .then(loadFiles)
                    .catch((e: unknown) => toastError(`Create: ${e instanceof Error ? e.message : String(e)}`));
                }
              }}
              onDownload={(p) => { window.open(downloadURL(base, sessionId, p), "_blank"); }}
              onDelete={(p) => {
                run(deleteFile(base, sessionId, p).pipe(Effect.provide(WickClientLayer)))
                  .then(loadFiles)
                  .catch((e: unknown) => toastError(`Delete: ${e instanceof Error ? e.message : String(e)}`));
              }}
              onNewHere={(dir) => {
                const name = prompt("File name:");
                if (name) {
                  run(createFile(base, sessionId, `${dir}/${name}`, false).pipe(Effect.provide(WickClientLayer)))
                    .then(loadFiles)
                    .catch((e: unknown) => toastError(`Create: ${e instanceof Error ? e.message : String(e)}`));
                }
              }}
            />
          {:else if railTab === "process"}
            <ProcessPanel
              {processes}
              onKill={(sid) => { confirmKill = { sid, queued: false }; }}
              onDequeue={(sid) => { confirmKill = { sid, queued: true }; }}
            />
          {:else if railTab === "workspace"}
            <WorkspacePanel
              instances={wsInstances}
              bases={wsBases}
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
        {#if tab.id === "source" && scmChangeCount > 0}
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
