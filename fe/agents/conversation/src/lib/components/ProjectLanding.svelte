<script lang="ts">
  import type { ProjectOption, ProviderOption, SessionListItem, ComposerCommand } from "../types/agents.js";
  import { toastError } from "@wick-fe/common-stores";
  import { Effect } from "effect";
  import { WickClientLayer } from "@wick-fe/common-api";
  import { createSessionInProject } from "../api/options.js";
  import { searchProjectFiles } from "../api/files.js";
  import { listComposerCommands } from "../api/composer.js";
  import { Composer } from "@wick-fe/common-ui";
  import { NOTIFY_KEY } from "../notify-pref.js";
  import SessionList from "./SessionList.svelte";

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

  $effect(() => {
    if (!selectedProvider && providers.length > 0) selectedProvider = providerKey(providers[0]);
  });

  const providerSelect = $derived({
    options: providers.map((p) => ({
      label: p.name && p.name !== p.type ? `${p.type} · ${p.name}` : p.type,
      value: providerKey(p),
    })),
    value: selectedProvider,
    onChange: (v: string) => { selectedProvider = v; },
  });

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

  async function handleSend({ text, files }: { text: string; files: File[] }) {
    try {
      const url = await createSessionInProject(
        base,
        text,
        files,
        selectedProvider,
        project.id,
      );
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
    placeholder="Ask anything…   / commands · @ files"
    notifyKey={NOTIFY_KEY}
    provider={providerSelect}
    onSearchFiles={searchMentionFiles}
    commands={composerCommands}
  />

  <!-- Session list — reuses SessionList for status badge, kebab/delete, pagination, search -->
  <div class="flex flex-col gap-3 flex-1 min-h-0">
    <SessionList
      {sessions}
      {search}
      onSearch={(s) => { search = s; }}
      onSelect={onSelectSession}
    />
  </div>
</div>
