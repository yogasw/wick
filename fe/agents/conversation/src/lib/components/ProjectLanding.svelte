<script lang="ts">
  import type { ProjectOption, ProviderOption, SessionListItem, ComposerCommand } from "../types/agents.js";
  import { toastError } from "@wick-fe/common-stores";
  import { Effect } from "effect";
  import { WickClientLayer } from "@wick-fe/common-api";
  import { createSessionInProject, getPresetOptions, getProviderOptionModels } from "../api/options.js";
  import { searchProjectFiles } from "../api/files.js";
  import { listComposerCommands } from "../api/composer.js";
  import {
    attachSession,
    deleteTicket,
    getProjectTickets,
    getTicketFilter,
    getTicketPrefs,
    saveTicketFilter,
    saveTicketPrefs,
    type EmptiedTicket,
  } from "../api/tickets.js";
  import type { TicketBoard, TicketFilter } from "../types/agents.js";
  import { Composer } from "@wick-fe/common-ui";
  import { NOTIFY_KEY } from "../notify-pref.js";
  import SessionList from "./SessionList.svelte";
  import KanbanBoard from "./KanbanBoard.svelte";
  import TicketDetail from "./TicketDetail.svelte";

  type Props = {
    base: string;
    project: ProjectOption;
    providers: ProviderOption[];
    sessions: SessionListItem[];
    onPin: () => void;
    onSelectSession: (id: string) => void;
  };

  let { base, project, providers, sessions, onPin, onSelectSession }: Props = $props();

  let search = $state("");
  let selectedProvider = $state<string>("");

  // A named instance is "type/name"; a default collapses to the bare type.
  function providerKey(p: ProviderOption): string {
    return p.name && p.name !== p.type ? `${p.type}/${p.name}` : p.type;
  }

  // Resolve the project's configured default ("codex" or "codex/name") to a
  // provider actually present in the list: exact key first, then bare type.
  //
  // The project's model rides along as the composer's packed
  // "type/name::modelID" value, but ONLY on an exact instance match: the
  // fallbacks land on a different instance, and a model id resolves against
  // the registry of the instance it was chosen on.
  function defaultProviderKey(): string {
    if (providers.length === 0) return "";
    const want = (project.defaultProvider ?? "").trim();
    if (want) {
      const exact = providers.find((p) => providerKey(p) === want);
      if (exact) {
        const key = providerKey(exact);
        const model = (project.defaultModel ?? "").trim();
        return model ? `${key}::${model}` : key;
      }
      const byType = providers.find((p) => p.type === want.split("/")[0]);
      if (byType) return providerKey(byType);
    }
    return providerKey(providers[0]);
  }

  $effect(() => {
    if (!selectedProvider && providers.length > 0) selectedProvider = defaultProviderKey();
  });

  // Live model loader — same contract as the conversation composer: split the
  // "type/name" value and fetch that instance's current vendor models, falling
  // back (composer-side) to the static list on error.
  function loadProviderModels(optionValue: string, opts?: { entry?: string }) {
    const slash = optionValue.indexOf("/");
    const type = slash < 0 ? optionValue : optionValue.slice(0, slash);
    const name = slash < 0 ? optionValue : optionValue.slice(slash + 1);
    return Effect.runPromise(getProviderOptionModels(base, type, name, opts).pipe(Effect.provide(WickClientLayer)));
  }

  const providerSelect = $derived({
    options: providers.map((p) => ({
      label: p.name && p.name !== p.type ? `${p.type} · ${p.name}` : p.type,
      value: providerKey(p),
      badge: p.usesAIRouter ? "AI Router" : undefined,
      // Carry the model list so a multi-model provider (wick) shows the
      // nested model picker here too — matching the conversation composer.
      models: p.models,
    })),
    value: selectedProvider,
    onChange: (v: string) => { selectedProvider = v; },
    loadModels: loadProviderModels,
  });

  // Preset selector — same affordance as the new-session page, so an init
  // from a project landing can override the preset before the session
  // starts. "" = the default preset (or the project's configured default).
  let presets = $state<string[]>([]);
  let selectedPreset = $state<string>("");
  $effect(() => {
    Effect.runPromise(getPresetOptions(base).pipe(Effect.provide(WickClientLayer)))
      .then((names) => { presets = names; })
      .catch(() => { /* presets optional */ });
  });
  const presetSelect = $derived(
    presets.length > 0
      ? {
          options: [
            { label: "— preset (default) —", value: "" },
            ...presets.map((n) => ({ label: n, value: n })),
          ],
          value: selectedPreset,
          onChange: (v: string) => { selectedPreset = v; },
        }
      : undefined,
  );

  // `@` searches THIS project's folder; `/` shows skills only (pre-session).
  function searchMentionFiles(query: string): Promise<string[]> {
    return Effect.runPromise(searchProjectFiles(base, project.id, query).pipe(Effect.provide(WickClientLayer)))
      .catch(() => [] as string[]);
  }
  let composerCommands = $state<ComposerCommand[]>([]);
  $effect(() => {
    const providerType = selectedProvider ? selectedProvider.split("/")[0] : "";
    Effect.runPromise(listComposerCommands(base, "new", providerType).pipe(Effect.provide(WickClientLayer)))
      .then((res) => {
        composerCommands = (res.commands ?? []).map((c) => ({
          value: c.insert ?? c.id, label: c.label, hint: c.hint, category: c.category,
        }));
      })
      .catch(() => { /* commands optional */ });
  });

  const chatCount = $derived(sessions.length);

  /* ── ticket board (only when the project has ticket mode enabled) ── */
  let board = $state<TicketBoard | null>(null);
  let ticketFilter = $state<TicketFilter>({});

  /* The filter IS the request: statuses, assignee and the untracked rail all
     decide what the server builds, so a switched-off column costs nothing to
     poll instead of arriving and being discarded. */
  /* Derived, not a plain function, so the fetch below re-runs on the three
     fields that change the REQUEST and not on view_mode, which only changes
     how the same data is drawn. */
  const boardOptions = $derived.by(() => {
    const saved = ticketFilter.statuses;
    /* An empty saved list means "all statuses" — the request omits the param
       so a project that later adds a stage shows it. The board spells "every
       chip off" with a single sentinel entry, which becomes an explicit empty
       set: no columns drawn, no cards fetched. */
    const statuses =
      !saved || saved.length === 0 ? undefined : saved[0] === " none" ? [] : saved;
    return {
      rows: 3,
      statuses,
      assignee: ticketFilter.assignee || undefined,
      untracked: ticketFilter.show_untracked === true,
      untrackedLimit: 25,
    };
  });

  /* The saved filter arrives first, then the board it describes: fetching
     the board before knowing the filter would request the wrong thing and
     immediately request it again. */
  let filterLoaded = $state(false);
  $effect(() => {
    Effect.runPromise(getTicketFilter(base, project.id).pipe(Effect.provide(WickClientLayer)))
      .then((f) => { ticketFilter = f ?? {}; })
      .catch(() => { /* filter optional — the defaults are a fine board */ })
      .finally(() => { filterLoaded = true; });
  });

  /* The fetch keys off this string rather than off boardOptions itself:
     every filter edit rebuilds that object, so an object dependency would
     re-fetch an identical board whenever the user merely switched
     list/card. */
  const boardRequestKey = $derived(project.id + "|" + JSON.stringify(boardOptions));

  $effect(() => {
    if (!filterLoaded) return;
    boardRequestKey; // the sole dependency: re-fetch when the request changes
    reloadBoard();
  });

  const ticketEnabled = $derived(board?.config?.enabled === true);
  const viewMode = $derived(ticketEnabled && ticketFilter.view_mode === "card" ? "card" : "list");

  /* Which ticket's detail is open, if any. Read from ?ticket= on load so a
     link out of the conversation rail lands here. */
  let openTicketId = $state<string | null>(
    new URLSearchParams(window.location.search).get("ticket"),
  );

  function reloadBoard() {
    Effect.runPromise(
      getProjectTickets(base, project.id, boardOptions).pipe(Effect.provide(WickClientLayer)),
    )
      .then((b) => { board = b; })
      .catch(() => { /* keep the previous board on a transient failure */ });
  }

  /* ── a ticket that just lost its last chat ──
     A ticket with no sessions tracks nothing, so removal is offered. The
     answer can be made standing ("don't ask again"), which is safe here
     precisely because deleting an EMPTY ticket destroys no conversation —
     unlike deleting one that still holds chats, which always asks. */
  let emptied = $state<EmptiedTicket | null>(null);
  let dontAskEmpty = $state(false);
  let autoDeleteEmpty = $state<"" | "always" | "never">("");

  $effect(() => {
    Effect.runPromise(getTicketPrefs(base).pipe(Effect.provide(WickClientLayer)))
      .then((p) => { autoDeleteEmpty = p.auto_delete_empty ?? ""; })
      .catch(() => { /* asking is the safe default */ });
  });

  function handleEmptied(t: EmptiedTicket) {
    if (autoDeleteEmpty === "never") return;
    if (autoDeleteEmpty === "always") {
      removeEmptyTicket(t.id);
      return;
    }
    emptied = t;
    dontAskEmpty = false;
  }

  function removeEmptyTicket(ticketId: string) {
    // "keep": an empty ticket has no chats to take with it, so this is only
    // ever the ticket record.
    Effect.runPromise(deleteTicket(base, ticketId, "keep").pipe(Effect.provide(WickClientLayer)))
      .then(reloadBoard)
      .catch((e: unknown) => toastError(e instanceof Error ? e.message : "Failed to delete the ticket"));
  }

  function answerEmptied(remove: boolean) {
    const t = emptied;
    emptied = null;
    if (dontAskEmpty) {
      const choice = remove ? "always" : "never";
      autoDeleteEmpty = choice;
      Effect.runPromise(
        saveTicketPrefs(base, { auto_delete_empty: choice }).pipe(Effect.provide(WickClientLayer)),
      ).catch(() => { /* the prompt will simply appear again */ });
    }
    if (remove && t) removeEmptyTicket(t.id);
    else reloadBoard();
  }

  /* Starting a session on a ticket is a two-step: create it in this
     project, then attach. The composer owns creation, so the ticket id is
     stashed and applied once the new session exists. */
  function newSessionInTicket(ticketId: string) {
    pendingTicketId = ticketId;
    openTicketId = null;
  }
  let pendingTicketId = $state<string | null>(null);
  /* The ticket the next message lands on. SELECTING one is enough — having
     a ticket open on screen and typing into the composer above it can only
     mean the new chat belongs to it, and requiring "+ New session" first
     made the obvious path the one that silently did nothing. The stash
     still exists for that button, which closes the detail on its way out.
     Cleared explicitly, so "Start without a ticket" also drops the open
     one rather than being undone by it on the next render. */
  const activeTicketId = $derived(pendingTicketId ?? openTicketId);
  /* Resolved to its card so the composer can name it; a ticket picked
     before the board finished loading still has its id to fall back on. */
  const activeTicket = $derived(
    activeTicketId ? (board?.tickets.find((t) => t.id === activeTicketId) ?? null) : null,
  );
  const activeTicketLabel = $derived(
    activeTicket ? activeTicket.title || activeTicket.id : (activeTicketId ?? ""),
  );
  /* The placeholder sits on ONE line that cannot wrap, so a long ticket
     title would run past the input's right edge and hide the "/ commands"
     hint after it. The footer below states the title in full and wraps, so
     nothing is lost by clipping it here. */
  const activeTicketShort = $derived(
    activeTicketLabel.length > 32 ? activeTicketLabel.slice(0, 32).trimEnd() + "…" : activeTicketLabel,
  );

  let filterSaveTimer: ReturnType<typeof setTimeout> | undefined;
  function applyFilter(f: TicketFilter) {
    ticketFilter = f;
    clearTimeout(filterSaveTimer);
    filterSaveTimer = setTimeout(() => {
      Effect.runPromise(
        saveTicketFilter(base, project.id, ticketFilter).pipe(Effect.provide(WickClientLayer)),
      ).catch(() => { /* saving the preference is best-effort */ });
    }, 400);
  }
  const setViewMode = (mode: "list" | "card") => applyFilter({ ...ticketFilter, view_mode: mode });

  async function handleSend({ text, files }: { text: string; files: File[] }) {
    try {
      const url = await createSessionInProject(
        base,
        text,
        files,
        selectedProvider,
        project.id,
        selectedPreset,
      );
      // Attach before navigating: the new session must already belong to
      // the ticket when it opens, or its first spawn would miss the
      // ticket pointer and its notes.
      const ticketId = activeTicketId;
      if (ticketId) {
        const sessionId = url.split("/").pop()?.split("?")[0] ?? "";
        if (sessionId) {
          await Effect.runPromise(
            attachSession(base, ticketId, sessionId).pipe(Effect.provide(WickClientLayer)),
          ).catch(() => { /* the session is still usable unattached */ });
        }
        pendingTicketId = null;
      }
      window.location.href = url;
    } catch (err) {
      toastError(err instanceof Error ? err.message : "Failed to create session");
    }
  }
