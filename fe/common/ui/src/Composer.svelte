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
  import type { ComposerCommand, ComposerSelect, ComposerSelectOption, ComposerModelOption } from "./composer-types.js";

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
  // Which side the popup aligns to: "left" (opened from the + button) or
  // "right" (opened from the project/provider chip on the right).
  let plusAnchor = $state<"left" | "right">("left");
  let plusEl: HTMLDivElement | undefined = $state();
  let plusMenuEl: HTMLDivElement | undefined = $state();
  $effect(() => {
    if (!plusOpen) return;
    function onDown(e: MouseEvent) {
      // Close on any click outside the popup itself — but ignore clicks on the
      // toolbar triggers (+ / chips), which own their open/close.
      const t = e.target as HTMLElement;
      if (plusMenuEl?.contains(t)) return;
      if (t.closest?.('[data-plus-trigger]')) return;
      plusOpen = false;
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
    modelDrillOpt = null;
    plusAnchor = "left"; // the + button lives at the far left
    computePlacement();
    plusOpen = true;
  }

  // Toolbar chips open the + menu straight at the matching drill-in. The
  // project + provider chips sit on the RIGHT of the toolbar, so their popup
  // right-aligns to them instead of flying out from the far-left + button.
  function openProjectPicker() {
    plusView = "project";
    plusAnchor = "right";
    computePlacement();
    plusOpen = true;
  }
  function openProviderPicker() {
    plusAnchor = "right";
    computePlacement();
    plusOpen = true;
    enterProviderView(); // jump straight to the current provider's models if it has any
  }
  // Imperative open for the parent (e.g. a `/provider` command routes here so
  // the slash menu and the toolbar chip share ONE picker — no separate modal).
  export function openProvider() {
    openProviderPicker();
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
  // Provider picker is up to 3 levels: TYPE ▸ INSTANCE ▸ MODEL, each
  // collapsing when it has only one choice.
  //   typeDrillKey  — which provider TYPE is drilled into (level 2), or "".
  //   modelDrillOpt — which INSTANCE option is drilled into for models (lvl 3).
  let typeDrillKey = $state<string>("");
  let modelDrillOpt = $state<ComposerSelectOption | null>(null);
  function closeModelDrill() {
    modelDrillOpt = null;
    modelDrillSearch = "";
    setDrill = null;
  }
  function closeTypeDrill() {
    typeDrillKey = "";
  }

  // Lazy live-model loading for the drilled provider. `modelCache` holds the
  // list keyed by option value once fetched; `modelLoading` is the set of
  // values currently in flight. Cache persists across open/close so a second
  // drill is instant. On error we simply keep the static list (no cache
  // entry), so the picker never blocks on discovery.
  let modelCache = $state<Record<string, ComposerModelOption[]>>({});
  let modelLoading = $state<Set<string>>(new Set());
  let modelDrillSearch = $state("");
  let modelDrillSearchEl = $state<HTMLInputElement | undefined>();

  // Level 4: a LIVE SET row inside the model drill was opened. setDrill holds
  // the set's model row; its expansion is cached under a per-set key so
  // reopening is instant. Back from a set returns to the provider model list.
  let setDrill = $state<ComposerModelOption | null>(null);
  function setCacheKey(optValue: string, m: ComposerModelOption): string {
    return `${optValue}::set::${m.id}`;
  }
  function isLiveSet(m: ComposerModelOption): boolean {
    return !!m.live;
  }

  // The list to render: for a live-set drill (level 4), the set's expansion;
  // otherwise the drilled provider's models (fetched or static).
  const drillBaseModels = $derived.by<ComposerModelOption[]>(() => {
    if (!modelDrillOpt) return [];
    if (setDrill) return modelCache[setCacheKey(modelDrillOpt.value, setDrill)] ?? [];
    return modelCache[modelDrillOpt.value] ?? modelDrillOpt.models ?? [];
  });
  function modelRowMatches(m: ComposerModelOption, q: string): boolean {
    const hay = `${m.id} ${m.label}`.toLowerCase();
    for (const raw of q.toLowerCase().split(/\s+/)) {
      const t = raw.trim();
      if (t === "" || t === "-" || t === "!") continue;
      const exclude = t.startsWith("-") || t.startsWith("!");
      const needle = exclude ? t.slice(1) : t;
      const hit = hay.includes(needle);
      if (exclude ? hit : !hit) return false;
    }
    return true;
  }
  const drillModels = $derived.by(() => {
    const q = modelDrillSearch.trim();
    return q ? drillBaseModels.filter((m) => modelRowMatches(m, q)) : drillBaseModels;
  });

  // ── keyboard nav for the + menu (arrow up/down + Enter) ────────────────
  // A flat list of the CURRENT view's selectable rows, each with an action to
  // fire on Enter/click. Rebuilds per view so ↑/↓ always walks what's shown.
  let plusIndex = $state(0);
  type PlusRow = { run: () => void };
  const plusRows = $derived.by<PlusRow[]>(() => {
    if (!plusOpen) return [];
    if (modelDrillOpt) {
      const drill = modelDrillOpt;
      const rows: PlusRow[] = [];
      // "Use default" row only shows at the provider level, not filtering.
      if (!setDrill && !modelDrillSearch.trim()) {
        rows.push({ run: () => { provider?.onChange(drill.value); closeModelDrill(); plusView = "root"; plusOpen = false; } });
      }
      for (const m of drillModels) {
        // A live-set row opens its expansion (level 4); a plain model selects.
        if (!setDrill && isLiveSet(m)) rows.push({ run: () => openSetDrill(m) });
        else rows.push({ run: () => selectModel(drill, m.id) });
      }
      return rows;
    }
    if (plusView === "provider" && provider) {
      if (typeDrillKey) {
        const group = providerGroups.find((g) => g.type === typeDrillKey);
        return (group?.opts ?? []).map((o) => ({ run: () => pickInstance(o) }));
      }
      return providerGroups.map((g) => ({ run: () => pickType(g) }));
    }
    if ((plusView === "project" || plusView === "preset") && plusSelect) {
      return plusSelect.options.map((o) => ({ run: () => { plusSelect.onChange(o.value); plusView = "root"; plusOpen = false; } }));
    }
    return [];
  });
  // Reset the highlight whenever the view/list changes.
  $effect(() => { void plusRows.length; void plusView; void modelDrillOpt; void setDrill; plusIndex = 0; });

  function handlePlusKeys(e: KeyboardEvent) {
    if (e.key === "Escape") { e.preventDefault(); plusOpen = false; return; }
    const n = plusRows.length;
    if (n === 0) return;
    if (e.key === "ArrowDown") { e.preventDefault(); plusIndex = (plusIndex + 1) % n; }
    else if (e.key === "ArrowUp") { e.preventDefault(); plusIndex = (plusIndex - 1 + n) % n; }
    else if (e.key === "Enter") { e.preventDefault(); plusRows[plusIndex]?.run(); }
  }

  // Kick off (or reuse) a live-model load and focus the filter box whenever we
  // drill into a provider's model list.
  function openModelDrill(o: ComposerSelectOption) {
    modelDrillSearch = "";
    setDrill = null;
    modelDrillOpt = o;
    void loadModelsFor(o);
  }
  async function loadModelsFor(o: ComposerSelectOption) {
    if (!provider?.loadModels) return; // no loader wired → static list only
    if (modelCache[o.value] || modelLoading.has(o.value)) return; // cached / in flight
    const next = new Set(modelLoading);
    next.add(o.value);
    modelLoading = next;
    try {
      const live = await provider.loadModels(o.value);
      if (live && live.length > 0) modelCache = { ...modelCache, [o.value]: live };
    } catch {
      // Non-fatal — keep the static list.
    } finally {
      const done = new Set(modelLoading);
      done.delete(o.value);
      modelLoading = done;
    }
  }

  // Level 4: open a live-set row and load its expansion (vendor list narrowed
  // by the set's filter), cached under a per-set key.
  function openSetDrill(m: ComposerModelOption) {
    // Second click on the same live set collapses it back to the model list.
    if (setDrill && setDrill.id === m.id) { closeSetDrill(); return; }
    modelDrillSearch = "";
    setDrill = m;
    void loadSetModels(m);
  }
  function closeSetDrill() { setDrill = null; modelDrillSearch = ""; }
  async function loadSetModels(m: ComposerModelOption) {
    const o = modelDrillOpt;
    if (!o || !provider?.loadModels) return;
    const key = setCacheKey(o.value, m);
    if (modelCache[key] || modelLoading.has(key)) return;
    const next = new Set(modelLoading);
    next.add(key);
    modelLoading = next;
    try {
      const live = await provider.loadModels(o.value, { entry: m.id });
      modelCache = { ...modelCache, [key]: live ?? [] };
    } catch {
      modelCache = { ...modelCache, [key]: [] };
    } finally {
      const done = new Set(modelLoading);
      done.delete(key);
      modelLoading = done;
    }
  }

  // Reload button: drop the current view's cached list and re-fetch, so a
  // stale vendor list (models added/removed since first drill) can be
  // refreshed without reopening the whole picker.
  function reloadDrill() {
    const o = modelDrillOpt;
    if (!o) return;
    const key = setDrill ? setCacheKey(o.value, setDrill) : o.value;
    const next = { ...modelCache };
    delete next[key];
    modelCache = next;
    if (setDrill) void loadSetModels(setDrill);
    else void loadModelsFor(o);
  }

  // Commit a model selection and close the whole menu. Inside a live-set
  // (setDrill), the picked vendor model is encoded as "<entryID>@<vendorID>"
  // so the backend resolves the set entry (key/kind/base) then overrides the
  // concrete model — a raw vendor id alone isn't a registered model.
  function selectModel(o: ComposerSelectOption, modelID: string) {
    const pin = setDrill ? `${setDrill.id}@${modelID}` : modelID;
    provider?.onChange(`${o.value}::${pin}`);
    closeModelDrill();
    plusView = "root";
    plusOpen = false;
  }

  $effect(() => {
    if (modelDrillOpt && modelDrillSearchEl) { modelDrillSearchEl.focus(); return; }
    // No search input in this view → focus the popup so ↑/↓/Enter land on it.
    if (plusOpen && !modelDrillOpt && plusMenuEl) plusMenuEl.focus();
  });

  // rawType extracts the raw provider TYPE segment from an option value
  // ("claude/timA" → "claude"; a bare "wick" → "wick"). Distinct from
  // provType (which normalizes to a fixed brand set for the icon).
  function rawType(value: string): string {
    const key = splitModelPin(value).key;
    const slash = key.indexOf("/");
    return slash < 0 ? key : key.slice(0, slash);
  }

  // Provider options grouped by type, preserving order. Used to render the
  // level-1 (type) list and level-2 (instances of a type) list.
  const providerGroups = $derived.by(() => {
    const groups: { type: string; opts: ComposerSelectOption[] }[] = [];
    const idx = new Map<string, number>();
    for (const o of provider?.options ?? []) {
      const t = rawType(o.value);
      let i = idx.get(t);
      if (i === undefined) {
        i = groups.length;
        idx.set(t, i);
        groups.push({ type: t, opts: [] });
      }
      groups[i].opts.push(o);
    }
    return groups;
  });

  // hasModelDrill: an option worth a 3rd (model) level. True when it already
  // ships >1 static model, OR a live loader is wired — in that case we drill
  // in and fetch the vendor list on demand, even if the option shipped with no
  // (or one) static model. Lets a provider expose its full live model list
  // from the picker without prefetching every provider up front.
  function hasModelDrill(o: ComposerSelectOption): boolean {
    if (o.models && o.models.length > 1) return true;
    return !!provider?.loadModels;
  }

  // Entering the provider view from the root menu / chip. If a provider is
  // already selected and can drill into models, jump straight to its model
  // list (fetching live models on the way) — no bouncing back through the
  // whole type list. Otherwise show the type list to choose one.
  function enterProviderView() {
    typeDrillKey = "";
    modelDrillOpt = null;
    const key = splitModelPin(provider?.value ?? "").key;
    const cur = provider?.options.find((o) => o.value === key);
    if (cur && hasModelDrill(cur)) {
      plusView = "provider";
      openModelDrill(cur);
      return;
    }
    plusView = "provider";
  }

  // Selecting a provider TYPE at level 1: if it collapses to a single flat
  // choice, apply it directly; otherwise drill into level 2 (instances) or
  // level 3 (models). Keeps every level that has only one option invisible.
  function pickType(g: { type: string; opts: ComposerSelectOption[] }) {
    if (g.opts.length === 1) {
      const only = g.opts[0];
      if (hasModelDrill(only)) { openModelDrill(only); return; }
      provider?.onChange(only.value);
      plusView = "root"; plusOpen = false;
      return;
    }
    typeDrillKey = g.type;
  }

  // Selecting an INSTANCE at level 2: drill to models if it has them, else
  // apply directly.
  function pickInstance(o: ComposerSelectOption) {
    if (hasModelDrill(o)) { openModelDrill(o); return; }
    provider?.onChange(o.value);
    typeDrillKey = ""; plusView = "root"; plusOpen = false;
  }
  // Splits a selector value into its option key and an optional pinned
  // model id — "wick/wick::m_abc123" (3rd-level model pick) vs a plain
  // "type/name" (no model pin, or a provider without a models list).
  function splitModelPin(value: string): { key: string; modelID: string } {
    const i = value.indexOf("::");
    return i < 0 ? { key: value, modelID: "" } : { key: value.slice(0, i), modelID: value.slice(i + 2) };
  }
  function selLabel(s: ComposerSelect | undefined): string {
    if (!s) return "";
    const { key, modelID } = splitModelPin(s.value);
    const opt = s.options.find((o) => o.value === key);
    if (!opt) return "";
    if (!modelID) return opt.label;
    const model = opt.models?.find((m) => m.id === modelID);
    return model ? `${opt.label} · ${model.label}` : opt.label;
  }
  // Badge of the currently-selected option (e.g. "AI Router"), or "".
  function selBadge(s: ComposerSelect | undefined): string {
    if (!s) return "";
    const { key } = splitModelPin(s.value);
    return s.options.find((o) => o.value === key)?.badge ?? "";
  }
  // A selector is "active" when it has a real value (not the default/empty one).
  function isActive(s: ComposerSelect | undefined): boolean {
    return !!s && !!s.value;
  }
  // Provider brand from the "type/name" value: claude / codex / gemini / other.
  function provType(value: string): "claude" | "codex" | "gemini" | "wick" | "other" {
    const t = (value.split("/")[0] || "").toLowerCase();
    if (t.includes("claude")) return "claude";
    if (t.includes("codex") || t.includes("openai")) return "codex";
    if (t.includes("gemini")) return "gemini";
    if (t.includes("wick")) return "wick";
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
  {#if t === "claude" || t === "gemini" || t === "wick"}
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

<!-- The click handler is a mouse-only convenience (click empty space →
     focus the textarea); keyboard users tab straight into the textarea, so
     no keyboard equivalent is needed. Drag handlers power file drops. -->
<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
<!-- svelte-ignore a11y_click_events_have_key_events -->
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
  <div bind:this={plusEl} class="relative flex items-center gap-2 rounded-b-2xl border-t border-white-300 dark:border-navy-600 bg-white-200/60 dark:bg-navy-800/40 px-3 py-2">
    <!-- + hub menu -->
    <div class="shrink-0">
      <button
        type="button"
        aria-label="Add"
        title="Attach, context, commands, provider/project/preset"
        data-plus-trigger
        onclick={togglePlus}
        class="inline-flex items-center justify-center h-8 w-8 rounded-lg border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-600 transition-colors"
      >
        <svg viewBox="0 0 16 16" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="1.75"><path d="M8 3v10M3 8h10" stroke-linecap="round"/></svg>
      </button>
      {#if plusOpen}
        <!-- Anchored to whichever control opened it: left for the + button,
             right for the provider/project chips (they sit on the right).
             onkeydown drives ↑/↓/Enter row navigation for every view. -->
        <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
        <!-- In the model drill the inner list owns the scroll (fixed header +
             scrolling rows), so the outer must NOT also scroll — otherwise you
             get two nested scrollbars. Other views scroll on the outer box. -->
        <div bind:this={plusMenuEl} role="menu" tabindex="-1" onkeydown={handlePlusKeys} class="absolute {plusAnchor === 'right' ? 'right-2' : 'left-2'} z-30 w-[min(20rem,calc(100%-1rem))] max-h-80 {modelDrillOpt ? 'overflow-hidden flex flex-col' : 'overflow-y-auto'} rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 shadow-lg py-1 {menuPlacement === 'top' ? 'bottom-full mb-2' : 'top-full mt-2'}">
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
                  onclick={() => { if (row.key === "provider") enterProviderView(); else plusView = row.key as PlusView; }}
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
          {:else if modelDrillOpt}
            {@const drill = modelDrillOpt}
            {@const inSet = setDrill}
            {@const loading = inSet ? modelLoading.has(setCacheKey(drill.value, inSet)) : modelLoading.has(drill.value)}
            {@const showDefaultRow = !inSet && !modelDrillSearch.trim()}
            <!-- Header row: back + provider name + inline search. The search
                 lives here (always visible, auto-focused) so drilling in lands
                 straight on a typeable field. In a live-set (level 4) the back
                 button returns to the provider's model list. -->
            <div class="shrink-0 flex items-center gap-2 px-2 py-2 border-b border-white-300 dark:border-navy-600">
              <button
                type="button"
                onclick={inSet ? closeSetDrill : closeModelDrill}
                aria-label={inSet ? "Back to models" : "Back to providers"}
                class="shrink-0 rounded-md p-1 text-black-800 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-700 transition-colors"
              >
                <svg viewBox="0 0 16 16" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M10 4L6 8l4 4" stroke-linecap="round" stroke-linejoin="round"/></svg>
              </button>
              {@render provIcon(drill.value, "h-4 w-4 shrink-0")}
              <input
                type="text"
                bind:this={modelDrillSearchEl}
                bind:value={modelDrillSearch}
                placeholder={inSet ? `Search ${inSet.label}…` : `Search ${drill.label} models…`}
                class="min-w-0 flex-1 bg-transparent text-sm text-black-900 dark:text-white-100 placeholder:text-black-700 outline-none"
              />
              {#if loading}
                <svg class="shrink-0 h-3.5 w-3.5 animate-spin text-black-600" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M12 3a9 9 0 1 0 9 9" stroke-linecap="round"/></svg>
              {:else if provider?.loadModels}
                <!-- Reload: drop this list's cache and re-fetch (vendor list may
                     have changed since it was first loaded). -->
                <button type="button" onclick={reloadDrill} aria-label="Refresh models" title="Refresh models" class="shrink-0 rounded-md p-1 text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-700 transition-colors">
                  <svg viewBox="0 0 24 24" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M3 12a9 9 0 1 0 3-6.7L3 8"/><path d="M3 3v5h5"/></svg>
                </button>
              {/if}
            </div>
            <div class="flex-1 min-h-0 overflow-y-auto py-1">
              <!-- "Use default" pins the provider without a specific model, so
                   a drilled-in provider is still selectable on its own. Hidden
                   inside a live-set (level 4). -->
              {#if showDefaultRow}
                {@const isDefaultSel = provider?.value === drill.value}
                {@const hi = plusIndex === 0}
                <button
                  type="button"
                  onmouseenter={() => (plusIndex = 0)}
                  onclick={() => { provider?.onChange(drill.value); closeModelDrill(); plusView = "root"; plusOpen = false; }}
                  class="flex w-full items-center justify-between gap-3 px-3 py-1.5 text-left text-sm transition-colors {hi ? 'bg-green-500/10 text-slate-800 dark:text-white-100' : isDefaultSel ? 'bg-green-500/5 text-slate-800 dark:text-white-100' : 'text-black-800 dark:text-white-200 hover:bg-white-200 dark:hover:bg-navy-700'}"
                >
                  <span class="truncate">Use {drill.label} <span class="text-black-700 dark:text-black-600">(default model)</span></span>
                  {#if isDefaultSel}<span class="shrink-0 text-green-600 dark:text-green-400">✓</span>{/if}
                </button>
                <div class="mx-3 my-1 border-t border-white-300 dark:border-navy-600"></div>
              {/if}
              {#each drillModels as m, mi (m.id)}
                {@const live = !inSet && isLiveSet(m)}
                {@const pinnedValue = `${drill.value}::${m.id}`}
                {@const isSel = !live && (provider?.value === pinnedValue || (provider?.value === drill.value && m.default))}
                {@const rowIdx = (showDefaultRow ? 1 : 0) + mi}
                {@const hi = plusIndex === rowIdx}
                <button
                  type="button"
                  onmouseenter={() => (plusIndex = rowIdx)}
                  onclick={() => { if (live) openSetDrill(m); else selectModel(drill, m.id); }}
                  class="flex w-full items-start justify-between gap-3 px-3 py-1.5 text-left transition-colors {hi ? 'bg-green-500/10 text-slate-800 dark:text-white-100' : isSel ? 'bg-green-500/5 text-slate-800 dark:text-white-100' : 'text-black-800 dark:text-white-200 hover:bg-white-200 dark:hover:bg-navy-700'}"
                >
                  <span class="flex flex-col min-w-0">
                    <span class="flex items-center gap-2 text-sm">
                      <span class="truncate">{m.label}</span>
                      {#if m.default}<span class="shrink-0 rounded-full bg-green-500/10 px-1.5 py-0.5 text-[10px] font-medium text-green-600 dark:text-green-400">default</span>{/if}
                      {#if live}<span class="shrink-0 rounded-full bg-green-500/10 px-1.5 py-0.5 text-[10px] font-medium text-green-600 dark:text-green-400">live set</span>{/if}
                    </span>
                    {#if m.desc}<span class="text-[11px] text-black-700 dark:text-black-600 leading-snug">{m.desc}</span>{/if}
                  </span>
                  {#if live}
                    <svg viewBox="0 0 16 16" class="h-3.5 w-3.5 shrink-0 mt-0.5 text-black-700 dark:text-black-600" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M6 4l4 4-4 4" stroke-linecap="round" stroke-linejoin="round"/></svg>
                  {:else if isSel}<span class="shrink-0 mt-0.5 text-green-600 dark:text-green-400">✓</span>{/if}
                </button>
              {:else}
                <div class="px-3 py-3 text-xs text-black-700 dark:text-black-600">
                  {#if loading}Loading models…{:else if modelDrillSearch.trim()}No models match “{modelDrillSearch}”.{:else}No extra models — use the default above.{/if}
                </div>
              {/each}
            </div>
          {:else if typeDrillKey && provider}
            {@const group = providerGroups.find((g) => g.type === typeDrillKey)}
            <button
              type="button"
              onclick={closeTypeDrill}
              class="flex w-full items-center gap-2 px-3 py-2 text-left text-xs font-semibold text-black-800 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-700 transition-colors"
            >
              <svg viewBox="0 0 16 16" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M10 4L6 8l4 4" stroke-linecap="round" stroke-linejoin="round"/></svg>
              {@render provIcon(typeDrillKey, "h-4 w-4")}
              {typeDrillKey}
            </button>
            <div class="border-t border-white-300 dark:border-navy-600"></div>
            {#each group?.opts ?? [] as opt, oi (opt.value)}
              {@const isSel = splitModelPin(provider.value).key === opt.value}
              {@const hi = plusIndex === oi}
              <button
                type="button"
                onmouseenter={() => (plusIndex = oi)}
                onclick={() => pickInstance(opt)}
                class="flex w-full items-center justify-between gap-3 px-3 py-1.5 text-left text-sm transition-colors {hi ? 'bg-green-500/10 text-slate-800 dark:text-white-100' : isSel ? 'bg-green-500/5 text-slate-800 dark:text-white-100' : 'text-black-800 dark:text-white-200 hover:bg-white-200 dark:hover:bg-navy-700'}"
              >
                <span class="flex items-center gap-2 min-w-0">
                  <span class="truncate">{opt.label}</span>
                  {#if opt.badge}<span class="shrink-0 rounded-full bg-green-500/10 px-1.5 py-0.5 text-[10px] font-medium text-green-600 dark:text-green-400">{opt.badge}</span>{/if}
                </span>
                {#if hasModelDrill(opt)}
                  <svg viewBox="0 0 16 16" class="h-3.5 w-3.5 shrink-0 text-black-700 dark:text-black-600" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M6 4l4 4-4 4" stroke-linecap="round" stroke-linejoin="round"/></svg>
                {:else if isSel}
                  <span class="shrink-0 text-green-600 dark:text-green-400">✓</span>
                {/if}
              </button>
            {/each}
          {:else if plusView === "provider" && provider}
            {@const selKey = splitModelPin(provider.value).key}
            <button
              type="button"
              onclick={() => (plusView = "root")}
              class="flex w-full items-center gap-2 px-3 py-2 text-left text-xs font-semibold text-black-800 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-700 transition-colors"
            >
              <svg viewBox="0 0 16 16" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M10 4L6 8l4 4" stroke-linecap="round" stroke-linejoin="round"/></svg>
              {@render provIcon(provider.value, "h-4 w-4")}
              Provider
            </button>
            <div class="border-t border-white-300 dark:border-navy-600"></div>
            {#each providerGroups as g, gi (g.type)}
              {@const single = g.opts.length === 1 ? g.opts[0] : null}
              {@const nested = g.opts.length > 1 || (single ? hasModelDrill(single) : false)}
              {@const isSel = rawType(provider.value) === g.type}
              {@const hi = plusIndex === gi}
              <button
                type="button"
                onmouseenter={() => (plusIndex = gi)}
                onclick={() => pickType(g)}
                class="flex w-full items-center justify-between gap-3 px-3 py-1.5 text-left text-sm transition-colors {hi ? 'bg-green-500/10 text-slate-800 dark:text-white-100' : isSel ? 'bg-green-500/5 text-slate-800 dark:text-white-100' : 'text-black-800 dark:text-white-200 hover:bg-white-200 dark:hover:bg-navy-700'}"
              >
                <span class="flex items-center gap-2 min-w-0">
                  {@render provIcon(g.type, "h-4 w-4 shrink-0")}
                  <span class="truncate">{single ? single.label : g.type}</span>
                  {#if single?.badge}<span class="shrink-0 rounded-full bg-green-500/10 px-1.5 py-0.5 text-[10px] font-medium text-green-600 dark:text-green-400">{single.badge}</span>{/if}
                </span>
                {#if nested}
                  <svg viewBox="0 0 16 16" class="h-3.5 w-3.5 shrink-0 text-black-700 dark:text-black-600" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M6 4l4 4-4 4" stroke-linecap="round" stroke-linejoin="round"/></svg>
                {:else if single && single.value === selKey}
                  <span class="shrink-0 text-green-600 dark:text-green-400">✓</span>
                {/if}
              </button>
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
              {:else}
                <svg viewBox="0 0 16 16" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M2 5h6M11 5h3M2 11h3M8 11h6" stroke-linecap="round"/><circle cx="9.5" cy="5" r="1.5"/><circle cx="6.5" cy="11" r="1.5"/></svg>
              {/if}
              {plusView === "project" ? "Project" : "Preset"}
            </button>
            <div class="border-t border-white-300 dark:border-navy-600"></div>
            {#each sel.options as opt, si (opt.value)}
              {@const hi = plusIndex === si}
              <button
                type="button"
                onmouseenter={() => (plusIndex = si)}
                onclick={() => { sel.onChange(opt.value); plusView = "root"; plusOpen = false; }}
                class="flex w-full items-center justify-between gap-3 px-3 py-1.5 text-left text-sm transition-colors {hi ? 'bg-green-500/10 text-slate-800 dark:text-white-100' : opt.value === sel.value ? 'bg-green-500/5 text-slate-800 dark:text-white-100' : 'text-black-800 dark:text-white-200 hover:bg-white-200 dark:hover:bg-navy-700'}"
              >
                <span class="flex items-center gap-2 min-w-0">
                  <span class="truncate">{opt.label}</span>
                  {#if opt.badge}<span class="shrink-0 rounded-full bg-green-500/10 px-1.5 py-0.5 text-[10px] font-medium text-green-600 dark:text-green-400">{opt.badge}</span>{/if}
                </span>
                {#if opt.value === sel.value}
                  <span class="shrink-0 text-green-600 dark:text-green-400">✓</span>
                {/if}
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
        data-plus-trigger
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
          data-plus-trigger
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
