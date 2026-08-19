<script lang="ts">
  // Coverage: how many agent processes actually have a ceiling over them,
  // and what to do about the ones that do not.
  //
  // This sits above the agent tables on purpose. The number that matters
  // is how many processes are UNCOVERED, and reading that after scrolling
  // past a list of healthy-looking rows is reading it too late.
  //
  // Two things this panel refuses to pretend:
  //
  //   - Writing the shim is not installing it. The system path has to
  //     point at it, and that needs root. The command is shown for a
  //     person to run rather than executed here: a sudo call from an HTTP
  //     request hangs on a password prompt nobody can answer, and giving
  //     wick passwordless root would mean anyone reaching this page can
  //     write to /usr/local/bin.
  //   - The shim does not catch everything. A caller that runs the binary
  //     by its full path never passes through it, which is exactly how an
  //     unrelated service can run the same binary unbounded.

  import { Effect } from "effect";
  import { WickClientLayer } from "@wick-fe/common-api";
  import { toastOk, toastError } from "@wick-fe/common-stores";
  import { fetchWrapperStatusE, installWrapperE, uninstallWrapperE } from "$lib/api.js";
  import { humanBytes } from "$lib/format.js";
  import type { WrapperStatus, WrapperAction } from "$lib/types.js";

  interface Props {
    base: string;
    // Suggested per-agent ceiling, so a fresh install does not have to be
    // told a number the page already derived from this machine.
    suggestedMB?: number;
  }
  let { base, suggestedMB = 0 }: Props = $props();

  let status = $state<WrapperStatus | null>(null);
  let action = $state<WrapperAction | null>(null);
  let busy = $state(false);
  let expanded = $state(false);
  let error = $state("");

  async function load(): Promise<void> {
    try {
      status = await Effect.runPromise(
        fetchWrapperStatusE(base).pipe(Effect.provide(WickClientLayer)),
      );
      error = "";
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    }
  }

  $effect(() => {
    void load();
  });

  async function install(): Promise<void> {
    busy = true;
    try {
      action = await Effect.runPromise(
        installWrapperE(base, { limit_mb: suggestedMB }).pipe(Effect.provide(WickClientLayer)),
      );
      await load();
    } catch (e) {
      toastError(e instanceof Error ? e.message : String(e));
    } finally {
      busy = false;
    }
  }

  // Step one of two: ask for the restore command. The shim file is only
  // removed on the confirmed call, after the path has been pointed back.
  async function beginUninstall(): Promise<void> {
    busy = true;
    try {
      action = await Effect.runPromise(
        uninstallWrapperE(base, {}).pipe(Effect.provide(WickClientLayer)),
      );
    } catch (e) {
      toastError(e instanceof Error ? e.message : String(e));
    } finally {
      busy = false;
    }
  }

  async function finishUninstall(): Promise<void> {
    busy = true;
    try {
      const res = await Effect.runPromise(
        uninstallWrapperE(base, { confirmed: true }).pipe(Effect.provide(WickClientLayer)),
      );
      toastOk(res.message);
      action = null;
      await load();
    } catch (e) {
      toastError(e instanceof Error ? e.message : String(e));
    } finally {
      busy = false;
    }
  }

  async function copyCommands(): Promise<void> {
    const text = (action?.commands ?? []).join("\n");
    try {
      await navigator.clipboard.writeText(text);
      toastOk("Copied");
    } catch {
      // Refused on an insecure origin. The text is selectable, so this
      // is not worth an error.
    }
  }

  const anyInstalled = $derived((status?.providers ?? []).some((p) => p.installed));
  const uncovered = $derived(status?.unisolated ?? 0);
  const covered = $derived(status?.isolated ?? 0);
  // Named separately because the fix differs: one of wick's own needs a
  // setting changed, while a stranger can only be bounded at its own
  // service — no shim reaches a caller using a full path.
  const strays = $derived((status?.processes ?? []).filter((p) => !p.isolated && !p.from_wick));
  const ownUncovered = $derived((status?.processes ?? []).filter((p) => !p.isolated && p.from_wick));
</script>

