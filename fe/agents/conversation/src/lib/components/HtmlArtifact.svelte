<script lang="ts">
  /* The single HTML-artifact preview used by BOTH render paths:
       - file artifacts in the gallery (pass `url` — content is fetched), and
       - inline HTML the model emitted in the message body (pass `src`).
     It renders a borderless, auto-height iframe (no inner scrollbar — it grows
     to its content via the height reporter) with a floating ⋮ menu carrying
     Full screen / Show code / Download. Self-contained fullscreen so it works
     the same whether mounted in the Svelte tree or via mount() from richRender. */
  import { KebabMenu } from "@wick-fe/common-ui";
  import { buildAutoHeightSrcdoc, getFileContext } from "../richRender.js";
  import { safeReadPath } from "../artifactPath.js";

  type Props = {
    /** inline source (message-body HTML). Mutually exclusive with url. */
    src?: string;
    /** file URL to fetch the HTML from. Mutually exclusive with src. */
    url?: string;
    /** forced-download endpoint; defaults to url for file artifacts. */
    downloadUrl?: string;
    /** when mounted into a [data-html-artifact] host that streams, the host
        element whose data-html-src grows each token — observed so the preview
        refreshes in place instead of resetting on remount. */
    srcHost?: HTMLElement;
    name?: string;
  };
  let { src, url, downloadUrl, srcHost, name = "preview.html" }: Props = $props();

  const id = `html-artifact-${Math.round(performance.now())}-${Math.floor((performance.now() * 1000) % 100000)}`;

  let raw = $state<string | null>(src ?? null);
  let srcdoc = $state("");
  let loadErr = $state("");
  let height = $state(320);
  let showCode = $state(false);
  let fullscreen = $state(false);
  // Bumped on every reload so the iframe remounts even when the refetched
  // bytes are byte-identical — a plain srcdoc reassign to the same string
  // won't re-run the artifact's scripts, making Reload look like a no-op.
  let reloadKey = $state(0);
  // The mounted iframes (inline + fullscreen). The file bridge only answers
  // requests whose source is one of THESE windows, so with many artifacts on
  // screen each request is served exactly once (by its own host).
  let frameEl = $state<HTMLIFrameElement | null>(null);
  let fsFrameEl = $state<HTMLIFrameElement | null>(null);

  const MAX_HEIGHT = 2400;

  function applyRaw(next: string) {
    if (next === raw) return;
    raw = next;
    srcdoc = buildAutoHeightSrcdoc(next, id);
  }

  async function ensureLoaded() {
    if (raw !== null) {
      srcdoc = buildAutoHeightSrcdoc(raw, id);
      return;
    }
    if (!url) return;
    try {
      const res = await fetch(url);
      raw = await res.text();
      srcdoc = buildAutoHeightSrcdoc(raw, id);
    } catch (e) {
      loadErr = e instanceof Error ? e.message : String(e);
    }
  }

  // Re-fetch a url-backed preview: the file may have changed on disk since the
  // first render. cache:no-store bypasses the HTTP cache so we get fresh bytes.
  // No-op for inline (src) artifacts — there is no source to re-read.
  async function reload() {
    if (!url) return;
    loadErr = "";
    try {
      const res = await fetch(url, { cache: "no-store" });
      raw = await res.text();
      srcdoc = buildAutoHeightSrcdoc(raw, id);
      reloadKey++; // force iframe remount so the artifact's scripts re-run
    } catch (e) {
      loadErr = e instanceof Error ? e.message : String(e);
    }
  }

  // Serve a file the sandboxed artifact asked for over the postMessage bridge.
  // The artifact cannot fetch (sandbox + CSP connect-src none); this parent
  // has the session, validates the path, fetches, and posts the bytes back.
  async function serveFileReq(win: Window, reqId: unknown, rawPath: unknown) {
    const reply = (msg: Record<string, unknown>) => {
      try { win.postMessage({ type: "wick-file-resp", reqId, ...msg }, "*"); } catch { /* frame gone */ }
    };
    const path = safeReadPath(rawPath);
    if (!path) { reply({ ok: false, error: "invalid path" }); return; }
    const { base, sessionId } = getFileContext();
    if (!base || !sessionId) { reply({ ok: false, error: "no session context" }); return; }
    try {
      const res = await fetch(
        `${base}/sessions/${sessionId}/files/download?path=${encodeURIComponent(path)}`,
        { cache: "no-store" },
      );
      if (!res.ok) { reply({ ok: false, error: `HTTP ${res.status}` }); return; }
      reply({ ok: true, content: await res.text() });
    } catch (e) {
      reply({ ok: false, error: e instanceof Error ? e.message : String(e) });
    }
  }

  type DtQueryOpts = {
    sort?: string;
    limit?: number;
    offset?: number;
    filters?: Record<string, { op?: string; v?: unknown }>;
  };
  type DtReq = { reqId: unknown; op?: string; slug?: unknown; id?: unknown; row?: unknown; opts?: DtQueryOpts };

  // Build the ?sort=…&limit=…&f.<col>.op=…&f.<col>.v=… query string for a
  // data-table query op from the widget-supplied opts. Mirrors the grammar
  // the Go handler (parseFilterQuery/parseSortQuery) already understands.
  function buildDtQuery(opts: DtQueryOpts | undefined): string {
    if (!opts) return "";
    const p = new URLSearchParams();
    if (opts.sort) p.set("sort", String(opts.sort));
    if (opts.limit != null) p.set("limit", String(opts.limit));
    if (opts.offset != null) p.set("offset", String(opts.offset));
    if (opts.filters && typeof opts.filters === "object") {
      for (const [col, f] of Object.entries(opts.filters)) {
        if (!f || typeof f !== "object") continue;
        if (f.op) p.set(`f.${col}.op`, String(f.op));
        if (f.v != null) p.set(`f.${col}.v`, String(f.v));
      }
    }
    const s = p.toString();
    return s ? `?${s}` : "";
  }

  // Serve a data-table request the sandboxed artifact posted over the bridge.
  // Same trust model as serveFileReq: the artifact can't reach the network
  // (opaque origin + CSP connect-src none), so the parent — which carries the
  // session cookie — makes the access-checked call and posts the JSON back.
  // Ownership is enforced SERVER-side per table; the slug from the widget is
  // untrusted and re-validated here + in the handler.
  async function serveDataTableReq(win: Window, msg: DtReq) {
    const reply = (m: Record<string, unknown>) => {
      try { win.postMessage({ type: "wick-dt-resp", reqId: msg.reqId, ...m }, "*"); } catch { /* frame gone */ }
    };
    const { base } = getFileContext();
    if (!base) { reply({ ok: false, error: "no session context" }); return; }
    const slug = typeof msg.slug === "string" ? msg.slug.trim() : "";
    if (!/^[a-z0-9-]+$/.test(slug)) { reply({ ok: false, error: "invalid table slug" }); return; }
    const root = `${base}/api/data-tables/${encodeURIComponent(slug)}/rows`;
    const jsonHeaders = { "content-type": "application/json" };
    try {
      let res: Response;
      switch (msg.op) {
        case "query":
          res = await fetch(root + buildDtQuery(msg.opts), { cache: "no-store" });
          break;
        case "insert":
          res = await fetch(root, { method: "POST", headers: jsonHeaders, body: JSON.stringify(msg.row ?? {}) });
          break;
        case "update":
          res = await fetch(`${root}/${encodeURIComponent(String(msg.id))}`,
            { method: "PATCH", headers: jsonHeaders, body: JSON.stringify(msg.row ?? {}) });
          break;
        case "delete":
          res = await fetch(`${root}/${encodeURIComponent(String(msg.id))}`, { method: "DELETE" });
          break;
        default:
          reply({ ok: false, error: `unknown op: ${String(msg.op)}` });
          return;
      }
      const text = await res.text();
      let data: unknown = null;
      try { data = text ? JSON.parse(text) : null; } catch { data = text; }
      if (!res.ok) {
        const errMsg = data && typeof data === "object" && "error" in data
          ? String((data as { error: unknown }).error)
          : `HTTP ${res.status}`;
        reply({ ok: false, error: errMsg });
        return;
      }
      reply({ ok: true, data });
    } catch (e) {
      reply({ ok: false, error: e instanceof Error ? e.message : String(e) });
    }
  }

  $effect(() => {
    ensureLoaded();
    function onMsg(e: MessageEvent) {
      const d = e.data as
        | { type?: string; id?: string; height?: number; reqId?: unknown; path?: unknown; op?: string; slug?: unknown; row?: unknown; opts?: DtQueryOpts }
        | null;
      if (!d) return;
      if (d.type === "wick-artifact-height" && d.id === id && d.height) {
        height = Math.min(MAX_HEIGHT, Math.ceil(d.height));
        return;
      }
      // Only answer requests coming from THIS component's own iframe(s), so
      // with several artifacts mounted each request is served exactly once.
      const fromOwnFrame = !!e.source &&
        (e.source === frameEl?.contentWindow || e.source === fsFrameEl?.contentWindow);
      if (d.type === "wick-file-req" && fromOwnFrame) {
        void serveFileReq(e.source as Window, d.reqId, d.path);
        return;
      }
      if (d.type === "wick-dt-req" && fromOwnFrame) {
        void serveDataTableReq(e.source as Window, d as DtReq);
      }
    }
    window.addEventListener("message", onMsg);
    return () => window.removeEventListener("message", onMsg);
  });

  // While the inline block streams, renderLive keeps THIS mounted node and
  // rewrites the host's data-html-src each token. Observe it so the preview
  // grows with the source instead of staying at the first partial.
  $effect(() => {
    if (!srcHost || typeof MutationObserver === "undefined") return;
    const obs = new MutationObserver(() => {
      const next = srcHost.getAttribute("data-html-src");
      if (next && next.trim()) applyRaw(next);
    });
    obs.observe(srcHost, { attributes: true, attributeFilter: ["data-html-src"] });
    return () => obs.disconnect();
  });

  function download() {
    const href = downloadUrl || url;
    if (href) {
      const a = document.createElement("a");
      a.href = href;
      a.download = name;
      document.body.appendChild(a);
      a.click();
      a.remove();
      return;
    }
    if (raw !== null) {
      const blob = new Blob([raw], { type: "text/html" });
      const u = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = u;
      a.download = name;
      document.body.appendChild(a);
      a.click();
      a.remove();
      setTimeout(() => URL.revokeObjectURL(u), 1000);
    }
  }

  const menuItems = $derived([
    { label: "Full screen", onclick: () => (fullscreen = true) },
    { label: showCode ? "Show preview" : "Show code", onclick: () => (showCode = !showCode) },
    // Only url-backed previews can be re-fetched; inline artifacts have no source.
    ...(url ? [{ label: "Reload", onclick: reload }] : []),
    { label: "Download", onclick: download },
  ]);

  $effect(() => {
    if (!fullscreen) return;
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") fullscreen = false;
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  });
</script>

