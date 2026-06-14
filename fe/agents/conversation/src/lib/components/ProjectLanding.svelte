<script lang="ts">
  import type { ProjectOption, ProviderOption, SessionListItem } from "../types/agents.js";
  import { toastError } from "@wick-fe/common-stores";
  import { createSessionInProject } from "../api/options.js";
  import Composer from "./Composer.svelte";
  import ComposerToolbar from "./ComposerToolbar.svelte";

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
  const activeProjectId = $derived<string | null>(project.id);

  $effect(() => {
    if (!selectedProvider && providers.length > 0) selectedProvider = providers[0].type;
  });

  const isManaged = $derived(project.path === "");
  const chatCount = $derived(sessions.length);

  const filtered = $derived(
    search.trim() === ""
      ? sessions
      : sessions.filter((s) => s.label.toLowerCase().includes(search.trim().toLowerCase()))
  );

  function formatLastActive(ts: string): string {
    if (!ts) return "";
    const d = new Date(ts);
    if (isNaN(d.getTime())) return ts;
    const now = Date.now();
    const diff = Math.floor((now - d.getTime()) / 1000);
    if (diff < 60) return "just now";
    if (diff < 3600) return `${Math.floor(diff / 60)}m ago`;
    if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`;
    return `${Math.floor(diff / 86400)}d ago`;
  }

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

<div class="flex flex-col h-full p-6 max-w-2xl mx-auto w-full gap-6">

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
  <div class="flex items-start justify-between gap-4">
    <div class="flex items-center gap-3 min-w-0">
      <div class="shrink-0 flex items-center justify-center w-10 h-10 rounded-xl bg-white-200 dark:bg-navy-700 border border-white-300 dark:border-navy-600">
        <svg viewBox="0 0 16 16" class="h-5 w-5 text-black-800 dark:text-white-100" fill="none" stroke="currentColor" stroke-width="1.5">
          <path d="M2 4a1 1 0 011-1h3l2 2h5a1 1 0 011 1v6a1 1 0 01-1 1H3a1 1 0 01-1-1V4z" stroke-linecap="round" stroke-linejoin="round"></path>
        </svg>
      </div>
      <div class="min-w-0">
        <h1 class="text-lg font-semibold text-black-900 dark:text-white-100 truncate">{project.name}</h1>
        <p class="text-xs text-black-700 dark:text-black-600 mt-0.5">
          {chatCount} chats · {isManaged ? "managed" : project.path}
        </p>
      </div>
    </div>

    <div class="flex items-center gap-2 shrink-0">
      <button
        type="button"
        onclick={onPin}
        class="inline-flex items-center gap-1.5 rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-3 py-1.5 text-xs font-medium text-black-800 dark:text-white-100 hover:bg-white-200 dark:hover:bg-navy-600 transition-colors"
      >
        <svg viewBox="0 0 16 16" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="1.5">
          <path d="M10 2L14 6l-4 4-3-3-4 4V9l4-4 3 3 3-3-3-3z" stroke-linecap="round" stroke-linejoin="round"></path>
        </svg>
        Pin as default
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

  <!-- Compose box: real Composer + ComposerToolbar -->
  {#snippet toolbarLeading()}
    <ComposerToolbar
      {providers}
      projects={[project]}
      activeProvider={selectedProvider}
      activeProjectId={activeProjectId}
      onProviderChange={(type) => { selectedProvider = type; }}
      onProjectChange={(_id) => { /* project fixed for landing */ }}
    />
  {/snippet}
  <Composer
    onSend={handleSend}
    placeholder="Ask anything… (Shift+Enter for new line)"
    leadingActions={toolbarLeading}
  />

  <!-- Session search + list -->
  <div class="flex flex-col gap-3 flex-1 min-h-0">
    <div class="relative shrink-0">
      <svg
        viewBox="0 0 16 16"
        class="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-black-600 dark:text-black-700 pointer-events-none"
        fill="none"
        stroke="currentColor"
        stroke-width="1.5"
      >
        <circle cx="6.5" cy="6.5" r="4.5"></circle>
        <path d="M10.5 10.5l3 3" stroke-linecap="round"></path>
      </svg>
      <input
        type="text"
        placeholder="Search chats in this project…"
        value={search}
        oninput={(e) => { search = (e.target as HTMLInputElement).value; }}
        class="w-full rounded-xl border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-700 pl-9 pr-4 py-2.5 text-sm text-black-900 dark:text-white-100 placeholder-black-600 dark:placeholder-black-700 focus:border-green-500 focus:ring-2 focus:ring-green-200 dark:focus:ring-green-800 focus:outline-none"
      />
    </div>

    {#if filtered.length === 0}
      <div class="flex flex-col items-center py-12 text-center">
        <p class="text-sm text-black-700 dark:text-black-600">
          {sessions.length === 0 ? "No chats in this project yet." : "No chats match your search."}
        </p>
      </div>
    {:else}
      <div class="overflow-y-auto flex-1 rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 divide-y divide-white-300 dark:divide-navy-600">
        {#each filtered as sess (sess.id)}
          <div
            role="button"
            tabindex="0"
            onclick={() => onSelectSession(sess.id)}
            onkeydown={(e) => e.key === "Enter" && onSelectSession(sess.id)}
            class="flex items-center justify-between gap-4 px-5 py-3.5 cursor-pointer hover:bg-white-200 dark:hover:bg-navy-800 transition-colors"
          >
            <div class="flex-1 min-w-0">
              <p class="truncate text-sm font-medium text-black-900 dark:text-white-100">
                {sess.label || "New session"}
              </p>
              <p class="text-xs text-black-600 dark:text-black-700 mt-0.5">
                {formatLastActive(sess.last_active)}
              </p>
            </div>
            <svg viewBox="0 0 16 16" class="h-4 w-4 shrink-0 text-black-600 dark:text-black-700" fill="none" stroke="currentColor" stroke-width="1.5">
              <path d="M6 4l4 4-4 4" stroke-linecap="round" stroke-linejoin="round"></path>
            </svg>
          </div>
        {/each}
      </div>
    {/if}
  </div>
</div>