<div class="rounded-xl border border-white-300 bg-white-100 shadow-sm dark:border-navy-600 dark:bg-navy-700">
  <div class="flex flex-wrap items-start justify-between gap-3 px-5 py-4">
    <div class="min-w-0">
      <h2 class="text-sm font-semibold text-black-900 dark:text-white-100">Coverage</h2>
      {#if status?.supported}
        <p class="mt-0.5 text-xs text-black-700 dark:text-black-600">
          {covered} agent process{covered === 1 ? "" : "es"} under a limit
          {#if uncovered > 0}
            · <span class="font-medium text-amber-700 dark:text-amber-500">
              {uncovered} with no limit at all
            </span>
          {/if}
        </p>
      {:else}
        <p class="mt-0.5 text-xs text-black-700 dark:text-black-600">
          {status?.notice ?? "This machine has no cgroups, so nothing can be isolated here."}
        </p>
      {/if}
    </div>

    {#if status?.supported}
      <div class="flex items-center gap-2">
        <button
          type="button"
          class="rounded-lg border border-white-300 px-3 py-1.5 text-xs text-black-700 transition-colors hover:bg-white-200 dark:border-navy-600 dark:text-black-600 dark:hover:bg-navy-600"
          onclick={() => (expanded = !expanded)}
        >
          {expanded ? "Hide details" : "Details"}
        </button>
        {#if anyInstalled}
          <button
            type="button"
            class="rounded-lg border border-white-300 px-3 py-1.5 text-xs text-black-700 transition-colors hover:bg-white-200 disabled:opacity-50 dark:border-navy-600 dark:text-black-600 dark:hover:bg-navy-600"
            disabled={busy}
            onclick={() => void beginUninstall()}
          >
            Remove shim
          </button>
        {:else}
          <button
            type="button"
            class="rounded-lg bg-blue-600 px-3 py-1.5 text-xs font-medium text-white-100 transition-colors hover:bg-blue-700 disabled:opacity-50"
            disabled={busy}
            onclick={() => void install()}
          >
            Cover agents started outside wick
          </button>
        {/if}
      </div>
    {/if}
  </div>

  {#if error}
    <p class="border-t border-white-300 px-5 py-3 text-xs text-red-600 dark:border-navy-600 dark:text-red-400">
      {error}
    </p>
  {/if}

  <!-- The privileged step. Shown as text with a copy button, never run
       for the operator: this is what actually changes /usr/local/bin. -->
  {#if action}
    <div class="border-t border-white-300 px-5 py-4 dark:border-navy-600">
      <p class="text-xs text-black-900 dark:text-white-100">{action.message}</p>
      {#if action.commands?.length}
        <div class="mt-2 rounded-lg bg-white-200 p-3 dark:bg-navy-800">
          <div class="flex items-start justify-between gap-3">
            <pre class="min-w-0 flex-1 overflow-x-auto font-mono text-[11px] leading-relaxed text-black-900 dark:text-white-100">{action.commands.join(
                "\n",
              )}</pre>
            <button
              type="button"
              class="shrink-0 rounded border border-white-300 px-2 py-0.5 text-[11px] text-black-700 hover:bg-white-100 dark:border-navy-600 dark:text-black-600 dark:hover:bg-navy-700"
              onclick={() => void copyCommands()}
            >
              Copy
            </button>
          </div>
        </div>
      {/if}
      <div class="mt-2 flex items-center gap-2">
        {#if anyInstalled}
          <!-- Only offered after the restore command has been shown.
               Removing the shim while the path still points at it would
               break every spawn until the path is fixed. -->
          <button
            type="button"
            class="rounded bg-red-600 px-2.5 py-1 text-[11px] text-white-100 hover:bg-red-700 disabled:opacity-50"
            disabled={busy}
            onclick={() => void finishUninstall()}
          >
            I ran it — remove the shim
          </button>
        {/if}
        <button
          type="button"
          class="rounded border border-white-300 px-2.5 py-1 text-[11px] text-black-700 hover:bg-white-200 dark:border-navy-600 dark:text-black-600 dark:hover:bg-navy-600"
          onclick={() => (action = null)}
        >
          Dismiss
        </button>
      </div>
    </div>
  {/if}

  <!-- The uncovered processes, split by what can actually be done about
       them. A single count would hide that half of them are beyond any
       setting on this page. -->
  {#if status?.supported && uncovered > 0}
    <div class="border-t border-white-300 px-5 py-4 dark:border-navy-600">
      {#if ownUncovered.length}
        <p class="text-xs text-black-900 dark:text-white-100">
          <span class="font-medium">{ownUncovered.length}</span> of wick's own agents have no limit.
        </p>
        <p class="mt-0.5 text-xs text-black-700 dark:text-black-600">
          Turn on <span class="font-medium">on spawn</span> in Agent settings, or install the shim above.
        </p>
      {/if}
      {#if strays.length}
        <p class="text-xs text-black-900 {ownUncovered.length ? 'mt-3' : ''} dark:text-white-100">
          <span class="font-medium">{strays.length}</span>
          process{strays.length === 1 ? "" : "es"} on this machine run the same binaries outside wick.
        </p>
        <p class="mt-0.5 text-xs text-black-700 dark:text-black-600">
          A caller that runs the binary by its full path never passes through the shim, so these can
          only be bounded at the service that starts them:
        </p>
        <pre class="mt-1.5 overflow-x-auto rounded bg-white-200 px-2 py-1 font-mono text-[11px] text-black-700 dark:bg-navy-800 dark:text-black-600">sudo systemctl edit &lt;that-service&gt;
# MemoryMax=1500M
# MemoryHigh=infinity
# MemorySwapMax=0</pre>
        <ul class="mt-2 space-y-0.5">
          {#each strays.slice(0, 5) as p (p.pid)}
            <li class="font-mono text-[11px] text-black-700 dark:text-black-600">
              {p.name} pid {p.pid} · {humanBytes(p.rss_bytes)}
            </li>
          {/each}
        </ul>
      {/if}
    </div>
  {/if}

  {#if expanded && status?.supported}
    <div class="border-t border-white-300 px-5 py-4 dark:border-navy-600">
      <h3 class="text-xs font-medium text-black-900 dark:text-white-100">Interception</h3>
      <table class="mt-2 w-full text-left text-xs">
        <tbody>
          {#each status.providers ?? [] as p (p.name)}
            <tr class="border-b border-white-300 last:border-0 dark:border-navy-600">
              <td class="py-1.5 pr-4 font-medium text-black-900 dark:text-white-100">{p.name}</td>
              <td class="py-1.5 pr-4">
                {#if p.installed}
                  <span class="text-green-700 dark:text-green-500">intercepted</span>
                {:else}
                  <span class="text-black-700 dark:text-black-600">not intercepted</span>
                {/if}
              </td>
              <td class="py-1.5 font-mono text-[11px] text-black-700 dark:text-black-600">
                {p.link} → {p.link_target ?? "(missing)"}
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
      {#if !(status.providers ?? []).length}
        <p class="text-xs text-black-700 dark:text-black-600">
          No agent binary found on this machine's PATH.
        </p>
      {/if}
    </div>
  {/if}
</div>
