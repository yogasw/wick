<script lang="ts">
  /* The single HTML-artifact preview used by BOTH render paths:
       - file artifacts in the gallery (pass `url` — content is fetched), and
       - inline HTML the model emitted in the message body (pass `src`).
     It renders a borderless, auto-height iframe (no inner scrollbar — it grows
     to its content via the height reporter) with a floating ⋮ menu carrying
     Full screen / Show code / Download. Self-contained fullscreen so it works
     the same whether mounted in the Svelte tree or via mount() from richRender. */
  import { KebabMenu } from "@wick-fe/common-ui";
  import { buildArtifactSrcdoc, buildAutoHeightSrcdoc } from "../richRender.js";

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

  $effect(() => {
    ensureLoaded();
    function onMsg(e: MessageEvent) {
      const d = e.data as { type?: string; id?: string; height?: number } | null;
      if (!d || d.type !== "wick-artifact-height" || d.id !== id || !d.height) return;
      height = Math.min(MAX_HEIGHT, Math.ceil(d.height));
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
    <iframe
      {srcdoc}
      sandbox="allow-scripts"
      referrerpolicy="no-referrer"
      scrolling="no"
      title={name}
      class="block w-full"
      style="height:{height}px;border:0;overflow:hidden;background:transparent"
    ></iframe>
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
      srcdoc={raw !== null ? buildArtifactSrcdoc(raw) : ""}
      sandbox="allow-scripts"
      referrerpolicy="no-referrer"
      title={name}
      class="m-4 flex-1 w-full rounded-lg"
      style="border:0;background:transparent"
    ></iframe>
  </div>
{/if}
