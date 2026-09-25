<script lang="ts">
  /* This project's Agent Memory, inside the project's own settings.

     It lives here rather than on the global panel because the question it
     answers is a project question: what did agents remember while working
     HERE, and can I fix it. The global panel at /tools/agents/agentmemory
     still exists and still answers the store-wide one — the daemon, every
     project's analytics, the settings — and neither pretends to be the
     other.

     Everything project-scoped is resolved SERVER-side: the wick project id
     goes to /agentmemory/project-scope and comes back as the memory bucket
     the agents actually write to. This file never builds a bucket name
     (PLAN §22.2). */
  import { onMount } from "svelte";
  import { toastError, toastOk } from "@wick-fe/common-stores";
  import { WickClientLayer } from "@wick-fe/common-api";
  import { Effect } from "effect";
  import {
    deletePage,
    errText,
    fetchBackends,
    fetchCheckpoints,
    fetchProjects,
    fetchProjectPolicy,
    fetchProjectScope,
    previewBackfill,
    ProjectMemory,
    runBackfill,
    saveProjectPolicy,
    readPage,
    restorePage,
    searchWiki,
    writePage,
    type BackendInfo,
    type BackfillReport,
    type BackfillRequestEcho,
    type Checkpoint,
    type Page,
    type ProjectPolicy,
    type ProjectScope,
    type ProjectsResponse,
    type SearchResponse,
  } from "@wick-fe/common-agentmemory";

  type Props = {
    projectID: string;
    base: string;
    /* canManage comes from the shell, not from a fetch: the managing
       controls must never paint for someone who would only get a 403 from
       them (PLAN §23.3). */
    canManage: boolean;
  };
  let { projectID, base, canManage }: Props = $props();

  const run = <T,>(eff: Effect.Effect<T, unknown, never>): Promise<T> =>
    Effect.runPromise(eff.pipe(Effect.provide(WickClientLayer)) as Effect.Effect<T, unknown, never>);

  // Which backend this project's memory lives in. The panel supports more
  // than one; a project's tab reads the first registered one, and names it
  // so the answer is never "some store, somewhere".
  let backend = $state<BackendInfo | null>(null);
  let scope = $state<ProjectScope | null>(null);
  let scopeError = $state("");
  let projects = $state<ProjectsResponse | null>(null);
  let loading = $state(true);
  let busy = $state(false);

  let query = $state("");
  let search = $state<SearchResponse | null>(null);
  let searching = $state(false);

  let openPath = $state("");
  let page = $state<Page | null>(null);
  let pageLoading = $state(false);
  let pageError = $state("");
  let draft = $state("");
  let draftTitle = $state("");
  let draftKind = $state("");
  let note = $state("");
  let saveMsg = $state("");
  let saveFailed = $state(false);
  let checkpoints = $state<Checkpoint[] | null>(null);
  let checkpointsLoading = $state(false);

  // The last import's outcome, kept beside the request the server echoed
  // back: the cap warning is only honest when it compares what was asked for
  // with what was taken.
  let backfill = $state<BackfillReport | null>(null);
  let backfillReq = $state<BackfillRequestEcho | null>(null);
  let backfillError = $state("");

  // Whether this project uses Agent Memory at all. Read alongside the scope
  // because it decides whether anything below it means anything.
  let policy = $state<ProjectPolicy | null>(null);
  let policyBusy = $state(false);

  const backendID = $derived(backend?.id ?? "");
  const scoped = $derived(scope ? { workspace: scope.workspace, project: scope.project } : {});
  const briefing = $derived(
    scope
      ? ((projects?.projects ?? []).find((r) => r.workspace === scope!.workspace && r.project === scope!.project)
          ?.briefing ?? null)
      : null,
  );

  // DEFAULT_COMMITTED_NOTE stands in until a save brings the server's own
  // sentence back. Saying it BEFORE the first edit is the point: the
  // nervousness about changing what an agent will recall is what it is for.
  const DEFAULT_COMMITTED_NOTE =
    "Every write is committed to the wiki's git history, so an edit here can be restored from a checkpoint.";

  onMount(() => {
    void load();
  });

  async function load(): Promise<void> {
    loading = true;
    try {
      const list = await run(fetchBackends(base));
      backend = (list.backends ?? [])[0] ?? null;
      if (!backend) {
        scopeError = "No Agent Memory backend is registered on this host.";
        return;
      }
      try {
        scope = await run(fetchProjectScope(base, projectID));
        scopeError = "";
        try {
          policy = await run(fetchProjectPolicy(base, projectID));
        } catch {
          // A host with no per-project switch wired still shows the
          // memory itself; the card simply does not appear.
          policy = null;
        }
      } catch (e) {
        scope = null;
        scopeError = errText(e);
        return;
      }
      await loadProjects();
    } catch (e) {
      scopeError = errText(e);
    } finally {
      loading = false;
    }
  }

  async function loadProjects(): Promise<void> {
    if (!backendID) return;
    projects = await run(fetchProjects(base, backendID));
  }

  async function open(path: string): Promise<void> {
    if (!backendID || !path) return;
    openPath = path;
    page = null;
    pageError = "";
    saveMsg = "";
    saveFailed = false;
    pageLoading = true;
    try {
      const res = await run(readPage(base, backendID, path, scoped));
      if (res.error) pageError = res.hint ? `${res.error} — ${res.hint}` : res.error;
      else {
        page = res.page ?? null;
        draft = page?.body ?? "";
        draftTitle = page?.title ?? "";
        draftKind = "";
      }
    } catch (e) {
      pageError = errText(e);
    } finally {
      pageLoading = false;
    }
  }

  // newPage opens the editor on a page that does not exist yet. Nothing is
  // written until Save: a path typed by mistake must not create a page an
  // agent then recalls as fact.
  function newPage(path: string): void {
    openPath = path;
    page = { path, body: "" };
    draft = "";
    draftTitle = "";
    draftKind = "";
    pageError = "";
    saveMsg = "";
    saveFailed = false;
  }

  function close(): void {
    openPath = "";
    page = null;
    draft = "";
    pageError = "";
    saveMsg = "";
  }

  async function save(): Promise<void> {
    if (!backendID || !openPath) return;
    busy = true;
    saveMsg = "";
    saveFailed = false;
    try {
      const res = await run(writePage(base, backendID, scoped, { path: openPath, body: draft, title: draftTitle, kind: draftKind }));
      if (res.error) {
        saveFailed = true;
        saveMsg = res.hint ? `${res.error} — ${res.hint}` : res.error;
      } else {
        page = { ...(page ?? { path: openPath, body: "" }), body: draft, title: draftTitle || page?.title };
        note = res.note ?? note;
        saveMsg = res.result?.output ?? "Saved.";
        toastOk("Saved", `${openPath} was written.`);
        await loadProjects();
      }
    } catch (e) {
      saveFailed = true;
      saveMsg = errText(e);
      toastError("Could not save the page", errText(e));
    } finally {
      busy = false;
    }
  }

  async function remove(): Promise<void> {
    if (!backendID || !openPath) return;
    const path = openPath;
    busy = true;
    try {
      const res = await run(deletePage(base, backendID, scoped, path));
      if (res.error) {
        saveFailed = true;
        saveMsg = res.hint ? `${res.error} — ${res.hint}` : res.error;
      } else {
        toastOk("Deleted", `${res.result?.path ?? path} was removed. ${res.warning ?? ""}`.trim());
        close();
        await loadProjects();
      }
    } catch (e) {
      saveFailed = true;
      saveMsg = errText(e);
      toastError("Could not delete the page", errText(e));
    } finally {
      busy = false;
    }
  }

  async function loadCheckpoints(): Promise<void> {
    if (!backendID) return;
    checkpointsLoading = true;
    try {
      const res = await run(fetchCheckpoints(base, backendID));
      checkpoints = res.checkpoints ?? [];
    } catch (e) {
      checkpoints = [];
      toastError("Could not read the wiki's history", errText(e));
    } finally {
      checkpointsLoading = false;
    }
  }

  async function restore(oid: string): Promise<void> {
    if (!backendID || !openPath || !oid) return;
    const path = openPath;
    busy = true;
    try {
      const res = await run(restorePage(base, backendID, scoped, path, oid));
      if (res.error) {
        saveFailed = true;
        saveMsg = res.hint ? `${res.error} — ${res.hint}` : res.error;
      } else {
        saveFailed = false;
        saveMsg = res.note ?? "Restored.";
        toastOk("Restored", `${path} was restored from ${res.result?.restored_from?.slice(0, 12) ?? oid.slice(0, 12)}.`);
        // Re-read rather than assume: the restored body is the store's
        // answer, not one this page can reconstruct.
        await open(path);
        await loadCheckpoints();
        await loadProjects();
      }
    } catch (e) {
      saveFailed = true;
      saveMsg = errText(e);
      toastError("Could not restore the page", errText(e));
    } finally {
      busy = false;
    }
  }

  async function doSearch(): Promise<void> {
    const q = query.trim();
    if (!backendID || !q) return;
    searching = true;
    try {
      search = await run(searchWiki(base, backendID, q, scoped));
    } catch (e) {
      toastError("Search failed", errText(e));
    } finally {
      searching = false;
    }
  }

  // doBackfill runs one import against THIS project's bucket. The scope is
  // the server's own resolution, never a name built here (PLAN §22.2), so a
  // preview and a real run can never act on different buckets.
  async function doBackfill(dry: boolean): Promise<void> {
    if (!backendID || !scope) return;
    busy = true;
    backfillError = "";
    backfill = null;
    backfillReq = null;
    try {
      const res = await run(
        dry ? previewBackfill(base, backendID, scoped) : runBackfill(base, backendID, scoped),
      );
      if (res.error) {
        backfillError = res.hint ? `${res.error} — ${res.hint}` : res.error;
        return;
      }
      backfill = res.report ?? null;
      backfillReq = res.request ?? null;
      if (!dry) {
        toastOk("Imported", "This project's earlier sessions were imported.");
        // The cards are what the import was for, so they are re-read rather
        // than left showing the empty state that prompted it.
        await loadProjects();
      }
    } catch (e) {
      backfillError = errText(e);
      if (!dry) toastError("Could not import this project's history", errText(e));
    } finally {
      busy = false;
    }
  }

  async function setPolicy(value: ProjectPolicy["value"]): Promise<void> {
    policyBusy = true;
    try {
      // The answer is the RESOLVED policy: turning this project on changes
      // what every other project does, and the card has to say so at once.
      policy = await run(saveProjectPolicy(base, projectID, value));
      toastOk("Saved", policy.allowed ? "Agent Memory is on for this project." : "Agent Memory is off for this project.");
    } catch (e) {
      toastError("Could not change this project's setting", errText(e));
    } finally {
      policyBusy = false;
    }
  }

  function goGlobal(): void {
    window.location.href = `${base}/agentmemory`;
  }
