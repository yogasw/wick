<script lang="ts">
  import { onDestroy, onMount } from "svelte";
  import { humanBytes } from "../format.js";
  import { traceFileState, type TraceFileState } from "../fileState.js";
  import type { TraceContext, TraceDisplay } from "../types.js";
  type Props = { display: TraceDisplay; ctx?: TraceContext };
  let { display, ctx }: Props = $props();

  /* A chip, never a decode: payloads can be megabytes, and a trace with a
     dozen screenshots must open instantly. Bytes are fetched (blob_ref) or
     decoded (in-memory base64 of a live turn) only when the chip is
     clicked. */
  const ICON: Record<string, string> = { image: "🖼", pdf: "📄", audio: "🔊", video: "🎞" };
  const icon = $derived(ICON[display.kind] ?? "📦");
  const label = $derived(mimeLabel(display.mime ?? ""));
  const size = $derived(display.original_bytes ? humanBytes(display.original_bytes) : "");
  const name = $derived(display.name || `${display.kind}.${label.toLowerCase()}`);
  const inTrace = $derived(!display.too_large && (!!display.data || (!!display.blob_ref && !!ctx?.loadBlob)));
  /* The trace kept no bytes (or no way to fetch them) but the tool call
     named a file: fall back to the file on disk, and say honestly whether
     it is still the one the trace saw. */
  const fromPath = $derived(!inTrace && !!ctx?.sourcePath && !!ctx?.statPath && !!ctx?.loadPath);

  let url = $state<string | null>(null);
  let loading = $state(false);
  let failed = $state(false);
  // The trace's own blob is gone (retention swept it) — not a load error.
  let blobGone = $state(false);
  let checking = $state(false);
  let fileState = $state<TraceFileState | null>(null);
  let rel = "";

  const available = $derived(inTrace || (fromPath && (fileState?.state === "same" || fileState?.state === "changed")));
  const note = $derived(fileState?.state === "changed" ? "Ini versi sekarang, bukan versi saat trace" : "");
  const suffix = $derived(blobGone ? "output trace sudah dihapus" : (fromPath ? fileState?.label ?? "" : ""));
  const text = $derived([name, label, size, suffix].filter(Boolean).join(" · "));
  const title = $derived(
    display.too_large && !fromPath ? "Too large to keep in the trace"
      : blobGone ? "Output trace sudah dihapus"
      : inTrace ? "Click to load"
      : fromPath ? (fileState?.title ?? "Memeriksa file…")
      : "Not stored in the trace",
  );

  /* The chip mounts only when its result is expanded, so this is already
     lazy; the host batches and caches the stat per session. */
  onMount(() => {
    if (!fromPath) return;
    checking = true;
    ctx!.statPath!(ctx!.sourcePath!)
      .then((st) => {
        rel = st.rel ?? "";
        fileState = traceFileState(st, display.original_bytes, ctx?.calledAt);
      })
      .catch(() => { fileState = traceFileState(null); })
      .finally(() => { checking = false; });
  });

  function mimeLabel(mime: string): string {
    let sub = mime.includes("/") ? mime.slice(mime.indexOf("/") + 1) : mime;
    sub = sub.replace(/^x-/, "").split("+")[0];
    return !sub || sub === "octet-stream" ? "BINARY" : sub.toUpperCase();
  }

  function decode(data: string): Blob {
    const b64 = data.replace(/^data:[^,]*,/, "").replace(/\s+/g, "");
    const bin = atob(b64);
    const bytes = new Uint8Array(bin.length);
    for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
    return new Blob([bytes], { type: display.mime || "application/octet-stream" });
  }

  async function open(): Promise<void> {
    if (url) {
      openMedia();
      return;
    }
    if (!available || loading) return;
    loading = true;
    failed = false;
    try {
      const blob = display.data ? decode(display.data)
        : inTrace ? await ctx!.loadBlob!(display.blob_ref!)
        : await ctx!.loadPath!(rel);
      // The blob endpoint names its type by magic; keep the display's mime
      // when it has one so an SVG stays an SVG.
      const typed = display.mime && blob.type !== display.mime ? new Blob([blob], { type: display.mime }) : blob;
      url = URL.createObjectURL(typed);
    } catch (e) {
      // Both loaders name the status last ("blob e1: 404"); a 404 means the
      // bytes are gone, which is a state, not an error.
      const gone = /\b404$/.test(e instanceof Error ? e.message : "");
      if (gone && inTrace) blobGone = true;
      else if (gone) fileState = traceFileState({ status: "missing" });
      else failed = true;
    } finally {
      loading = false;
    }
  }

  function openMedia(): void {
    if (!url || !ctx?.onOpenMedia) return;
    const kind = display.kind === "image" ? "image" : display.kind === "pdf" ? "pdf" : "file";
    ctx.onOpenMedia({ url, name, kind, mime: display.mime ?? "", ...(note ? { note } : {}) });
  }

  onDestroy(() => { if (url) URL.revokeObjectURL(url); });
</script>

<div data-trace-kind="binary" data-binary-kind={display.kind} class="px-3 py-2">
  <button
    type="button"
    data-binary-chip
    onclick={open}
    disabled={(!available || blobGone) && !url}
    {title}
    data-file-state={fromPath ? (fileState?.state ?? "checking") : undefined}
    class="inline-flex max-w-full items-center gap-1.5 rounded-full border border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-700 px-2.5 py-1 text-[11px] text-black-900 dark:text-white-100 hover:bg-white-300 dark:hover:bg-navy-600 disabled:cursor-default disabled:opacity-80"
  >
    <span aria-hidden="true">{icon}</span>
    <span class="truncate font-mono {fileState?.state === 'missing' || blobGone ? 'line-through decoration-black-500' : ''}">{text}</span>
    {#if checking}<span class="opacity-60">memeriksa…</span>{/if}
    {#if display.too_large && !fromPath}<span class="text-amber-700 dark:text-amber-300">— too large to keep in trace</span>{/if}
    {#if loading}<span class="opacity-60">loading…</span>{/if}
  </button>
  {#if failed}<span class="ml-2 text-[11px] text-red-600 dark:text-red-400">failed to load</span>{/if}
  {#if url}
    <div class="mt-2 flex flex-col items-start gap-1.5" data-binary-view>
      {#if note}<p data-binary-note class="text-[11px] text-amber-700 dark:text-amber-300">{note}</p>{/if}
      {#if display.kind === "image"}
        <!-- <img> never runs script, so an SVG renders inert here. -->
        <button type="button" onclick={openMedia} class="block cursor-zoom-in">
          <img src={url} alt={name} class="max-h-80 max-w-full rounded border border-white-300 dark:border-navy-600" />
        </button>
      {:else if display.kind === "audio"}
        <audio controls src={url}></audio>
      {:else if display.kind === "video"}
        <video controls src={url} class="max-h-80 max-w-full"><track kind="captions" /></video>
      {:else if display.kind === "pdf" && ctx?.onOpenMedia}
        <button type="button" onclick={openMedia} class="text-[11px] text-link-400 underline">Open PDF</button>
      {/if}
      <a href={url} download={name} data-binary-download class="text-[11px] font-medium text-link-400 hover:underline">Download</a>
    </div>
  {/if}
</div>
