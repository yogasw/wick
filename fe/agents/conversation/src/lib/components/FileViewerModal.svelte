<script lang="ts">
  /* ace/monaco syntax highlighting: deferred enhancement */
  import type { FileContent } from "../types/agents.js";
  import { renderMarkdown } from "../markdown.js";

  function extOf(p: string): string {
    const i = p.lastIndexOf(".");
    return i === -1 ? "" : p.slice(i + 1).toLowerCase();
  }
  const IMAGE_EXTS = ["png", "jpg", "jpeg", "gif", "webp", "svg", "bmp", "ico"];

  type Props = {
    file: FileContent | null;
    dirty: boolean;
    onSave: (content: string) => void;
    onClose: () => void;
    downloadHref?: string;
  };

  let { file, dirty, onSave, onClose, downloadHref }: Props = $props();

  let editContent = $state("");

  $effect(() => {
    if (file) editContent = file.content ?? "";
  });

  const editable = $derived(file !== null && !file.binary && !file.tooBig);
  const ext = $derived(file ? extOf(file.path) : "");
  const isImage = $derived(IMAGE_EXTS.includes(ext));
  const isPdf = $derived(ext === "pdf");
  const isMarkdown = $derived(ext === "md" || ext === "markdown");
</script>

{#if file !== null}
  <div data-testid="file-viewer"
    class="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-sm p-4">
    <div class="w-full max-w-5xl h-[85vh] rounded-2xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-2xl flex flex-col overflow-hidden">
      <!-- Header -->
      <div class="flex items-center justify-between gap-3 px-4 py-3 border-b border-white-300 dark:border-navy-600 shrink-0">
        <div class="min-w-0 flex-1">
          <p class="text-xs font-mono text-black-900 dark:text-white-100 truncate">{file.path}</p>
        </div>
        <div class="flex items-center gap-1 shrink-0">
          {#if downloadHref}
            <a href={downloadHref} download class="inline-flex h-7 w-7 items-center justify-center rounded-lg text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800 transition-colors" title="Download">
              <svg viewBox="0 0 16 16" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="1.5">
                <path d="M8 2v8m0 0l-3-3m3 3l3-3M3 13h10" stroke-linecap="round" stroke-linejoin="round"/>
              </svg>
            </a>
          {/if}
          {#if editable}
            <button type="button" onclick={() => onSave(editContent)}
              class="inline-flex items-center gap-1 rounded-lg bg-green-500 px-2.5 py-1.5 text-[11px] font-medium text-white-100 hover:bg-green-600 transition-colors">
              <svg viewBox="0 0 12 12" class="h-3 w-3" fill="none" stroke="currentColor" stroke-width="1.5">
                <path d="M2 3a1 1 0 011-1h6l2 2v6a1 1 0 01-1 1H3a1 1 0 01-1-1V3z M4 9h4M4 2v2h3V2" stroke-linejoin="round"/>
              </svg>
              Save
            </button>
          {/if}
          <button type="button" title="Close" onclick={onClose}
            class="inline-flex h-7 w-7 items-center justify-center rounded-lg text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800 transition-colors">
            <svg viewBox="0 0 16 16" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="2">
              <path d="M4 4l8 8M12 4l-8 8" stroke-linecap="round"/>
            </svg>
          </button>
        </div>
      </div>

      <!-- Body -->
      <div class="flex-1 min-h-0 overflow-hidden">
        {#if isImage && downloadHref}
          <div class="flex items-center justify-center h-full bg-white-200 dark:bg-navy-800 p-4">
            <img src={downloadHref} alt={file.path} class="max-w-full max-h-full object-contain" />
          </div>
        {:else if isPdf && downloadHref}
          <iframe src={downloadHref} class="w-full h-full border-0" title="PDF preview"></iframe>
        {:else if isMarkdown && !file.binary && !file.tooBig}
          <div class="h-full overflow-auto prose prose-sm dark:prose-invert max-w-none p-6 text-sm text-black-900 dark:text-white-100">
            {@html renderMarkdown(file.content ?? "")}
          </div>
        {:else if file.binary}
          <div class="flex flex-col items-center justify-center h-full gap-3 px-6 text-center">
            <p class="text-sm text-black-900 dark:text-white-100">Binary file</p>
            <p class="text-xs text-black-700 dark:text-black-600">Use download to fetch the raw bytes.</p>
          </div>
        {:else if file.tooBig}
          <div class="flex flex-col items-center justify-center h-full gap-3 px-6 text-center">
            <p class="text-sm text-black-900 dark:text-white-100">File too large to preview</p>
            <p class="text-xs text-black-700 dark:text-black-600">Use download to fetch the raw bytes.</p>
          </div>
        {:else}
          <textarea
            class="w-full h-full p-4 text-xs font-mono text-black-900 dark:text-white-100 bg-white-100 dark:bg-navy-800 resize-none focus:outline-none"
            value={editContent}
            oninput={(e) => { editContent = (e.currentTarget as HTMLTextAreaElement).value; }}
          ></textarea>
        {/if}
      </div>
    </div>
  </div>
{/if}