</script>

<ProjectMemory
  {scope}
  {scopeError}
  {projects}
  {briefing}
  {loading}
  {busy}
  {canManage}
  backendName={backend?.name ?? ""}
  {policy}
  {policyBusy}
  onPolicy={(v) => void setPolicy(v)}
  {backfill}
  {backfillReq}
  {backfillError}
  onPreviewBackfill={() => void doBackfill(true)}
  onRunBackfill={() => void doBackfill(false)}
  {openPath}
  {page}
  {pageLoading}
  {pageError}
  {draft}
  {draftTitle}
  {draftKind}
  committedNote={note || DEFAULT_COMMITTED_NOTE}
  {saveMsg}
  {saveFailed}
  {checkpoints}
  {checkpointsLoading}
  {search}
  {searching}
  {query}
  onQuery={(q) => (query = q)}
  onSearch={() => void doSearch()}
  onOpen={(p) => void open(p)}
  onClose={close}
  onDraft={(v) => (draft = v)}
  onTitle={(v) => (draftTitle = v)}
  onKind={(v) => (draftKind = v)}
  onSave={() => void save()}
  onDelete={() => void remove()}
  onNewPage={newPage}
  onLoadCheckpoints={() => void loadCheckpoints()}
  onRestore={(oid) => void restore(oid)}
  onRefresh={() => void load()}
  onGoGlobal={goGlobal}
/>
