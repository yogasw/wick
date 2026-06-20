<script lang="ts">
  import { renderMarkdown } from "../markdown.js";
  import { buildArtifactSrcdoc } from "../richRender.js";

  type Item = { url: string; name: string; kind: "image" | "pdf" | "html" | "markdown" | "text" | "file" };
  type Props = { item: Item | null; onClose: () => void };
  let { item, onClose }: Props = $props();

  /* Document kinds (html/markdown/text) render their fetched content full
     screen instead of the zoom/pan image view. Content loads lazily per item. */
  const isDoc = $derived(!!item && (item.kind === "html" || item.kind === "markdown" || item.kind === "text"));
  let docState = $state<{ loading: boolean; html?: string; md?: string; text?: string; error?: string }>({ loading: false });

  async function loadDoc(it: Item) {
    docState = { loading: true };
    try {
      const res = await fetch(it.url);
      const raw = await res.text();
      if (it.kind === "html") docState = { loading: false, html: buildArtifactSrcdoc(raw) };
      else if (it.kind === "markdown") docState = { loading: false, md: renderMarkdown(raw) };
      else docState = { loading: false, text: raw };
    } catch (e) {
      docState = { loading: false, error: e instanceof Error ? e.message : String(e) };
    }
  }

  const MIN = 0.25;
  const MAX = 8;
  let scale = $state(1);
  let tx = $state(0);
  let ty = $state(0);
  let dragging = false;
  let lastX = 0;
  let lastY = 0;

  function reset() {
    scale = 1;
    tx = 0;
    ty = 0;
  }

  function clamp(v: number): number {
    return Math.min(MAX, Math.max(MIN, v));
  }

  function zoomBy(factor: number) {
    scale = clamp(scale * factor);
    if (scale === 1) {
      tx = 0;
      ty = 0;
    }
  }

  function onWheel(e: WheelEvent) {
    e.preventDefault();
    zoomBy(e.deltaY < 0 ? 1.15 : 1 / 1.15);
  }

  function onPointerDown(e: PointerEvent) {
    if (scale <= 1) return;
    dragging = true;
    lastX = e.clientX;
    lastY = e.clientY;
    (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
  }

  function onPointerMove(e: PointerEvent) {
    if (!dragging) return;
    tx += e.clientX - lastX;
    ty += e.clientY - lastY;
    lastX = e.clientX;
    lastY = e.clientY;
  }

  function onPointerUp() {
    dragging = false;
  }

  $effect(() => {
    if (!item) return;
    reset();
    if (item.kind === "html" || item.kind === "markdown" || item.kind === "text") {
      loadDoc(item);
    }
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") {
        e.preventDefault();
        onClose();
      } else if (e.key === "+" || e.key === "=") {
        zoomBy(1.2);
      } else if (e.key === "-") {
        zoomBy(1 / 1.2);
      } else if (e.key === "0") {
        reset();
      }
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  });
</script>

{#if item !== null}
  <div
    data-lightbox-modal
    class="fixed inset-0 z-50 flex flex-col bg-black/85 backdrop-blur-sm"
    role="presentation"
    onclick={(e) => { if (e.target === e.currentTarget) onClose(); }}
  >
    <div class="flex items-center justify-between gap-4 px-4 py-2 text-white-100">
      <span class="text-sm truncate">{item.name}</span>
      <div class="flex items-center gap-1.5 shrink-0">
        {#if !isDoc}
          <button type="button" aria-label="Zoom out" onclick={() => zoomBy(1 / 1.2)} class="rounded-lg px-2.5 py-1.5 hover:bg-white-100/10 transition-colors">−</button>
          <button type="button" aria-label="Reset zoom" onclick={reset} class="rounded-lg px-2 py-1.5 text-xs tabular-nums hover:bg-white-100/10 transition-colors">{Math.round(scale * 100)}%</button>
          <button type="button" aria-label="Zoom in" onclick={() => zoomBy(1.2)} class="rounded-lg px-2.5 py-1.5 hover:bg-white-100/10 transition-colors">＋</button>
        {/if}
        <a href={item.url} target="_blank" rel="noopener" aria-label="Open in new tab" class="rounded-lg px-2.5 py-1.5 text-xs hover:bg-white-100/10 transition-colors">Open ↗</a>
        <button type="button" aria-label="Close preview" onclick={onClose} class="inline-flex h-8 w-8 items-center justify-center rounded-lg hover:bg-white-100/10 transition-colors">✕</button>
      </div>
    </div>
    <div class="flex-1 overflow-hidden flex items-center justify-center p-4">
      {#if item.kind === "pdf"}
        <iframe src={item.url} title={item.name} class="w-full h-full bg-white-100 rounded-lg" style="border:0"></iframe>
      {:else if isDoc}
        {#if docState.loading}
          <div class="text-sm text-white-100/70">loading…</div>
        {:else if docState.error}
          <div class="text-sm text-red-300">{docState.error}</div>
        {:else if item.kind === "html"}
          <iframe srcdoc={docState.html} sandbox="allow-scripts" referrerpolicy="no-referrer" title={item.name} class="w-full h-full bg-white-100 rounded-lg" style="border:0"></iframe>
        {:else}
          <!-- markdown / text: scrollable document panel, full height, chat-readable width -->
          <div data-lightbox-doc class="h-full w-full max-w-3xl overflow-auto rounded-lg bg-white-100 dark:bg-navy-800 p-6">
            {#if item.kind === "markdown"}
              <div class="text-sm leading-relaxed break-words text-black-900 dark:text-white-100">{@html docState.md}</div>
            {:else}
              <pre class="whitespace-pre-wrap break-words font-mono text-xs leading-relaxed text-black-900 dark:text-white-100">{docState.text}</pre>
            {/if}
          </div>
        {/if}
      {:else}
        <img
          data-lightbox-media
          src={item.url}
          alt={item.name}
          draggable="false"
          onwheel={onWheel}
          onpointerdown={onPointerDown}
          onpointermove={onPointerMove}
          onpointerup={onPointerUp}
          class="max-w-full max-h-full object-contain select-none"
          style="transform: translate({tx}px, {ty}px) scale({scale}); cursor: {scale > 1 ? 'grab' : 'default'};"
        />
      {/if}
    </div>
  </div>
{/if}
