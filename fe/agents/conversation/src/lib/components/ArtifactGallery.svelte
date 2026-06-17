<script lang="ts">
  import type { Artifact } from "../types/agents.js";
  import { buildArtifactSrcdoc } from "../richRender.js";

  type OpenItem = { url: string; name: string; kind: Artifact["kind"] };
  type Props = { artifacts: Artifact[]; onOpen: (item: OpenItem) => void };
  let { artifacts, onOpen }: Props = $props();

  const isCarousel = $derived(artifacts.length > 4);
  let htmlSrcdoc = $state<Record<string, string>>({});

  async function loadHtml(a: Artifact) {
    if (htmlSrcdoc[a.path] !== undefined) return;
    try {
      const res = await fetch(a.url);
      htmlSrcdoc[a.path] = buildArtifactSrcdoc(await res.text());
    } catch {
      htmlSrcdoc[a.path] = "";
    }
  }

  function open(a: Artifact) {
    onOpen({ url: a.url, name: a.name, kind: a.kind });
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
    <div class="rounded-xl overflow-hidden border border-white-300 dark:border-navy-600 w-full max-w-[480px]">
      <div class="flex items-center justify-between px-3 py-1 bg-white-300 dark:bg-navy-600">
        <span class="text-[10px] uppercase tracking-wide text-black-600 dark:text-black-700 truncate">{a.name}</span>
        <a href={a.download_url} class="text-[10px] text-black-500 dark:text-black-600 hover:text-black-700 dark:hover:text-black-400 transition-colors">download</a>
      </div>
      {#await loadHtml(a)}
        <div class="px-4 py-3 text-xs text-black-600 dark:text-black-700">loading preview…</div>
      {:then}
        {#if htmlSrcdoc[a.path]}
          <iframe srcdoc={htmlSrcdoc[a.path]} sandbox="allow-scripts" referrerpolicy="no-referrer" loading="lazy" title={a.name} class="w-full bg-white-100" style="height:320px;border:0"></iframe>
        {/if}
      {/await}
    </div>
  {:else}
    <a
      href={a.download_url}
      title={a.name}
      class="inline-flex items-center gap-2 rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-xs text-black-900 dark:text-white-100 hover:bg-white-200 dark:hover:bg-navy-700 transition-colors max-w-[240px]"
    >
      <svg viewBox="0 0 16 16" class="h-4 w-4 shrink-0 text-green-500" fill="none" stroke="currentColor" stroke-width="1.5">
        <path d="M9 2H4a1 1 0 00-1 1v10a1 1 0 001 1h8a1 1 0 001-1V6L9 2z" stroke-linejoin="round"></path>
        <path d="M9 2v4h4" stroke-linejoin="round"></path>
      </svg>
      <span class="truncate">{a.name}</span>
    </a>
  {/if}
{/snippet}

{#if isCarousel}
  <div data-gallery-carousel class="flex gap-2 overflow-x-auto pb-1 snap-x">
    {#each artifacts as a (a.path)}
      <div class="snap-start shrink-0">{@render cell(a)}</div>
    {/each}
  </div>
{:else}
  <div data-gallery-grid class="flex flex-wrap gap-2">
    {#each artifacts as a (a.path)}
      {@render cell(a)}
    {/each}
  </div>
{/if}
