<script lang="ts">
  /* Agent Memory panel shell: backend switcher + tab strip.

     Overview, Projects, Analytics and Health are built. Wiki, Handoffs and
     Settings are registered here so the shape of the feature is visible, but
     their content belongs to later slices — a placeholder that says what will
     land is honest; a half-filled tab is not.

     Who fetches what matters here. Overview's two reads are cheap and
     LLM-free, so they poll; the project list is one HTTP call and is fetched
     when its tab opens; the diagnostics walk the local harness stores and the
     whole database, so they only run when asked (PLAN §13.5 point 7). */
  import { ToastHost, ConfirmDialog } from "@wick-fe/common-ui";
  import { toastOk, toastError, toastWarn } from "@wick-fe/common-stores";
  import { WickClientLayer } from "@wick-fe/common-api";
  import { Effect } from "effect";
  import {
    cancelHandoff,
    compactStore,
    fetchBackends,
    fetchHandoffs,
    fetchHealth,
    fetchMessages,
    fetchOverview,
    fetchProjects,
    fetchProjectPolicies,
    fetchProjectScope,
    fetchCheckpoints,
    fetchSettings,
    install,
    deletePage,
    previewBackfill,
    readPage,
    restorePage,
    writePage,
    restart,
    runBackfill,
    runSweep,
    saveSettings,
    searchWiki,
    start,
    stop,
    testConnection,
  } from "$lib/api.js";
  import type {
    AutostartLock,
    BackendInfo,
    Checkpoint,
    BackfillReport,
    Handoff,
    HandoffsResponse,
    HealthReport,
    MessagesResponse,
    Overview,
    Page,
    ProjectRow,
    BackfillRequestEcho,
    ProjectPolicyRoster,
    ProjectScope,
    ProjectsResponse,
    Scope,
    SearchHit,
    SearchResponse,
    Settings,
    SweepReport,
    Tab,
    TestResult,
  } from "$lib/types.js";
  import { dotFor, errText } from "$lib/format.js";
  import { projectKey, scopeKeyOf } from "$lib/projects.js";
  import type { HandoffCell } from "$lib/projects.js";
  import OverviewTab from "$lib/Overview.svelte";
  import ProjectsTab from "$lib/Projects.svelte";
  import AnalyticsTab from "$lib/Analytics.svelte";
  import HealthTab from "$lib/Health.svelte";
  import WikiTab from "$lib/Wiki.svelte";
  import HandoffsTab from "$lib/Handoffs.svelte";
  import SettingsTab from "$lib/Settings.svelte";
  import TabStrip from "$lib/TabStrip.svelte";
  import { cancelConfirmBody, cancelOutcome } from "$lib/handoffs.js";
  import { hitKey } from "$lib/wiki.js";
  import { CAPTURE_ASSISTANT_WARNING, SWEEP_WARNING } from "$lib/settings.js";

  // Run an api Effect against the real HTTP client layer. api.ts exposes
  // Effects (no layer provided) so tests can swap in a mock layer; the SPA
  // provides WickClientLayer here at the edge.
  const run = <T,>(eff: Effect.Effect<T, unknown, never>): Promise<T> =>
    Effect.runPromise(eff.pipe(Effect.provide(WickClientLayer)) as Effect.Effect<T, unknown, never>);

  const app = document.getElementById("app");
  const base: string = (app?.dataset.base ?? "").replace(/\/$/, "");

  // Who is reading. The Go shell inlines it (PLAN §23.3) so the managing
  // controls are never painted for someone who would only get a 403 from
  // them — they are left out entirely rather than rendered dead.
  const canManage: boolean = (app?.dataset.canManage ?? "") === "true";

  // Opened from a project's "⋯" menu: ?project=<wick project id>. The id is
  // all the FE knows; the bucket it maps to is resolved by the server, which
  // owns that mapping (PLAN §22.2).
  const scopedProjectID: string =
    typeof window !== "undefined" ? (new URLSearchParams(window.location.search).get("project") ?? "").trim() : "";

  // All seven are built. The blurbs stay: they are what the tab strip's
  // tooltips read from, and they say what a tab is for before it has data.
  const TABS: { id: Tab; label: string; enabled: boolean; blurb: string }[] = [
    { id: "overview", label: "Overview", enabled: true, blurb: "" },
    {
      id: "projects",
      label: "Projects",
      enabled: true,
      blurb:
        "Every workspace/project in the store, with its sessions, observations, pages and last activity — plus a per-project backfill.",
    },
    {
      id: "analytics",
      label: "Analytics",
      enabled: true,
      blurb:
        "Growth over time, ingest accepted vs dropped vs shed, hook spool depth, index coverage, storage, and capture split per provider.",
    },
    {
      id: "wiki",
      label: "Wiki",
      enabled: true,
      blurb: "Browse and search what agents wrote down, and read the raw observations behind one session.",
    },
    {
      id: "handoffs",
      label: "Handoffs",
      enabled: true,
      blurb: "Open cross-agent batons and messages — what two providers working the same project look like.",
    },
    {
      id: "health",
      label: "Health",
      enabled: true,
      blurb:
        "Capture coverage, the cross-project contamination audit, and project-name collisions — the failures nobody sees without a UI.",
    },
    {
      id: "settings",
      label: "Settings",
      enabled: true,
      blurb: "Daemon, access, capture and privacy, model providers, retention and backfill defaults.",
    },
  ];

  // The project this panel was opened for, once the server has resolved it.
  // scopeError is kept apart from it: "we could not work out which bucket" is
  // a different thing to say than "that bucket is empty".
  let scope = $state<ProjectScope | null>(null);
  let scopeError = $state("");

  let backends = $state<BackendInfo[]>([]);
  let activeId = $state("");
  let overviews = $state<Record<string, Overview>>({});
  let loaded = $state(false);
  let loading = $state(true);
  let busy = $state(false);
  let tab = $state<Tab>("overview");

  let test = $state<TestResult | null>(null);
  let confirmStop = $state(false);
  // The last install's log, kept on the Overview card. A failure is shown
  // verbatim: the server's log names the asset, the checksum and the path,
  // and summarising it is how a fixable error becomes a mystery.
  let installMsg = $state("");
  let installFailed = $state(false);

  // Projects tab state. `projects` holds the whole response, failure and all
  // — the reason a read came back empty is as much a result as the rows.
  let projects = $state<ProjectsResponse | null>(null);
  let projectsLoading = $state(false);
  let selectedProject = $state("");
  let handoffCells = $state<Record<string, HandoffCell>>({});
  let backfillReport = $state<BackfillReport | null>(null);
  // What the server actually ran. Kept beside the report because the cap it
  // resolved is not in the report: skipped_for_cap says how many were left
  // out, and only this says what the limit was.
  let backfillRequest = $state<BackfillRequestEcho | null>(null);
  let backfillError = $state("");

  // Health tab state. Null means "not checked yet", which the tab says out
  // loud rather than rendering as a clean bill of health.
  let health = $state<HealthReport | null>(null);
  let healthLoading = $state(false);
  // Who is recording and who went quiet. Read with the health checks: the
  // trial is one of the silent failures this tab is for, and a finding that
  // names no projects is only half an answer.
  let policyRoster = $state<ProjectPolicyRoster | null>(null);

  // Wiki tab state. The search response and the open page are separate reads
  // and separate failures: a page that will not load must not blank the
  // result list that found it.
  let wikiQuery = $state("");
  let wikiScope = $state("");
  let wikiRes = $state<SearchResponse | null>(null);
  let wikiSearching = $state(false);
  let wikiSelected = $state("");
  let wikiPage = $state<Page | null>(null);
  let wikiPageError = $state("");
  let wikiPageLoading = $state(false);

  // Handoffs tab state. Both listings are project-scoped — the backend
  // resolves an unscoped one from a working directory, which answers about
  // whichever project it fell back to (PLAN §14).
  let handoffScope = $state("");
  let handoffRes = $state<HandoffsResponse | null>(null);
  let handoffLoading = $state(false);
  let messages = $state<MessagesResponse | null>(null);
  let cancelling = $state("");
  let pendingCancel = $state<Handoff | null>(null);

  // Settings tab state. `form` is what the user is editing and `stored` is
  // what the server last confirmed — keeping both is what lets the page say
  // "unsaved" and lets Discard mean something.
  let form = $state<Settings | null>(null);
  let storedSettings = $state<Settings | null>(null);
  let settingsLock = $state<AutostartLock | null>(null);
  let defaultPort = $state(0);
  let saving = $state(false);
  let restartPending = $state(false);
  let advanced = $state(false);
  let sweep = $state<SweepReport | null>(null);
  let sweepError = $state("");
  let confirmAssistant = $state(false);
  let confirmSweep = $state(false);

  const ov = $derived<Overview | null>(overviews[activeId] ?? null);
  const active = $derived(backends.find((b) => b.id === activeId));
  const activeTab = $derived(TABS.find((t) => t.id === tab) ?? TABS[0]);

  async function loadBackends(): Promise<void> {
    try {
      const res = await run(fetchBackends(base));
      backends = res.backends ?? [];
      if (backends.length && !backends.find((b) => b.id === activeId)) {
        activeId = backends[0].id;
      }
      await refreshAll();
    } catch (e) {
      toastError("Could not list Agent Memory backends", errText(e));
    } finally {
      loaded = true;
      loading = false;
    }
  }

  async function refresh(): Promise<void> {
    const id = activeId;
    if (!id) return;
    try {
      const o = await run(fetchOverview(base, id));
      overviews = { ...overviews, [id]: o };
    } catch {
      /* transient — keep the last known reading rather than blanking the page */
    }
  }

  async function refreshAll(): Promise<void> {
    for (const b of backends) {
      try {
        const o = await run(fetchOverview(base, b.id));
        overviews = { ...overviews, [b.id]: o };
      } catch {
        /* transient */
      }
    }
  }

  function switchTo(id: string): void {
    if (id === activeId) return;
    activeId = id;
    // Everything below belongs to the backend we are leaving. Keeping it
    // would show one store's projects under another store's name.
    test = null;
    projects = null;
    selectedProject = "";
    handoffCells = {};
    backfillReport = null;
    backfillRequest = null;
    backfillError = "";
    health = null;
    wikiRes = null;
    wikiPage = null;
    wikiPageError = "";
    wikiSelected = "";
    handoffRes = null;
    messages = null;
    handoffScope = "";
    form = null;
    storedSettings = null;
    sweep = null;
    sweepError = "";
    void refresh();
    void loadTab(tab);
  }

  async function act(fn: () => Promise<unknown>, failMsg: string, okMsg: string): Promise<void> {
    busy = true;
    try {
      await fn();
      toastOk("Done", okMsg);
    } catch (e) {
      toastError(failMsg, errText(e));
    } finally {
      busy = false;
      await refresh();
    }
  }

  const onStart = () =>
    act(() => run(start(base, activeId)), "Start failed", `${active?.name ?? "The daemon"} started.`);
  const onRestart = () =>
    act(() => run(restart(base, activeId)), "Restart failed", `${active?.name ?? "The daemon"} restarted.`);

  function doStop(): void {
    confirmStop = false;
    void act(() => run(stop(base, activeId)), "Stop failed", `${active?.name ?? "The daemon"} stopped.`);
  }

  // onInstall downloads the backend's binary into wick's own directory. It
  // is not a "start": the daemon stays exactly as it was, which matters on a
  // host where one is already running that wick did not spawn — the server
  // says so in `note` and that sentence is shown rather than swallowed.
  async function onInstall(): Promise<void> {
    const id = activeId;
    if (!id) return;
    busy = true;
    installMsg = "";
    installFailed = false;
    try {
      const res = await run(install(base, id));
      installFailed = false;
      installMsg = [res.output, res.note].filter(Boolean).join("\n").trim();
      toastOk("Installed", res.path ? `Installed at ${res.path}.` : "The backend is installed.");
    } catch (e) {
      installFailed = true;
      installMsg = errText(e);
      toastError("Install failed", errText(e));
    } finally {
      busy = false;
      await refresh();
    }
  }

  async function onTest(): Promise<void> {
    busy = true;
    try {
      test = await run(testConnection(base, activeId));
      if (test.ok) toastOk("Connected", `${active?.name ?? "The daemon"} answered its health check.`);
      else toastError("No answer", test.error ?? "The daemon did not answer.");
    } catch (e) {
      test = null;
      toastError("Test failed", errText(e));
    } finally {
      busy = false;
    }
  }

  // ── tab data ───────────────────────────────────────────────────────

  // loadTab fetches what a tab needs the first time it is opened. Nothing
  // here joins the poll: the project list is one HTTP call, and the health
  // checks are the expensive pair.
  async function loadTab(t: Tab): Promise<void> {
    if (!activeId) return;
    if (t === "projects" && !projects) await loadProjects();
    if (t === "health" && !health) await loadHealth();
    // Wiki and Handoffs both need the project list — one to scope a search,
    // the other because a handoff listing without a project is answered about
    // whichever one the daemon resolves on its own.
    if ((t === "wiki" || t === "handoffs") && !projects) await loadProjects();
    if (t === "settings" && !form) await loadSettings();
  }

  function selectTab(t: Tab): void {
    tab = t;
    void loadTab(t);
  }

  // resolveScope asks the server which bucket the wick project in ?project=
  // uses. It runs once, before the first project list, so the Projects tab
  // already knows what it was opened for. A failure is recorded and shown —
  // never silently widened to the whole store, which would look like the
  // project's memory and be someone else's.
  async function resolveScope(): Promise<void> {
    if (!scopedProjectID) return;
    try {
      scope = await run(fetchProjectScope(base, scopedProjectID));
      scopeError = "";
    } catch (e) {
      scope = null;
      scopeError = errText(e);
    }
  }

  async function loadProjects(): Promise<void> {
    const id = activeId;
    projectsLoading = true;
    try {
      projects = await run(fetchProjects(base, id));
      // Opened for one project: select its row, so the detail below the
      // table is already the one the menu pointed at.
      if (scope) {
        const key = scopeKeyOf(scope);
        if ((projects.projects ?? []).some((r) => projectKey(r) === key)) selectedProject = key;
      }
      await loadHandoffCounts(projects.projects ?? []);
    } catch (e) {
      toastError("Could not list projects", errText(e));
    } finally {
      projectsLoading = false;
    }
  }

  // loadHandoffCounts fills the pending-handoff column. It is one scoped
  // server call per project, which is why it runs once on open rather than on
  // the poll — and why a project whose count fails keeps its own error
  // instead of blanking the column for everyone.
  async function loadHandoffCounts(rows: ProjectRow[]): Promise<void> {
    const id = activeId;
    handoffCells = Object.fromEntries(rows.map((r) => [projectKey(r), { state: "loading" } as HandoffCell]));
    await Promise.all(
      rows.map(async (r) => {
        const key = projectKey(r);
        try {
          const res = await run(fetchHandoffs(base, id, { workspace: r.workspace, project: r.project }));
          const cell: HandoffCell = res.reason || res.error
            ? { state: "error", message: res.error ?? res.reason ?? "unreadable" }
            : { state: "ok", count: (res.handoffs ?? []).length };
          handoffCells = { ...handoffCells, [key]: cell };
        } catch (e) {
          handoffCells = { ...handoffCells, [key]: { state: "error", message: errText(e) } };
        }
      }),
    );
  }

  async function loadHealth(): Promise<void> {
    const id = activeId;
    healthLoading = true;
    try {
      health = await run(fetchHealth(base, id));
      // A roster that fails must not cost the page its checks: it explains
      // a finding, it is not the finding.
      policyRoster = await run(fetchProjectPolicies(base)).catch(() => null);
    } catch (e) {
      toastError("Could not run the health checks", errText(e));
    } finally {
      healthLoading = false;
    }
  }

  function selectProject(key: string): void {
    // Clicking the row already selected closes the detail rather than
    // re-running its backfill report against a stale one.
    selectedProject = selectedProject === key ? "" : key;
    backfillReport = null;
    backfillRequest = null;
    backfillError = "";
  }

  // onBackfill runs one import. Preview and the real thing share a path
  // because they share every failure mode; only `real` differs, and neither
  // ever sets force — that flag multiplies stored observations and belongs to
  // an explicit per-session repair, not to this button (PLAN §11.1, §11.5).
  async function onBackfill(row: ProjectRow, real: boolean): Promise<void> {
    busy = true;
    backfillError = "";
    try {
      const opts = { workspace: row.workspace, project: row.project };
      const res = real
        ? await run(runBackfill(base, activeId, opts))
        : await run(previewBackfill(base, activeId, opts));
      if (res.error) {
        backfillError = res.hint ? `${res.error} — ${res.hint}` : res.error;
        backfillReport = null;
        backfillRequest = null;
      } else {
        backfillReport = res.report ?? null;
        backfillRequest = res.request ?? null;
        if (real) {
          toastOk("Import finished", `${row.workspace}/${row.project} was imported.`);
          await refresh();
        }
      }
    } catch (e) {
      backfillError = errText(e);
    } finally {
      busy = false;
    }
  }

  // onCompact reclaims free database pages. It refreshes afterwards because
  // the whole point is watching "reclaimable" go to zero.
  async function onCompact(): Promise<void> {
    busy = true;
    try {
      const res = await run(compactStore(base, activeId));
      if (res.error) toastError("Compaction failed", res.error);
      else toastOk("Compacted", res.report?.output ?? "The database was rewritten.");
    } catch (e) {
      toastError("Compaction failed", errText(e));
    } finally {
      busy = false;
      await refresh();
    }
  }

  // ── wiki ───────────────────────────────────────────────────────────

  // scopeOf turns the "workspace/project" picker value back into a scope. An
  // empty picker means store-wide, which is a real choice for search and not
  // a missing value — so it yields an empty scope rather than a guess.
  function scopeOf(key: string): Scope {
    if (!key) return {};
    const [workspace, project] = key.split("/");
    return { workspace, project };
  }

  async function doSearch(): Promise<void> {
    const id = activeId;
    const q = wikiQuery.trim();
    if (!id || !q) return;
    wikiSearching = true;
    try {
      wikiRes = await run(searchWiki(base, id, q, scopeOf(wikiScope)));
    } catch (e) {
      toastError("Search failed", errText(e));
    } finally {
      wikiSearching = false;
    }
  }

  // openPage reads one page in full. A hit carries its own workspace/project,
  // and those are used rather than the picker's: a store-wide search returns
  // rows from several projects, and reading them all under the picker's scope
  // would ask the wrong project for the page.
  async function openPage(h: SearchHit): Promise<void> {
    const id = activeId;
    if (!id) return;
    wikiSelected = hitKey(h);
    wikiPageLoading = true;
    wikiPageError = "";
    wikiPage = null;
    try {
      const res = await run(readPage(base, id, h.path, { workspace: h.workspace, project: h.project }));
      if (res.error) wikiPageError = res.hint ? `${res.error} — ${res.hint}` : res.error;
      else wikiPage = res.page ?? null;
    } catch (e) {
      wikiPageError = errText(e);
    } finally {
      wikiPageLoading = false;
    }
  }

  // ── handoffs ───────────────────────────────────────────────────────

  async function loadHandoffs(): Promise<void> {
    const id = activeId;
    const key = handoffScope;
    if (!id || !key) return;
    handoffLoading = true;
    try {
      const scope = scopeOf(key);
      // Both in flight together: they are the two halves of one answer, and
      // a mailbox that fails must not delay the batons.
      const [h, m] = await Promise.all([
        run(fetchHandoffs(base, id, scope)),
        run(fetchMessages(base, id, scope, "inbox")).catch((e) => ({ error: errText(e) }) as MessagesResponse),
      ]);
      handoffRes = h;
      messages = m;
    } catch (e) {
      toastError("Could not read handoffs", errText(e));
    } finally {
      handoffLoading = false;
    }
  }

  function setHandoffScope(key: string): void {
    handoffScope = key;
    handoffRes = null;
    messages = null;
    void loadHandoffs();
  }

  // doCancelHandoff retires one baton, and reports what actually happened.
  //
  // The distinction it exists for: the backend answers a cancel for a baton
  // that is already gone with `cancelled: false` and NO error. Reporting that
  // as a success would tell the operator they stopped something that had
  // already been accepted — and they would stop looking for the agent holding
  // it. cancelOutcome keeps the three cases apart.
  async function doCancelHandoff(): Promise<void> {
    const h = pendingCancel;
    const id = activeId;
    pendingCancel = null;
    if (!h || !id) return;
    cancelling = h.id;
    try {
      const res = await run(cancelHandoff(base, id, scopeOf(handoffScope), h.id));
      const out = cancelOutcome(res.result, res.error);
      if (out.kind === "cancelled") toastOk(out.title, out.body);
      else if (out.kind === "gone") toastWarn(out.title, out.body);
      else toastError(out.title, out.body);
    } catch (e) {
      const out = cancelOutcome(null, errText(e));
      toastError(out.title, out.body);
    } finally {
      cancelling = "";
      await loadHandoffs();
    }
  }

  // ── settings ───────────────────────────────────────────────────────

  async function loadSettings(): Promise<void> {
    const id = activeId;
    if (!id) return;
    try {
      const res = await run(fetchSettings(base, id));
      storedSettings = res.settings;
      form = { ...res.settings };
      settingsLock = res.autostart_lock;
      defaultPort = res.default_port ?? 0;
    } catch (e) {
      toastError("Could not read settings", errText(e));
    }
  }

  function setField<K extends keyof Settings>(key: K, value: Settings[K]): void {
    if (!form) return;
    form = { ...form, [key]: value };
  }

  async function doSaveSettings(): Promise<void> {
    const id = activeId;
    if (!form || !id) return;
    saving = true;
    try {
      const res = await run(saveSettings(base, id, form));
      storedSettings = res.settings;
      form = { ...res.settings };
      settingsLock = res.autostart_lock;
      restartPending = res.restart_pending ?? false;
      toastOk("Saved", restartPending ? "Restart the daemon to apply them." : "They apply on the next start.");
      await refresh();
    } catch (e) {
      toastError("Could not save settings", errText(e));
    } finally {
      saving = false;
    }
  }

  // Turning assistant capture ON is confirmed; turning it off is not. The
  // asymmetry is the point — the dangerous direction is the one that needs
  // the sentence (PLAN §13.5 point 2).
  function onCaptureAssistant(next: boolean): void {
    if (next) confirmAssistant = true;
    else setField("capture_assistant", false);
  }

  async function doSweep(dryRun: boolean): Promise<void> {
    const id = activeId;
    confirmSweep = false;
    if (!id) return;
    busy = true;
    sweepError = "";
    try {
      const res = await run(runSweep(base, id, scopeOf(""), dryRun));
      if (res.error) {
        sweepError = res.hint ? `${res.error} — ${res.hint}` : res.error;
        sweep = null;
      } else {
        sweep = res.report ?? null;
        if (!dryRun) {
          toastOk("Sweep finished", "The store was swept.");
          await refresh();
        }
      }
    } catch (e) {
      sweepError = errText(e);
    } finally {
      busy = false;
    }
  }

  // The two reads behind the Overview (daemon status + store counters) are
  // cheap and LLM-free, so polling them is fine. The expensive diagnostics
  // belong to the Health tab and run only when it is opened
  // (PLAN §13.5 point 7).
  $effect(() => {
    void resolveScope().then(loadBackends);
    const t = setInterval(() => void refresh(), 5000);
    return () => clearInterval(t);
  });

  // stopBody names what stopping actually costs, including who is affected.
  const stopBody = $derived(
    (ov?.used_by?.length ?? 0) > 0
      ? `While the daemon is down, ${ov?.used_by?.map((i) => (i.name && i.name !== i.type ? `${i.type}/${i.name}` : i.type)).join(", ")} cannot recall anything and nothing they do is captured. Stored memory is not deleted.`
      : "Nothing is using the daemon right now, so stopping it affects no running agent. Stored memory is not deleted.",
  );
