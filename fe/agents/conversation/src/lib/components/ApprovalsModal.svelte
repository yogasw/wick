<script lang="ts">
  import type { ApprovalRequest, ApprovalDecision } from "../types/agents.js";

  type Props = {
    request: ApprovalRequest | null;
    // reason accompanies a "guide" decision and is delivered to the
    // agent, which is what lets it correct course instead of stopping.
    onDecide: (decision: ApprovalDecision, reason?: string) => void;
    onClose?: () => void;
    error?: string;
  };

  let { request, onDecide, onClose, error = "" }: Props = $props();

  // 0 = the approval timeout is off, so the daemon keeps the prompt open
  // while this tab is watching. Nothing to count towards.
  let countdown = $state(0);

  $effect(() => {
    const total = request?.expires_in_sec ?? 0;
    countdown = total;
    if (!request || total <= 0) return;
    const timer = setInterval(() => {
      countdown -= 1;
      // Stop at zero and leave the modal up. Deciding here would POST a
      // decision for a request the daemon has already expired, which
      // comes back 410 — the countdown is a hint, not the authority.
      // The real close arrives as an approval_resolved SSE event.
      if (countdown <= 0) {
        countdown = 0;
        clearInterval(timer);
      }
    }, 1000);
    return () => clearInterval(timer);
  });

  // Guided refusal is a two-step: reveal the note field, then send. A
  // reason is mandatory — without one this is just a block that failed
  // to explain itself, and the agent gets nothing to act on.
  let noteOpen = $state(false);
  let note = $state("");

  let dialogEl: HTMLDivElement | undefined = $state();
  let noteEl: HTMLTextAreaElement | undefined = $state();

  $effect(() => {
    // Reset per request so a note typed for one command can't leak into
    // the decision on the next.
    request;
    noteOpen = false;
    note = "";
  });

  // The prompt interrupts whoever was typing in the composer. Leaving
  // focus there would send the shortcut letters into their message
  // instead of answering, so the dialog claims focus on arrival.
  $effect(() => {
    if (!request) return;
    dialogEl?.focus();
  });

  // Follow the note field in and back out, so the keyboard path never
  // needs a click to land somewhere useful.
  $effect(() => {
    if (!request) return;
    if (noteOpen) noteEl?.focus();
    else dialogEl?.focus();
  });

  function cancelNote() {
    noteOpen = false;
    note = "";
  }

  function decide(decision: ApprovalDecision) {
    onDecide(decision);
  }

  function sendGuide() {
    const trimmed = note.trim();
    if (!trimmed) return;
    onDecide("guide", trimmed);
  }

  function dismiss() {
    decide("block");
    onClose?.();
  }

  // The modal interrupts whatever the user was doing, so every action is
  // reachable from the keyboard — reaching for the mouse is the slow path
  // this exists to avoid. Single letters are safe as shortcuts only while
  // the note is closed; once it opens the user is typing prose, and the
  // handler below steps aside.
  $effect(() => {
    if (!request) return;
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") {
        e.preventDefault();
        // Back out of the note first — a stray Escape while typing a
        // correction shouldn't also block the command.
        if (noteOpen) {
          cancelNote();
          return;
        }
        dismiss();
        return;
      }
      // While the note is open, keys belong to the textarea. Enter/Escape
      // are handled there (and above); everything else is just typing.
      if (noteOpen) return;
      // Let real shortcuts (copy, devtools, browser) through untouched.
      if (e.ctrlKey || e.metaKey || e.altKey) return;

      switch (e.key.toLowerCase()) {
        case "enter": // default action — the one most prompts want
        case "a":
          e.preventDefault();
          decide("approve_once");
          break;
        case "s":
          e.preventDefault();
          decide("approve_session");
          break;
        case "w":
          e.preventDefault();
          decide("approve_always");
          break;
        case "n":
          e.preventDefault();
          noteOpen = true;
          break;
        case "b":
          e.preventDefault();
          decide("block");
          break;
      }
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  });

  // Enter sends, Shift+Enter breaks the line — same contract as the
  // message composer, so the muscle memory carries over.
  function onNoteKey(e: KeyboardEvent) {
    if (e.key === "Enter" && !e.shiftKey && !e.ctrlKey && !e.metaKey) {
      e.preventDefault();
      sendGuide();
    }
  }
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
    <!-- tabindex -1 makes the dialog programmatically focusable without
         putting it in the tab order; it is the parking spot for focus
         whenever no field is open. -->
    <div
      bind:this={dialogEl}
      data-approval-dialog
      role="dialog"
      aria-modal="true"
      aria-label="Approve command"
      tabindex="-1"
      class="relative flex flex-col w-full max-w-lg mx-4 max-h-[calc(100dvh-2rem)] rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-xl focus:outline-none"
    >
      <div
        class="shrink-0 border-b border-white-300 dark:border-navy-600 px-6 py-4 flex items-center justify-between"
      >
        <div class="flex items-center gap-2">
          <span class="inline-flex h-2 w-2 rounded-full bg-amber-500 animate-pulse"></span>
          <h2 class="text-base font-semibold text-black-900 dark:text-white-100">Approve command?</h2>
        </div>
        <div class="flex items-center gap-3">
          <div class="font-mono text-xs text-black-700 dark:text-black-600 tabular-nums">
            {#if countdown > 0}
              {countdown}s
            {:else}
              Waiting
            {/if}
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

      <div data-approval-body class="min-h-0 flex-1 overflow-y-auto px-6 py-5 space-y-4">
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
        <div data-approval-error class="shrink-0 px-6 pb-1 text-xs font-medium text-neg-400">{error}</div>
      {/if}

      <div data-approval-actions class="shrink-0 border-t border-white-300 dark:border-navy-600 px-6 py-4 flex flex-col gap-2">
        <div class="grid grid-cols-1 sm:grid-cols-3 gap-2">
          <button
            type="button"
            class="rounded-lg bg-green-500 px-3 py-2 text-xs font-medium text-white-100 hover:bg-green-600 active:bg-green-700 transition-colors"
            onclick={() => decide("approve_once")}
          >Approve once<kbd class="hidden sm:inline ml-1 rounded border border-current/30 px-1 py-px text-[10px] font-mono opacity-70">A</kbd></button>
          <button
            type="button"
            class="rounded-lg border border-green-500 dark:border-green-600 px-3 py-2 text-xs font-medium text-green-700 dark:text-green-400 hover:bg-green-50 dark:hover:bg-green-900/20 transition-colors"
            onclick={() => decide("approve_session")}
          >Allow this session<kbd class="hidden sm:inline ml-1 rounded border border-current/30 px-1 py-px text-[10px] font-mono opacity-70">S</kbd></button>
          <button
            type="button"
            class="rounded-lg border border-green-500 dark:border-green-600 px-3 py-2 text-xs font-medium text-green-700 dark:text-green-400 hover:bg-green-50 dark:hover:bg-green-900/20 transition-colors"
            onclick={() => decide("approve_always")}
          >Always allow<kbd class="hidden sm:inline ml-1 rounded border border-current/30 px-1 py-px text-[10px] font-mono opacity-70">W</kbd></button>
        </div>

        {#if noteOpen}
          <!-- The note is the whole point of this path: it reaches the
               agent, which then picks a different approach instead of
               stopping. -->
          <div class="flex flex-col gap-2">
            <textarea
              bind:this={noteEl}
              data-approval-note
              rows="2"
              bind:value={note}
              onkeydown={onNoteKey}
              placeholder="Why not, and what should it do instead?"
              class="w-full rounded-lg border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-xs text-black-900 dark:text-white-100 placeholder:text-black-700 dark:placeholder:text-black-600 focus:outline-none focus:border-green-500 resize-none"
            ></textarea>
            <div class="grid grid-cols-2 gap-2">
              <button
                type="button"
                class="rounded-lg border border-white-400 dark:border-navy-600 px-3 py-2 text-xs font-medium text-black-800 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-600 transition-colors"
                onclick={cancelNote}
              >Cancel</button>
              <button
                type="button"
                disabled={note.trim() === ""}
                class="rounded-lg bg-cau-400 px-3 py-2 text-xs font-medium text-white-100 hover:bg-cau-500 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
                onclick={sendGuide}
              >Send<kbd class="hidden sm:inline ml-1 rounded border border-current/30 px-1 py-px text-[10px] font-mono opacity-70">&crarr;</kbd></button>
            </div>
            <div class="hidden sm:block text-[10px] text-black-700 dark:text-black-600">
              Enter to send &middot; Shift+Enter for a new line &middot; Esc to cancel
            </div>
          </div>
        {:else}
          <div class="grid grid-cols-2 gap-2">
            <button
              type="button"
              class="rounded-lg border border-cau-400 px-3 py-2 text-xs font-medium text-cau-400 hover:bg-cau-50 dark:hover:bg-cau-900/20 transition-colors"
              onclick={() => { noteOpen = true; }}
            >Block with note<kbd class="hidden sm:inline ml-1 rounded border border-current/30 px-1 py-px text-[10px] font-mono opacity-70">N</kbd></button>
            <button
              type="button"
              class="rounded-lg bg-red-600 px-3 py-2 text-xs font-medium text-white-100 hover:bg-red-700 active:bg-red-800 transition-colors"
              onclick={() => decide("block")}
            >Block<kbd class="hidden sm:inline ml-1 rounded border border-current/30 px-1 py-px text-[10px] font-mono opacity-70">B</kbd></button>
          </div>
        {/if}
      </div>
    </div>
  </div>
{/if}
