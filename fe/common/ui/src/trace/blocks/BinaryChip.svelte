<script lang="ts">
  import { onDestroy } from "svelte";
  import { humanBytes } from "../format.js";
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
  const text = $derived([name, label, size].filter(Boolean).join(" · "));
  const available = $derived(!display.too_large && (!!display.data || (!!display.blob_ref && !!ctx?.loadBlob)));

  let url = $state<string | null>(null);
  let loading = $state(false);
  let failed = $state(false);

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
      const blob = display.data ? decode(display.data) : await ctx!.loadBlob!(display.blob_ref!);
      // The blob endpoint names its type by magic; keep the display's mime
      // when it has one so an SVG stays an SVG.
      const typed = display.mime && blob.type !== display.mime ? new Blob([blob], { type: display.mime }) : blob;
      url = URL.createObjectURL(typed);
    } catch {
      failed = true;
    } finally {
      loading = false;
    }
  }

  function openMedia(): void {
    if (!url || !ctx?.onOpenMedia) return;
    const kind = display.kind === "image" ? "image" : display.kind === "pdf" ? "pdf" : "file";
    ctx.onOpenMedia({ url, name, kind, mime: display.mime ?? "" });
  }

  onDestroy(() => { if (url) URL.revokeObjectURL(url); });
</script>

<div data-trace-kind="binary" data-binary-kind={display.kind} class="px-3 py-2">
  <button
    type="button"
    data-binary-chip
    onclick={open}
    disabled={!available && !url}
    title={display.too_large ? "Too large to keep in the trace" : available ? "Click to load" : "Not stored in the trace"}
    class="inline-flex max-w-full items-center gap-1.5 rounded-full border border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-700 px-2.5 py-1 text-[11px] text-black-900 dark:text-white-100 hover:bg-white-300 dark:hover:bg-navy-600 disabled:cursor-default disabled:opacity-80"
  >
    <span aria-hidden="true">{icon}</span>
    <span class="truncate font-mono">{text}</span>
    {#if display.too_large}<span class="text-amber-700 dark:text-amber-300">— too large to keep in trace</span>{/if}
    {#if loading}<span class="opacity-60">loading…</span>{/if}
  </button>
  {#if failed}<span class="ml-2 text-[11px] text-red-600 dark:text-red-400">failed to load</span>{/if}
  {#if url}
    <div class="mt-2 flex flex-col items-start gap-1.5" data-binary-view>
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
