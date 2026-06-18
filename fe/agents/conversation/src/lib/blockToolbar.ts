/* Per-block hover toolbar ("···" → Copy / Download / Copy PNG) for rendered
   chat blocks. The rendered markdown is innerHTML (not Svelte components), so
   the toolbar is injected into the DOM. Call attachToolbar(block, spec) from a
   renderer once the block is rendered — e.g. renderMermaid after the SVG is
   swapped in. Idempotent via a data-toolbar marker on the block.

   Only wire this for blocks worth exporting (diagrams, math, artifacts). Plain
   code fences keep their own inline Copy button. */

export interface ToolbarSpec {
  /** raw text to copy / download */
  source: () => string;
  filename: string;
  mime: string;
  /** present only when the block can be exported as a raster image */
  svg?: () => SVGSVGElement | null;
}

function downloadBlob(blob: Blob, filename: string): void {
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

/* Rasterise an inline <svg> to a PNG and hand it to cb. */
function svgToPng(svg: SVGSVGElement, cb: (blob: Blob | null) => void): void {
  const xml = new XMLSerializer().serializeToString(svg);
  const rect = svg.getBoundingClientRect();
  const w = Math.max(1, Math.ceil(rect.width || svg.viewBox.baseVal.width || 800));
  const h = Math.max(1, Math.ceil(rect.height || svg.viewBox.baseVal.height || 600));
  const img = new Image();
  const blob = new Blob([xml], { type: "image/svg+xml;charset=utf-8" });
  const url = URL.createObjectURL(blob);
  img.onload = () => {
    const scale = 2;
    const canvas = document.createElement("canvas");
    canvas.width = w * scale;
    canvas.height = h * scale;
    const ctx = canvas.getContext("2d");
    if (!ctx) { URL.revokeObjectURL(url); cb(null); return; }
    ctx.scale(scale, scale);
    ctx.drawImage(img, 0, 0, w, h);
    URL.revokeObjectURL(url);
    canvas.toBlob(cb, "image/png");
  };
  img.onerror = () => { URL.revokeObjectURL(url); cb(null); };
  img.src = url;
}

function downloadPng(svg: SVGSVGElement, filename: string): void {
  svgToPng(svg, (blob) => {
    if (blob) downloadBlob(blob, filename);
  });
}

/* Closes every open block menu except `keep`. Menus carry .wick-block-menu
   and live on <body>. */
function closeAllMenus(keep?: HTMLElement): void {
  document.querySelectorAll<HTMLElement>(".wick-block-menu:not(.hidden)").forEach((m) => {
    if (m !== keep) m.classList.add("hidden");
  });
}

let outsideClickBound = false;
function bindOutsideClick(): void {
  if (outsideClickBound) return;
  outsideClickBound = true;
  document.addEventListener("click", () => closeAllMenus());
  window.addEventListener("scroll", () => closeAllMenus(), true);
}

/* Builds the floating "···" button; its dropdown is appended to <body> so no
   block wrapper's overflow-hidden can clip it. */
function buildToolbar(spec: ToolbarSpec): HTMLElement {
  /* Visibility is driven by `[data-toolbar]:hover > .wick-block-toolbar` in
     richRender.css — scoped to the hovered block only. Tailwind group-hover is
     intentionally NOT used: the chat bubble is itself a `.group`, so it would
     reveal every block's toolbar at once. */
  const wrap = document.createElement("div");
  wrap.className =
    "wick-block-toolbar absolute top-1.5 right-1.5 z-10 opacity-0 transition-opacity";

  const trigger = document.createElement("button");
  trigger.type = "button";
  trigger.setAttribute("aria-label", "Block actions");
  trigger.className =
    "inline-flex items-center justify-center h-6 w-6 rounded-md bg-white-100/90 dark:bg-navy-700/90 text-black-600 dark:text-black-500 hover:text-black-900 dark:hover:text-white-100 shadow-sm backdrop-blur";
  trigger.innerHTML = `<svg viewBox="0 0 16 16" class="h-3.5 w-3.5" fill="currentColor"><circle cx="3" cy="8" r="1.4"/><circle cx="8" cy="8" r="1.4"/><circle cx="13" cy="8" r="1.4"/></svg>`;

  const menu = document.createElement("div");
  menu.className =
    "wick-block-menu hidden fixed z-50 min-w-[160px] rounded-lg border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 shadow-lg py-1 text-xs";

  function item(label: string, onClick: () => void): void {
    const b = document.createElement("button");
    b.type = "button";
    b.className =
      "flex w-full items-center gap-2 px-3 py-1.5 text-left text-black-700 dark:text-black-500 hover:bg-white-200 dark:hover:bg-navy-700 hover:text-black-900 dark:hover:text-white-100 transition-colors";
    b.textContent = label;
    b.addEventListener("click", (e) => {
      e.stopPropagation();
      onClick();
      menu.classList.add("hidden");
    });
    menu.appendChild(b);
  }

  item("Copy to clipboard", () => void navigator.clipboard?.writeText(spec.source()));
  item("Download file", () =>
    downloadBlob(new Blob([spec.source()], { type: spec.mime }), spec.filename),
  );
  if (spec.svg) {
    const pngName = spec.filename.replace(/\.[^.]+$/, "") + ".png";
    item("Download as PNG", () => {
      const el = spec.svg!();
      if (el) downloadPng(el, pngName);
    });
  }

  trigger.addEventListener("click", (e) => {
    e.stopPropagation();
    closeAllMenus(menu);
    const willOpen = menu.classList.contains("hidden");
    if (willOpen) {
      const r = trigger.getBoundingClientRect();
      menu.style.top = `${Math.round(r.bottom + 4)}px`;
      menu.style.left = "";
      menu.style.right = `${Math.round(window.innerWidth - r.right)}px`;
    }
    menu.classList.toggle("hidden");
  });

  document.body.appendChild(menu);
  wrap.append(trigger);
  return wrap;
}

/* Attaches the hover toolbar to a single rendered block. No-op if already
   attached (data-toolbar marker). */
export function attachToolbar(block: HTMLElement, spec: ToolbarSpec): void {
  if (block.hasAttribute("data-toolbar")) return;
  bindOutsideClick();
  block.setAttribute("data-toolbar", "");
  block.classList.add("relative");
  block.appendChild(buildToolbar(spec));
}
