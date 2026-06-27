<script lang="ts">
  import { renderMarkdown } from "../markdown.js";
  import { buildArtifactSrcdoc } from "../richRender.js";

  type Item = {
    url: string;
    name: string;
    kind: "image" | "pdf" | "html" | "markdown" | "text" | "file";
    /* For an image card: the page the image was found on. Drives the
       favicon + domain caption at the bottom of the viewer. */
    sourceUrl?: string;
  };
  /* A gallery: `items` is the full set the viewer can page through and `index`
     is where it opens. A single attachment / artifact is just a one-element
     gallery (`items=[it]`, `index=0`), so every call site shares one viewer. */
  type Props = { items: Item[] | null; index?: number; onClose: () => void };
  let { items, index = 0, onClose }: Props = $props();

  /* live position within the gallery — seeded from `index` whenever a new
     gallery opens (the $effect below), then driven by prev/next + arrows. */
  let cur = $state(0);
  const list = $derived(items ?? []);
  const item: Item | null = $derived(list.length ? (list[cur] ?? list[0]) : null);
  const many = $derived(list.length > 1);

  function go(delta: number) {
    if (!list.length) return;
    cur = (cur + delta + list.length) % list.length;
  }

  /* Domain shown in the bottom caption for an image card. Falls back to the
     image url's host when no explicit sourceUrl was supplied. */
  function hostOf(it: Item | null): string {
    if (!it) return "";
    try {
      return new URL(it.sourceUrl || it.url).hostname.replace(/^www\./, "");
    } catch {
      return "";
    }
  }
  const sourceHost = $derived(hostOf(item));
  const sourceHref = $derived(item?.sourceUrl || item?.url || "");

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

  /* Re-seed the position from `index` whenever a NEW gallery opens. Keyed on
     the gallery identity (items reference) so paging within the same gallery
     doesn't reset; opening a different gallery jumps to its clicked index. */
  let openedFor: Item[] | null = null;
  $effect(() => {
    if (items && items !== openedFor) {
      openedFor = items;
      cur = Math.min(Math.max(index, 0), items.length - 1);
    }
    if (!items) openedFor = null;
  });

  /* Reset zoom + (re)load any doc each time the visible item changes. */
  $effect(() => {
    if (!item) return;
    reset();
    if (item.kind === "html" || item.kind === "markdown" || item.kind === "text") {
      loadDoc(item);
    }
  });

  /* Keyboard: Esc closes, +/-/0 zoom (image only), arrows page the gallery. */
  $effect(() => {
    if (!item) return;
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") {
        e.preventDefault();
        onClose();
      } else if (e.key === "ArrowRight" && many) {
        e.preventDefault();
        go(1);
      } else if (e.key === "ArrowLeft" && many) {
        e.preventDefault();
        go(-1);
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
        {#if many}
          <span data-lightbox-counter class="px-2 text-xs tabular-nums text-white-100/70">{cur + 1} / {list.length}</span>
        {/if}
        {#if !isDoc}
          <button type="button" aria-label="Zoom out" onclick={() => zoomBy(1 / 1.2)} class="rounded-lg px-2.5 py-1.5 hover:bg-white-100/10 transition-colors">−</button>
          <button type="button" aria-label="Reset zoom" onclick={reset} class="rounded-lg px-2 py-1.5 text-xs tabular-nums hover:bg-white-100/10 transition-colors">{Math.round(scale * 100)}%</button>
          <button type="button" aria-label="Zoom in" onclick={() => zoomBy(1.2)} class="rounded-lg px-2.5 py-1.5 hover:bg-white-100/10 transition-colors">＋</button>
        {/if}
        <a href={item.url} target="_blank" rel="noopener" aria-label="Open in new tab" class="rounded-lg px-2.5 py-1.5 text-xs hover:bg-white-100/10 transition-colors">Open ↗</a>
        <button type="button" aria-label="Close preview" onclick={onClose} class="inline-flex h-8 w-8 items-center justify-center rounded-lg hover:bg-white-100/10 transition-colors">✕</button>
      </div>
    </div>
    <!-- clicking the empty area around the media closes the viewer (like a
         standard image preview); clicks on the image / nav buttons / iframe
         hit their own elements, so only bare-backdrop clicks reach here. -->
    <div
      class="relative flex-1 overflow-hidden flex items-center justify-center p-4"
      role="presentation"
      onclick={(e) => { if (e.target === e.currentTarget) onClose(); }}
    >
      {#if many}
        <!-- prev/next: large, edge-anchored, semi-transparent (mirrors the
             Claude.ai gallery). Hidden for a single-image gallery. -->
        <button
          type="button"
          aria-label="Previous image"
          onclick={() => go(-1)}
          class="absolute left-2 top-1/2 -translate-y-1/2 z-10 inline-flex h-11 w-11 items-center justify-center rounded-full bg-black/40 text-white-100 hover:bg-black/60 transition-colors"
        >
          <svg viewBox="0 0 20 20" class="h-5 w-5" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 4l-6 6 6 6" /></svg>
        </button>
        <button
          type="button"
          aria-label="Next image"
          onclick={() => go(1)}
          class="absolute right-2 top-1/2 -translate-y-1/2 z-10 inline-flex h-11 w-11 items-center justify-center rounded-full bg-black/40 text-white-100 hover:bg-black/60 transition-colors"
        >
          <svg viewBox="0 0 20 20" class="h-5 w-5" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M8 4l6 6-6 6" /></svg>
        </button>
      {/if}

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

    {#if sourceHost}
      <!-- source caption: favicon + domain of the page the image came from,
           a click-through to that page. Mirrors the Claude.ai image viewer. -->
      <div class="flex items-center justify-center px-4 py-3">
        <a
          data-lightbox-source
          href={sourceHref}
          target="_blank"
          rel="noopener"
          class="inline-flex items-center gap-2 rounded-full bg-white-100/10 px-3 py-1.5 text-xs text-white-100/90 hover:bg-white-100/20 transition-colors max-w-full"
        >
          <img
            src={`https://${sourceHost}/favicon.ico`}
            onerror={(e) => { (e.currentTarget as HTMLImageElement).src = `https://www.google.com/s2/favicons?domain=${sourceHost}&sz=32`; }}
            alt=""
            class="h-4 w-4 rounded-sm shrink-0 bg-white-100/20"
          />
          <span class="truncate">{sourceHost}</span>
        </a>
      </div>
    {/if}
  </div>
{/if}
