<script lang="ts">
  import type { Snippet } from "svelte";

  type Props = {
    onSend: (msg: { text: string; files: File[] }) => void;
    disabled?: boolean;
    placeholder?: string;
    showShiftEnterHint?: boolean;
    leadingActions?: Snippet;
  };

  let { onSend, disabled = false, placeholder = "Message…", showShiftEnterHint = false, leadingActions }: Props = $props();

  let text = $state("");
  let files: File[] = $state([]);
  let fileInputEl: HTMLInputElement | undefined = $state();
  let textareaEl: HTMLTextAreaElement | undefined = $state();

  const isDesktop = () => typeof window !== "undefined" && typeof window.matchMedia === "function" && window.matchMedia("(pointer: fine)").matches;

  function focusTextarea() {
    textareaEl?.focus();
  }

  $effect(() => {
    if (textareaEl && isDesktop()) {
      textareaEl.focus();
    }
  });

  $effect(() => {
    if (!isDesktop()) return;

    function onGlobalKeydown(e: KeyboardEvent) {
      if (!textareaEl) return;
      // ignore modifier-only, ctrl/meta combos, special keys
      if (e.ctrlKey || e.metaKey || e.altKey) return;
      if (e.key.length !== 1) return;
      const active = document.activeElement;
      if (active === textareaEl) return;
      if (active instanceof HTMLInputElement || active instanceof HTMLTextAreaElement || (active as HTMLElement)?.isContentEditable) return;
      textareaEl.focus();
    }

    window.addEventListener("keydown", onGlobalKeydown);
    return () => window.removeEventListener("keydown", onGlobalKeydown);
  });

  const canSend = $derived(!disabled && (text.trim().length > 0 || files.length > 0));

  function autoResize() {
    if (!textareaEl) return;
    textareaEl.style.height = "auto";
    textareaEl.style.height = Math.min(textareaEl.scrollHeight, 160) + "px";
  }

  function doSend() {
    if (!canSend) return;
    onSend({ text: text.trim(), files: [...files] });
    text = "";
    files = [];
    // reset height after clear
    if (textareaEl) {
      textareaEl.style.height = "auto";
    }
  }

  function handleKeyDown(e: KeyboardEvent) {
    if (e.key === "Enter" && !e.shiftKey && !e.ctrlKey && !e.metaKey) {
      e.preventDefault();
      doSend();
    }
  }

  function handleFileChange(e: Event) {
    const input = e.currentTarget as HTMLInputElement;
    if (!input.files) return;
    const added = Array.from(input.files);
    files = [...files, ...added];
    input.value = "";
  }

  function removeFile(index: number) {
    files = files.filter((_, i) => i !== index);
  }

  function handleDrop(e: DragEvent) {
    e.preventDefault();
    if (!e.dataTransfer) return;
    const dropped = Array.from(e.dataTransfer.files);
    if (dropped.length > 0) files = [...files, ...dropped];
  }

  function handlePaste(e: ClipboardEvent) {
    if (!e.clipboardData) return;
    const items = Array.from(e.clipboardData.items);
    const pasted = items
      .filter((item) => item.kind === "file")
      .map((item) => item.getAsFile())
      .filter((f): f is File => f !== null);
    if (pasted.length > 0) files = [...files, ...pasted];
  }
</script>

<div
  role="region"
  aria-label="Message composer"
  class="flex flex-col rounded-2xl border border-white-300 bg-white-100 dark:border-navy-600 dark:bg-navy-800 shadow-md"
  data-composer-drop
  ondragover={(e) => e.preventDefault()}
  ondrop={handleDrop}
  onclick={(e) => {
    const t = e.target as HTMLElement;
    if (t.tagName !== "BUTTON" && t.tagName !== "INPUT" && t.tagName !== "TEXTAREA" && !t.closest("button") && !t.closest("input")) {
      focusTextarea();
    }
  }}
>
  {#if files.length > 0}
    <div class="flex flex-wrap gap-1.5 px-3 pt-3">
      {#each files as file, i}
        <span class="inline-flex items-center gap-1 rounded-lg border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-2 py-1 text-xs text-black-900 dark:text-white-100">
          <span class="truncate max-w-[160px]">{file.name}</span>
          <button
            type="button"
            aria-label={`Remove ${file.name}`}
            class="shrink-0 text-black-500 hover:text-black-900 dark:text-black-600 dark:hover:text-white-100"
            onclick={() => removeFile(i)}
          >×</button>
        </span>
      {/each}
    </div>
  {/if}

  <textarea
    class="w-full resize-none bg-transparent px-4 pt-3 pb-2 text-sm text-black-900 dark:text-white-100 placeholder:text-black-600 dark:placeholder:text-black-700 outline-none leading-relaxed"
    style="overflow-y: auto; height: 43px;"
    rows="1"
    {placeholder}
    bind:this={textareaEl}
    bind:value={text}
    onkeydown={handleKeyDown}
    onpaste={handlePaste}
    oninput={autoResize}
  ></textarea>

  <div class="flex items-center justify-between px-3 pb-3 pt-2">
    <div class="flex items-center gap-2">
      <button
        type="button"
        aria-label="Attach files"
        class="inline-flex items-center justify-center h-7 w-7 rounded-lg border border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-700 text-black-700 dark:text-black-600 hover:bg-white-300 dark:hover:bg-navy-600 transition-colors"
        onclick={() => fileInputEl?.click()}
      >
        <svg viewBox="0 0 24 24" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <path d="m21.44 11.05-9.19 9.19a6 6 0 0 1-8.49-8.49l8.57-8.57A4 4 0 1 1 18 7.84l-8.59 8.57a2 2 0 0 1-2.83-2.83l8.49-8.48"></path>
        </svg>
      </button>
      {#if leadingActions}
        {@render leadingActions()}
      {/if}
    </div>

    <input
      bind:this={fileInputEl}
      type="file"
      multiple
      class="hidden"
      onchange={handleFileChange}
      aria-label="File attachment picker"
    />

    <div class="flex items-center gap-2">
      {#if showShiftEnterHint}
        <span class="hidden sm:block text-[10px] text-black-600 dark:text-black-700">Shift+Enter for newline</span>
      {/if}
      <button
        type="button"
        aria-label="Send"
        disabled={!canSend}
        class="flex items-center justify-center h-8 w-8 rounded-xl transition-colors disabled:opacity-40 disabled:cursor-not-allowed
          {canSend
            ? 'bg-green-500 text-white-100 hover:bg-green-600 active:bg-green-700'
            : 'bg-green-500 text-white-100'}"
        onclick={doSend}
      >
        <svg viewBox="0 0 16 16" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M13 8L3 3l2.5 5L3 13l10-5z" stroke-linecap="round" stroke-linejoin="round"></path>
        </svg>
      </button>
    </div>
  </div>
</div>