</script>

<div class="flex flex-col h-full p-6 max-w-4xl mx-auto w-full gap-6">

  <!-- Back link -->
  <a
    href={`${base}/sessions`}
    class="inline-flex items-center gap-1.5 text-xs text-black-700 dark:text-black-600 hover:text-green-600 dark:hover:text-green-400 transition-colors w-fit"
  >
    <svg viewBox="0 0 16 16" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
      <path d="M10 4L6 8l4 4" stroke-linecap="round" stroke-linejoin="round"></path>
    </svg>
    All chats
  </a>

  <!-- Project header -->
  <div class="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
    <div class="flex items-center gap-3 min-w-0">
      <div class="shrink-0 flex items-center justify-center w-10 h-10 rounded-xl bg-white-200 dark:bg-navy-700 border border-white-300 dark:border-navy-600">
        <svg viewBox="0 0 16 16" class="h-5 w-5 text-black-800 dark:text-white-100" fill="none" stroke="currentColor" stroke-width="1.5">
          <path d="M2 4a1 1 0 011-1h3l2 2h5a1 1 0 011 1v6a1 1 0 01-1 1H3a1 1 0 01-1-1V4z" stroke-linecap="round" stroke-linejoin="round"></path>
        </svg>
      </div>
      <div class="min-w-0">
        <h1 class="text-lg font-semibold text-black-900 dark:text-white-100 truncate">{project.name}</h1>
        <p class="text-xs text-black-700 dark:text-black-600 mt-0.5">
          {chatCount} chats · {project.managed ? "managed" : "custom"}
        </p>
        {#if project.path}
          <p class="text-[11px] font-mono text-black-600 dark:text-black-700 mt-0.5 truncate">{project.path}</p>
        {/if}
      </div>
    </div>

    <div class="flex flex-wrap items-center gap-2">
      <button
        type="button"
        onclick={onPin}
        aria-pressed={project.pinned}
        class="inline-flex items-center gap-1.5 rounded-lg border px-3 py-1.5 text-xs font-medium transition-colors {project.pinned
          ? 'border-green-500 bg-green-500 text-white-100 hover:bg-green-600'
          : 'border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-700 text-black-800 dark:text-white-100 hover:bg-white-200 dark:hover:bg-navy-600'}"
      >
        <span class="text-[11px] leading-none {project.pinned ? '' : 'grayscale'}">📌</span>
        {project.pinned ? "Pinned as default" : "Pin as default"}
      </button>
      <a
        href={`${base}/projects/${project.id}`}
        class="inline-flex items-center gap-1.5 rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-3 py-1.5 text-xs font-medium text-black-800 dark:text-white-100 hover:bg-white-200 dark:hover:bg-navy-600 transition-colors"
      >
        <svg viewBox="0 0 16 16" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="1.5">
          <circle cx="8" cy="8" r="6"></circle>
          <path d="M8 5v3l2 2" stroke-linecap="round" stroke-linejoin="round"></path>
        </svg>
        Settings
      </a>
    </div>
  </div>

  <!-- Compose box: shared Composer (project is fixed here, shown in the header). -->
  <Composer
    onSend={handleSend}
    placeholder={activeTicketId
      ? `Ask anything on ${activeTicketShort}…   / commands · @ files`
      : "Ask anything…   / commands · @ files"}
    notifyKey={NOTIFY_KEY}
    provider={providerSelect}
    preset={presetSelect}
    onSearchFiles={searchMentionFiles}
    commands={composerCommands}
  />
  <!-- With a ticket selected the composer says so by NAME, and offers the
       way out: picking a ticket is a mode, and a mode you cannot see or
       leave is a trap. Without one the line stays the plain project
       default. -->
  {#if activeTicketId}
    <p class="text-center text-xs text-black-600 dark:text-black-700">
      New session on <span class="font-medium text-green-600 dark:text-green-400"
        >{activeTicketLabel}</span
      >
      in <span class="font-medium text-black-800 dark:text-black-600">{project.name}</span> — this
      chat joins the ticket and reads its notes.
      <button
        type="button"
        onclick={() => { pendingTicketId = null; openTicketId = null; }}
        class="underline transition-colors hover:text-black-800 dark:hover:text-black-500"
        >Start without a ticket</button
      >
    </p>
  {:else}
    <p class="text-center text-xs text-black-600 dark:text-black-700">
      New session in <span class="font-medium text-black-800 dark:text-black-600">{project.name}</span>{#if project.defaultProvider} · defaults to <span class="font-mono">{project.defaultProvider}</span>{/if}. Pick provider / model / preset above to override for this session.
    </p>
  {/if}

  <!-- Session list / ticket board. The List|Card toggle appears only when
       this project has ticket mode enabled; the choice is saved per user. -->
  <div class="flex flex-col gap-3 flex-1 min-h-0">
    {#if ticketEnabled}
      <div class="flex items-center justify-between">
        <div class="inline-flex overflow-hidden rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-700">
          <button
            type="button"
            aria-pressed={viewMode === "list"}
            onclick={() => setViewMode("list")}
            class={"px-4 py-1.5 text-xs font-medium transition-colors " + (viewMode === "list"
              ? "bg-green-500 text-white-100"
              : "text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-600")}
          >List</button>
          <button
            type="button"
            aria-pressed={viewMode === "card"}
            onclick={() => setViewMode("card")}
            class={"px-4 py-1.5 text-xs font-medium transition-colors " + (viewMode === "card"
              ? "bg-green-500 text-white-100"
              : "text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-600")}
          >Card</button>
        </div>
      </div>
    {/if}

    {#if ticketEnabled && openTicketId}
      <TicketDetail
        {base}
        ticketId={openTicketId}
        onBack={() => { openTicketId = null; reloadBoard(); }}
        onOpenSession={onSelectSession}
        onNewSession={newSessionInTicket}
      />
    {:else if ticketEnabled && viewMode === "card" && board}
      <KanbanBoard
        {base}
        projectId={project.id}
        {board}
        filter={ticketFilter}
        onFilter={applyFilter}
        onOpen={(id) => { openTicketId = id; }}
        onOpenSession={onSelectSession}
        onReload={reloadBoard}
        onEmptied={handleEmptied}
      />
    {:else}
      <SessionList
        {sessions}
        {search}
        onSearch={(s) => { search = s; }}
        onSelect={onSelectSession}
      />
    {/if}
  </div>
</div>

<!-- The last chat left a ticket, so the ticket now tracks nothing. Offering
     removal here (rather than doing it silently) keeps a deliberately-empty
     ticket — one just created, not yet used — from disappearing. -->
{#if emptied}
  <div class="fixed inset-0 z-50 flex items-center justify-center bg-navy-900/40 p-4">
    <div class="w-full max-w-sm rounded-xl border border-white-300 bg-white-100 p-5 shadow-xl dark:border-navy-600 dark:bg-navy-700">
      <h2 class="text-sm font-semibold text-black-900 dark:text-white-100">
        Delete the empty ticket?
      </h2>
      <p class="mt-2 text-xs leading-relaxed text-black-700 dark:text-black-600">
        <span class="rounded bg-white-200 px-1 font-mono text-[10px] dark:bg-navy-800">{emptied.id}</span>
        “{emptied.title}” has no chats left. Nothing else is deleted — the chat you moved is
        safe on its new ticket.
      </p>

      <label class="mt-4 flex cursor-pointer items-start gap-2 text-xs text-black-800 dark:text-black-600">
        <input
          type="checkbox"
          bind:checked={dontAskEmpty}
          class="mt-0.5 h-3.5 w-3.5 rounded border-white-400 text-green-600 focus:ring-green-500 dark:border-navy-600"
        />
        <span>
          Don’t ask again — remember my answer.
          <span class="mt-0.5 block text-[11px] text-black-700 dark:text-black-600">
            Only for empty tickets. Deleting a ticket that still holds chats always asks.
          </span>
        </span>
      </label>

      <div class="mt-4 flex justify-end gap-2">
        <button
          type="button"
          onclick={() => answerEmptied(false)}
          class="rounded-lg border border-white-400 px-3 py-1.5 text-xs text-black-800 transition-colors hover:bg-white-200 dark:border-navy-600 dark:text-black-600 dark:hover:bg-navy-800"
        >Keep it</button>
        <button
          type="button"
          onclick={() => answerEmptied(true)}
          class="rounded-lg bg-neg-400 px-3 py-1.5 text-xs font-semibold text-white-100 transition-colors hover:bg-neg-500"
        >Delete ticket</button>
      </div>
    </div>
  </div>
{/if}
