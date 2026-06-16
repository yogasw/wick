/* Enriches the static markdown HTML rendered into a chat bubble: turns the
   common-md placeholders into rendered Mermaid diagrams, syntax-highlighted
   code, and KaTeX math. Each library is lazy-loaded on first use, the work is
   idempotent (a `data-enriched` marker), and it is debounced so a streaming
   message that re-renders on every token does not thrash the renderers. */
import "katex/dist/katex.min.css";
import "./richRender.css";

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
    el.setAttribute("data-enriched", "");
    const src = el.getAttribute("data-mermaid-src") ?? "";
    try {
      const { svg } = await mermaid.render(`wmmd-${++mermaidSeq}`, src);
      el.innerHTML = `<div class="flex justify-center overflow-x-auto p-2">${svg}</div>`;
    } catch {
      /* keep the raw-code fallback already inside the placeholder */
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

/* Svelte action: attach to the element whose innerHTML holds rendered
   markdown. Re-runs (debounced) whenever the bound text changes. */
export function enrich(node: HTMLElement, _text: string) {
  let timer: ReturnType<typeof setTimeout> | undefined;
  function run(): void {
    clearTimeout(timer);
    timer = setTimeout(() => {
      void renderMermaid(node);
      void highlightCode(node);
      void renderMath(node);
    }, 120);
  }
  run();
  return {
    update(_next: string) {
      run();
    },
    destroy() {
      clearTimeout(timer);
    },
  };
}
