<script lang="ts">
  import type { ApprovalRequest, ApprovalDecision } from "../types/agents.js";

  type Props = {
    request: ApprovalRequest | null;
    onDecide: (decision: ApprovalDecision) => void;
    onClose?: () => void;
    error?: string;
  };

  let { request, onDecide, onClose, error = "" }: Props = $props();

  let countdown = $state(25);

  $effect(() => {
    if (!request) return;
    countdown = 25;
    const timer = setInterval(() => {
      countdown -= 1;
      if (countdown <= 0) {
        clearInterval(timer);
        onDecide("block");
      }
    }, 1000);
    return () => clearInterval(timer);
  });

  function decide(decision: ApprovalDecision) {
    onDecide(decision);
  }

  function dismiss() {
    decide("block");
    onClose?.();
  }

  $effect(() => {
    if (!request) return;
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") {
        e.preventDefault();
        dismiss();
      }
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  });
</script>

{#if request !== null}
  <div class="fixed inset-0 z-50 flex items-center justify-center">
    <button
      type="button"
      data-approval-backdrop
      aria-label="Dismiss"
      class="absolute inset-0 bg-black/60 backdrop-blur-sm"
      onclick={dismiss}
    ></button>
    <div
      class="relative w-full max-w-lg mx-4 rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-xl"
    >
      <div
        class="border-b border-white-300 dark:border-navy-600 px-6 py-4 flex items-center justify-between"
      >
        <div class="flex items-center gap-2">
          <span class="inline-flex h-2 w-2 rounded-full bg-amber-500 animate-pulse"></span>
          <h2 class="text-base font-semibold text-black-900 dark:text-white-100">Approve command?</h2>
        </div>
        <div class="flex items-center gap-3">
          <div class="font-mono text-xs text-black-700 dark:text-black-600 tabular-nums">
            {countdown}s
          </div>
          <button
            type="button"
            class="rounded p-1 text-black-600 dark:text-black-500 hover:text-black-900 dark:hover:text-white-100 hover:bg-white-300 dark:hover:bg-navy-600 transition-colors"
            aria-label="Close"
            onclick={() => { decide("block"); onClose?.(); }}
          >
            <svg xmlns="http://www.w3.org/2000/svg" class="h-4 w-4" viewBox="0 0 20 20" fill="currentColor">
              <path fill-rule="evenodd" d="M4.293 4.293a1 1 0 011.414 0L10 8.586l4.293-4.293a1 1 0 111.414 1.414L11.414 10l4.293 4.293a1 1 0 01-1.414 1.414L10 11.414l-4.293 4.293a1 1 0 01-1.414-1.414L8.586 10 4.293 5.707a1 1 0 010-1.414z" clip-rule="evenodd"></path>
            </svg>
          </button>
        </div>
      </div>

      <div class="px-6 py-5 space-y-4">
        <dl class="space-y-2 text-xs">
          <div class="flex gap-3">
            <dt class="w-20 shrink-0 text-black-700 dark:text-black-600">Agent</dt>
            <dd class="font-mono text-black-900 dark:text-white-100">{request.agent_name || "—"}</dd>
          </div>
          <div class="flex gap-3">
            <dt class="w-20 shrink-0 text-black-700 dark:text-black-600">Tool</dt>
            <dd class="font-mono text-black-900 dark:text-white-100">{request.tool || "—"}</dd>
          </div>
          <div class="flex gap-3">
            <dt class="w-20 shrink-0 text-black-700 dark:text-black-600">Work dir</dt>
            <dd class="font-mono text-black-900 dark:text-white-100 break-all">{request.work_dir || "—"}</dd>
          </div>
        </dl>
        <div>
          <div class="text-xs text-black-700 dark:text-black-600 mb-1">Command</div>
          <pre
            class="rounded-lg bg-white-200 dark:bg-navy-800 px-3 py-2.5 text-xs font-mono text-black-900 dark:text-white-100 whitespace-pre-wrap break-all"
          >{request.cmd || ""}</pre>
        </div>
      </div>

      {#if error}
        <div data-approval-error class="px-6 pb-1 text-xs font-medium text-neg-400">{error}</div>
      {/if}

      <div
        class="border-t border-white-300 dark:border-navy-600 px-6 py-4 grid grid-cols-4 gap-2"
      >
        <button
          type="button"
          class="rounded-lg bg-green-500 px-3 py-2 text-xs font-medium text-white-100 hover:bg-green-600 active:bg-green-700 transition-colors"
          onclick={() => decide("approve_once")}
        >Approve once</button>
        <button
          type="button"
          class="rounded-lg border border-green-500 dark:border-green-600 px-3 py-2 text-xs font-medium text-green-700 dark:text-green-400 hover:bg-green-50 dark:hover:bg-green-900/20 transition-colors"
          onclick={() => decide("approve_session")}
        >Allow this session</button>
        <button
          type="button"
          class="rounded-lg border border-green-500 dark:border-green-600 px-3 py-2 text-xs font-medium text-green-700 dark:text-green-400 hover:bg-green-50 dark:hover:bg-green-900/20 transition-colors"
          onclick={() => decide("approve_always")}
        >Always allow</button>
        <button
          type="button"
          class="rounded-lg bg-red-600 px-3 py-2 text-xs font-medium text-white-100 hover:bg-red-700 active:bg-red-800 transition-colors"
          onclick={() => decide("block")}
        >Block</button>
      </div>
    </div>
  </div>
{/if}
