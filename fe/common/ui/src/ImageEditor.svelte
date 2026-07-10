<script lang="ts">
  /* A small canvas image annotator — crop, arrow/rect/ellipse, freehand pen, and
     blur/redact — used to mark up a screenshot or an attached image before it's
     sent. Works at the image's natural resolution (the canvas is displayed
     scaled) so exports stay crisp. Returns a PNG File via onDone. */
  type Tool = "arrow" | "rect" | "ellipse" | "pen" | "blur" | "crop";
  type Pt = { x: number; y: number };
  type Shape =
    | { type: "arrow" | "rect" | "ellipse"; color: string; width: number; a: Pt; b: Pt }
    | { type: "pen"; color: string; width: number; points: Pt[] }
    | { type: "blur"; a: Pt; b: Pt };

  type Props = {
    open: boolean;
    /** Image data URL (or any loadable src) to edit. */
    src: string;
    name?: string;
    onDone: (file: File) => void;
    onCancel: () => void;
  };
  let { open, src, name = "image.png", onDone, onCancel }: Props = $props();

  const COLORS = ["#ef4444", "#f59e0b", "#22c55e", "#3b82f6", "#ffffff", "#111827"];

  let canvasEl = $state<HTMLCanvasElement>();
  let tool = $state<Tool>("arrow");
  let color = $state("#ef4444");
  let strokeWidth = $state(4);

  // Base image + committed shapes. `history` snapshots {baseSrc, shapes} so undo
  // can step back across shape adds AND crops (a crop re-bases the image).
  let baseSrc = $state("");
  let baseImg: HTMLImageElement | null = null;
  let shapes = $state<Shape[]>([]);
  let history: { baseSrc: string; shapes: Shape[] }[] = [];

  let drawing = false;
  let draft: Shape | null = null;

  // (Re)load the base image whenever it changes, then paint.
  $effect(() => {
    if (!open || !baseSrc) return;
    const img = new Image();
    img.onload = () => { baseImg = img; sizeCanvas(); redraw(); };
    img.src = baseSrc;
  });

  // Seed base from the incoming src each time the editor opens.
  $effect(() => {
    if (open) { baseSrc = src; shapes = []; history = []; draft = null; }
  });

  function sizeCanvas() {
    if (!canvasEl || !baseImg) return;
    canvasEl.width = baseImg.naturalWidth;
    canvasEl.height = baseImg.naturalHeight;
  }

  function drawShape(ctx: CanvasRenderingContext2D, s: Shape) {
    if (s.type === "blur") {
      const x = Math.min(s.a.x, s.b.x), y = Math.min(s.a.y, s.b.y);
      const w = Math.abs(s.b.x - s.a.x), h = Math.abs(s.b.y - s.a.y);
      if (w < 2 || h < 2) return;
      // Pixelate: sample the region already painted, downscale then upscale.
      const tmp = document.createElement("canvas");
      const f = 0.07;
      tmp.width = Math.max(1, Math.round(w * f));
      tmp.height = Math.max(1, Math.round(h * f));
      const tctx = tmp.getContext("2d")!;
      tctx.drawImage(ctx.canvas, x, y, w, h, 0, 0, tmp.width, tmp.height);
      ctx.imageSmoothingEnabled = false;
      ctx.drawImage(tmp, 0, 0, tmp.width, tmp.height, x, y, w, h);
      ctx.imageSmoothingEnabled = true;
      return;
    }
    ctx.strokeStyle = s.color;
    ctx.fillStyle = s.color;
    ctx.lineWidth = s.width;
    ctx.lineCap = "round";
    ctx.lineJoin = "round";
    if (s.type === "pen") {
      ctx.beginPath();
      s.points.forEach((p, i) => (i ? ctx.lineTo(p.x, p.y) : ctx.moveTo(p.x, p.y)));
      ctx.stroke();
      return;
    }
    if (s.type === "rect") {
      ctx.strokeRect(Math.min(s.a.x, s.b.x), Math.min(s.a.y, s.b.y), Math.abs(s.b.x - s.a.x), Math.abs(s.b.y - s.a.y));
      return;
    }
    if (s.type === "ellipse") {
      ctx.beginPath();
      ctx.ellipse((s.a.x + s.b.x) / 2, (s.a.y + s.b.y) / 2, Math.abs(s.b.x - s.a.x) / 2, Math.abs(s.b.y - s.a.y) / 2, 0, 0, Math.PI * 2);
      ctx.stroke();
      return;
    }
    // arrow
    ctx.beginPath();
    ctx.moveTo(s.a.x, s.a.y);
    ctx.lineTo(s.b.x, s.b.y);
    ctx.stroke();
    const ang = Math.atan2(s.b.y - s.a.y, s.b.x - s.a.x);
    const head = Math.max(10, s.width * 3);
    ctx.beginPath();
    ctx.moveTo(s.b.x, s.b.y);
    ctx.lineTo(s.b.x - head * Math.cos(ang - Math.PI / 6), s.b.y - head * Math.sin(ang - Math.PI / 6));
    ctx.lineTo(s.b.x - head * Math.cos(ang + Math.PI / 6), s.b.y - head * Math.sin(ang + Math.PI / 6));
    ctx.closePath();
    ctx.fill();
  }

  function redraw() {
    if (!canvasEl || !baseImg) return;
    const ctx = canvasEl.getContext("2d")!;
    ctx.clearRect(0, 0, canvasEl.width, canvasEl.height);
    ctx.drawImage(baseImg, 0, 0);
    for (const s of shapes) drawShape(ctx, s);
    if (draft) drawShape(ctx, draft);
  }

  function toCanvasPt(e: PointerEvent): Pt {
    const r = canvasEl!.getBoundingClientRect();
    return {
      x: ((e.clientX - r.left) / r.width) * canvasEl!.width,
      y: ((e.clientY - r.top) / r.height) * canvasEl!.height,
    };
  }

  function snapshot() {
    history.push({ baseSrc, shapes: [...shapes] });
  }

  function onPointerDown(e: PointerEvent) {
    if (!canvasEl) return;
    canvasEl.setPointerCapture(e.pointerId);
    drawing = true;
    const p = toCanvasPt(e);
    if (tool === "pen") draft = { type: "pen", color, width: strokeWidth, points: [p] };
    else if (tool === "blur") draft = { type: "blur", a: p, b: p };
    else if (tool === "crop") draft = { type: "rect", color: "#38bdf8", width: 2, a: p, b: p };
    else draft = { type: tool, color, width: strokeWidth, a: p, b: p };
    redraw();
  }
  function onPointerMove(e: PointerEvent) {
    if (!drawing || !draft) return;
    const p = toCanvasPt(e);
    if (draft.type === "pen") draft.points.push(p);
    else draft.b = p;
    redraw();
  }
  function onPointerUp() {
    if (!drawing || !draft) return;
    drawing = false;
    const d = draft;
    draft = null;
    if (tool === "crop") {
      if (d.type !== "pen") applyCrop(d.a, d.b);
    } else {
      const tooSmall = d.type !== "pen" && Math.abs(d.b.x - d.a.x) < 3 && Math.abs(d.b.y - d.a.y) < 3;
      if (!tooSmall) { snapshot(); shapes = [...shapes, d]; }
    }
    redraw();
  }

  function applyCrop(a: Pt, b: Pt) {
    if (!canvasEl) return;
    const x = Math.min(a.x, b.x), y = Math.min(a.y, b.y);
    const w = Math.abs(b.x - a.x), h = Math.abs(b.y - a.y);
    if (w < 8 || h < 8) return;
    // Flatten current composite (base + shapes) then keep only the crop region.
    redraw();
    const out = document.createElement("canvas");
    out.width = Math.round(w);
    out.height = Math.round(h);
    out.getContext("2d")!.drawImage(canvasEl, x, y, w, h, 0, 0, out.width, out.height);
    snapshot();
    shapes = [];
    baseSrc = out.toDataURL("image/png"); // triggers reload effect → redraw
  }

  function undo() {
    const prev = history.pop();
    if (!prev) return;
    shapes = prev.shapes;
    if (prev.baseSrc !== baseSrc) baseSrc = prev.baseSrc;
    else redraw();
  }

  function done() {
    if (!canvasEl) return;
    redraw();
    canvasEl.toBlob((blob) => {
      if (!blob) return;
      onDone(new File([blob], name, { type: "image/png" }));
    }, "image/png");
  }

  const TOOLS: { id: Tool; label: string }[] = [
    { id: "arrow", label: "Arrow" },
    { id: "rect", label: "Rectangle" },
    { id: "ellipse", label: "Circle" },
    { id: "pen", label: "Pen" },
    { id: "blur", label: "Blur" },
    { id: "crop", label: "Crop" },
  ];

  $effect(() => {
    if (!open) return;
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") onCancel();
      else if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "z") { e.preventDefault(); undo(); }
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  });
</script>