</script>

<div class="flex h-full flex-col bg-white-200 dark:bg-navy-800">
  <ToastHost />

  <!-- Header -->
  <div
    class="flex shrink-0 flex-col gap-3 px-4 pt-4 pb-3 sm:flex-row sm:items-start sm:justify-between sm:gap-4 sm:px-6 sm:pt-6 sm:pb-4"
  >
    <div class="flex min-w-0 items-center gap-3">
      <div
        class="flex h-12 w-12 flex-shrink-0 items-center justify-center rounded-lg bg-green-200 dark:bg-green-800 text-green-700 dark:text-green-300"
        aria-hidden="true"
      >
        <svg viewBox="0 0 16 16" class="h-6 w-6" fill="none" stroke="currentColor" stroke-width="1.5">
          <ellipse cx="8" cy="4" rx="5" ry="2"></ellipse>
          <path d="M3 4v8c0 1.1 2.24 2 5 2s5-.9 5-2V4" stroke-linecap="round"></path>
          <path d="M3 8c0 1.1 2.24 2 5 2s5-.9 5-2" stroke-linecap="round"></path>
        </svg>
      </div>
      <div class="min-w-0">
        <h1 class="text-lg font-semibold text-black-900 dark:text-white-100 sm:text-[1.375rem]">Agent Memory</h1>
        <p class="mt-0.5 hidden text-sm text-black-800 dark:text-black-600 sm:block">
          {active?.blurb ??
            "A local store of what agents learned, so context survives a session ending and a switch of provider CLI."}
        </p>
      </div>
    </div>
    <div class="flex flex-shrink-0 flex-wrap items-center gap-2">
      <TabStrip tabs={TABS} active={tab} onSelect={selectTab} />
    </div>
  </div>

  <!-- Which backend this panel is talking about — ALWAYS, not only when
       there is a choice. With one backend the page used to name it nowhere,
       so every number on it was about "some store"; the provider page names
       its provider either way, and more memory backends are coming. One
       control, two renderings: a label for one, the picker for several. -->
  {#if backends.length === 1}
    <div class="flex shrink-0 flex-wrap items-center gap-2 px-4 pb-3 sm:px-6" data-testid="backend-label">
      <span
        class="inline-flex items-center gap-2 rounded-lg border border-white-300 bg-white-100 px-3 py-1.5 text-[0.8125rem] font-medium text-black-800 dark:border-navy-600 dark:bg-navy-700 dark:text-black-600"
      >
        <span class={`h-1.5 w-1.5 rounded-full ${dotFor(overviews[backends[0].id]?.daemon)}`}></span>
        {#if backends[0].icon}
          <svg viewBox="0 0 16 16" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="1.5"
            >{@html backends[0].icon}</svg
          >
        {/if}
        {backends[0].name}
      </span>
    </div>
  {:else if backends.length > 1}
    <div class="flex shrink-0 flex-wrap items-center gap-2 px-4 pb-3 sm:px-6" data-testid="backend-picker">
      {#each backends as b (b.id)}
        <button
          type="button"
          onclick={() => switchTo(b.id)}
          class={`inline-flex items-center gap-2 rounded-lg border px-3 py-1.5 text-[0.8125rem] font-medium transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-green-200 dark:focus-visible:ring-green-800 ${
            b.id === activeId
              ? "border-green-400 bg-green-200 dark:bg-green-800 text-green-700 dark:text-green-300"
              : "border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 text-black-800 dark:text-black-600 hover:text-black-900 dark:hover:text-white-100"
          }`}
        >
          <span class={`h-1.5 w-1.5 rounded-full ${dotFor(overviews[b.id]?.daemon)}`}></span>
          {#if b.icon}
            <svg viewBox="0 0 16 16" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="1.5"
              >{@html b.icon}</svg
            >
          {/if}
          {b.name}
        </button>
      {/each}
    </div>
  {/if}

  <!-- Body -->
  <div class="min-h-0 flex-1 overflow-y-auto">
    {#if loaded && backends.length === 0}
      <div class="mx-auto w-full max-w-3xl px-6 py-12 text-center text-sm text-black-800 dark:text-black-600">
        No Agent Memory backend is registered on this host.
      </div>
    {:else if tab === "overview"}
      <OverviewTab
        {ov}
        {loading}
        {busy}
        {test}
        {canManage}
        {installMsg}
        {installFailed}
        {onInstall}
        {onStart}
        {onRestart}
        {onTest}
        onStop={() => (confirmStop = true)}
      />
    {:else if tab === "projects"}
      <ProjectsTab
        res={projects}
        loading={projectsLoading}
        {busy}
        selected={selectedProject}
        handoffs={handoffCells}
        backfill={backfillReport}
        backfillReq={backfillRequest}
        {backfillError}
        {canManage}
        {scope}
        {scopeError}
        onSelect={selectProject}
        onPreviewBackfill={(r) => void onBackfill(r, false)}
        onRunBackfill={(r) => void onBackfill(r, true)}
        onRefresh={() => void loadProjects()}
        onGoSettings={() => selectTab("settings")}
      />
    {:else if tab === "analytics"}
      <AnalyticsTab
        {ov}
        {loading}
        {busy}
        {canManage}
        onCompact={() => void onCompact()}
        onGoOverview={() => selectTab("overview")}
      />
    {:else if tab === "health"}
      <HealthTab
        report={health}
        {ov}
        roster={policyRoster}
        scope={{}}
        loading={healthLoading}
        onRefresh={() => void loadHealth()}
        onGoOverview={() => selectTab("overview")}
      />
    {:else if tab === "wiki"}
      <WikiTab
        res={wikiRes}
        page={wikiPage}
        pageError={wikiPageError}
        projects={projects?.projects ?? []}
        scopeKey={wikiScope}
        query={wikiQuery}
        searching={wikiSearching}
        loadingPage={wikiPageLoading}
        selected={wikiSelected}
        onQuery={(q) => (wikiQuery = q)}
        onScope={(k) => (wikiScope = k)}
        onSearch={() => void doSearch()}
        onOpen={(h) => void openPage(h)}
        onGoOverview={() => selectTab("overview")}
      />
    {:else if tab === "handoffs"}
      <HandoffsTab
        res={handoffRes}
        {messages}
        projects={projects?.projects ?? []}
        scopeKey={handoffScope}
        loading={handoffLoading}
        {busy}
        {canManage}
        {cancelling}
        onScope={setHandoffScope}
        onRefresh={() => void loadHandoffs()}
        onCancel={(h) => (pendingCancel = h)}
        onGoOverview={() => selectTab("overview")}
      />
    {:else}
      <SettingsTab
        {form}
        stored={storedSettings}
        lock={settingsLock}
        {defaultPort}
        running={ov?.daemon?.running ?? false}
        {restartPending}
        {saving}
        {busy}
        {test}
        {sweep}
        {sweepError}
        {advanced}
        {canManage}
        onField={setField}
        onSave={() => void doSaveSettings()}
        onReset={() => (form = storedSettings ? { ...storedSettings } : null)}
        onTest={() => void onTest()}
        {onCaptureAssistant}
        onPreviewSweep={() => void doSweep(true)}
        onRunSweep={() => (confirmSweep = true)}
        onToggleAdvanced={() => (advanced = !advanced)}
      />
    {/if}
  </div>
</div>

<ConfirmDialog
  open={confirmStop}
  title="Stop the Agent Memory daemon?"
  body={stopBody}
  confirmLabel="Stop daemon"
  destructive={true}
  onConfirm={doStop}
  onCancel={() => (confirmStop = false)}
/>

<!-- Cancelling a baton. The body names what stops being handed over, not just
     that something does — and there is no dialog anywhere for the backend's
     --expire-all, which is deliberately not exposed (PLAN §20.2). -->
<ConfirmDialog
  open={pendingCancel !== null}
  title="Cancel this handoff?"
  body={cancelConfirmBody(pendingCancel)}
  confirmLabel="Cancel handoff"
  cancelLabel="Keep it"
  destructive={true}
  onConfirm={() => void doCancelHandoff()}
  onCancel={() => (pendingCancel = null)}
/>

<ConfirmDialog
  open={confirmAssistant}
  title="Store the assistant's replies?"
  body={CAPTURE_ASSISTANT_WARNING}
  confirmLabel="Store assistant text"
  destructive={true}
  onConfirm={() => {
    confirmAssistant = false;
    setField("capture_assistant", true);
  }}
  onCancel={() => (confirmAssistant = false)}
/>

<ConfirmDialog
  open={confirmSweep}
  title="Run the retention sweep?"
  body={SWEEP_WARNING}
  confirmLabel="Run sweep"
  destructive={true}
  onConfirm={() => void doSweep(false)}
  onCancel={() => (confirmSweep = false)}
/>
