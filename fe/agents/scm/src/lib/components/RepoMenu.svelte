<script lang="ts">
  // Repository actions — fetch/pull/push and the branch operations.
  //
  // It lives on the REPO row, not in the Graph header, because these act on
  // the repository rather than on the history: put it in the History tab and
  // it disappears the moment you switch to Changes, which is exactly when
  // someone reaches for "pull" after seeing what changed.
  import { fetchRemote, createBranch, renameBranch, deleteBranch, loadStatus } from "$lib/git-actions";
  import { branch } from "$lib/stores/scm";

  // Which way the panel opens. In the branch bar there is no room below —
  // it IS the bottom of the panel — so a menu anchored downward renders off
  // the edge and reads as "the button did nothing".
  type Props = { direction?: "up" | "down" };
  let { direction = "down" }: Props = $props();

  let open = $state(false);
  let busy = $state(false);

  async function run(fn: () => Promise<unknown>) {
    open = false;
    busy = true;
    try {
      await fn();
    } finally {
      busy = false;
    }
  }

  async function newBranch() {
    const name = prompt("New branch name:", "");
    if (name) await run(() => createBranch(name.trim()));
  }
  async function renameCurrent() {
    const cur = $branch?.name ?? "";
    if (!cur) return;
    const to = prompt(`Rename "${cur}" to:`, cur);
    if (to) await run(() => renameBranch(cur, to.trim()));
  }
  async function removeBranch() {
    const name = prompt("Delete which branch?", "");
    if (name) await run(() => deleteBranch(name.trim()));
  }

  const item =
    "block w-full rounded px-2 py-1 text-left text-xs text-black-800 hover:bg-white-200 dark:text-black-600 dark:hover:bg-navy-800";
</script>

<div class="relative">
  <button
    type="button"
    onclick={(e) => { e.stopPropagation(); open = !open; }}
    disabled={busy}
    title="Repository actions"
    aria-label="Repository actions"
    class="inline-flex h-[30px] w-7 shrink-0 items-center justify-center rounded-lg border border-white-300 text-[13px] leading-none text-black-700 hover:bg-white-200 disabled:opacity-40 dark:border-navy-600 dark:text-black-600 dark:hover:bg-navy-800 transition-colors"
  >⋯</button>

  {#if open}
    <button type="button" class="fixed inset-0 z-10 cursor-default" aria-label="Close" onclick={() => (open = false)}></button>
    <div
      class={"absolute right-0 z-20 w-52 rounded-lg border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-1 shadow-lg " +
        (direction === "up" ? "bottom-full mb-1" : "top-full mt-1")}
    >
      <!-- No Pull/Push here: their buttons are two inches to the left, and
           an overflow that repeats the thing it overflows from is just a
           longer way to click the same button. -->
      <button type="button" class={item} onclick={() => run(() => fetchRemote())}>Fetch</button>
      <div class="my-1 border-t border-white-300 dark:border-navy-600"></div>
      <button type="button" class={item} onclick={newBranch}>New branch…</button>
      <button type="button" class={item} onclick={renameCurrent} disabled={!$branch?.name}>Rename current branch…</button>
      <button
        type="button"
        class="block w-full rounded px-2 py-1 text-left text-xs text-cau-600 hover:bg-white-200 dark:text-cau-400 dark:hover:bg-navy-800"
        onclick={removeBranch}
      >Delete branch…</button>
      <div class="my-1 border-t border-white-300 dark:border-navy-600"></div>
      <button type="button" class={item} onclick={() => run(() => loadStatus())}>Refresh</button>
    </div>
  {/if}
</div>