{#if open}
  <div class="fixed inset-0 z-50 flex flex-col bg-black/80 backdrop-blur-sm" role="presentation" onclick={(e) => { if (e.target === e.currentTarget) onCancel(); }}>
    <!-- toolbar -->
    <div class="flex flex-wrap items-center gap-2 px-4 py-2 text-white-100">
      <div class="flex items-center gap-1 rounded-lg bg-navy-800/80 p-1">
        {#each TOOLS as t (t.id)}
          <button
            type="button"
            title={t.label}
            aria-label={t.label}
            aria-pressed={tool === t.id}
            onclick={() => (tool = t.id)}
            class="rounded-md px-2.5 py-1 text-xs font-medium transition-colors {tool === t.id ? 'bg-green-500 text-white-100' : 'text-white-200 hover:bg-navy-700'}"
          >{t.label}</button>
        {/each}
      </div>

      <div class="flex items-center gap-1">
        {#each COLORS as c (c)}
          <button
            type="button"
            aria-label={`Color ${c}`}
            onclick={() => (color = c)}
            class="h-6 w-6 rounded-full border-2 transition-transform {color === c ? 'border-white-100 scale-110' : 'border-white-100/30'}"
            style={`background:${c}`}
          ></button>
        {/each}
      </div>

      <label class="flex items-center gap-1.5 text-xs">
        <span>Size</span>
        <input type="range" min="1" max="24" bind:value={strokeWidth} class="w-24" />
      </label>

      <button type="button" onclick={undo} disabled={history.length === 0} class="rounded-lg bg-navy-800/80 px-3 py-1.5 text-xs hover:bg-navy-700 disabled:opacity-40">Undo</button>

      <div class="ml-auto flex items-center gap-2">
        <button type="button" onclick={onCancel} class="rounded-lg px-3 py-1.5 text-xs hover:bg-navy-700">Cancel</button>
        <button type="button" onclick={done} class="rounded-lg bg-green-500 px-4 py-1.5 text-xs font-medium hover:bg-green-600">Done</button>
      </div>
    </div>

    <!-- canvas -->
    <div class="flex-1 min-h-0 overflow-auto p-4 flex items-center justify-center">
      <canvas
        bind:this={canvasEl}
        onpointerdown={onPointerDown}
        onpointermove={onPointerMove}
        onpointerup={onPointerUp}
        class="max-w-full max-h-full rounded shadow-2xl {tool === 'crop' ? 'cursor-crosshair' : 'cursor-crosshair'}"
        style="touch-action:none; background:#fff"
      ></canvas>
    </div>
  </div>
{/if}
