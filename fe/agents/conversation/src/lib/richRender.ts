/* Enriches the static markdown HTML rendered into a chat bubble: turns the
   common-md placeholders into rendered Mermaid diagrams, syntax-highlighted
   code, and KaTeX math. Each library is lazy-loaded on first use, the work is
   idempotent (a `data-enriched` marker), and it is debounced so a streaming
   message that re-renders on every token does not thrash the renderers. */
import "katex/dist/katex.min.css";
import "./richRender.css";
import { attachToolbar } from "./blockToolbar.js";
import { renderMarkdown } from "./markdown.js";

type MermaidModule = {
  initialize: (config: Record<string, unknown>) => void;
  render: (id: string, src: string) => Promise<{ svg: string }>;
};
type HljsModule = {
  highlight: (code: string, opts: { language: string }) => { value: string };
  highlightAuto: (code: string) => { value: string };
  getLanguage: (name: string) => unknown;
};
type KatexModule = {
  render: (tex: string, el: HTMLElement, opts: Record<string, unknown>) => void;
};

let mermaidPromise: Promise<MermaidModule> | null = null;
let hljsPromise: Promise<HljsModule> | null = null;
let katexPromise: Promise<KatexModule> | null = null;

function isDark(): boolean {
  return typeof document !== "undefined" && document.documentElement.classList.contains("dark");
}

function loadMermaid(): Promise<MermaidModule> {
  if (!mermaidPromise) {
    mermaidPromise = import("mermaid").then((m) => {
      const mermaid = m.default as unknown as MermaidModule;
      const dark = isDark();
      /* theme "base" + themeVariables gives nodes a clear filled colour
         (warm amber by default) instead of the washed-out default theme.
         Any `style`/`classDef` the diagram itself declares still wins, so
         per-node colours authored by the model are preserved. */
      mermaid.initialize({
        startOnLoad: false,
        securityLevel: "strict",
        fontFamily: "inherit",
        theme: "base",
        themeVariables: dark
          ? { primaryColor: "#3f3a16", primaryBorderColor: "#eab308", primaryTextColor: "#fef9c3", lineColor: "#94a3b8", secondaryColor: "#1e3a5f", secondaryBorderColor: "#3b82f6", tertiaryColor: "#14402a", tertiaryBorderColor: "#22c55e" }
          : { primaryColor: "#fef3c7", primaryBorderColor: "#f59e0b", primaryTextColor: "#111827", lineColor: "#6b7280", secondaryColor: "#e0f2fe", secondaryBorderColor: "#3b82f6", tertiaryColor: "#dcfce7", tertiaryBorderColor: "#22c55e" },
      });
      return mermaid;
    });
  }
  return mermaidPromise;
}

function loadHljs(): Promise<HljsModule> {
  if (!hljsPromise) {
    hljsPromise = import("highlight.js").then((m) => m.default as unknown as HljsModule);
  }
  return hljsPromise;
}

function loadKatex(): Promise<KatexModule> {
  if (!katexPromise) {
    katexPromise = import("katex").then((m) => m.default as unknown as KatexModule);
  }
  return katexPromise;
}

let mermaidSeq = 0;

async function renderMermaid(node: HTMLElement): Promise<void> {
  const els = node.querySelectorAll<HTMLElement>("[data-mermaid]:not([data-enriched])");
  if (!els.length) return;
  const mermaid = await loadMermaid();
  for (const el of els) {
    const src = el.getAttribute("data-mermaid-src") ?? "";
    try {
      const { svg } = await mermaid.render(`wmmd-${++mermaidSeq}`, src);
      el.setAttribute("data-enriched", "");
      el.innerHTML = `<div class="flex justify-center overflow-x-auto p-2">${svg}</div>`;
      /* hover toolbar: Copy .mmd source / Download / Copy diagram as PNG */
      attachToolbar(el, {
        source: () => src,
        filename: "diagram.mmd",
        mime: "text/plain;charset=utf-8",
        svg: () => el.querySelector("svg"),
      });
    } catch {
      /* parse failed: reveal the raw-code fallback (CSS shows pre again) */
      el.setAttribute("data-enriched", "");
      el.setAttribute("data-render-failed", "");
    }
  }
}