<!-- Borderless so the preview reads as one with the conversation. -->
<div class="relative w-full overflow-hidden rounded-xl">
  <!-- ⋮ chip floats over the (interactive) iframe. NOTE: no transform/filter/
       backdrop-blur here — any of those make this a containing block for the
       KebabMenu's position:fixed dropdown, which then gets clipped by the
       container's overflow-hidden and never shows. A solid bg + z keeps it
       legible and clickable without that trap. -->
  <div class="absolute right-1.5 top-1.5 z-30 rounded-lg bg-white-200 dark:bg-navy-700 shadow-sm">
    <KebabMenu ariaLabel={`Actions for ${name}`} items={menuItems} />
  </div>
  {#if loadErr}
    <div class="px-4 py-3 text-xs text-red-500">{loadErr}</div>
  {:else if showCode}
    <pre class="max-h-[480px] overflow-auto bg-white-200 px-4 py-3 font-mono text-xs leading-relaxed text-black-900 dark:bg-navy-800 dark:text-white-100"><code>{raw ?? ""}</code></pre>
  {:else if srcdoc}
    {#key reloadKey}
      <iframe
        bind:this={frameEl}
        {srcdoc}
        sandbox="allow-scripts"
        referrerpolicy="no-referrer"
        scrolling="no"
        title={name}
        class="block w-full"
        style="height:{height}px;border:0;overflow:hidden;background:transparent"
      ></iframe>
    {/key}
  {:else}
    <div class="px-4 py-3 text-xs text-black-600 dark:text-black-700">loading preview…</div>
  {/if}
</div>

{#if fullscreen}
  <div
    class="fixed inset-0 z-50 flex flex-col bg-black/85 backdrop-blur-sm"
    role="presentation"
    onclick={(e) => { if (e.target === e.currentTarget) fullscreen = false; }}
  >
    <div class="flex items-center justify-between gap-4 px-4 py-2 text-white-100">
      <span class="truncate text-sm">{name}</span>
      <button type="button" aria-label="Close preview" onclick={() => (fullscreen = false)} class="inline-flex h-8 w-8 items-center justify-center rounded-lg hover:bg-white-100/10">✕</button>
    </div>
    <iframe
      bind:this={fsFrameEl}
      srcdoc={raw !== null ? buildAutoHeightSrcdoc(raw, `${id}-fs`) : ""}
      sandbox="allow-scripts"
      referrerpolicy="no-referrer"
      title={name}
      class="m-4 flex-1 w-full rounded-lg"
      style="border:0;background:transparent"
    ></iframe>
  </div>
{/if}
