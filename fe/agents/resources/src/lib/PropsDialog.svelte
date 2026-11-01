<script lang="ts">
  // Properties of one process, opened from its ⋯ menu.
  //
  // Read once when the dialog opens — one request for one pid, nothing
  // polled and nothing cached — because the answer is only wanted when
  // someone asks: "which build is this?", most of all while a handover has
  // two wick servers running under the same name.

  import { Effect } from "effect";
  import { WickClientLayer } from "@wick-fe/common-api";
  import { fetchProcessPropsE } from "$lib/api.js";
  import { portal } from "$lib/portal.js";
  import { humanBytes } from "$lib/format.js";
  import type { ProcessProps } from "$lib/types.js";

  interface Props {
    base: string;
    pid: number;
    onClose: () => void;
  }
  let { base, pid, onClose }: Props = $props();

  let props = $state<ProcessProps | null>(null);
  let error = $state("");

  $effect(() => {
    const want = pid;
    props = null;
    error = "";
    Effect.runPromise(fetchProcessPropsE(base, want).pipe(Effect.provide(WickClientLayer)))
      .then((p) => {
        if (want === pid) props = p;
      })
      .catch((e) => {
        if (want === pid) error = e instanceof Error ? e.message : String(e);
      });
  });

  function since(iso?: string): string {
    if (!iso) return "";
    const t = new Date(iso);
    if (Number.isNaN(t.getTime())) return "";
    const s = Math.max(0, Math.round((Date.now() - t.getTime()) / 1000));
    const ago =
      s < 120 ? `${s}s` : s < 7200 ? `${Math.round(s / 60)}m` : s < 172800 ? `${Math.round(s / 3600)}h` : `${Math.round(s / 86400)}d`;
    return `${t.toLocaleString()} (${ago} ago)`;
  }

  // [label, value, mono] — empty values are dropped rather than shown as
  // a dash, so the list only says what is known.
  let rows = $derived.by((): [string, string, boolean][] => {
    const p = props;
    if (!p) return [];
    const version = p.version ? `${/^\d/.test(p.version) ? "v" : ""}${p.version}` : "unknown";
    const out: [string, string, boolean][] = [
      ["Version", p.version_source ? `${version} · from ${p.version_source}` : version, true],
      ["PID", String(p.pid), true],
      ["Parent PID", p.ppid ? String(p.ppid) : "", true],
      ["Name", p.name, true],
      ["State", p.state ?? "", false],
      ["Started", since(p.started_at), false],
      ["Memory", humanBytes(p.rss_bytes), false],
      ["Threads", p.threads ? String(p.threads) : "", false],
      ["OS user", p.os_user ?? "", false],
      ["Executable", p.exe ? (p.exe_deleted ? `${p.exe} (replaced on disk since it started)` : p.exe) : "", true],
      ["Working dir", p.cwd ?? "", true],
      ["Command", p.cmdline ?? "", true],
    ];
    return out.filter(([, v]) => v !== "");
  });
</script>

<svelte:window
  onkeydown={(e) => {
    if (e.key === "Escape") onClose();
  }}
/>

<!-- Backdrop and panel are portalled: the explorer sits inside an
     overflow-hidden card that would clip a fixed layer. -->
<button
  use:portal={{ x: 0, y: 0, fill: true }}
  type="button"
  class="cursor-default"
  style="background: rgb(10 12 16 / 0.4)"
  aria-label="Close properties"
  onclick={onClose}
></button>

<div
  use:portal={{ x: 0, y: 0, fill: true }}
  class="pointer-events-none !z-50 flex items-center justify-center p-4"
>
  <div
    role="dialog"
    aria-modal="true"
    aria-label="Process properties"
    class="pointer-events-auto max-h-[85vh] w-full max-w-xl overflow-y-auto rounded-lg border border-white-300 bg-white-100 shadow-xl dark:border-navy-600 dark:bg-navy-800"
  >
    <div class="flex items-center justify-between border-b border-white-300 px-4 py-2.5 dark:border-navy-600">
      <h2 class="text-sm font-medium text-black-900 dark:text-white-100">
        Properties · pid {pid}
        {#if props?.self}
          <span class="ml-1.5 rounded border border-white-300 px-1 py-px align-middle text-[10px] font-normal text-black-700 dark:border-navy-600 dark:text-black-600">
            this wick server
          </span>
        {/if}
      </h2>
      <button
        type="button"
        class="rounded px-1.5 text-black-700 hover:bg-white-300 dark:text-black-600 dark:hover:bg-navy-600"
        aria-label="Close"
        onclick={onClose}
      >
        ✕
      </button>
    </div>
    <div class="px-4 py-3 text-xs">
      {#if error}
        <p class="text-red-600 dark:text-red-400">{error}</p>
      {:else if !props}
        <p class="text-black-700 dark:text-black-600">Reading…</p>
      {:else}
        <dl class="grid grid-cols-[7.5rem_1fr] gap-x-3 gap-y-1.5">
          {#each rows as [label, value, mono] (label)}
            <dt class="text-black-700 dark:text-black-600">{label}</dt>
            <dd class="min-w-0 break-all text-black-900 dark:text-white-100 {mono ? 'font-mono' : ''}">{value}</dd>
          {/each}
        </dl>
      {/if}
    </div>
  </div>
</div>
