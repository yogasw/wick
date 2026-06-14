<script lang="ts">
  type Props = {
    onSend: (msg: { text: string; files: File[] }) => void;
    disabled?: boolean;
    placeholder?: string;
  };

  let { onSend, disabled = false, placeholder = "Message…" }: Props = $props();

  let text = $state("");
  let files: File[] = $state([]);
  let fileInputEl: HTMLInputElement | undefined = $state();

  const canSend = $derived(!disabled && (text.trim().length > 0 || files.length > 0));

  function doSend() {
    if (!canSend) return;
    onSend({ text: text.trim(), files: [...files] });
    text = "";
    files = [];
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
  class="flex flex-col gap-2 rounded-2xl border border-white-300 bg-white-50 dark:border-navy-600 dark:bg-navy-900 p-3"
  data-composer-drop
  ondragover={(e) => e.preventDefault()}
  ondrop={handleDrop}
>
  {#if files.length > 0}
    <div class="flex flex-wrap gap-1.5">
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
    class="w-full resize-none bg-transparent text-sm text-black-900 dark:text-white-100 placeholder:text-black-400 dark:placeholder:text-black-600 outline-none"
    rows="1"
    {placeholder}
    bind:value={text}
    onkeydown={handleKeyDown}
    onpaste={handlePaste}
  ></textarea>

  <div class="flex items-center justify-between gap-2">
    <button
      type="button"
      aria-label="Attach files"
      class="rounded-lg p-1.5 text-black-500 hover:text-green-500 dark:text-black-600 dark:hover:text-green-400"
      onclick={() => fileInputEl?.click()}
    >
      <svg viewBox="0 0 16 16" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="1.5">
        <path d="M14 8.5V11a4 4 0 01-8 0V4a2.5 2.5 0 015 0v7a1 1 0 01-2 0V5" stroke-linecap="round" stroke-linejoin="round" />
      </svg>
    </button>

    <input
      bind:this={fileInputEl}
      type="file"
      multiple
      class="hidden"
      onchange={handleFileChange}
      aria-label="File attachment picker"
    />

    <button
      type="button"
      aria-label="Send"
      disabled={!canSend}
      class="rounded-xl px-4 py-1.5 text-sm font-medium transition-colors
        {canSend
          ? 'bg-green-500 text-white-100 hover:bg-green-600'
          : 'bg-white-300 text-black-400 dark:bg-navy-700 dark:text-black-600 cursor-not-allowed'}"
      onclick={doSend}
    >Send</button>
  </div>
</div>
