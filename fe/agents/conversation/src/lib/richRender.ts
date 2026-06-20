/* Enriches the static markdown HTML rendered into a chat bubble: turns the
   common-md placeholders into rendered Mermaid diagrams, syntax-highlighted
   code, and KaTeX math. Each library is lazy-loaded on first use, the work is
   idempotent (a `data-enriched` marker), and it is debounced so a streaming
   message that re-renders on every token does not thrash the renderers. */
import "katex/dist/katex.min.css";
import "./richRender.css";
import { mount } from "svelte";
import { attachToolbar } from "./blockToolbar.js";
import { renderMarkdown } from "./markdown.js";
import HtmlArtifact from "./components/HtmlArtifact.svelte";

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

/* Trims a mid-stream mermaid source back to a parseable prefix so the diagram
   "paints" as statements arrive. Mermaid can't parse a half-typed statement,
   so we drop the trailing incomplete line; keeping the header (first line)
   ensures the diagram type is always present. Best-effort — the renderer still
   falls back to the last good frame when even this won't parse. */
function completePartialMermaid(src: string): string {
  const lines = src.split("\n");
  /* drop a trailing line that looks unfinished (open arrow / dangling token) */
  while (lines.length > 1) {
    const last = lines[lines.length - 1].trim();
    if (last === "" || /[-=>|:[{(]$/.test(last) || /--+\s*$/.test(last)) {
      lines.pop();
      continue;
    }
    break;
  }
  return lines.join("\n").trim();
}

async function renderMermaid(node: HTMLElement): Promise<void> {
  /* Partial (streaming) blocks re-render as more lines arrive; complete blocks
     render once. Mirrors renderSvg's partial handling. */
  const els = node.querySelectorAll<HTMLElement>(
    "[data-mermaid][data-mermaid-partial], [data-mermaid]:not([data-enriched])",
  );
  if (!els.length) return;
  const mermaid = await loadMermaid();
  for (const el of els) {
    const src = el.getAttribute("data-mermaid-src") ?? "";
    const partial = el.hasAttribute("data-mermaid-partial");
    /* skip re-render when a partial block's source hasn't grown since last paint */
    if (partial && el.getAttribute("data-mermaid-rendered") === src) continue;

    /* Race guard: stamp this attempt's sequence on the element. Mermaid.render
       is async and the live painter wipes innerHTML each token; if a newer
       attempt started meanwhile, drop this stale result instead of writing a
       detached/old SVG (the source of the flicker). */
    const seq = ++mermaidSeq;
    el.setAttribute("data-mermaid-seq", String(seq));

    let svg: string | null = null;
    try {
      svg = (await mermaid.render(`wmmd-${seq}`, src)).svg;
    } catch {
      if (partial) {
        /* mid-stream parse fail: retry on the largest parseable prefix */
        const repaired = completePartialMermaid(src);
        if (repaired && repaired !== src) {
          try { svg = (await mermaid.render(`wmmd-${seq}r`, repaired)).svg; } catch { /* keep last frame */ }
        }
      }
    }

    /* a newer paint superseded this attempt — discard */
    if (el.getAttribute("data-mermaid-seq") !== String(seq)) continue;

    if (svg) {
      el.setAttribute("data-mermaid-rendered", src);
      el.innerHTML = `<div class="flex justify-center overflow-x-auto p-2">${svg}</div>`;
      if (!partial) {
        el.setAttribute("data-enriched", "");
        /* hover toolbar: Copy .mmd source / Download / Copy diagram as PNG */
        attachToolbar(el, {
          source: () => src,
          filename: "diagram.mmd",
          mime: "text/plain;charset=utf-8",
          svg: () => el.querySelector("svg"),
        });
      }
    } else if (!partial) {
      /* complete block that won't parse even repaired: reveal raw-code fallback */
      el.setAttribute("data-enriched", "");
      el.setAttribute("data-render-failed", "");
    }
    /* partial with no parseable frame yet: keep whatever's shown (raw or last
       good frame), don't flash — next paint retries */
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

/* Some generated SVGs declare a viewBox / width-height smaller than their real
   content (e.g. the last node's box extends past the stated height), clipping
   the overflow in the source itself. Recompute the viewBox from the rendered
   bounding box (+ small padding) so nothing is cut off. Must run AFTER the svg
   is in the DOM and laid out — getBBox needs layout. No-op on failure. */
export function correctViewBox(svg: SVGSVGElement): void {
  try {
    const bb = svg.getBBox();
    if (!bb.width || !bb.height) return;
    const vb = svg.viewBox.baseVal;
    /* only widen when content actually spills past the declared viewBox */
    const spills =
      !vb ||
      (vb.width === 0 && vb.height === 0) ||
      bb.x < vb.x - 0.5 ||
      bb.y < vb.y - 0.5 ||
      bb.x + bb.width > vb.x + vb.width + 0.5 ||
      bb.y + bb.height > vb.y + vb.height + 0.5;
    if (!spills) return;
    const pad = Math.max(8, Math.min(bb.width, bb.height) * 0.02);
    const x = bb.x - pad;
    const y = bb.y - pad;
    const w = bb.width + pad * 2;
    const h = bb.height + pad * 2;
    svg.setAttribute("viewBox", `${x} ${y} ${w} ${h}`);
    svg.setAttribute("width", String(w));
    svg.setAttribute("height", String(h));
  } catch {
    /* getBBox can throw on a detached / empty svg — leave it as-is */
  }
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
    /* now in the DOM: fix a too-small viewBox so a node that spills past it
       isn't clipped (complete blocks only — a partial is still growing) */
    if (!partial) correctViewBox(svg);
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

/* Theme bridge injected into every artifact iframe so HTML the model writes
   can match the chat's light/dark theme. We expose CSS variables (which the
   system prompt tells the model to use) and set `color-scheme` so native form
   controls adapt. We do NOT force a background — HTML that styles itself wins;
   only the :root vars + color-scheme are provided. `dark` is mirrored onto the
   artifact's <html> too, so authors can also key off `.dark` / a media-like
   class. Read from the parent's class-based dark mode at build time. */
function artifactThemeStyle(): string {
  const dark = isDark();
  const vars = dark
    ? "--wick-bg:#0f172a;--wick-surface:#1e293b;--wick-fg:#f1f5f9;--wick-muted:#94a3b8;--wick-border:#334155;--wick-accent:#22c55e;"
    : "--wick-bg:#ffffff;--wick-surface:#f1f5f9;--wick-fg:#0f172a;--wick-muted:#64748b;--wick-border:#e2e8f0;--wick-accent:#16a34a;";
  return `<style>:root{color-scheme:${dark ? "dark" : "light"};${vars}}</style>`;
}

export function buildArtifactSrcdoc(html: string): string {
  const meta = `<meta http-equiv="Content-Security-Policy" content="${ARTIFACT_CSP}">`;
  const head = meta + artifactThemeStyle();
  const htmlClass = isDark() ? ' class="dark"' : "";
  if (/<head[\s>]/i.test(html)) return html.replace(/<head[^>]*>/i, (m) => `${m}${head}`);
  if (/<html[\s>]/i.test(html)) return html.replace(/<html[^>]*>/i, (m) => `${m}<head>${head}</head>`);
  return `<!doctype html><html${htmlClass}><head><meta charset="utf-8">${head}</head><body>${html}</body></html>`;
}

/* Inline reporter (CSP allows script-src 'unsafe-inline') that posts the
   document's full scroll height to the parent on load, mutation, and resize.
   The host iframe listens for {type:"wick-artifact-height"} and grows to fit,
   so the inline preview has no inner scrollbar — it reads as one with the
   chat. id correlates the message to the right iframe when several are shown. */
export function artifactHeightReporter(id: string): string {
  // Measuring scrollHeight alone breaks when the doc sizes to the viewport
  // (body{min-height:100vh} / flex-centering): inside the iframe 100vh ===
  // the iframe's CURRENT height, so scrollHeight just echoes whatever we set
  // and the content stays clipped. The body's children keep their natural
  // size though, so the farthest child's bottom edge gives the real height.
  return `<script>(function(){
    var de=document.documentElement;
    function h(){
      var b=document.body, max=de.scrollHeight;
      if(b){
        max=Math.max(max,b.scrollHeight,b.offsetHeight);
        var k=b.children;
        for(var i=0;i<k.length;i++){var bot=k[i].getBoundingClientRect().bottom; if(bot>max)max=bot;}
      }
      return Math.ceil(max)||0;
    }
    function send(){var hh=h(); if(hh>0){try{parent.postMessage({type:"wick-artifact-height",id:${JSON.stringify(id)},height:hh},"*");}catch(e){}}}
    window.addEventListener("load",send);
    window.addEventListener("resize",send);
    if(window.ResizeObserver){try{new ResizeObserver(send).observe(de); if(document.body){new ResizeObserver(send).observe(document.body);}}catch(e){}}
    if(window.MutationObserver){try{new MutationObserver(send).observe(de,{subtree:true,childList:true,attributes:true});}catch(e){}}
    setTimeout(send,50);setTimeout(send,300);setTimeout(send,1000);
  })();<\/script>`;
}

/* buildArtifactSrcdoc + the height reporter injected before </body> (or
   appended). Used by the inline gallery preview that auto-grows to content. */
export function buildAutoHeightSrcdoc(html: string, id: string): string {
  const doc = buildArtifactSrcdoc(html);
  const reporter = artifactHeightReporter(id);
  if (/<\/body>/i.test(doc)) return doc.replace(/<\/body>/i, `${reporter}</body>`);
  return doc + reporter;
}

/* Inline HTML artifacts (HTML the model emitted in the message body) render
   through the SAME Svelte component as the file-artifact gallery
   (HtmlArtifact.svelte): a borderless auto-height preview with a floating ⋮
   menu (Full screen / Show code / Download). The placeholder div is enriched
   in place by mounting the component into it with the inline source. */
function renderHtmlArtifacts(node: HTMLElement): void {
  const els = node.querySelectorAll<HTMLElement>("[data-html-artifact]:not([data-enriched])");
  for (const el of els) {
    el.setAttribute("data-enriched", "");
    const src = el.getAttribute("data-html-src") ?? "";
    if (!src.trim()) continue;
    el.innerHTML = "";
    el.className = "w-full";
    // Pass the host element so the component can re-read data-html-src while
    // the block is still streaming (renderLive transplants this node forward
    // and updates the attribute each token instead of remounting).
    mount(HtmlArtifact, {
      target: el,
      props: { src, srcHost: el, name: el.getAttribute("data-html-name") || "preview.html" },
    });
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
/* Includes partial mermaid/svg blocks (which carry data-*-rendered instead of
   data-enriched) so a frame already painted mid-stream survives the per-token
   innerHTML swap. Critical for ASYNC mermaid: without transplanting the last
   rendered node, each paint shows raw source until mermaid.render resolves —
   the flicker. */
const ENRICHED_SEL =
  "[data-mermaid][data-enriched], [data-mermaid][data-mermaid-rendered], " +
  "[data-svg][data-enriched], [data-svg][data-svg-rendered], " +
  "[data-html-artifact][data-enriched], [data-math][data-enriched], code[data-code-lang][data-enriched]";

function srcKey(el: Element): string {
  return (
    el.getAttribute("data-mermaid-src") ??
    el.getAttribute("data-svg-src") ??
    el.getAttribute("data-html-src") ??
    el.getAttribute("data-math-src") ??
    (el.classList.contains("hljs") ? el.textContent ?? "" : "")
  );
}

/* A growing partial diagram changes src every token, so an exact-src cache
   never hits. Match the single in-flight partial of the same kind instead, so
   its painted frame transplants forward; renderMermaid/renderSvg then refresh
   it in place once the new (larger) source renders. */
const PARTIAL_KEY = { m: "mp:partial", s: "sp:partial", h: "hp:partial" } as const;

export function renderLive(node: HTMLElement, text: string) {
  let timer: ReturnType<typeof setTimeout> | undefined;
  let last = "";

  function kindOf(el: Element): "m" | "s" | "h" | "t" | "c" {
    return el.getAttribute("data-mermaid") !== null ? "m"
      : el.getAttribute("data-svg") !== null ? "s"
      : el.getAttribute("data-html-artifact") !== null ? "h"
      : el.getAttribute("data-math") !== null ? "t" : "c";
  }

  function paint(t: string): void {
    /* snapshot already-rendered blocks from the current DOM. Exact-src key for
       finished blocks; a single partial-of-kind key for the in-flight diagram
       so its painted frame transplants forward as its source grows. */
    const cache = new Map<string, Element>();
    node.querySelectorAll(ENRICHED_SEL).forEach((el) => {
      const kind = kindOf(el);
      cache.set(`${kind}:${srcKey(el)}`, el);
      if (kind === "m" && el.hasAttribute("data-mermaid-partial")) cache.set(PARTIAL_KEY.m, el);
      if (kind === "s" && el.hasAttribute("data-svg-partial")) cache.set(PARTIAL_KEY.s, el);
      // An HTML artifact's source grows every token while it streams, so the
      // exact-src cache never hits — keep the single in-flight one keyed so its
      // mounted iframe transplants forward (no remount → no height reset).
      if (kind === "h") cache.set(PARTIAL_KEY.h, el);
    });

    const tmp = document.createElement("div");
    tmp.innerHTML = renderMarkdown(t);

    /* transplant unchanged rendered blocks so they are not reset to raw text */
    tmp.querySelectorAll("[data-mermaid], [data-svg], [data-html-artifact], [data-math]").forEach((fresh) => {
      const kind = kindOf(fresh);
      let done = cache.get(`${kind}:${srcKey(fresh)}`);
      /* growing partial: reuse the in-flight frame and update its source so the
         renderer refreshes the already-visible diagram in place (no raw flash) */
      if (!done && kind === "m" && fresh.hasAttribute("data-mermaid-partial")) {
        done = cache.get(PARTIAL_KEY.m);
        if (done) done.setAttribute("data-mermaid-src", fresh.getAttribute("data-mermaid-src") ?? "");
      }
      if (!done && kind === "s" && fresh.hasAttribute("data-svg-partial")) {
        done = cache.get(PARTIAL_KEY.s);
        if (done) done.setAttribute("data-svg-src", fresh.getAttribute("data-svg-src") ?? "");
      }
      // Streaming HTML artifact: reuse the already-mounted iframe and push the
      // grown source onto its host attribute. HtmlArtifact observes the attr
      // and refreshes its preview in place (so it keeps growing, not resets).
      if (!done && kind === "h") {
        done = cache.get(PARTIAL_KEY.h);
        if (done) done.setAttribute("data-html-src", fresh.getAttribute("data-html-src") ?? "");
      }
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
