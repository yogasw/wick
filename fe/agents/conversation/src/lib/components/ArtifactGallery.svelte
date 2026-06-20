<script lang="ts">
  import { KebabMenu } from "@wick-fe/common-ui";
  import type { Artifact } from "../types/agents.js";
  import HtmlArtifact from "./HtmlArtifact.svelte";

  type OpenItem = { url: string; name: string; kind: Artifact["kind"] };
  type Props = { artifacts: Artifact[]; onOpen: (item: OpenItem) => void };
  let { artifacts, onOpen }: Props = $props();

  const isCarousel = $derived(artifacts.length > 4);

  function open(a: Artifact) {
    onOpen({ url: a.url, name: a.name, kind: a.kind });
  }

  /* Menu for the non-HTML doc cards (markdown/text get fullscreen + download;
     binary files just download). HTML carries its own menu inside HtmlArtifact. */
  function menuItems(a: Artifact) {
    const items: { label: string; onclick: () => void }[] = [];
    if (a.kind === "markdown" || a.kind === "text") {
      items.push({ label: "Full screen", onclick: () => open(a) });
    }
    items.push({ label: "Download", onclick: () => triggerDownload(a) });
    return items;
  }

  function triggerDownload(a: Artifact) {
    // download_url is the forced-download endpoint; fall back to the raw url
    // when a backend hasn't populated it so download never silently no-ops.
    const href = a.download_url || a.url;
    if (!href) return;
    const link = document.createElement("a");
    link.href = href;
    link.download = a.name;
    document.body.appendChild(link);
    link.click();
    link.remove();
  }
</script>

{#snippet cell(a: Artifact)}
  {#if a.kind === "image"}
    <button
      type="button"
      title={a.name}
      onclick={() => open(a)}
      class="block rounded-xl overflow-hidden border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 hover:shadow-md transition-shadow cursor-zoom-in"
    >
      <img src={a.url} alt={a.name} loading="lazy" class="block max-h-56 max-w-[240px] object-contain bg-white-200 dark:bg-navy-900" />
    </button>
  {:else if a.kind === "pdf"}
    <button
      type="button"
      title={a.name}
      onclick={() => open(a)}
      class="inline-flex items-center gap-2 rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-xs text-black-900 dark:text-white-100 hover:bg-white-200 dark:hover:bg-navy-700 transition-colors max-w-[240px]"
    >
      <span class="font-semibold text-red-500">PDF</span>
      <span class="truncate">{a.name}</span>
    </button>
  {:else if a.kind === "html"}
    <!-- Shared HTML artifact component (same one richRender mounts for inline
         HTML blocks): borderless auto-height preview + ⋮ menu. -->
    <div data-html-artifact class="w-full">
      <HtmlArtifact url={a.url} downloadUrl={a.download_url} name={a.name} />
    </div>
  {:else if a.kind === "markdown" || a.kind === "text"}
    {@render docCard(a)}
  {:else}
    {@render docCard(a)}
  {/if}
{/snippet}

{#snippet docCard(a: Artifact)}
  <!-- File-style card: icon + name + type, plus a ⋮ menu (Full screen / Download).
       Clicking the body opens the fullscreen viewer for previewable kinds. -->
  {@const previewable = a.kind === "markdown" || a.kind === "text"}
  <div class="flex items-center gap-3 rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2.5 w-full max-w-[420px]">
    <button type="button" onclick={() => (previewable ? open(a) : triggerDownload(a))} title={a.name} class="flex min-w-0 flex-1 items-center gap-3 text-left">
      <span class="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg border border-white-300 dark:border-navy-600 text-black-600 dark:text-black-700">
        <svg viewBox="0 0 16 16" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="1.5">
          <path d="M9 2H4a1 1 0 00-1 1v10a1 1 0 001 1h8a1 1 0 001-1V6L9 2z" stroke-linejoin="round"></path>
          <path d="M9 2v4h4" stroke-linejoin="round"></path>
        </svg>
      </span>
      <span class="min-w-0">
        <span class="block truncate text-sm font-medium text-black-900 dark:text-white-100">{a.name}</span>
        <span class="block text-[11px] uppercase tracking-wide text-black-600 dark:text-black-700">{a.kind === "markdown" ? "Document · MD" : a.kind === "text" ? "Document · Text" : "File"}</span>
      </span>
    </button>
    <div class="shrink-0">
      <KebabMenu ariaLabel={`Actions for ${a.name}`} items={menuItems(a)} />
    </div>
  </div>
{/snippet}

{#if isCarousel}
  <div data-gallery-carousel class="flex gap-2 overflow-x-auto pb-1 snap-x">
    {#each artifacts as a (a.path)}
      <div class="snap-start shrink-0">{@render cell(a)}</div>
    {/each}
  </div>
{:else}
  <div data-gallery-grid class="flex flex-col items-center gap-2">
    {#each artifacts as a (a.path)}
      {@render cell(a)}
    {/each}
  </div>
{/if}
