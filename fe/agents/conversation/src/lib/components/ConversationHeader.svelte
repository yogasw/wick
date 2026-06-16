<script lang="ts">
  import type { SSEStatus } from "../types/agents.js";
  import type { LifecycleState } from "../stores/thread.js";
  import { idleCountdownText } from "../idleCountdown.js";

  export type ActiveView = "conversation" | "commands" | "approvals" | "raw";

  const TAB_LABELS: Record<ActiveView, string> = {
    conversation: "Conversation",
    commands: "Commands",
    approvals: "Approvals",
    raw: "Raw",
  };

  const TAB_ORDER: ActiveView[] = ["conversation", "commands", "approvals", "raw"];

  type Props = {
    title: string;
    agentLabel?: string;
    sseStatus: SSEStatus;
    lifecycle?: LifecycleState;
    idleTimeoutMs?: number;
    activeView?: ActiveView;
    onKill: () => void;
    onDelete: () => void;
    onTabChange?: (view: ActiveView) => void;
  };

  let {
    title,
    agentLabel = "",
    sseStatus,
    lifecycle,
    idleTimeoutMs = 120_000,
    activeView = "conversation",
    onKill,
    onDelete,
    onTabChange,
  }: Props = $props();

  let tabMenuOpen = $state(false);
  let buttonEl: HTMLButtonElement | undefined = $state();
  let menuPos = $state({ top: 0, left: 0 });

  function toggleTabMenu() {
    if (!tabMenuOpen && buttonEl) {
      const r = buttonEl.getBoundingClientRect();
      menuPos = { top: r.bottom + 4, left: r.left };
    }
    tabMenuOpen = !tabMenuOpen;
  }

  function selectTab(view: ActiveView) {
    tabMenuOpen = false;
    onTabChange?.(view);
  }

  $effect(() => {
    if (!tabMenuOpen) return;

    function handleKeydown(e: KeyboardEvent) {
      if (e.key === "Escape") tabMenuOpen = false;
    }

    function handleClick(e: MouseEvent) {
      const target = e.target as Node;
      if (buttonEl && buttonEl.contains(target)) return;
      const menu = document.querySelector("[data-tab-dropdown]");
      if (menu && menu.contains(target)) return;
      tabMenuOpen = false;
    }

    window.addEventListener("keydown", handleKeydown);
    window.addEventListener("click", handleClick);

    return () => {
      window.removeEventListener("keydown", handleKeydown);
      window.removeEventListener("click", handleClick);
    };
  });

  const statusClass = $derived(
    sseStatus === "connected"
      ? "border-green-300 dark:border-green-700 text-green-700 dark:text-green-300"
      : sseStatus === "error"
        ? "border-neg-300 dark:border-neg-700 text-neg-600 dark:text-neg-400"
        : "border-white-300 dark:border-navy-600 text-black-600 dark:text-black-700",
  );

  const statusLabel = $derived(
    sseStatus === "connected" ? "live" : sseStatus === "error" ? "error" : "connecting",
  );

  const lcVisible = $derived(lifecycle && lifecycle.state !== "" && lifecycle.state !== "killed");

  const lcClass = $derived(
    lifecycle?.state === "spawning"
      ? "border-amber-300 dark:border-amber-700 bg-amber-50 dark:bg-amber-900/20 text-amber-700 dark:text-amber-300"
      : lifecycle?.state === "working"
        ? "border-green-300 dark:border-green-700 bg-green-50 dark:bg-green-900/20 text-green-700 dark:text-green-300"
        : "border-blue-300 dark:border-blue-700 bg-blue-50 dark:bg-blue-900/20 text-blue-700 dark:text-blue-300",
  );

  const lcLabel = $derived(
    lifecycle?.state === "spawning"
      ? "spawning…"
      : lifecycle?.state === "working"
        ? "working"
        : lifecycle?.state === "idle"
          ? "idle"
          : "",
  );

  // Split agentLabel into instance name + provider type for two-tone display
  // Format: "Wick claude" → instance="Wick", type="claude"
  // Format: "claude" → instance="", type="claude"
  const agentParts = $derived(() => {
    if (!agentLabel) return { instance: "", type: "" };
    const parts = agentLabel.split(" ");
    if (parts.length >= 2) return { instance: parts[0], type: parts.slice(1).join(" ") };
    return { instance: "", type: agentLabel };
  });

  /* idle countdown state */
  let idleEnteredAt = $state(0);
  let countdownText = $state("");

  $effect(() => {
    if (lifecycle?.state !== "idle") {
      countdownText = "";
      idleEnteredAt = 0;
      return;
    }

    const atMs = lifecycle.at || (idleEnteredAt || Date.now());
    if (!idleEnteredAt) idleEnteredAt = atMs;

    countdownText = idleCountdownText(atMs, idleTimeoutMs, Date.now());

    const timer = setInterval(() => {
      countdownText = idleCountdownText(atMs, idleTimeoutMs, Date.now());
    }, 1000);

    return () => clearInterval(timer);
  });
</script>