async function highlightCode(node: HTMLElement): Promise<void> {
  const els = node.querySelectorAll<HTMLElement>("code[data-code-lang]:not([data-enriched])");
  if (!els.length) return;
  const hljs = await loadHljs();
  for (const el of els) {
    el.setAttribute("data-enriched", "");
    const lang = el.getAttribute("data-code-lang") ?? "";
    const code = el.textContent ?? "";
    try {
      const res = lang && hljs.getLanguage(lang) ? hljs.highlight(code, { language: lang }) : hljs.highlightAuto(code);
      el.innerHTML = res.value;
      el.classList.add("hljs");
    } catch {
      /* keep the plain escaped code */
    }
  }
}

/* Parses untrusted SVG markup and strips anything that can execute or phone
   home: <script>, event handlers (on*), external/javascript: URLs, and
   <foreignObject> (which can embed arbitrary HTML). Returns the sanitised
   <svg> element, or null when the markup has no usable root. */
function sanitiseSvg(markup: string): SVGSVGElement | null {
  let root: Element | null = null;

  /* 1) strict XML parse (preferred — preserves SVG namespace exactly) */
  let doc = new DOMParser().parseFromString(markup, "image/svg+xml");
  if (doc.querySelector("parsererror")) {
    /* XML is strict: a bare & (common in text/URLs) breaks it. Escape
       ampersands that aren't already an entity, then retry. */
    const fixed = markup.replace(/&(?!#?\w+;)/g, "&amp;");
    doc = new DOMParser().parseFromString(fixed, "image/svg+xml");
  }
  if (!doc.querySelector("parsererror") && doc.documentElement.nodeName.toLowerCase() === "svg") {
    root = doc.documentElement;
  }

  /* 2) lenient fallback: the HTML parser never throws on imperfect markup.
     Reparenting the parsed <svg> into the SVG namespace via outerHTML round-trip
     keeps it a real SVGSVGElement that renders. */
  if (!root) {
    const html = new DOMParser().parseFromString(markup, "text/html");
    const found = html.querySelector("svg");
    if (found) {
      const reparsed = new DOMParser().parseFromString(found.outerHTML, "image/svg+xml");
      if (!reparsed.querySelector("parsererror") && reparsed.documentElement.nodeName.toLowerCase() === "svg") {
        root = reparsed.documentElement;
      }
    }
  }

  if (!root || root.nodeName.toLowerCase() !== "svg") return null;
  const walk = (el: Element) => {
    const tag = el.nodeName.toLowerCase();
    if (tag === "script" || tag === "foreignobject") { el.remove(); return; }
    for (const attr of Array.from(el.attributes)) {
      const name = attr.name.toLowerCase();
      const val = attr.value.trim().toLowerCase();
      if (name.startsWith("on")) { el.removeAttribute(attr.name); continue; }
      if ((name === "href" || name === "xlink:href" || name === "src") &&
          (val.startsWith("javascript:") || (!val.startsWith("#") && !val.startsWith("data:image/")))) {
        el.removeAttribute(attr.name);
      }
    }
    for (const child of Array.from(el.children)) walk(child);
  };
  walk(root);
  return root as unknown as SVGSVGElement;
}

/* Turns mid-stream SVG source into parseable markup so it can be rendered
   progressively ("painting" effect): drops a trailing half-typed tag, then
   closes every still-open element and appends </svg>. Self-closing and void-ish
   shapes are ignored. Best-effort — returns "" when there's no <svg> yet. */
function completePartialSvg(src: string): string {
  let s = src;
  /* drop an unfinished trailing tag like `<path d="M 3` (no closing >) */
  const lastLt = s.lastIndexOf("<");
  const lastGt = s.lastIndexOf(">");
  if (lastLt > lastGt) s = s.slice(0, lastLt);
  if (!/<svg[\s>]/i.test(s)) return "";
  /* track open (non-self-closed) tags to close them in reverse */
  const stack: string[] = [];
  const tagRe = /<(\/?)([a-zA-Z][\w:-]*)((?:[^>"']|"[^"]*"|'[^']*')*?)(\/?)>/g;
  let m: RegExpExecArray | null;
  while ((m = tagRe.exec(s)) !== null) {
    const [, closing, name, , selfClose] = m;
    if (selfClose) continue;
    if (closing) { if (stack[stack.length - 1] === name) stack.pop(); }
    else stack.push(name);
  }
  for (let i = stack.length - 1; i >= 0; i--) s += `</${stack[i]}>`;
  return s;
}

function renderSvg(node: HTMLElement): void {
  /* Partial (streaming) blocks re-render every paint as more shapes arrive;
     complete blocks render once. */
  const els = node.querySelectorAll<HTMLElement>(
    "[data-svg][data-svg-partial], [data-svg]:not([data-enriched])",
  );
  for (const el of els) {
    const src = el.getAttribute("data-svg-src") ?? "";
    const partial = el.hasAttribute("data-svg-partial");
    /* skip re-render when a partial block's source hasn't grown since last paint */
    if (partial && el.getAttribute("data-svg-rendered") === src) continue;

    /* Best-effort render: try the source as-is, then fall back to auto-closing
       any open tags (handles mid-stream AND a complete-but-slightly-malformed
       SVG). Only give up to raw when even the repaired markup won't parse. */
    let svg = src ? sanitiseSvg(src) : null;
    if (!svg) {
      const repaired = completePartialSvg(src);
      if (repaired) svg = sanitiseSvg(repaired);
    }
    if (!svg) {
      if (!partial) {
        /* a complete block that won't parse even repaired: show raw source */
        el.setAttribute("data-enriched", "");
        el.setAttribute("data-render-failed", "");
      }
      continue; /* partial not yet parseable: keep "rendering…" */
    }
    el.setAttribute("data-enriched", "");
    el.setAttribute("data-svg-rendered", src);
    el.innerHTML = "";
    const box = document.createElement("div");
    box.className = "flex justify-center overflow-x-auto p-2";
    box.appendChild(svg);
    el.appendChild(box);
    if (!partial) {
      attachToolbar(el, {
        source: () => src,
        filename: "image.svg",
        mime: "image/svg+xml;charset=utf-8",
        svg: () => el.querySelector("svg"),
      });
    }
  }
}

async function renderMath(node: HTMLElement): Promise<void> {
  const els = node.querySelectorAll<HTMLElement>("[data-math]:not([data-enriched])");
  if (!els.length) return;
  const katex = await loadKatex();
  for (const el of els) {
    el.setAttribute("data-enriched", "");
    const tex = el.getAttribute("data-math-src") ?? "";
    const display = el.hasAttribute("data-math-display");
    try {
      katex.render(tex, el, { displayMode: display, throwOnError: false, output: "html" });
    } catch {
      /* keep the raw $…$ fallback */
    }
  }
}

/* Content-Security-Policy injected into every HTML-artifact iframe. The
   iframe is also sandboxed without allow-same-origin, so it runs in an opaque
   origin (no access to the parent's cookies/storage/DOM). The CSP then blocks
   every exfiltration channel: connect-src none (no fetch/XHR/WebSocket),
   form-action none (no submitting a form anywhere), img/font/media data: only
   (no external beacons), script-src inline only (no external scripts), and no
   nested frames or base override. Inline scripts still run, so the artifact
   stays interactive — it just cannot phone home or read anything outside it. */
const ARTIFACT_CSP =
  "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data:; font-src data:; media-src data:; connect-src 'none'; form-action 'none'; frame-src 'none'; object-src 'none'; base-uri 'none'";

export function buildArtifactSrcdoc(html: string): string {
  const meta = `<meta http-equiv="Content-Security-Policy" content="${ARTIFACT_CSP}">`;
  if (/<head[\s>]/i.test(html)) return html.replace(/<head[^>]*>/i, (m) => `${m}${meta}`);
  if (/<html[\s>]/i.test(html)) return html.replace(/<html[^>]*>/i, (m) => `${m}<head>${meta}</head>`);
  return `<!doctype html><html><head><meta charset="utf-8">${meta}</head><body>${html}</body></html>`;
}

function renderHtmlArtifacts(node: HTMLElement): void {
  const els = node.querySelectorAll<HTMLElement>("[data-html-artifact]:not([data-enriched])");
  for (const el of els) {
    el.setAttribute("data-enriched", "");
    const src = el.getAttribute("data-html-src") ?? "";
    if (!src.trim()) continue;

    const header = document.createElement("div");
    header.className = "flex items-center justify-between px-3 py-1 bg-white-300 dark:bg-navy-600";
    const label = document.createElement("span");
    label.className = "text-[10px] text-black-600 dark:text-black-700 uppercase tracking-wide";
    label.textContent = "HTML preview";
    const toggle = document.createElement("button");
    toggle.type = "button";
    toggle.className = "text-[10px] text-black-500 dark:text-black-600 hover:text-black-700 dark:hover:text-black-400 transition-colors px-1.5 py-0.5 rounded hover:bg-white-400 dark:hover:bg-navy-500";
    toggle.textContent = "Show code";
    header.append(label, toggle);

    const iframe = document.createElement("iframe");
    iframe.setAttribute("sandbox", "allow-scripts");
    iframe.setAttribute("referrerpolicy", "no-referrer");
    iframe.setAttribute("loading", "lazy");
    iframe.setAttribute("title", "HTML preview");
    iframe.className = "w-full bg-white-100";
    iframe.style.height = "360px";
    iframe.style.border = "0";
    iframe.srcdoc = buildArtifactSrcdoc(src);

    const code = document.createElement("pre");
    code.className = "hidden overflow-x-auto px-4 py-3 text-xs font-mono text-black-900 dark:text-white-100 bg-white-200 dark:bg-navy-800 leading-relaxed";
    const codeInner = document.createElement("code");
    codeInner.textContent = src;
    code.appendChild(codeInner);

    let showingCode = false;
    toggle.addEventListener("click", () => {
      showingCode = !showingCode;
      iframe.classList.toggle("hidden", showingCode);
      code.classList.toggle("hidden", !showingCode);
      toggle.textContent = showingCode ? "Show preview" : "Show code";
    });

    el.innerHTML = "";
    el.append(header, iframe, code);
  }
}

function enrichAll(node: HTMLElement): void {
  renderHtmlArtifacts(node);
  void renderMermaid(node);
  renderSvg(node);
  void highlightCode(node);
  void renderMath(node);
}

/* Svelte action for STATIC (committed) messages: enrich the markdown already
   placed in innerHTML by {@html}. Runs SYNCHRONOUSLY on mount so a committed
   message (or a page reload) never flashes raw mermaid/svg source before the
   render — only updates (rare for committed turns) are debounced. */
export function enrich(node: HTMLElement, _text: string) {
  let timer: ReturnType<typeof setTimeout> | undefined;
  enrichAll(node);
  return {
    update(_next: string) {
      clearTimeout(timer);
      timer = setTimeout(() => enrichAll(node), 120);
    },
    destroy() { clearTimeout(timer); },
  };
}

/* Svelte action for the STREAMING live turn. Owns innerHTML itself (do NOT
   pair with {@html}) so it can re-render markdown on each token WITHOUT
   wiping diagrams that are already rendered — those flicker text→image→text
   otherwise. Strategy: re-render markdown into a detached fragment, then for
   every already-enriched block in the live DOM whose source is unchanged,
   transplant the rendered node into the new fragment before swapping it in.
   Only genuinely new/changed blocks re-enrich. */
const ENRICHED_SEL = "[data-mermaid][data-enriched], [data-svg][data-enriched], [data-html-artifact][data-enriched], [data-math][data-enriched], code[data-code-lang][data-enriched]";

function srcKey(el: Element): string {
  return (
    el.getAttribute("data-mermaid-src") ??
    el.getAttribute("data-svg-src") ??
    el.getAttribute("data-html-src") ??
    el.getAttribute("data-math-src") ??
    (el.classList.contains("hljs") ? el.textContent ?? "" : "")
  );
}

export function renderLive(node: HTMLElement, text: string) {
  let timer: ReturnType<typeof setTimeout> | undefined;
  let last = "";

  function paint(t: string): void {
    /* snapshot already-rendered blocks from the current DOM, keyed by source */
    const cache = new Map<string, Element>();
    node.querySelectorAll(ENRICHED_SEL).forEach((el) => {
      const k = `${el.getAttribute("data-mermaid") !== null ? "m" : el.getAttribute("data-svg") !== null ? "s" : el.getAttribute("data-html-artifact") !== null ? "h" : el.getAttribute("data-math") !== null ? "t" : "c"}:${srcKey(el)}`;
      if (k.slice(2)) cache.set(k, el);
    });

    const tmp = document.createElement("div");
    tmp.innerHTML = renderMarkdown(t);

    /* transplant unchanged rendered blocks so they are not reset to raw text */
    tmp.querySelectorAll("[data-mermaid], [data-svg], [data-html-artifact], [data-math]").forEach((fresh) => {
      const tag = fresh.getAttribute("data-mermaid") !== null ? "m" : fresh.getAttribute("data-svg") !== null ? "s" : fresh.getAttribute("data-html-artifact") !== null ? "h" : "t";
      const k = `${tag}:${srcKey(fresh)}`;
      const done = cache.get(k);
      if (done) fresh.replaceWith(done);
    });

    node.innerHTML = "";
    while (tmp.firstChild) node.appendChild(tmp.firstChild);
    enrichAll(node);
  }

  paint(text);
  last = text;

  return {
    update(next: string) {
      if (next === last) return;
      last = next;
      clearTimeout(timer);
      timer = setTimeout(() => paint(next), 80);
    },
    destroy() { clearTimeout(timer); },
  };
}
