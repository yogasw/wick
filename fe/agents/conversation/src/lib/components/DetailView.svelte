<script lang="ts">
  import { onMount, onDestroy } from "svelte";
  import { get } from "svelte/store";
  import { Effect } from "effect";
  import { WickClientLayer } from "@wick-fe/common-api";
  import { toastError } from "@wick-fe/common-stores";

  import { createThreadStore } from "../stores/thread.js";
  import type { ThreadMeta, LifecycleState } from "../stores/thread.js";
  import { connectSession } from "../stores/sse.js";
  import type { SSEStatus } from "../types/agents.js";
  import { currentAsk, showAsk, hideAsk } from "../stores/asks.js";
  import { currentApproval, showApproval, hideApproval } from "../stores/approvals.js";
  import { notify } from "../notify.js";
  import { push } from "../router.js";

  import { getConversation, getSessionMeta, deleteSession } from "../api/sessions.js";
  import { answerAsk } from "../api/asks.js";
  import { sendApprovalDecision } from "../api/approvals.js";
  import { sendMessage } from "../api/messages.js";
  import { listFiles, readFile, saveFile, createFile, downloadURL } from "../api/files.js";
  import { getProcesses, killProcess, dequeueProcess } from "../api/processes.js";
  import {
    listWorkspace, addWorkspace, saveWorkspaceConfig, testWorkspace,
    duplicateWorkspace, renameWorkspace, removeWorkspace,
  } from "../api/workspace.js";

  import ConversationHeader from "./ConversationHeader.svelte";
  import ConversationThread from "./ConversationThread.svelte";
  import Composer from "./Composer.svelte";
  import ContextPanel from "./ContextPanel.svelte";
  import FileViewerModal from "./FileViewerModal.svelte";
  import ProcessPanel from "./ProcessPanel.svelte";
  import WorkspacePanel from "./WorkspacePanel.svelte";
  import ScmDock from "./ScmDock.svelte";
  import AskUserModal from "./AskUserModal.svelte";
  import ApprovalsModal from "./ApprovalsModal.svelte";

  import type {
    ConversationTurn, LiveTurn, TypingState,
    ContextFileEntry, AskAnswer, ApprovalDecision,
    WsInstance, WsBase, ProcessInfo, FileContent,
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

  /* ── workspace panel state ────────────────────────────────────── */
  let wsInstances = $state<WsInstance[]>([]);
  let wsBases = $state<WsBase[]>([]);
  let wsOpenCards = $state<Record<string, boolean>>({});

  /* ── SCM dock ─────────────────────────────────────────────────── */
  const scmAssetUrl =
    document.getElementById("app")?.dataset.scmAsset ??
    document.querySelector<HTMLElement>("[data-scm-asset]")?.dataset.scmAsset ??
    "";
  let scmOpen = $state(false);
  let scmChangeCount = $state(0);

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
      .then((res) => { processes = res; })
      .catch((e: unknown) => toastError(`Processes: ${e instanceof Error ? e.message : String(e)}`));
  }

  function loadWorkspace() {
    run(listWorkspace(base, sessionId).pipe(Effect.provide(WickClientLayer)))
      .then((res) => { wsInstances = res.instances; wsBases = res.bases; })
      .catch((e: unknown) => toastError(`Workspace: ${e instanceof Error ? e.message : String(e)}`));
  }

  /* ── auto-scroll thread to bottom ─────────────────────────────── */
  $effect(() => {
    const _dep1 = turns.length;
    const _dep2 = live;
    if (threadEl) {
      threadEl.scrollTop = threadEl.scrollHeight;
    }
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
        try { showAsk(JSON.parse(ev.data ?? "{}")); } catch (_) { /* skip */ }
      } else if (ev.type === "ask_user_resolved") {
        try { hideAsk(JSON.parse(ev.data ?? "{}")); } catch (_) { /* skip */ }
      } else if (ev.type === "approval_request") {
        try {
          const req = JSON.parse(ev.data ?? "{}");
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
    run(killProcess(base, sessionId).pipe(Effect.provide(WickClientLayer)))
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
      toastError(`Approval: ${e instanceof Error ? e.message : String(e)}`);
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
    try {
      await run(sendMessage(base, sessionId, msg).pipe(Effect.provide(WickClientLayer)));
    } catch (e: unknown) {
      toastError(`Send: ${e instanceof Error ? e.message : String(e)}`);
    }
  }

  /* ── rail toggle ──────────────────────────────────────────────── */
  function toggleRail(tab: RailTab) {
    if (tab === "source") {
      scmOpen = !scmOpen;
      railTab = scmOpen ? "source" : null;
      return;
    }
    railTab = railTab === tab ? null : tab;
    if (railTab !== "source") scmOpen = false;
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
      })
      .catch(() => { title = sessionId; });

    startSSE();
    loadFiles();
    loadProcesses();
    loadWorkspace();
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

  const sideOpen = $derived(railTab !== null && railTab !== "source");
</script>

<!-- Full-height flex row: main area + vertical rail -->
<div class="flex h-full min-w-0 overflow-hidden">

  <!-- Centre column: header + thread + ask + composer -->
  <div class="relative flex flex-col flex-1 min-w-0 overflow-hidden" data-session-id={sessionId}>

    <!-- Zone 1: header bar -->
    <ConversationHeader
      title={threadMeta.title || title}
      {agentLabel}
      {sseStatus}
      lifecycle={agentLifecycle}
      onKill={handleKill}
      onDelete={handleDelete}
    />

    <!-- Zone 2: scrollable thread -->
    <div
      class="flex-1 min-h-0 overflow-y-auto bg-white-200 dark:bg-navy-800"
      bind:this={threadEl}
      data-chat-panel
    >
      <div class="max-w-4xl mx-auto w-full px-4 md:px-6 pt-4 pb-4">
        <ConversationThread {turns} {live} {typing} />
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

    <!-- Zone 4: composer docked at bottom -->
    <div class="relative shrink-0 px-4 md:px-6 pb-6 pt-2 bg-white-200 dark:bg-navy-800">
      <div class="max-w-4xl mx-auto">
        <Composer
          onSend={handleSend}
          placeholder="Message the agent…"
          showShiftEnterHint={true}
        />
      </div>
    </div>
  </div>

  <!-- Side panel: slides in when a rail tab is active (non-source) -->
  {#if sideOpen}
    <div
      class="hidden lg:flex flex-col w-80 shrink-0 border-l border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 overflow-hidden"
    >
      {#if railTab === "context"}
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
        />
      {:else if railTab === "process"}
        <ProcessPanel
          {processes}
          onKill={(sid) => {
            run(killProcess(base, sid).pipe(Effect.provide(WickClientLayer)))
              .then(loadProcesses)
              .catch((e: unknown) => toastError(`Kill: ${e instanceof Error ? e.message : String(e)}`));
          }}
          onDequeue={(sid) => {
            run(dequeueProcess(base, sid).pipe(Effect.provide(WickClientLayer)))
              .then(loadProcesses)
              .catch((e: unknown) => toastError(`Dequeue: ${e instanceof Error ? e.message : String(e)}`));
          }}
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
          {#if railTab === "context"}
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
            />
          {:else if railTab === "process"}
            <ProcessPanel
              {processes}
              onKill={(sid) => {
                run(killProcess(base, sid).pipe(Effect.provide(WickClientLayer)))
                  .then(loadProcesses)
                  .catch((e: unknown) => toastError(`Kill: ${e instanceof Error ? e.message : String(e)}`));
              }}
              onDequeue={(sid) => {
                run(dequeueProcess(base, sid).pipe(Effect.provide(WickClientLayer)))
                  .then(loadProcesses)
                  .catch((e: unknown) => toastError(`Dequeue: ${e instanceof Error ? e.message : String(e)}`));
              }}
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

  <!-- SCM dock (Source tab) — driven by ScmDock component -->
  {#if scmOpen}
    <ScmDock
      {sessionId}
      assetUrl={scmAssetUrl}
      bind:open={scmOpen}
      changeCount={scmChangeCount}
      onOpenChange={(v) => {
        scmOpen = v;
        if (!v) railTab = null;
      }}
    />
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
          railTab === tab.id || (tab.id === "source" && scmOpen)
            ? "bg-green-50 dark:bg-green-900/20"
            : "hover:bg-white-200 dark:hover:bg-navy-800",
        ].join(" ")}
      >
        {#if tab.id === "source" && scmChangeCount > 0}
          <span class="relative">
            <svg
              viewBox="0 0 16 16"
              class={[
                "h-4 w-4",
                railTab === "source" || scmOpen ? "text-green-500" : "text-green-500",
              ].join(" ")}
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
            railTab === tab.id || (tab.id === "source" && scmOpen)
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
  onClose={() => hideApproval()}
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
