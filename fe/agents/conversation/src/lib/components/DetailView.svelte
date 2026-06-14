<script lang="ts">
  import { onMount, onDestroy } from "svelte";
  import { get } from "svelte/store";
  import { Effect } from "effect";
  import { WickClientLayer } from "@wick-fe/common-api";
  import { toastError } from "@wick-fe/common-stores";

  import { createThreadStore } from "../stores/thread.js";
  import type { ThreadMeta } from "../stores/thread.js";
  import { connectSession } from "../stores/sse.js";
  import { currentAsk, showAsk, hideAsk } from "../stores/asks.js";
  import { currentApproval, showApproval, hideApproval } from "../stores/approvals.js";
  import { notify } from "../notify.js";

  import { getConversation, getSessionMeta } from "../api/sessions.js";
  import { answerAsk } from "../api/asks.js";
  import { sendApprovalDecision } from "../api/approvals.js";
  import { sendMessage } from "../api/messages.js";
  import { listFiles, readFile, saveFile, createFile, downloadURL } from "../api/files.js";
  import { getProcesses, killProcess, dequeueProcess } from "../api/processes.js";
  import {
    listWorkspace, addWorkspace, saveWorkspaceConfig, testWorkspace,
    duplicateWorkspace, renameWorkspace, removeWorkspace,
  } from "../api/workspace.js";

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
  let threadMeta = $state<ThreadMeta>({});

  const unsubTurns = thread.turns.subscribe((v) => { turns = v; });
  const unsubLive = thread.live.subscribe((v) => { live = v; });
  const unsubTyping = thread.typing.subscribe((v) => { typing = v; });
  const unsubMeta = thread.meta.subscribe((v) => { threadMeta = v; });

  /* ── session title ─────────────────────────────────────────────── */
  let title = $state("");

  /* ── SSE close handle ─────────────────────────────────────────── */
  let closeSSE: (() => void) | null = null;

  /* ── side panel tabs ──────────────────────────────────────────── */
  type Tab = "context" | "process" | "config";
  let activeTab = $state<Tab>("context");

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
    try {
      await run(sendMessage(base, sessionId, msg).pipe(Effect.provide(WickClientLayer)));
    } catch (e: unknown) {
      toastError(`Send: ${e instanceof Error ? e.message : String(e)}`);
    }
  }

  /* ── mount/unmount ────────────────────────────────────────────── */
  onMount(() => {
    run(getConversation(base, sessionId).pipe(Effect.provide(WickClientLayer)))
      .then((res) => thread.setHistory(res.turns))
      .catch((e: unknown) => toastError(`History: ${e instanceof Error ? e.message : String(e)}`));

    run(getSessionMeta(base, sessionId).pipe(Effect.provide(WickClientLayer)))
      .then((res) => { title = res.label || res.id; })
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
    unsubMeta();
  });
</script>

<div class="flex h-full overflow-hidden">
  <!-- Left column: thread + composer -->
  <div class="flex flex-col flex-1 min-w-0 overflow-hidden border-r border-white-300 dark:border-navy-600">
    <!-- Title bar -->
    <div class="shrink-0 flex items-center gap-2 px-4 py-2 border-b border-white-300 dark:border-navy-600">
      <a
        href="#/"
        class="text-xs text-black-600 dark:text-black-700 hover:text-green-500 dark:hover:text-green-400 transition-colors"
      >← Sessions</a>
      {#if title || threadMeta.title}
        <span class="text-xs text-black-400 dark:text-black-600">·</span>
        <span class="text-sm font-medium text-black-900 dark:text-white-100 truncate">
          {threadMeta.title || title}
        </span>
      {/if}
      <div class="ml-auto flex items-center gap-2">
        <ScmDock
          {sessionId}
          assetUrl={scmAssetUrl}
          bind:open={scmOpen}
          changeCount={scmChangeCount}
          onOpenChange={(v) => { scmOpen = v; }}
        />
      </div>
    </div>

    <!-- Scrollable thread area -->
    <div class="flex-1 overflow-y-auto">
      <ConversationThread {turns} {live} {typing} />
    </div>

    <!-- Ask inline (appears above composer when active) -->
    <div class="shrink-0 px-4">
      <AskUserModal
        request={$currentAsk}
        onSubmit={handleAskSubmit}
        onDismiss={hideAsk}
      />
    </div>

    <!-- Composer -->
    <div class="shrink-0 px-4 pb-4">
      <Composer onSend={handleSend} />
    </div>
  </div>

  <!-- Right column: tabbed side panel -->
  <div class="flex flex-col w-80 shrink-0 overflow-hidden">
    <!-- Tab bar -->
    <div class="flex shrink-0 border-b border-white-300 dark:border-navy-600">
      {#each [["context", "Context"], ["process", "Process"], ["config", "Config"]] as [tabKey, tabLabel]}
        <button
          type="button"
          class={[
            "flex-1 px-2 py-2 text-xs font-medium transition-colors",
            activeTab === tabKey
              ? "border-b-2 border-green-500 text-green-600 dark:text-green-400"
              : "text-black-600 dark:text-black-700 hover:text-black-900 dark:hover:text-white-100",
          ].join(" ")}
          onclick={() => { activeTab = tabKey as Tab; }}
        >{tabLabel}</button>
      {/each}
    </div>

    <!-- Panel body -->
    <div class="flex-1 overflow-hidden flex flex-col">
      {#if activeTab === "context"}
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
      {:else if activeTab === "process"}
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
      {:else}
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
