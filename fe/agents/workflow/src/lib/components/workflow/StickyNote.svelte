<script lang="ts">
  // n8n-style sticky note — a `sticky_note` node in graph.nodes. Rendered
  // by Canvas in a layer BEHIND the edge SVG and the node cards so a note
  // can frame a group of steps. The note is a BOARD: `content` is its
  // title (plain markdown, top left) and `texts` are small sticky cards
  // placed freely on it (relative 0..1, so they follow a resize), each
  // with its own color + font size. Double-click empty board space adds a
  // yellow card, double-click a card / the title edits it, drag a card to
  // move it, drag its right edge to set its width. When the board is
  // selected, 8 handles (corners + edges) resize it image-editor style.
  // Owns its own pointer gestures (capture, deltas divided by the canvas
  // zoom). Position lives in node._canvas like any node.
  import { tick } from "svelte";
  import { renderMarkdown } from "@wick-fe/common-md";
  import type { Node, StickyText } from "$lib/types/workflow";
  import { STICKY_NOTE_W, STICKY_NOTE_H } from "$lib/stores/editor";
  import { clampText, newTextId, resizeRect, toRelative, RESIZE_DIRS, STICKY_COLORS, STICKY_TEXT_SIZES, STICKY_TEXT_W, type ResizeDir } from "$lib/stickyText";

  const MIN_TEXT_W = 40; // px
  // Handle placement + cursor per direction (inline: these offsets are
  // not in the shared Tailwind config).
  const HANDLE_STYLE: Record<ResizeDir, string> = {
    nw: "left:-5px;top:-5px;cursor:nwse-resize",
    n: "left:50%;top:-5px;transform:translateX(-50%);cursor:ns-resize",
    ne: "right:-5px;top:-5px;cursor:nesw-resize",
    e: "right:-5px;top:50%;transform:translateY(-50%);cursor:ew-resize",
    se: "right:-5px;bottom:-5px;cursor:nwse-resize",
    s: "left:50%;bottom:-5px;transform:translateX(-50%);cursor:ns-resize",
    sw: "left:-5px;bottom:-5px;cursor:nesw-resize",
    w: "left:-5px;top:50%;transform:translateY(-50%);cursor:ew-resize",
  };

  let {
    note,
    zoom = 1,
    selected = false,
    locked = false,
    onselect,
    onpatch,
    ondelete,
  }: {
    note: Node;
    zoom?: number;
    selected?: boolean;
    locked?: boolean;
    onselect?: () => void;
    onpatch?: (patch: Partial<Node>) => void;
    ondelete?: () => void;
  } = $props();

  let noteEl: HTMLDivElement | undefined = $state();
  // Card being edited (may be a new one not stored yet), or the title.
  let editing: StickyText | null = $state(null);
  let editingTitle = $state(false);
  let draft = $state("");
  let activeText: string | null = $state(null);
  let textareaEl: HTMLTextAreaElement | undefined = $state();

  const x = $derived(note._canvas?.x ?? 0);
  const y = $derived(note._canvas?.y ?? 0);
  const width = $derived(note.width || STICKY_NOTE_W);
  const height = $derived(note.height || STICKY_NOTE_H);
  const color = $derived(note.color || "yellow");
  const texts = $derived(note.texts ?? []);
  const titleHtml = $derived(renderMarkdown(note.content ?? ""));
  const hasTitle = $derived(!!(note.content ?? "").trim());
  // A new card shows while it is being typed; it is stored on commit.
  const shown = $derived(editing && !texts.some((t) => t.id === editing!.id) ? [...texts, editing] : texts);
  const activeCard = $derived(texts.find((t) => t.id === activeText));
  const busy = $derived(!!editing || editingTitle);

  // One gesture at a time: "move" drags the board, "resize" drags one of
  // the 8 handles, "text"/"textw" move a card / set its width. Start
  // values snapshot the origin so the delta is applied to a fixed point
  // (no drift from rounding per move).
  let gesture: {
    kind: "move" | "resize" | "text" | "textw";
    dir?: ResizeDir;
    startX: number;
    startY: number;
    x: number;
    y: number;
    w: number;
    h: number;
    text?: StickyText;
    textH?: number;
  } | null = null;

  function begin(e: PointerEvent, kind: "move" | "resize", dir?: ResizeDir) {
    if (e.button !== 0) return;
    e.stopPropagation();
    onselect?.();
    if (locked || busy) return;
    e.preventDefault();
    (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
    gesture = { kind, dir, startX: e.clientX, startY: e.clientY, x, y, w: width, h: height };
  }

  // Card gestures stop propagation so the board (and the canvas pan)
  // never moves while a card is dragged.
  function beginText(e: PointerEvent, t: StickyText, kind: "text" | "textw") {
    if (e.button !== 0) return;
    e.stopPropagation();
    activeText = t.id;
    (e.currentTarget as HTMLElement).closest<HTMLElement>("[data-text-id]")?.focus();
    if (locked || busy) return;
    e.preventDefault();
    (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
    const box = noteEl?.querySelector<HTMLElement>(`[data-text-id="${t.id}"]`);
    gesture = { kind, startX: e.clientX, startY: e.clientY, x, y, w: width, h: height, text: t, textH: toRelative(box?.offsetHeight ?? 0, height) };
  }

  function move(e: PointerEvent) {
    if (!gesture) return;
    const dx = (e.clientX - gesture.startX) / zoom;
    const dy = (e.clientY - gesture.startY) / zoom;
    if (gesture.kind === "move") {
      onpatch?.({ _canvas: { x: Math.round(gesture.x + dx), y: Math.round(gesture.y + dy) } });
    } else if (gesture.kind === "resize") {
      const r = resizeRect(gesture.dir!, gesture, dx, dy);
      onpatch?.({ width: r.w, height: r.h, _canvas: { x: r.x, y: r.y } });
    } else {
      const t = gesture.text!;
      const minW = toRelative(MIN_TEXT_W, gesture.w);
      const next = gesture.kind === "text"
        ? clampText({ ...t, x: t.x + toRelative(dx, gesture.w), y: t.y + toRelative(dy, gesture.h) }, gesture.textH, minW)
        : clampText({ ...t, width: Math.min(1 - t.x, (t.width || STICKY_TEXT_W) + toRelative(dx, gesture.w)) }, 0, minW);
      onpatch?.({ texts: texts.map((o) => (o.id === t.id ? next : o)) });
    }
  }

  function end(e: PointerEvent) {
    if (!gesture) return;
    gesture = null;
    const el = e.currentTarget as HTMLElement;
    if (el.hasPointerCapture(e.pointerId)) el.releasePointerCapture(e.pointerId);
  }

  function setCard(id: string, patch: Partial<StickyText>) {
    onpatch?.({ texts: texts.map((o) => (o.id === id ? { ...o, ...patch } : o)) });
  }

  function removeCard(id: string) {
    activeText = null;
    onpatch?.({ texts: texts.filter((o) => o.id !== id) });
  }

  async function focusEditor() {
    await tick();
    textareaEl?.focus();
  }

  function startEdit(t: StickyText) {
    if (locked) return;
    activeText = t.id;
    draft = t.content;
    editing = t;
    focusEditor();
  }

  function startTitleEdit() {
    if (locked) return;
    draft = note.content ?? "";
    editingTitle = true;
    focusEditor();
  }

  // Double-click on empty board space: new yellow card at the click point.
  function addTextAt(e: MouseEvent) {
    e.stopPropagation();
    if (locked || !noteEl) return;
    const r = noteEl.getBoundingClientRect(); // already scaled by zoom
    const t = clampText(
      { id: newTextId(texts), content: "", x: toRelative(e.clientX - r.left, r.width), y: toRelative(e.clientY - r.top, r.height), width: STICKY_TEXT_W, color: "yellow" },
      0,
      toRelative(MIN_TEXT_W, width),
    );
    startEdit(t);
  }

  // Esc / blur: store the draft; an emptied card is removed.
  function commitEdit() {
    if (editingTitle) {
      editingTitle = false;
      if (draft !== (note.content ?? "")) onpatch?.({ content: draft });
      return;
    }
    if (!editing) return;
    const t = editing;
    editing = null;
    const exists = texts.some((o) => o.id === t.id);
    if (!draft.trim()) {
      if (exists) removeCard(t.id);
      return;
    }
    if (exists && draft === t.content) return;
    onpatch?.({ texts: exists ? texts.map((o) => (o.id === t.id ? { ...o, content: draft } : o)) : [...texts, { ...t, content: draft }] });
  }

  // Delete/Backspace on a focused (not edited) card removes only that
  // card; stopPropagation keeps the canvas from deleting the board.
  function textKeydown(e: KeyboardEvent, t: StickyText) {
    if (busy || locked) return;
    if (e.key === "Delete" || e.key === "Backspace") {
      e.preventDefault();
      e.stopPropagation();
      removeCard(t.id);
    } else if (e.key === "Enter") {
      e.preventDefault();
      e.stopPropagation();
      startEdit(t);
    }
  }

  function editorKeydown(e: KeyboardEvent) {
    e.stopPropagation();
    if (e.key === "Escape" || ((e.ctrlKey || e.metaKey) && e.key === "Enter")) {
      e.preventDefault();
      textareaEl?.blur();
    }
  }

  // Toolbar buttons must not steal focus from the card (its blur would
  // drop the selection before the click lands) nor start a board drag.
  function keepFocus(e: PointerEvent) {
    e.preventDefault();
    e.stopPropagation();
  }
</script>

<div
  bind:this={noteEl}
  class="sticky-note sticky-{color} absolute rounded-md border"
  class:sticky-selected={selected}
  class:cursor-move={!locked}
  style="left: {x}px; top: {y}px; width: {width}px; height: {height}px;"
  data-note-id={note.id}
  role="presentation"
  onpointerdown={(e) => begin(e, "move")}
  onpointermove={move}
  onpointerup={end}
  onpointercancel={end}
  ondblclick={addTextAt}
>
  {#if selected && !locked && !busy && !activeCard}
    <!-- Board toolbar: colour presets + delete. Sits just above the
         board; stopPropagation keeps a swatch click from starting a drag. -->
    <div
      class="absolute -top-9 left-0 flex items-center gap-1 rounded-md px-1.5 py-1 shadow bg-white-100 dark:bg-navy-700 border border-white-300 dark:border-navy-600"
      role="toolbar"
      aria-label="Sticky board"
      tabindex="-1"
      onpointerdown={(e) => e.stopPropagation()}
      ondblclick={(e) => e.stopPropagation()}
    >
      {#each STICKY_COLORS as c (c)}
        <button
          type="button"
          class="sticky-swatch sticky-{c} h-5 w-5 rounded-full border"
          class:sticky-swatch-active={c === color}
          title={c}
          aria-label="Board color {c}"
          aria-pressed={c === color}
          onclick={() => onpatch?.({ color: c })}
        ></button>
      {/each}
      <span class="mx-0.5 h-4 w-px bg-white-300 dark:bg-navy-600"></span>
      <button
        type="button"
        class="h-6 w-6 rounded flex items-center justify-center text-black-700 dark:text-white-100 hover:bg-white-200 dark:hover:bg-navy-600 hover:text-rose-500"
        title="Delete note (Del)"
        aria-label="Delete note"
        onclick={() => ondelete?.()}
      >
        <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><path d="M3 6h18M8 6V4h8v2M19 6l-1 14H6L5 6"/></svg>
      </button>
    </div>
  {/if}

  <!-- Board content: clipped so a resize never lets a card spill over
       the nodes the board frames. -->
  <div class="absolute inset-0 overflow-hidden rounded-md">
    {#if editingTitle}
      <textarea
        bind:this={textareaEl}
        bind:value={draft}
        rows="3"
        class="sticky-text absolute left-[3%] top-[2%] w-[94%] resize-y rounded bg-transparent px-1 py-0.5 text-sm font-mono outline-none"
        placeholder="Board title (markdown)"
        onpointerdown={(e) => e.stopPropagation()}
        onblur={commitEdit}
        onkeydown={editorKeydown}
      ></textarea>
    {:else if hasTitle}
      <!-- Legacy / AI `content` = the board's title: plain text, no card. -->
      <div
        class="sticky-md sticky-text absolute left-[3%] top-[2%] w-[94%] px-1 py-0.5 select-none"
        role="presentation"
        title="Double-click to edit the title"
        ondblclick={(e) => { e.stopPropagation(); startTitleEdit(); }}
      >{@html titleHtml}</div>
    {:else if shown.length === 0}
      <p class="sticky-text px-3 py-2 text-sm opacity-60 italic select-none pointer-events-none">Double-click to add a sticky</p>
    {/if}

    {#each shown as t (t.id)}
      {@const isEditing = editing?.id === t.id}
      <div
        class="sticky-card sticky-{t.color || 'yellow'} sticky-size-{t.size || 'md'} group absolute rounded-md border"
        class:sticky-card-active={activeText === t.id && !isEditing && !locked}
        class:cursor-grab={!locked && !isEditing}
        style="left: {t.x * 100}%; top: {t.y * 100}%; width: {(t.width || STICKY_TEXT_W) * 100}%;"
        data-text-id={t.id}
        role="button"
        tabindex="0"
        aria-label="Sticky"
        onpointerdown={(e) => (isEditing ? e.stopPropagation() : beginText(e, t, "text"))}
        onpointermove={move}
        onpointerup={end}
        onpointercancel={end}
        ondblclick={(e) => { e.stopPropagation(); if (!isEditing) startEdit(t); }}
        onkeydown={(e) => textKeydown(e, t)}
        onblur={() => { if (activeText === t.id && !isEditing) activeText = null; }}
      >
        {#if isEditing}
          <textarea
            bind:this={textareaEl}
            bind:value={draft}
            rows="4"
            class="sticky-text sticky-fs block w-full resize-y rounded-md bg-transparent px-2 py-1.5 font-mono outline-none"
            placeholder="Markdown: # heading, **bold**, - list, `code`"
            onpointerdown={(e) => e.stopPropagation()}
            onblur={commitEdit}
            onkeydown={editorKeydown}
          ></textarea>
        {:else}
          <div class="sticky-md sticky-text px-2 py-1.5 select-none">{@html renderMarkdown(t.content)}</div>
          {#if !locked}
            <!-- Right-edge width handle; visible on hover or while selected. -->
            <div
              class="sticky-text-w absolute -right-1 top-0 h-full w-2 cursor-ew-resize opacity-0 group-hover:opacity-100"
              class:opacity-100={activeText === t.id}
              role="presentation"
              title="Sticky width"
              onpointerdown={(e) => beginText(e, t, "textw")}
              onpointermove={move}
              onpointerup={end}
              onpointercancel={end}
            ></div>
          {/if}
        {/if}
      </div>
    {/each}
  </div>

  {#if activeCard && !locked && !busy}
    <!-- Card toolbar: colour + font size + delete for the selected card.
         Outside the clipped layer so it can sit above the card. -->
    <div
      class="absolute z-10 flex items-center gap-1 rounded-md px-1.5 py-1 shadow bg-white-100 dark:bg-navy-700 border border-white-300 dark:border-navy-600 cursor-default"
      style="left: {activeCard.x * 100}%; top: calc({activeCard.y * 100}% - 2.25rem);"
      role="toolbar"
      aria-label="Sticky"
      tabindex="-1"
      onpointerdown={keepFocus}
      ondblclick={(e) => e.stopPropagation()}
    >
      {#each STICKY_COLORS as c (c)}
        <button
          type="button"
          class="sticky-swatch sticky-{c} h-5 w-5 rounded-full border"
          class:sticky-swatch-active={c === (activeCard.color || "yellow")}
          title={c}
          aria-label="Sticky color {c}"
          aria-pressed={c === (activeCard.color || "yellow")}
          onclick={() => setCard(activeCard.id, { color: c })}
        ></button>
      {/each}
      <span class="mx-0.5 h-4 w-px bg-white-300 dark:bg-navy-600"></span>
      {#each STICKY_TEXT_SIZES as s (s)}
        {@const on = s === (activeCard.size || "md")}
        <button
          type="button"
          class="h-6 w-6 rounded text-xs font-semibold uppercase text-black-700 dark:text-white-100 hover:bg-white-200 dark:hover:bg-navy-600"
          class:sticky-size-active={on}
          title="Font size {s}"
          aria-label="Font size {s}"
          aria-pressed={on}
          onclick={() => setCard(activeCard.id, { size: s })}
        >{s[0]}</button>
      {/each}
      <span class="mx-0.5 h-4 w-px bg-white-300 dark:bg-navy-600"></span>
      <button
        type="button"
        class="h-6 w-6 rounded flex items-center justify-center text-black-700 dark:text-white-100 hover:bg-white-200 dark:hover:bg-navy-600 hover:text-rose-500"
        title="Delete sticky (Del)"
        aria-label="Delete sticky"
        onclick={() => removeCard(activeCard.id)}
      >
        <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><path d="M3 6h18M8 6V4h8v2M19 6l-1 14H6L5 6"/></svg>
      </button>
    </div>
  {/if}

  {#if selected && !locked && !busy}
    {#each RESIZE_DIRS as dir (dir)}
      <div
        class="sticky-handle absolute h-2.5 w-2.5 rounded-sm border"
        style={HANDLE_STYLE[dir]}
        role="presentation"
        title="Resize"
        ondblclick={(e) => e.stopPropagation()}
        onpointerdown={(e) => begin(e, "resize", dir)}
        onpointermove={move}
        onpointerup={end}
        onpointercancel={end}
      ></div>
    {/each}
  {/if}
</div>

<style>
  /* Colour presets, shared by the board and its cards. --sn-paper is the
     light pastel and --sn-hue the dark-mode tint (RGB triplets so alpha
     can vary). Light: pastel paper + dark ink. Dark: a translucent tint
     of the hue + light ink, so nothing turns into a bright block with
     unreadable text. Plain CSS instead of Tailwind tokens because most of
     these hues are not in the shared palette (tailwind.config.js replaces
     the defaults). */
  .sticky-yellow { --sn-paper: 254 249 195; --sn-hue: 234 179 8;   --sn-bd: #facc15; --sn-fg: #422006; --sn-dfg: #fef9c3; }
  .sticky-green  { --sn-paper: 220 252 231; --sn-hue: 34 197 94;   --sn-bd: #4ade80; --sn-fg: #14532d; --sn-dfg: #dcfce7; }
  .sticky-blue   { --sn-paper: 219 234 254; --sn-hue: 59 130 246;  --sn-bd: #60a5fa; --sn-fg: #1e3a8a; --sn-dfg: #dbeafe; }
  .sticky-purple { --sn-paper: 237 233 254; --sn-hue: 139 92 246;  --sn-bd: #a78bfa; --sn-fg: #3b0764; --sn-dfg: #ede9fe; }
  .sticky-red    { --sn-paper: 254 226 226; --sn-hue: 239 68 68;   --sn-bd: #f87171; --sn-fg: #7f1d1d; --sn-dfg: #fee2e2; }
  .sticky-gray   { --sn-paper: 241 245 249; --sn-hue: 148 163 184; --sn-bd: #94a3b8; --sn-fg: #0f172a; --sn-dfg: #e2e8f0; }

  /* Board: muted so the cards on it stand out. */
  .sticky-note { background: rgb(var(--sn-paper) / 0.5); border-color: var(--sn-bd); color: var(--sn-fg); }
  :global(.dark) .sticky-note { background: rgb(var(--sn-hue) / 0.07); border-color: rgb(var(--sn-hue) / 0.4); color: var(--sn-dfg); }
  .sticky-selected { box-shadow: 0 0 0 2px #10b981; }

  /* Card: full-strength paper + thin shadow. */
  .sticky-card { background: rgb(var(--sn-paper)); border-color: var(--sn-bd); color: var(--sn-fg); box-shadow: 0 1px 3px rgb(0 0 0 / 0.15); }
  :global(.dark) .sticky-card { background: rgb(var(--sn-hue) / 0.3); border-color: rgb(var(--sn-hue) / 0.6); color: var(--sn-dfg); box-shadow: 0 1px 3px rgb(0 0 0 / 0.45); }
  .sticky-card:focus { outline: none; }
  .sticky-card-active { box-shadow: 0 0 0 2px #10b981; }

  .sticky-size-sm { --sn-fs: 0.6875rem; }
  .sticky-size-md { --sn-fs: 0.8125rem; }
  .sticky-size-lg { --sn-fs: 1rem; }
  .sticky-fs { font-size: var(--sn-fs, 0.8125rem); }

  .sticky-swatch { background: rgb(var(--sn-paper)); border-color: var(--sn-bd); }
  :global(.dark) .sticky-swatch { background: rgb(var(--sn-hue) / 0.55); border-color: rgb(var(--sn-hue) / 0.55); }
  .sticky-swatch-active,
  .sticky-size-active { box-shadow: 0 0 0 2px #10b981; }

  .sticky-handle { background: #fff; border-color: #10b981; }
  :global(.dark) .sticky-handle { background: #0f172a; }

  .sticky-text { color: inherit; }
  .sticky-text::placeholder { color: inherit; opacity: 0.5; }

  /* renderMarkdown hard-codes chat colours on p/li/code; inherit the
     board/card ink instead so every preset stays readable in both
     themes. Font size follows the card's size (title = md). */
  .sticky-md :global(*) { color: inherit; }
  .sticky-md :global(p),
  .sticky-md :global(li) { font-size: var(--sn-fs, 0.8125rem); line-height: 1.4; margin: 0.15rem 0; }
  .sticky-md :global(h1) { font-size: calc(var(--sn-fs, 0.8125rem) * 1.4); font-weight: 700; margin: 0.1rem 0 0.3rem; }
  .sticky-md :global(h2) { font-size: calc(var(--sn-fs, 0.8125rem) * 1.23); font-weight: 700; margin: 0.1rem 0 0.25rem; }
  .sticky-md :global(h3),
  .sticky-md :global(h4) { font-size: calc(var(--sn-fs, 0.8125rem) * 1.08); font-weight: 600; margin: 0.1rem 0 0.2rem; }
  .sticky-md :global(ul) { list-style: disc; padding-left: 1.1rem; }
  .sticky-md :global(ol) { list-style: decimal; padding-left: 1.1rem; }
  .sticky-md :global(code) { background: rgb(0 0 0 / 0.08); }
  :global(.dark) .sticky-md :global(code) { background: rgb(255 255 255 / 0.12); }
  .sticky-md :global(a) { text-decoration: underline; }

  .sticky-text-w { background: linear-gradient(90deg, transparent 35%, var(--sn-bd) 35%, var(--sn-bd) 65%, transparent 65%); }
</style>
