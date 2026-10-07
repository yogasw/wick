<script lang="ts">
  // Which credential a push or pull runs as.
  //
  // The panel's own git has no credentials, so against a private remote
  // Push simply failed. The Git CLI connector has them — along with the
  // branch policy and the audit trail — so the panel borrows one. Which
  // one is asked, never assumed: the suggestion is a guess from the
  // connector's label, and pushing under an identity the user did not
  // choose is precisely what this dialog exists to prevent.
  import type { GitConnector } from "$lib/api/scm";

  type Props = {
    /** Verb being authorised, for the copy: "push" | "pull" | "" (settings). */
    action?: string;
    repo: string;
    remoteURL?: string;
    candidates: GitConnector[];
    /** Currently remembered connector, if any. */
    selected?: string;
    /** The failure that prompted this dialog, when it opened because git
        refused for want of a credential. */
    error?: string;
    onConfirm: (connectorID: string) => void;
    onCancel: () => void;
  };

  let {
    action = "",
    repo,
    remoteURL = "",
    candidates,
    selected = "",
    error = "",
    onConfirm,
    onCancel,
  }: Props = $props();

  // "Run plain git" is a real choice, not the absence of one: stored, it
  // means the user decided the machine's own credentials are right here,
  // and the panel stops asking.
  const NATIVE = "native";

  // Preselect what the session already uses, else plain git. Something
  // is ALWAYS selected — a dialog that opens with nothing chosen and a
  // dead Save button asks the user to guess what it wants — and the
  // default is the one that changes nothing about how git already runs.
  let choice = $state(selected || NATIVE);

  const title = $derived(
    action
      ? `${action === "push" ? "Push" : "Pull"} needs a credential`
      : "Git credential for this session",
  );

  // Escape and a click outside both mean "not now" — a dialog you cannot
  // dismiss the ordinary way reads as broken.
  function onKey(e: KeyboardEvent) {
    if (e.key === "Escape") onCancel();
  }
</script>

<svelte:window onkeydown={onKey} />

<div
  class="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4"
  role="presentation"
  onclick={onCancel}
>
  <div
    class="w-full max-w-md rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 shadow-2xl"
    role="dialog"
    aria-modal="true"
    onclick={(e) => e.stopPropagation()}
  >
    <div class="border-b border-white-300 dark:border-navy-600 px-4 py-3">
      <h2 class="text-sm font-semibold text-black-900 dark:text-white-100">{title}</h2>
      <p class="mt-0.5 text-[11px] text-black-700 dark:text-black-600">
        {repo}{remoteURL ? ` · ${remoteURL}` : ""}
      </p>
      {#if error}
        <p class="mt-2 rounded border border-rose-200 dark:border-rose-900/50 bg-rose-50 dark:bg-rose-900/20 px-2 py-1.5 text-[11px] text-rose-700 dark:text-rose-300 break-words">
          {error}
        </p>
      {/if}
    </div>

    <div class="max-h-72 overflow-y-auto p-2">
      {#if candidates.length === 0}
        <p class="px-2 pb-2 pt-4 text-center text-xs text-black-700 dark:text-black-600">
          No Git CLI connector is available to you, so this runs as plain git with whatever
          credentials the machine has.
        </p>
      {:else}
        {#each candidates as c (c.id)}
          <button
            type="button"
            onclick={() => (choice = c.id)}
            class="flex w-full items-start gap-2 rounded-lg px-3 py-2 text-left transition-colors
                   {choice === c.id
              ? 'bg-green-50 dark:bg-green-900/20 ring-1 ring-green-500'
              : 'hover:bg-white-200 dark:hover:bg-navy-700'}"
          >
            <span
              class="mt-1 h-3 w-3 shrink-0 rounded-full border-2 {choice === c.id
                ? 'border-green-500 bg-green-500'
                : 'border-white-400 dark:border-navy-500'}"
            ></span>
            <span class="min-w-0 flex-1">
              <span class="block truncate text-xs font-medium text-black-900 dark:text-white-100">
                {c.label}
              </span>
              <span class="block truncate text-[11px] text-black-700 dark:text-black-600">
                {c.author ? `commits as ${c.author}` : "no commit identity set"}
              </span>
            </span>
            {#if c.suggested}
              <span
                class="shrink-0 rounded px-1.5 py-0.5 text-[10px] text-black-700 dark:text-black-400 bg-white-300 dark:bg-navy-600"
                title="Its label names this remote's host — a guess, not a verified match"
              >likely</span>
            {/if}
          </button>
        {/each}
      {/if}

      <!-- Always offered: the machine's own git, with no connector. -->
      <button
        type="button"
        onclick={() => (choice = NATIVE)}
        class="mt-1 flex w-full items-start gap-2 rounded-lg px-3 py-2 text-left transition-colors
               {choice === NATIVE
          ? 'bg-green-50 dark:bg-green-900/20 ring-1 ring-green-500'
          : 'hover:bg-white-200 dark:hover:bg-navy-700'}"
      >
        <span
          class="mt-1 h-3 w-3 shrink-0 rounded-full border-2 {choice === NATIVE
            ? 'border-green-500 bg-green-500'
            : 'border-white-400 dark:border-navy-500'}"
        ></span>
        <span class="min-w-0 flex-1">
          <span class="block text-xs font-medium text-black-900 dark:text-white-100">
            Native git
          </span>
          <span class="block text-[11px] text-black-700 dark:text-black-600">
            No connector — uses this machine's git credentials, and no branch policy applies
          </span>
        </span>
      </button>
    </div>

    <p class="px-4 pb-2 text-[11px] text-black-700 dark:text-black-600">
      Used for every repo in this session, and only by you. Change it any time with the key
      button.
    </p>

    <div class="flex justify-end gap-2 border-t border-white-300 dark:border-navy-600 px-4 py-3">
      <button
        type="button"
        onclick={onCancel}
        class="rounded-lg px-3 py-1.5 text-xs text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-700"
      >Cancel</button>
      {#if selected}
        <!-- Clearing is how you go back to being asked, or to plain git. -->
        <button
          type="button"
          onclick={() => onConfirm("")}
          class="rounded-lg px-3 py-1.5 text-xs text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-700"
        >Clear</button>
      {/if}
      <button
        type="button"
        disabled={!choice}
        onclick={() => onConfirm(choice)}
        class="rounded-lg bg-green-500 px-3 py-1.5 text-xs font-medium text-white-100 hover:bg-green-600 disabled:opacity-50"
      >{action ? `Continue ${action}` : "Save"}</button>
    </div>
  </div>
</div>
