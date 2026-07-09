<script lang="ts">
  /* The single message composer used everywhere — the new-session page, the
     project landing, and the live session. One component so the input, toolbar,
     attachments, and `@`/`/` autocomplete stay identical across all of them.

     Everything context-specific is a prop:
       - provider/project/preset: themed toolbar dropdowns (omit → hidden)
       - notifyKey: enables the notification bell (omit → hidden)
       - mentionFiles/onSearchFiles: `@` file search (omit → `@` inert)
       - commands: `/` command menu (omit → `/` inert)
       - submitLabel: text beside the send arrow (omit → icon only) */
  import { toastOk, toastError } from "@wick-fe/common-stores";
  import Select from "./Select.svelte";
  import type { ComposerCommand, ComposerSelect } from "./composer-types.js";

  type Props = {
    onSend: (msg: { text: string; files: File[] }) => void;
    disabled?: boolean;
    placeholder?: string;
    showShiftEnterHint?: boolean;
    submitLabel?: string;
    /** Initial textarea height in rows (grows with content up to a cap). 1 = the
        compact single-line session composer; 3 = the taller new-session box. */
    minRows?: number;
    /** When false, the send button stays enabled even when empty (the caller's
        onSend validates instead) — matches the new-session page. Default true:
        disabled until there's text or a file (the live session composer). */
    requireContent?: boolean;
    /** localStorage key for the notification-bell preference; omit to hide the bell. */
    notifyKey?: string;
    provider?: ComposerSelect;
    project?: ComposerSelect;
    preset?: ComposerSelect;
    /** `@` mention: client-side fallback list used only when onSearchFiles is absent. */
    mentionFiles?: string[];
    /** `@` mention: backend search; when set it drives the menu (fresh per keystroke). */
    onSearchFiles?: (query: string) => Promise<string[]>;
    /** `/` command menu entries (built-in actions + skills). */
    commands?: ComposerCommand[];
  };

  let {
    onSend,
    disabled = false,
    placeholder = "Message…",
    showShiftEnterHint = false,
    submitLabel,
    minRows = 1,
    requireContent = true,
    notifyKey,
    provider,
    project,
    preset,
    mentionFiles = [],
    onSearchFiles,
    commands = [],
  }: Props = $props();

  let text = $state("");
  let files: File[] = $state([]);
  let fileInputEl: HTMLInputElement | undefined = $state();
  let textareaEl: HTMLTextAreaElement | undefined = $state();

  /* ── notification bell ──────────────────────────────────────────────── */
  let notifyOn = $state(
    !!notifyKey && typeof localStorage !== "undefined" && localStorage.getItem(notifyKey) === "true",
  );
  let bellDenied = $state(
    typeof Notification !== "undefined" && Notification.permission === "denied",
  );

  async function handleBellClick() {
    if (typeof Notification === "undefined" || !notifyKey) return;
    if (notifyOn) {
      notifyOn = false;
      try { localStorage.setItem(notifyKey, "false"); } catch { /* blocked */ }
      toastOk("Notifications muted");
      return;
    }
    if (Notification.permission === "denied") {
      bellDenied = true;
      toastError("Notifications blocked — enable them in your browser settings");
      return;
    }
    if (Notification.permission === "default") {
      const perm = await Notification.requestPermission();
      if (perm !== "granted") {
        bellDenied = perm === "denied";
        toastError("Notifications blocked — enable them in your browser settings");
        return;
      }
    }
    notifyOn = true;
    bellDenied = false;
    try { localStorage.setItem(notifyKey, "true"); } catch { /* blocked */ }
    toastOk("Notifications enabled");
  }

  /* ── @-mention / slash-command autocomplete ─────────────────────────────
     Typing `@` (at start or after whitespace) opens a file picker; typing `/`
     at the very start opens the command menu. A dedicated search input in the
     popup owns the query so spaces work ("a b"); the `@…`/`/…` in the textarea
     is a placeholder replaced on select. */
  type MenuItem = ComposerCommand; // files use {value,label}; commands add category/run
  let menuOpen = $state(false);
  let menuKind = $state<"@" | "/" | null>(null);
  let menuQuery = $state("");
  let menuTriggerPos = $state(0);
  let menuTextEnd = $state(0);
  let menuIndex = $state(0);
  let searchInputEl: HTMLInputElement | undefined = $state();
  let menuEl: HTMLDivElement | undefined = $state();

  let fileResults = $state<string[]>([]);
  let searchSeq = 0;

  $effect(() => {
    if (!menuOpen || menuKind !== "@" || !onSearchFiles) return;
    const q = menuQuery;
    const seq = ++searchSeq;
    const t = setTimeout(() => {
      onSearchFiles(q)
        .then((res) => { if (seq === searchSeq) fileResults = res; })
        .catch(() => { if (seq === searchSeq) fileResults = []; });
    }, 120);
    return () => clearTimeout(t);
  });

  const filtered = $derived.by<MenuItem[]>(() => {
    if (!menuOpen || !menuKind) return [];
    if (menuKind === "/") {
      const q = menuQuery.toLowerCase();
      const matches = q
        ? commands.filter((i) => i.value.toLowerCase().includes(q) || i.label.toLowerCase().includes(q))
        : commands;
      return matches.slice(0, 50);
    }
    if (onSearchFiles) return fileResults.slice(0, 50).map((p) => ({ value: p, label: p }));
    const terms = menuQuery.toLowerCase().split(/\s+/).filter(Boolean);
    const scored: { item: MenuItem; score: number }[] = [];
    for (const p of mentionFiles) {
      const s = scoreFile(p, terms);
      if (s !== null) scored.push({ item: { value: p, label: p }, score: s });
    }
    scored.sort((a, b) => a.score - b.score);
    return scored.slice(0, 50).map((s) => s.item);
  });

  $effect(() => {
    void filtered.length;
    menuIndex = 0;
  });

  // Click outside closes the popup. Capture phase so a parent that
  // stopPropagation()s mousedown can't swallow it.
  $effect(() => {
    if (!menuOpen) return;
    function onDown(e: MouseEvent) {
      const target = e.target as Node;
      if (menuEl && menuEl.contains(target)) return;
      if (textareaEl && textareaEl.contains(target)) return;
      closeMenu();
    }
    window.addEventListener("mousedown", onDown, true);
    return () => window.removeEventListener("mousedown", onDown, true);
  });

  function detectTrigger(before: string): { kind: "@" | "/"; query: string; pos: number } | null {
    const slash = /^\/(\S*)$/.exec(before);
    if (slash) return { kind: "/", query: slash[1], pos: 0 };
    const at = /(?:^|\s)@(\S[^\n]*|)$/.exec(before);
    if (at) return { kind: "@", query: at[1], pos: before.length - at[1].length - 1 };
    return null;
  }

  function scoreFile(path: string, terms: string[]): number | null {
    const p = path.toLowerCase();
    let score = 0;
    for (const t of terms) {
      const idx = p.indexOf(t);
      if (idx === -1) return null;
      score += idx;
    }
    if (terms.length) {
      const base = p.slice(p.lastIndexOf("/") + 1);
      if (base.includes(terms[terms.length - 1])) score -= 1000;
    }
    return score + p.length;
  }

  function refreshMenu() {
    const caret = textareaEl?.selectionStart ?? text.length;
    const t = detectTrigger(text.slice(0, caret));
    if (!t) { closeMenu(); return; }
    const hasSource = t.kind === "@" ? (!!onSearchFiles || mentionFiles.length > 0) : commands.length > 0;
    if (!hasSource) { closeMenu(); return; }
    const wasClosed = !menuOpen;
    menuKind = t.kind;
    menuTriggerPos = t.pos;
    menuTextEnd = caret;
    menuQuery = t.query;
    if (wasClosed) {
      menuIndex = 0;
      menuOpen = true;
      queueMicrotask(() => searchInputEl?.focus());
    }
  }

  function closeMenu() {
    menuOpen = false;
    menuKind = null;
  }

  function selectItem(item: MenuItem | undefined) {
    if (!menuKind || !item) return;
    const prefix = text.slice(0, menuTriggerPos);
    const suffix = text.slice(menuTextEnd);
    if (item.run) {
      text = prefix + suffix;
      closeMenu();
      queueMicrotask(() => {
        textareaEl?.focus();
        textareaEl?.setSelectionRange(prefix.length, prefix.length);
        autoResize();
      });
      item.run();
      return;
    }
    const token = menuKind + item.value + " ";
    text = prefix + token + suffix;
    closeMenu();
    const nextCaret = (prefix + token).length;
    queueMicrotask(() => {
      textareaEl?.focus();
      textareaEl?.setSelectionRange(nextCaret, nextCaret);
      autoResize();
    });
  }

  function handleMenuKeys(e: KeyboardEvent): boolean {
    if (!menuOpen) return false;
    if (e.key === "Escape") { e.preventDefault(); closeMenu(); textareaEl?.focus(); return true; }
    if (filtered.length === 0) return false;
    if (e.key === "ArrowDown") { e.preventDefault(); menuIndex = (menuIndex + 1) % filtered.length; return true; }
    if (e.key === "ArrowUp") { e.preventDefault(); menuIndex = (menuIndex - 1 + filtered.length) % filtered.length; return true; }
    if (e.key === "Enter" || e.key === "Tab") { e.preventDefault(); selectItem(filtered[menuIndex]); return true; }
    return false;
  }

  const isDesktop = () => typeof window !== "undefined" && typeof window.matchMedia === "function" && window.matchMedia("(pointer: fine)").matches;

  function focusTextarea() {
    textareaEl?.focus();
  }

  $effect(() => {
    if (textareaEl && isDesktop()) textareaEl.focus();
  });

  $effect(() => {
    if (!isDesktop()) return;
    function onGlobalKeydown(e: KeyboardEvent) {
      if (!textareaEl) return;
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

  const canSend = $derived(!disabled && (!requireContent || text.trim().length > 0 || files.length > 0));

  const minHeightPx = $derived(minRows > 1 ? minRows * 22 + 16 : 43);
  const MAX_HEIGHT = 240;

  function autoResize() {
    if (!textareaEl) return;
    textareaEl.style.height = "auto";
    textareaEl.style.height = Math.max(minHeightPx, Math.min(textareaEl.scrollHeight, MAX_HEIGHT)) + "px";
  }

  function doSend() {
    if (!canSend) return;
    onSend({ text: text.trim(), files: [...files] });
    text = "";
    files = [];
    closeMenu();
    if (textareaEl) textareaEl.style.height = `${minHeightPx}px`;
  }

  function handleKeyDown(e: KeyboardEvent) {
    if (handleMenuKeys(e)) return;
    if (e.key === "Enter" && !e.shiftKey && !e.ctrlKey && !e.metaKey) {
      e.preventDefault();
      doSend();
    }
  }

  function handleInput() {
    autoResize();
    refreshMenu();
  }

  function handleFileChange(e: Event) {
    const input = e.currentTarget as HTMLInputElement;
    if (!input.files) return;
    files = [...files, ...Array.from(input.files)];
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
    const pasted = Array.from(e.clipboardData.items)
      .filter((item) => item.kind === "file")
      .map((item) => item.getAsFile())
      .filter((f): f is File => f !== null);
    if (pasted.length > 0) files = [...files, ...pasted];
  }
</script>

<div
  role="region"
  aria-label="Message composer"
  class="relative"
  data-composer-drop
  ondragover={(e) => e.preventDefault()}
  ondrop={handleDrop}
  onclick={(e) => {
    const t = e.target as HTMLElement;
    if (t.tagName !== "BUTTON" && t.tagName !== "INPUT" && t.tagName !== "TEXTAREA" && t.tagName !== "SELECT" && !t.closest("button") && !t.closest("input") && !t.closest("select")) {
      focusTextarea();
    }
  }}
>
  {#if menuOpen}
    <!-- Popup sits OUTSIDE the overflow-hidden box (as a sibling here) so it
         isn't clipped when it renders above the composer. -->
    <div bind:this={menuEl} class="absolute bottom-full left-0 right-0 mb-2 rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 shadow-lg z-20 overflow-hidden">
      <div class="flex items-center gap-2 border-b border-white-300 dark:border-navy-600 px-3 py-2">
        <span class="text-xs font-mono text-black-500 dark:text-black-600">{menuKind}</span>
        <input
          bind:this={searchInputEl}
          bind:value={menuQuery}
          onkeydown={handleMenuKeys}
          type="text"
          class="w-full bg-transparent text-sm text-black-900 dark:text-white-100 placeholder:text-black-600 dark:placeholder:text-black-700 outline-none"
          placeholder={menuKind === "@" ? "Search files… (space-separate terms)" : "Search commands…"}
          aria-label={menuKind === "@" ? "Search files" : "Search commands"}
        />
      </div>
      {#if filtered.length > 0}
        <div class="max-h-64 overflow-y-auto" role="listbox" aria-label={menuKind === "@" ? "File mentions" : "Commands"}>
          {#each filtered as item, i (item.value)}
            {#if item.category && item.category !== filtered[i - 1]?.category}
              <div class="px-3 pt-2 pb-1 text-[10px] font-semibold uppercase tracking-wide text-black-500 dark:text-black-600">{item.category}</div>
            {/if}
            <button
              type="button"
              role="option"
              aria-selected={i === menuIndex}
              class="flex w-full items-center justify-between gap-3 px-3 py-1.5 text-left text-xs transition-colors
                {i === menuIndex
                  ? 'bg-green-500/10 text-black-900 dark:text-white-100'
                  : 'text-black-800 dark:text-white-200 hover:bg-white-200 dark:hover:bg-navy-700'}"
              onmousedown={(e) => { e.preventDefault(); selectItem(item); }}
              onmouseenter={() => (menuIndex = i)}
            >
              <span class="truncate font-mono">{item.label}</span>
              {#if item.hint}
                <span class="shrink-0 text-[10px] text-black-500 dark:text-black-600">{item.hint}</span>
              {/if}
            </button>
          {/each}
        </div>
      {:else}
        <div class="px-3 py-2 text-xs text-black-500 dark:text-black-600">No matches</div>
      {/if}
    </div>
  {/if}

  <!-- Bordered box; overflow-hidden clips the toolbar's tint to the rounded corners. -->
  <div class="flex flex-col overflow-hidden rounded-2xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-sm">
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
    class="no-scrollbar block w-full resize-none border-0 bg-transparent px-4 pb-2 pt-3.5 text-sm text-black-900 dark:text-white-100 placeholder-black-600 dark:placeholder-black-700 focus:outline-none focus:ring-0 leading-relaxed"
    style="overflow-y: auto; height: {minHeightPx}px;"
    rows={minRows}
    {placeholder}
    bind:this={textareaEl}
    bind:value={text}
    onkeydown={handleKeyDown}
    onpaste={handlePaste}
    oninput={handleInput}
    onclick={refreshMenu}
  ></textarea>

  <input
    bind:this={fileInputEl}
    type="file"
    multiple
    class="hidden"
    onchange={handleFileChange}
    aria-label="File attachment picker"
  />

  <!-- Toolbar: three zones that never wrap — fixed bell/attach, a scrollable
       dropdown strip (so long provider labels don't push send onto a 2nd row on
       mobile), and a fixed send group. -->
  <div class="flex items-center gap-2 border-t border-white-300 dark:border-navy-600 bg-white-200/60 dark:bg-navy-800/40 px-3 py-2">
    <div class="flex items-center gap-2 shrink-0">
      {#if notifyKey}
        <button
          type="button"
          aria-label="Notifications"
          title={notifyOn ? "Mute notifications" : "Enable notifications"}
          onclick={handleBellClick}
          class="relative inline-flex items-center justify-center h-7 w-7 shrink-0 rounded-lg border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-600 transition-colors"
        >
          <svg viewBox="0 0 16 16" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
            <path d="M8 2.25c-2.07 0-3.75 1.68-3.75 3.75v2.25L3 9.75v.75h10v-0.75L11.75 8.25V6c0-2.07-1.68-3.75-3.75-3.75z" stroke-linejoin="round"></path>
            <path d="M6.5 12a1.5 1.5 0 0 0 3 0" stroke-linecap="round"></path>
            {#if bellDenied}<path d="M3 3l10 10" stroke-linecap="round"></path>{/if}
          </svg>
          {#if notifyOn && !bellDenied}
            <span class="absolute -top-0.5 -right-0.5 h-2 w-2 rounded-full bg-green-500 ring-2 ring-white-100 dark:ring-navy-700" aria-hidden="true"></span>
          {/if}
        </button>
      {/if}

      <button
        type="button"
        aria-label="Attach files"
        title="Attach file"
        class="inline-flex items-center justify-center h-7 w-7 shrink-0 rounded-lg border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-600 transition-colors"
        onclick={() => fileInputEl?.click()}
      >
        <svg viewBox="0 0 24 24" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <path d="m21.44 11.05-9.19 9.19a6 6 0 0 1-8.49-8.49l8.57-8.57A4 4 0 1 1 18 7.84l-8.59 8.57a2 2 0 0 1-2.83-2.83l8.49-8.48"></path>
        </svg>
      </button>
    </div>

    {#if project || provider || preset}
      <div class="no-scrollbar flex items-center gap-2 min-w-0 flex-1 overflow-x-auto">
        {#if project}
          <Select size="sm" value={project.value} options={project.options} onChange={project.onChange} class="shrink-0" />
        {/if}
        {#if provider}
          <Select size="sm" value={provider.value} options={provider.options} onChange={provider.onChange} class="shrink-0" />
        {/if}
        {#if preset}
          <Select size="sm" value={preset.value} options={preset.options} onChange={preset.onChange} class="shrink-0" />
        {/if}
      </div>
    {/if}

    <div class="ml-auto flex items-center gap-2 shrink-0">
      {#if showShiftEnterHint}
        <span class="hidden sm:block text-[10px] text-black-600 dark:text-black-700">Shift+Enter for newline</span>
      {/if}
      <button
        type="button"
        aria-label="Send"
        disabled={!canSend}
        class="inline-flex items-center justify-center gap-1.5 rounded-lg bg-green-500 text-white-100 font-medium transition-colors hover:bg-green-600 active:bg-green-700 disabled:opacity-50 disabled:cursor-not-allowed {submitLabel ? 'px-3 py-1.5 text-xs' : 'h-8 w-8'}"
        onclick={doSend}
      >
        {#if submitLabel}<span>{submitLabel}</span>{/if}
        <svg viewBox="0 0 16 16" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="2.5">
          <path d="M2.5 8h11M9 3.5L13.5 8 9 12.5" stroke-linecap="round" stroke-linejoin="round"></path>
        </svg>
      </button>
    </div>
  </div>
  </div>
</div>

<style>
  /* Keep the textarea and the dropdown strip scrollable but hide the scrollbar
     chrome — a visible track looks broken in the compact composer. */
  .no-scrollbar {
    scrollbar-width: none; /* Firefox */
    -ms-overflow-style: none; /* old Edge/IE */
  }
  .no-scrollbar::-webkit-scrollbar {
    display: none; /* Chrome / Safari */
  }
</style>
