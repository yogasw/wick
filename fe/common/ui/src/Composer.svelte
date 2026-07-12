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
  import ImageEditor from "./ImageEditor.svelte";
  import type { ComposerCommand, ComposerSelect } from "./composer-types.js";

  type Props = {
    onSend: (msg: { text: string; files: File[] }) => void;
    disabled?: boolean;
    placeholder?: string;
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
  let listEl: HTMLDivElement | undefined = $state();
  let rootEl: HTMLDivElement | undefined = $state();
  // Where the popup opens relative to the input. Chosen on open by whichever
  // side has more room: "top" (above) for a bottom-anchored session composer,
  // "bottom" (below) for the new-session/landing composer near the top of the
  // page — otherwise it clips against the content above it.
  let menuPlacement = $state<"top" | "bottom">("top");

  // The `+` toolbar menu (attach file, notifications) — Claude-style.
  let plusOpen = $state(false);
  let plusEl: HTMLDivElement | undefined = $state();
  $effect(() => {
    if (!plusOpen) return;
    function onDown(e: MouseEvent) {
      if (plusEl && !plusEl.contains(e.target as Node)) plusOpen = false;
    }
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") plusOpen = false;
    }
    window.addEventListener("mousedown", onDown, true);
    window.addEventListener("keydown", onKey);
    return () => {
      window.removeEventListener("mousedown", onDown, true);
      window.removeEventListener("keydown", onKey);
    };
  });

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

  // Keep the arrow-highlighted row visible — scroll it into the list's view so
  // keyboard nav doesn't require the mouse. `nearest` avoids jumping the page.
  $effect(() => {
    void menuIndex;
    if (!menuOpen || !listEl) return;
    const active = listEl.querySelector<HTMLElement>('[aria-selected="true"]');
    active?.scrollIntoView?.({ block: "nearest" });
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
    // `/` and `@` both fire at the start of the line OR right after whitespace,
    // so a command/mention can be inserted mid-message, not just as a prefix.
    const slash = /(?:^|\s)\/(\S*)$/.exec(before);
    if (slash) return { kind: "/", query: slash[1], pos: before.length - slash[1].length - 1 };
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
      // Open toward whichever side has more room so the popup doesn't clip.
      if (rootEl) {
        const r = rootEl.getBoundingClientRect();
        menuPlacement = window.innerHeight - r.bottom > r.top ? "bottom" : "top";
      }
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

  // `+` menu → "Add context": drop an `@` at the caret and open the file search.
  function addMention() {
    plusOpen = false;
    const sep = text && !text.endsWith(" ") ? " " : "";
    text = text + sep + "@";
    queueMicrotask(() => {
      textareaEl?.focus();
      const end = text.length;
      textareaEl?.setSelectionRange(end, end);
      autoResize();
      refreshMenu();
    });
  }

  const hasMentionSource = $derived(!!onSearchFiles || mentionFiles.length > 0);

  // Pick above/below by whichever side of the composer has more room, so a
  // toolbar menu doesn't clip when the composer sits high on the page.
  function computePlacement() {
    if (!rootEl) return;
    const r = rootEl.getBoundingClientRect();
    menuPlacement = window.innerHeight - r.bottom > r.top ? "bottom" : "top";
  }

  function togglePlus() {
    if (plusOpen) { plusOpen = false; return; }
    plusView = "root";
    computePlacement();
    plusOpen = true;
  }

  // Toolbar chips open the + menu straight at the matching drill-in.
  function openProjectPicker() {
    plusView = "project";
    computePlacement();
    plusOpen = true;
  }
  function openProviderPicker() {
    plusView = "provider";
    computePlacement();
    plusOpen = true;
  }

  /* ── screenshot + image editor ──────────────────────────────────────── */
  const canScreenshot = typeof navigator !== "undefined" && !!navigator.mediaDevices?.getDisplayMedia;
  let editorOpen = $state(false);
  let editorSrc = $state("");
  let editorName = $state("image.png");
  let editorTarget = $state(-1); // index in files to replace; -1 = add new

  function isImage(f: File): boolean {
    return f.type.startsWith("image/");
  }
  function openEditor(src: string, name: string, target: number) {
    editorSrc = src;
    editorName = name;
    editorTarget = target;
    editorOpen = true;
  }
  function editImageAt(i: number) {
    const f = files[i];
    if (!f) return;
    const reader = new FileReader();
    reader.onload = () => openEditor(String(reader.result), f.name, i);
    reader.readAsDataURL(f);
  }
  function onEditorDone(file: File) {
    if (editorTarget >= 0) files = files.map((f, i) => (i === editorTarget ? file : f));
    else files = [...files, file];
    editorOpen = false;
  }
  async function takeScreenshot() {
    plusOpen = false;
    if (!canScreenshot) return;
    try {
      const stream = await navigator.mediaDevices.getDisplayMedia({ video: true });
      const video = document.createElement("video");
      video.srcObject = stream;
      await video.play();
      await new Promise((r) => requestAnimationFrame(r));
      const c = document.createElement("canvas");
      c.width = video.videoWidth;
      c.height = video.videoHeight;
      c.getContext("2d")?.drawImage(video, 0, 0);
      stream.getTracks().forEach((t) => t.stop());
      openEditor(c.toDataURL("image/png"), "screenshot.png", -1);
    } catch {
      /* user cancelled the picker, or capture unsupported */
    }
  }

  // The `+` menu is a small hub: a root list, plus drill-in views for the
  // provider/project/preset selectors (everything except + and the bell lives
  // here). `plusView` tracks which view is shown.
  type PlusView = "root" | "provider" | "project" | "preset";
  let plusView = $state<PlusView>("root");
  const plusSelect = $derived(
    plusView === "provider" ? provider : plusView === "project" ? project : plusView === "preset" ? preset : undefined,
  );
  function selLabel(s: ComposerSelect | undefined): string {
    if (!s) return "";
    return s.options.find((o) => o.value === s.value)?.label ?? "";
  }
  // Badge of the currently-selected option (e.g. "AI Router"), or "".
  function selBadge(s: ComposerSelect | undefined): string {
    if (!s) return "";
    return s.options.find((o) => o.value === s.value)?.badge ?? "";
  }
  // A selector is "active" when it has a real value (not the default/empty one).
  function isActive(s: ComposerSelect | undefined): boolean {
    return !!s && !!s.value;
  }
  // Provider brand from the "type/name" value: claude / codex / gemini / other.
  function provType(value: string): "claude" | "codex" | "gemini" | "other" {
    const t = (value.split("/")[0] || "").toLowerCase();
    if (t.includes("claude")) return "claude";
    if (t.includes("codex") || t.includes("openai")) return "codex";
    if (t.includes("gemini")) return "gemini";
    return "other";
  }

  // The `/` toolbar button: same command menu as typing `/`. Prefix a `/` at the
  // start (if missing) and open the menu.
  function openSlashMenu() {
    if (!text.startsWith("/")) text = "/" + text;
    queueMicrotask(() => {
      textareaEl?.focus();
      textareaEl?.setSelectionRange(1, 1);
      refreshMenu();
    });
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

{#snippet provIcon(value: string, cls: string)}
  {@const t = provType(value)}
  {#if t === "claude" || t === "gemini"}
    <!-- Multicolor brand marks served statically from /public/img/providers (embedded). -->
    <img
      src={`/public/img/providers/${t}.svg`}
      alt=""
      aria-hidden="true"
      draggable="false"
      class={`${cls} object-contain`}
    />
  {:else if t === "codex"}
    <!-- OpenAI mark is monochrome: two static files toggled by the app's `.dark`
         class so it follows the in-app theme, not the OS-only prefers-color-scheme
         an <img> SVG would otherwise read. -->
    <img src="/public/img/providers/codex.svg" alt="" aria-hidden="true" draggable="false" class={`${cls} object-contain dark:hidden`} />
    <img src="/public/img/providers/codex-dark.svg" alt="" aria-hidden="true" draggable="false" class={`${cls} object-contain hidden dark:block`} />
  {:else}
    <svg class={cls} viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><circle cx="8" cy="5.5" r="2.5"/><path d="M3.5 13a4.5 4.5 0 019 0" stroke-linecap="round"/></svg>
  {/if}
{/snippet}

<div
  bind:this={rootEl}
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
    <div bind:this={menuEl} class="absolute left-0 right-0 z-20 rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 shadow-lg overflow-hidden {menuPlacement === 'top' ? 'bottom-full mb-2' : 'top-full mt-2'}">
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
        <div bind:this={listEl} class="max-h-64 overflow-y-auto" role="listbox" aria-label={menuKind === "@" ? "File mentions" : "Commands"}>
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
                  ? 'bg-green-500/10 text-slate-800 dark:text-white-100'
                  : 'text-black-800 dark:text-white-200 hover:bg-white-200 dark:hover:bg-navy-700'}"
              onmousedown={(e) => { e.preventDefault(); selectItem(item); }}
              onmouseenter={() => (menuIndex = i)}
            >
              <!-- `/` command menu: fixed-width name column so every hint lines
                   up in a straight second column (esp. skills). `@` file mentions
                   have no hint — let the filename use the full row instead. -->
              <span class="truncate font-mono {menuKind === '/' ? 'w-36 sm:w-44 shrink-0' : ''}">{item.label}</span>
              {#if item.hint}
                <span class="min-w-0 flex-1 truncate text-[10px] text-black-500 dark:text-black-600">{item.hint}</span>
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
  <div class="flex flex-col rounded-2xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-sm">
  {#if files.length > 0}
    <div class="flex flex-wrap gap-1.5 px-3 pt-3">
      {#each files as file, i}
        <span class="inline-flex items-center gap-1 rounded-lg border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-2 py-1 text-xs text-black-900 dark:text-white-100">
          <span class="truncate max-w-[160px]">{file.name}</span>
          {#if isImage(file)}
            <button
              type="button"
              aria-label={`Edit ${file.name}`}
              title="Edit image"
              class="shrink-0 text-black-500 hover:text-green-600 dark:text-black-600 dark:hover:text-green-400"
              onclick={() => editImageAt(i)}
            >
              <svg viewBox="0 0 16 16" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
                <path d="M11.5 2.5l2 2L6 12l-2.5.5L4 10l7.5-7.5z" stroke-linecap="round" stroke-linejoin="round"></path>
              </svg>
            </button>
          {/if}
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

  <!-- Toolbar: everything lives in the + menu (attach, context, commands,
       provider/project/preset) except the + button and the notification bell.
       Right side is just the send button. -->
  <div class="flex items-center gap-2 rounded-b-2xl border-t border-white-300 dark:border-navy-600 bg-white-200/60 dark:bg-navy-800/40 px-3 py-2">
    <!-- + hub menu -->
    <div bind:this={plusEl} class="relative shrink-0">
      <button
        type="button"
        aria-label="Add"
        title="Attach, context, commands, provider/project/preset"
        onclick={togglePlus}
        class="inline-flex items-center justify-center h-8 w-8 rounded-lg border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-600 transition-colors"
      >
        <svg viewBox="0 0 16 16" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="1.75"><path d="M8 3v10M3 8h10" stroke-linecap="round"/></svg>
      </button>
      {#if plusOpen}
        <div class="absolute left-0 z-30 min-w-[240px] max-h-80 overflow-y-auto rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 shadow-lg py-1 {menuPlacement === 'top' ? 'bottom-full mb-2' : 'top-full mt-2'}">
          {#if plusView === "root"}
            <button
              type="button"
              onclick={() => { plusOpen = false; fileInputEl?.click(); }}
              class="flex w-full items-center gap-2.5 px-3 py-2 text-left text-sm text-slate-800 dark:text-white-100 hover:bg-white-200 dark:hover:bg-navy-700 transition-colors"
            >
              <svg viewBox="0 0 24 24" class="h-4 w-4 shrink-0 text-black-800 dark:text-black-600" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="m21.44 11.05-9.19 9.19a6 6 0 0 1-8.49-8.49l8.57-8.57A4 4 0 1 1 18 7.84l-8.59 8.57a2 2 0 0 1-2.83-2.83l8.49-8.48"/></svg>
              Attach file or photo
            </button>
            {#if canScreenshot}
              <button
                type="button"
                onclick={takeScreenshot}
                class="flex w-full items-center gap-2.5 px-3 py-2 text-left text-sm text-slate-800 dark:text-white-100 hover:bg-white-200 dark:hover:bg-navy-700 transition-colors"
              >
                <svg viewBox="0 0 24 24" class="h-4 w-4 shrink-0 text-black-800 dark:text-black-600" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="5" width="18" height="14" rx="2"/><circle cx="12" cy="12" r="3"/><path d="M8 5l1.5-2h5L16 5"/></svg>
                Take screenshot
              </button>
            {/if}
            {#if hasMentionSource}
              <button
                type="button"
                onclick={addMention}
                class="flex w-full items-center gap-2.5 px-3 py-2 text-left text-sm text-slate-800 dark:text-white-100 hover:bg-white-200 dark:hover:bg-navy-700 transition-colors"
              >
                <svg viewBox="0 0 16 16" class="h-4 w-4 shrink-0 text-black-800 dark:text-black-600" fill="none" stroke="currentColor" stroke-width="1.5"><circle cx="8" cy="8" r="3"/><path d="M11 8v1.5a2 2 0 0 0 4 0V8a7 7 0 1 0-2.8 5.6" stroke-linecap="round"/></svg>
                Add context (@)
              </button>
            {/if}
            {#if commands.length > 0}
              <button
                type="button"
                onclick={() => { plusOpen = false; openSlashMenu(); }}
                class="flex w-full items-center gap-2.5 px-3 py-2 text-left text-sm text-slate-800 dark:text-white-100 hover:bg-white-200 dark:hover:bg-navy-700 transition-colors"
              >
                <span class="inline-flex h-4 w-4 shrink-0 items-center justify-center font-mono text-black-800 dark:text-black-600">/</span>
                Commands
              </button>
            {/if}

            {#if project || provider || preset}
              <div class="my-1 border-t border-white-300 dark:border-navy-600"></div>
            {/if}
            {#each [{ key: "project", sel: project, label: "Project" }, { key: "provider", sel: provider, label: "Provider" }, { key: "preset", sel: preset, label: "Preset" }] as row (row.key)}
              {#if row.sel}
                {@const iconCls = `h-4 w-4 shrink-0 ${isActive(row.sel) ? "text-green-600 dark:text-green-400" : "text-black-800 dark:text-black-600"}`}
                <button
                  type="button"
                  onclick={() => (plusView = row.key as PlusView)}
                  class="flex w-full items-center justify-between gap-3 px-3 py-2 text-left text-sm text-slate-800 dark:text-white-100 hover:bg-white-200 dark:hover:bg-navy-700 transition-colors"
                >
                  <span class="flex items-center gap-2 min-w-0">
                    {#if row.key === "project"}
                      <svg viewBox="0 0 16 16" class={iconCls} fill="none" stroke="currentColor" stroke-width="1.5"><path d="M2 4a1 1 0 011-1h3l1.5 1.5H13a1 1 0 011 1V12a1 1 0 01-1 1H3a1 1 0 01-1-1V4z" stroke-linejoin="round"/></svg>
                    {:else if row.key === "provider"}
                      {@render provIcon(row.sel.value, iconCls)}
                    {:else}
                      <svg viewBox="0 0 16 16" class={iconCls} fill="none" stroke="currentColor" stroke-width="1.5"><path d="M2 5h6M11 5h3M2 11h3M8 11h6" stroke-linecap="round"/><circle cx="9.5" cy="5" r="1.5"/><circle cx="6.5" cy="11" r="1.5"/></svg>
                    {/if}
                    <span class="text-black-800 dark:text-black-600">{row.label}</span>
                    <span class="truncate {isActive(row.sel) ? '' : 'text-black-800 dark:text-black-600'}">{selLabel(row.sel) || "—"}</span>
                  </span>
                  <svg viewBox="0 0 16 16" class="h-3.5 w-3.5 shrink-0 text-black-700 dark:text-black-600" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M6 4l4 4-4 4" stroke-linecap="round" stroke-linejoin="round"/></svg>
                </button>
              {/if}
            {/each}
          {:else if plusSelect}
            {@const sel = plusSelect}
            <button
              type="button"
              onclick={() => (plusView = "root")}
              class="flex w-full items-center gap-2 px-3 py-2 text-left text-xs font-semibold text-black-800 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-700 transition-colors"
            >
              <svg viewBox="0 0 16 16" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M10 4L6 8l4 4" stroke-linecap="round" stroke-linejoin="round"/></svg>
              {#if plusView === "project"}
                <svg viewBox="0 0 16 16" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M2 4a1 1 0 011-1h3l1.5 1.5H13a1 1 0 011 1V12a1 1 0 01-1 1H3a1 1 0 01-1-1V4z" stroke-linejoin="round"/></svg>
              {:else if plusView === "provider"}
                {@render provIcon(sel.value, "h-4 w-4")}
              {:else}
                <svg viewBox="0 0 16 16" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M2 5h6M11 5h3M2 11h3M8 11h6" stroke-linecap="round"/><circle cx="9.5" cy="5" r="1.5"/><circle cx="6.5" cy="11" r="1.5"/></svg>
              {/if}
              {plusView === "provider" ? "Provider" : plusView === "project" ? "Project" : "Preset"}
            </button>
            <div class="border-t border-white-300 dark:border-navy-600"></div>
            {#each sel.options as opt (opt.value)}
              <button
                type="button"
                onclick={() => { sel.onChange(opt.value); plusView = "root"; plusOpen = false; }}
                class="flex w-full items-center justify-between gap-3 px-3 py-1.5 text-left text-sm transition-colors {opt.value === sel.value ? 'bg-green-500/10 text-slate-800 dark:text-white-100' : 'text-black-800 dark:text-white-200 hover:bg-white-200 dark:hover:bg-navy-700'}"
              >
                <span class="flex items-center gap-2 min-w-0">
                  {#if plusView === "provider"}{@render provIcon(opt.value, "h-4 w-4 shrink-0")}{/if}
                  <span class="truncate">{opt.label}</span>
                  {#if opt.badge}<span class="shrink-0 rounded-full bg-green-500/10 px-1.5 py-0.5 text-[10px] font-medium text-green-600 dark:text-green-400">{opt.badge}</span>{/if}
                </span>
                {#if opt.value === sel.value}<span class="shrink-0 text-green-600 dark:text-green-400">✓</span>{/if}
              </button>
            {/each}
          {/if}
        </div>
      {/if}
    </div>

    <!-- notification bell (standalone icon) -->
    {#if notifyKey}
      <button
        type="button"
        aria-label="Notifications"
        title={bellDenied ? "Notifications blocked" : notifyOn ? "Mute notifications" : "Enable notifications"}
        onclick={handleBellClick}
        class="relative inline-flex items-center justify-center h-8 w-8 shrink-0 rounded-lg border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-600 transition-colors"
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

    <!-- active project chip (icon only) — shows ONLY when a project is set. -->
    {#if project && project.value}
      <button
        type="button"
        aria-label="Project"
        title={(selLabel(project) || "Project").replace(/^📁\s*/, "")}
        onclick={openProjectPicker}
        class="inline-flex items-center justify-center h-8 w-8 shrink-0 rounded-lg border border-green-500/40 bg-green-500/10 text-green-600 dark:text-green-400 hover:bg-green-500/20 transition-colors"
      >
        <svg viewBox="0 0 16 16" class="h-5 w-5" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M2 4a1 1 0 011-1h3l1.5 1.5H13a1 1 0 011 1V12a1 1 0 01-1 1H3a1 1 0 01-1-1V4z" stroke-linejoin="round"/></svg>
      </button>
    {/if}

    <!-- right: provider chip (Claude-style) + send -->
    <div class="ml-auto flex items-center gap-2 shrink-0">
      {#if provider}
        <button
          type="button"
          aria-label="Provider"
          title={selBadge(provider) ? `${selLabel(provider) || "Provider"} · via ${selBadge(provider)}` : selLabel(provider) || "Provider"}
          onclick={openProviderPicker}
          class="relative inline-flex items-center justify-center h-8 w-8 shrink-0 rounded-lg border border-green-500/40 bg-green-500/10 text-green-600 dark:text-green-400 hover:bg-green-500/20 transition-colors"
        >
          {@render provIcon(provider.value, "h-5 w-5")}
          {#if selBadge(provider)}<span class="absolute -top-0.5 -right-0.5 h-2 w-2 rounded-full bg-green-500 ring-2 ring-white-100 dark:ring-navy-700" aria-hidden="true"></span>{/if}
        </button>
      {/if}
      <button
        type="button"
        aria-label="Send"
        disabled={!canSend}
        class="inline-flex items-center justify-center gap-1.5 shrink-0 rounded-lg bg-green-500 text-white-100 font-medium transition-colors hover:bg-green-600 active:bg-green-700 disabled:opacity-50 disabled:cursor-not-allowed {submitLabel ? 'px-3 py-1.5 text-xs' : 'h-8 w-8'}"
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

<ImageEditor
  open={editorOpen}
  src={editorSrc}
  name={editorName}
  onDone={onEditorDone}
  onCancel={() => (editorOpen = false)}
/>

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
