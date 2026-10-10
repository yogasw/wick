<script lang="ts">
  /* Settings › Avatar and the create wizard, style Image (mockup
     avatar-settings-image): Upload image + Reset, then Position — Fit /
     Fill, drag to move, pinch / wheel / slider to zoom. The crop is only
     numbers (x, y in % of the frame, zoom); the server clamps them again.
     The picture is shown through an <img> whose src passed safeImageSrc,
     never through markup. */
  import { clampOffset, clampZoom, effectiveZoom, imageTransform, safeImageSrc, IMAGE_ZOOM_MAX, IMAGE_ZOOM_MIN } from "@wick-fe/common-avatar";

  type ImageCrop = { fit: string; x: number; y: number; zoom: number };
  type Props = {
    /** The picture to place: the stored URL or a picked file's blob: URL; "" = none yet. */
    src: string;
    aspect: number;
    crop: ImageCrop;
    busy?: boolean;
    error?: string;
    labelClass?: string;
    onFile: (f: File) => void;
    onReset: () => void;
    onCrop: (c: ImageCrop) => void;
  };
  let { src, aspect, crop, busy = false, error = "", labelClass = "", onFile, onReset, onCrop }: Props = $props();

  const safe = $derived(safeImageSrc(src));
  const view = $derived({ ...crop, aspect });
  const zoom = $derived(effectiveZoom(view));
  const sm =
    "rounded-lg border border-white-300 px-2.5 py-1 text-xs text-black-900 hover:bg-white-200 disabled:opacity-50 dark:border-navy-600 dark:text-white-100 dark:hover:bg-navy-600";

  let frame: HTMLDivElement | undefined = $state();
  const ptr = new Map<number, [number, number]>();
  let pinch = 0;

  function set(c: Partial<ImageCrop>) {
    onCrop({ ...crop, ...c });
  }
  function onPointerDown(e: PointerEvent) {
    if (!safe) return;
    frame?.setPointerCapture?.(e.pointerId);
    ptr.set(e.pointerId, [e.clientX, e.clientY]);
    pinch = 0;
  }
  function onPointerMove(e: PointerEvent) {
    const p = ptr.get(e.pointerId);
    if (!p || !frame) return;
    const w = frame.getBoundingClientRect().width || 1;
    if (ptr.size === 2) {
      const other = [...ptr.entries()].find(([k]) => k !== e.pointerId)?.[1];
      if (other) {
        const d = Math.hypot(e.clientX - other[0], e.clientY - other[1]);
        if (pinch) set({ zoom: clampZoom((zoom * d) / pinch) });
        pinch = d;
      }
    } else {
      set({ x: clampOffset(crop.x + ((e.clientX - p[0]) / w) * 100), y: clampOffset(crop.y + ((e.clientY - p[1]) / w) * 100) });
    }
    ptr.set(e.pointerId, [e.clientX, e.clientY]);
  }
  function onPointerUp(e: PointerEvent) {
    ptr.delete(e.pointerId);
    pinch = 0;
  }
  function onWheel(e: WheelEvent) {
    if (!safe) return;
    e.preventDefault();
    set({ zoom: clampZoom(zoom * (e.deltaY < 0 ? 1.08 : 0.93)) });
  }
  function onKey(e: KeyboardEvent) {
    const step = e.shiftKey ? 10 : 2;
    const d = { ArrowLeft: [-step, 0], ArrowRight: [step, 0], ArrowUp: [0, -step], ArrowDown: [0, step] }[e.key];
    if (!d || !safe) return;
    e.preventDefault();
    set({ x: clampOffset(crop.x + d[0]), y: clampOffset(crop.y + d[1]) });
  }
  function pick(e: Event) {
    const input = e.currentTarget as HTMLInputElement;
    const f = input.files?.[0];
    input.value = "";
    if (f) onFile(f);
  }
</script>

