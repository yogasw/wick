<script lang="ts">
  type Props = {
    stagedCount: number;
    busy: boolean;
    onCommit: (message: string) => Promise<boolean>;
  };
  let { stagedCount, busy, onCommit }: Props = $props();

  let message = $state("");

  async function doCommit() {
    const ok = await onCommit(message);
    if (ok) message = "";
  }
</script>

<div class="border-b border-white-300 dark:border-navy-600 p-3">
  <input
    type="text"
    bind:value={message}
    placeholder="Commit message (Enter to commit)"
    onkeydown={(e) => {
      if (e.key === "Enter") {
        e.preventDefault();
        if (!busy && stagedCount > 0 && message.trim()) doCommit();
      }
    }}
    class="w-full rounded-lg border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-2.5 py-2 text-xs text-black-900 dark:text-white-100 placeholder-black-600 dark:placeholder-black-700 focus:border-green-500 focus:ring-2 focus:ring-green-200 dark:focus:ring-green-800 focus:outline-none"
  />
  <button
    type="button"
    onclick={doCommit}
    disabled={busy || stagedCount === 0 || !message.trim()}
    class="mt-2 w-full rounded-lg bg-green-500 px-3 py-2 text-xs font-medium text-white-100 hover:bg-green-600 disabled:opacity-50 transition-colors"
  >
    Commit {stagedCount > 0 ? `(${stagedCount})` : ""}
  </button>
</div>