<!-- Absolute floating transparent bar — same pattern as release -->
<div class="absolute top-0 left-0 right-0 z-20 flex flex-wrap items-center justify-between gap-2 pl-12 pr-2 md:px-4 py-2">

  <!-- Left: tab dropdown only -->
  <div class="shrink-0">
    <button
      bind:this={buttonEl}
      type="button"
      aria-label="Tab menu"
      onclick={toggleTabMenu}
      class="inline-flex items-center gap-1.5 rounded-lg border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-1.5 text-xs font-medium text-black-900 dark:text-white-100 hover:bg-white-200 dark:hover:bg-navy-700 transition-colors"
    >
      <svg viewBox="0 0 12 12" class="h-3 w-3" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
        <path d="M1 3h10M1 6h10M1 9h6" stroke-linecap="round"></path>
      </svg>
      {TAB_LABELS[activeView]}
      <svg viewBox="0 0 12 12" class="h-3 w-3 opacity-50" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
        <path d="M3 4.5l3 3 3-3" stroke-linecap="round" stroke-linejoin="round"></path>
      </svg>
    </button>
  </div>

  <!-- Right: instance + provider + lifecycle + sse + kill + delete -->
  <div class="flex items-center gap-2 shrink-0">
    {#if agentLabel}
      {#if agentParts().instance}
        <span class="hidden md:inline text-xs font-medium text-black-700 dark:text-black-600">{agentParts().instance}</span>
        <span class="hidden md:inline text-xs text-black-500 dark:text-black-600">{agentParts().type}</span>
      {:else}
        <span class="hidden md:inline text-xs text-black-500 dark:text-black-600">{agentParts().type}</span>
      {/if}
    {/if}

    {#if lcVisible}
      <span
        data-lifecycle-badge
        class={[
          "inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-[10px] font-medium",
          lcClass,
        ].join(" ")}
      >
        {#if lifecycle?.state === "spawning" || lifecycle?.state === "working"}
          <svg viewBox="0 0 8 8" class="h-1.5 w-1.5 animate-pulse" fill="currentColor" aria-hidden="true">
            <circle cx="4" cy="4" r="3"></circle>
          </svg>
        {:else}
          <svg viewBox="0 0 8 8" class="h-1.5 w-1.5" fill="currentColor" aria-hidden="true">
            <circle cx="4" cy="4" r="3"></circle>
          </svg>
        {/if}
        <span data-lifecycle-label>{lcLabel}</span>
        {#if lifecycle?.state === "idle" && countdownText}
          <span data-idle-countdown class="ml-0.5 opacity-80">{countdownText}</span>
        {/if}
      </span>
    {/if}

    <span
      class={[
        "inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-[10px] font-medium",
        statusClass,
      ].join(" ")}
    >
      {#if sseStatus === "connecting"}
        <svg viewBox="0 0 16 16" class="h-2.5 w-2.5 animate-spin" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
          <path d="M8 2a6 6 0 016 6" stroke-linecap="round"></path>
        </svg>
      {:else}
        <svg viewBox="0 0 8 8" class="h-1.5 w-1.5" fill="currentColor" aria-hidden="true">
          <circle cx="4" cy="4" r="3"></circle>
        </svg>
      {/if}
      {statusLabel}
    </span>

    <button
      type="button"
      aria-label="Kill session"
      onclick={onKill}
      class="inline-flex items-center gap-1.5 rounded-lg border border-cau-400 dark:border-cau-600 px-2 md:px-3 py-1.5 text-xs font-medium text-cau-600 dark:text-cau-400 hover:bg-cau-50 dark:hover:bg-cau-900/20 transition-colors"
    >
      <svg viewBox="0 0 12 12" class="h-3 w-3" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
        <circle cx="6" cy="6" r="4.5"></circle>
        <path d="M4 4l4 4M8 4L4 8" stroke-linecap="round"></path>
      </svg>
      <span class="hidden md:inline">Kill</span>
    </button>

    <button
      type="button"
      aria-label="Delete session"
      onclick={onDelete}
      class="inline-flex items-center gap-1.5 rounded-lg border border-neg-300 dark:border-neg-700 px-2 md:px-3 py-1.5 text-xs font-medium text-neg-600 dark:text-neg-400 hover:bg-neg-50 dark:hover:bg-neg-900/20 transition-colors"
    >
      <svg viewBox="0 0 12 12" class="h-3 w-3" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
        <path d="M2 3h8M4 3V2h4v1M5 5.5v3M7 5.5v3M3 3l.5 7h5L9 3" stroke-linecap="round" stroke-linejoin="round"></path>
      </svg>
      <span class="hidden md:inline">Delete</span>
    </button>
  </div>
</div>

{#if tabMenuOpen}
  <div
    data-tab-dropdown
    class="fixed z-[1000] min-w-[140px] rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-lg py-1"
    style="top:{menuPos.top}px; left:{menuPos.left}px;"
  >
    {#each TAB_ORDER as tab}
      <button
        type="button"
        onclick={() => selectTab(tab)}
        class={[
          "w-full text-left block px-3 py-2 text-xs transition-colors",
          activeView === tab
            ? "text-green-600 dark:text-green-400 font-medium bg-green-50 dark:bg-green-900/20"
            : "text-black-900 dark:text-white-100 hover:bg-white-200 dark:hover:bg-navy-800",
        ].join(" ")}
      >
        {TAB_LABELS[tab]}
      </button>
    {/each}
  </div>
{/if}