<div class="space-y-3" data-testid="avatar-image-editor">
  <div>
    <div class="flex items-center justify-between gap-2">
      <span class={labelClass}>Image</span>
      <span class="flex gap-1.5">
        <label class="{sm} cursor-pointer {busy ? 'pointer-events-none opacity-50' : ''}">
          Upload image
          <input type="file" class="sr-only" accept="image/png,image/jpeg,image/webp" disabled={busy} data-testid="avatar-image-file" onchange={pick} />
        </label>
        <button type="button" class={sm} disabled={busy || !src} data-testid="avatar-image-reset" onclick={onReset}>Reset</button>
      </span>
    </div>
    <p class="mt-1 text-xs {error ? 'text-red-600 dark:text-red-400' : 'text-black-800 dark:text-black-600'}" data-testid="avatar-image-msg" aria-live="polite">
      {#if busy}Uploading image…{:else if error}{error}{:else}PNG, JPEG or WebP up to 2 MB. Poses are animated from outside, so the image itself never changes.{/if}
    </p>
  </div>
  <div>
    <div class="flex items-center justify-between gap-2">
      <span class={labelClass}>Position</span>
      <span class="flex gap-1.5">
        <button type="button" class={sm} disabled={!safe} aria-pressed={!crop.zoom && crop.fit === "fit"} data-testid="avatar-image-fit" onclick={() => onCrop({ fit: "fit", x: 0, y: 0, zoom: 0 })}>Fit</button>
        <button type="button" class={sm} disabled={!safe} aria-pressed={!crop.zoom && crop.fit !== "fit"} data-testid="avatar-image-fill" onclick={() => onCrop({ fit: "fill", x: 0, y: 0, zoom: 0 })}>Fill</button>
      </span>
    </div>
    <p class="mt-1 text-xs text-black-800 dark:text-black-600">Drag to move, pinch or use the slider to zoom. The dimmed part is cropped out of the avatar.</p>
    <div class="mt-2 flex justify-center rounded-lg border border-white-300 bg-white-200 p-6 dark:border-navy-600 dark:bg-navy-700">
      <!-- svelte-ignore a11y_no_noninteractive_tabindex, a11y_no_noninteractive_element_interactions -->
      <div
        bind:this={frame}
        class="relative h-40 w-40 touch-none select-none {safe ? 'cursor-grab' : ''}"
        role="group"
        aria-label="Image position (arrow keys move)"
        tabindex={safe ? 0 : -1}
        data-testid="avatar-image-frame"
        onpointerdown={onPointerDown}
        onpointermove={onPointerMove}
        onpointerup={onPointerUp}
        onpointercancel={onPointerUp}
        onwheel={onWheel}
        onkeydown={onKey}
      >
        {#if safe}
          <img src={safe} alt="" draggable="false" class="pointer-events-none absolute inset-0 h-full w-full object-contain opacity-30" style:transform={imageTransform(view)} />
          <div class="absolute inset-0 overflow-hidden rounded-full ring-2 ring-green-500">
            <img src={safe} alt="" draggable="false" class="pointer-events-none h-full w-full object-contain" style:transform={imageTransform(view)} />
          </div>
        {:else}
          <div class="absolute inset-0 flex items-center justify-center rounded-full border-2 border-dashed border-white-300 text-center text-xs text-black-800 dark:border-navy-600 dark:text-black-600">No image yet</div>
        {/if}
      </div>
    </div>
    <div class="mt-2 flex items-center gap-2">
      <input
        type="range"
        class="flex-1 accent-green-500"
        min={IMAGE_ZOOM_MIN}
        max={IMAGE_ZOOM_MAX}
        step="0.01"
        aria-label="Zoom"
        disabled={!safe}
        value={zoom}
        data-testid="avatar-image-zoom"
        oninput={(e) => set({ zoom: clampZoom(Number(e.currentTarget.value)) })}
      />
      <output class="w-12 text-right text-xs tabular-nums text-black-800 dark:text-black-600">{zoom.toFixed(2)}×</output>
    </div>
  </div>
</div>
