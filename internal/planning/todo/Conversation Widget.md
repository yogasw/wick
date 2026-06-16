# Conversation Widget — Svelte Implementation Plan v2

> Tujuan: membangun conversation UI di Svelte yang bisa render SVG interaktif dan HTML iframe secara aman, persis seperti Claude.ai, lengkap dengan AI response streaming.
>
> **v2 update:** Tiap tipe konten punya (1) komponen & styling sendiri, (2) toggle/hide/expand, (3) action toolbar per tipe.

---

## Daftar Isi

1. [Arsitektur Overview](#1-arsitektur-overview)
2. [Struktur Folder](#2-struktur-folder)
3. [System Prompt AI](#3-system-prompt-ai)
4. [Streaming Response dari Claude API](#4-streaming-response-dari-claude-api)
5. [Message Parser — Deteksi Blok Konten](#5-message-parser--deteksi-blok-konten)
6. [BlockWrapper — Shell Universal per Tipe](#6-blockwrapper--shell-universal-per-tipe)
7. [Render SVG Interaktif](#7-render-svg-interaktif)
8. [Render HTML via Sandboxed iframe](#8-render-html-via-sandboxed-iframe)
9. [Render Text / Markdown](#9-render-text--markdown)
10. [Render Image Grid](#10-render-image-grid)
11. [Action Toolbar per Tipe](#11-action-toolbar-per-tipe)
12. [Toggle & Expand per Blok](#12-toggle--expand-per-blok)
13. [Keamanan (Security)](#13-keamanan-security)
14. [State Management](#14-state-management)
15. [Komponen Utama: ChatWindow](#15-komponen-utama-chatwindow)
16. [Tips & Gotchas](#16-tips--gotchas)

---

## 1. Arsitektur Overview

```
User input
    │
    ▼
+-------------------+
| ChatInput.svelte  |  ← textarea + kirim
+-------------------+
    │
    ▼
+-------------------+
| API Handler       |  ← POST /api/chat → Claude API (streaming SSE)
| (SvelteKit route) |
+-------------------+
    │ ReadableStream
    ▼
+-------------------+
| MessageList       |  ← loop messages
+-------------------+
    │
    ▼ parse tiap message (setelah stream selesai)
+-----------------------------------------------+
| ContentRenderer.svelte                        |
|  ├── BlockWrapper.svelte  ← shell universal   |
|  │    ├── header: ikon tipe + judul + toolbar |
|  │    ├── toggle collapse/expand              |
|  │    └── slot konten                         |
|  │                                            |
|  ├── TextBlock.svelte     ← markdown          |
|  ├── SvgBlock.svelte      ← inline SVG        |
|  ├── HtmlBlock.svelte     ← sandboxed iframe  |
|  └── ImageGrid.svelte     ← grid foto         |
+-----------------------------------------------+
```

**Prinsip utama:**
- Setiap blok konten dibungkus `BlockWrapper` — header konsisten, toggle, toolbar
- SVG di-render **inline** di DOM — lebih ringan, bisa interact langsung
- HTML di-render via **sandboxed iframe** — isolasi total
- Action toolbar berbeda tiap tipe (copy SVG, download HTML, buka gambar, dll)
- Parse dilakukan **setelah stream selesai** — hindari tag terpotong

---

## 2. Struktur Folder

```
src/
├── lib/
│   ├── components/
│   │   ├── Chat/
│   │   │   ├── ChatWindow.svelte
│   │   │   ├── ChatInput.svelte
│   │   │   ├── MessageList.svelte
│   │   │   └── MessageBubble.svelte
│   │   ├── Renderer/
│   │   │   ├── ContentRenderer.svelte   ← router blok
│   │   │   ├── BlockWrapper.svelte      ← shell universal (BARU)
│   │   │   ├── ActionToolbar.svelte     ← toolbar aksi (BARU)
│   │   │   ├── TextBlock.svelte
│   │   │   ├── SvgBlock.svelte
│   │   │   ├── HtmlBlock.svelte
│   │   │   └── ImageGrid.svelte
│   │   └── UI/
│   │       ├── LoadingDots.svelte
│   │       ├── StreamingCursor.svelte
│   │       ├── CollapseToggle.svelte    ← (BARU)
│   │       └── Toast.svelte             ← feedback copy/download (BARU)
│   ├── stores/
│   │   ├── chat.ts
│   │   └── ui.ts
│   ├── utils/
│   │   ├── parser.ts
│   │   ├── sanitize.ts
│   │   ├── stream.ts
│   │   └── actions.ts                  ← copy, download, share (BARU)
│   └── types.ts
├── routes/
│   └── api/chat/+server.ts
└── app.html
```

---

## 3. System Prompt AI

```typescript
// src/lib/config/systemPrompt.ts

export const SYSTEM_PROMPT = `
Kamu adalah asisten AI dalam sebuah chat interface.

## FORMAT OUTPUT

Gunakan tag berikut untuk konten non-teks:

### SVG Interaktif
<svg_widget title="nama_singkat">
<svg width="100%" viewBox="0 0 680 400">
  ...
</svg>
</svg_widget>

### HTML Widget
<html_widget title="nama_singkat">
<!DOCTYPE html>
<html><head>...</head><body>...</body></html>
</html_widget>

### Image Results
<image_results query="kata pencarian">
[
  { "url": "https://...", "title": "...", "source": "domain.com" }
]
</image_results>

## ATURAN
- SVG: pakai width="100%" dan viewBox, jangan hardcode px width
- HTML widget: sertakan DOCTYPE lengkap, jangan pakai position:fixed
- Teks biasa: tulis markdown normal, tanpa tag apapun
- Jangan sebut tag-tag ini ke user
`;
```

---

## 4. Streaming Response dari Claude API

### SvelteKit API Route

```typescript
// src/routes/api/chat/+server.ts

import { ANTHROPIC_API_KEY } from '$env/static/private';
import { SYSTEM_PROMPT } from '$lib/config/systemPrompt';
import type { RequestHandler } from './$types';

export const POST: RequestHandler = async ({ request }) => {
  const { messages } = await request.json();

  const response = await fetch('https://api.anthropic.com/v1/messages', {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      'x-api-key': ANTHROPIC_API_KEY,
      'anthropic-version': '2023-06-01',
    },
    body: JSON.stringify({
      model: 'claude-sonnet-4-6',
      max_tokens: 4096,
      system: SYSTEM_PROMPT,
      stream: true,
      messages,
    }),
  });

  return new Response(response.body, {
    headers: {
      'Content-Type': 'text/event-stream',
      'Cache-Control': 'no-cache',
      'Connection': 'keep-alive',
    },
  });
};
```

### Stream Handler (Client)

```typescript
// src/lib/utils/stream.ts

export async function streamChat(
  messages: { role: string; content: string }[],
  onChunk: (text: string) => void,
  onDone: () => void,
  onError: (err: Error) => void
) {
  const res = await fetch('/api/chat', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ messages }),
  });

  if (!res.ok) { onError(new Error(`API error: ${res.status}`)); return; }

  const reader = res.body!.getReader();
  const decoder = new TextDecoder();
  let buffer = '';

  while (true) {
    const { done, value } = await reader.read();
    if (done) break;

    buffer += decoder.decode(value, { stream: true });
    const lines = buffer.split('\n');
    buffer = lines.pop() ?? '';

    for (const line of lines) {
      if (!line.startsWith('data: ')) continue;
      const data = line.slice(6);
      if (data === '[DONE]') { onDone(); return; }
      try {
        const parsed = JSON.parse(data);
        if (parsed.type === 'content_block_delta') {
          onChunk(parsed.delta?.text ?? '');
        }
      } catch { /* skip */ }
    }
  }
  onDone();
}
```

---

## 5. Message Parser — Deteksi Blok Konten

```typescript
// src/lib/utils/parser.ts

export type BlockType = 'text' | 'svg' | 'html' | 'images';

export type Block =
  | { type: 'text';   content: string }
  | { type: 'svg';    title: string; content: string }
  | { type: 'html';   title: string; content: string }
  | { type: 'images'; query: string; items: ImageItem[] };

export interface ImageItem {
  url: string;
  title: string;
  source: string;
}

const PATTERNS = [
  { re: /<svg_widget\s+title="([^"]*)">([\s\S]*?)<\/svg_widget>/g,    type: 'svg'    },
  { re: /<html_widget\s+title="([^"]*)">([\s\S]*?)<\/html_widget>/g,  type: 'html'   },
  { re: /<image_results\s+query="([^"]*)">([\s\S]*?)<\/image_results>/g, type: 'images' },
] as const;

export function parseBlocks(raw: string): Block[] {
  type MatchItem = { index: number; end: number; block: Block };
  const matches: MatchItem[] = [];

  for (const { re, type } of PATTERNS) {
    let m: RegExpExecArray | null;
    const regex = new RegExp(re.source, re.flags);
    while ((m = regex.exec(raw)) !== null) {
      const [full, titleOrQuery, body] = m;
      let block: Block;

      if (type === 'images') {
        try {
          block = { type: 'images', query: titleOrQuery, items: JSON.parse(body.trim()) };
        } catch { continue; }
      } else {
        block = { type, title: titleOrQuery, content: body.trim() } as Block;
      }

      matches.push({ index: m.index, end: m.index + full.length, block });
    }
  }

  matches.sort((a, b) => a.index - b.index);

  const blocks: Block[] = [];
  let cursor = 0;

  for (const match of matches) {
    if (match.index > cursor) {
      const text = raw.slice(cursor, match.index).trim();
      if (text) blocks.push({ type: 'text', content: text });
    }
    blocks.push(match.block);
    cursor = match.end;
  }

  const tail = raw.slice(cursor).trim();
  if (tail) blocks.push({ type: 'text', content: tail });

  return blocks;
}

// Cek apakah semua tag sudah tertutup (untuk streaming)
export function isStreamComplete(raw: string): boolean {
  for (const { re } of PATTERNS) {
    const opens = (raw.match(new RegExp(re.source.split('>')[0], 'g')) ?? []).length;
    const closeTag = re.source.match(/<\\\/(\w+)>/)?.[1];
    if (!closeTag) continue;
    const closes = (raw.match(new RegExp(`</${closeTag}>`, 'g')) ?? []).length;
    if (opens !== closes) return false;
  }
  return true;
}
```

---

## 6. BlockWrapper — Shell Universal per Tipe

`BlockWrapper` adalah komponen pembungkus yang memberikan setiap blok konten:
- Header dengan ikon + label tipe + judul
- Tombol collapse/expand
- Slot untuk action toolbar
- Slot untuk konten

```svelte
<!-- src/lib/components/Renderer/BlockWrapper.svelte -->
<script lang="ts">
  import type { BlockType } from '$lib/utils/parser';

  export let type: BlockType;
  export let title: string = '';
  export let collapsible: boolean = true;

  let collapsed = false;

  const META: Record<BlockType, { icon: string; label: string; color: string }> = {
    text:   { icon: 'ti-align-left',   label: 'Teks',    color: 'var(--color-text-secondary)' },
    svg:    { icon: 'ti-vector',        label: 'Diagram', color: '#534AB7' },
    html:   { icon: 'ti-code',          label: 'Widget',  color: '#0F6E56' },
    images: { icon: 'ti-photo',         label: 'Gambar',  color: '#D85A30' },
  };

  $: meta = META[type];
</script>

{#if type === 'text'}
  <!-- Text tidak pakai wrapper — langsung render -->
  <slot />
{:else}
  <div class="block-wrapper block-{type}" class:collapsed>
    <div class="block-header">
      <div class="block-meta">
        <i class="ti {meta.icon}" style="color: {meta.color}" aria-hidden="true"></i>
        <span class="block-label" style="color: {meta.color}">{meta.label}</span>
        {#if title}
          <span class="block-title">{title}</span>
        {/if}
      </div>

      <div class="block-actions">
        <!-- Slot untuk action toolbar per tipe -->
        <slot name="actions" />

        {#if collapsible}
          <button
            class="collapse-btn"
            onclick={() => collapsed = !collapsed}
            aria-label={collapsed ? 'Perluas' : 'Ciutkan'}
          >
            <i class="ti {collapsed ? 'ti-chevron-down' : 'ti-chevron-up'}"></i>
          </button>
        {/if}
      </div>
    </div>

    {#if !collapsed}
      <div class="block-content">
        <slot />
      </div>
    {/if}
  </div>
{/if}

<style>
  .block-wrapper {
    border: 0.5px solid var(--color-border-tertiary);
    border-radius: 12px;
    overflow: hidden;
    margin: 0.75rem 0;
    background: var(--color-background-primary);
  }

  .block-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 8px 12px;
    border-bottom: 0.5px solid var(--color-border-tertiary);
    background: var(--color-background-secondary);
    gap: 8px;
  }

  .collapsed .block-header {
    border-bottom: none;
  }

  .block-meta {
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: 13px;
    min-width: 0;
  }

  .block-label {
    font-weight: 500;
    font-size: 12px;
    white-space: nowrap;
  }

  .block-title {
    color: var(--color-text-secondary);
    font-size: 12px;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .block-actions {
    display: flex;
    align-items: center;
    gap: 4px;
    flex-shrink: 0;
  }

  .collapse-btn {
    background: transparent;
    border: none;
    padding: 4px 6px;
    cursor: pointer;
    color: var(--color-text-secondary);
    border-radius: 6px;
    display: flex;
    align-items: center;
    font-size: 14px;
  }

  .collapse-btn:hover {
    background: var(--color-background-tertiary);
  }

  .block-content {
    padding: 0;
  }

  /* Tipe-spesifik styling */
  .block-svg .block-content    { padding: 1rem; }
  .block-images .block-content { padding: 0.75rem; }
  .block-html .block-content   { padding: 0; }
</style>
```

---

## 7. Render SVG Interaktif

```svelte
<!-- src/lib/components/Renderer/SvgBlock.svelte -->
<script lang="ts">
  import { onMount, createEventDispatcher } from 'svelte';
  import { sanitizeSvg } from '$lib/utils/sanitize';
  import { copyToClipboard } from '$lib/utils/actions';
  import BlockWrapper from './BlockWrapper.svelte';

  export let content: string;
  export let title: string;

  const dispatch = createEventDispatcher<{ sendprompt: { text: string } }>();

  let container: HTMLDivElement;
  let copied = false;

  onMount(() => {
    const sanitized = sanitizeSvg(content);
    container.innerHTML = sanitized;

    const svg = container.querySelector('svg');
    if (svg) {
      svg.style.width = '100%';
      svg.style.height = 'auto';
      svg.style.display = 'block';
    }

    // Bridge sendPrompt untuk onclick di dalam SVG
    (window as any).sendPrompt = (text: string) => {
      dispatch('sendprompt', { text });
    };
  });

  async function handleCopySvg() {
    await copyToClipboard(content);
    copied = true;
    setTimeout(() => (copied = false), 2000);
  }

  function handleDownloadSvg() {
    const blob = new Blob([content], { type: 'image/svg+xml' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = `${title || 'diagram'}.svg`;
    a.click();
    URL.revokeObjectURL(url);
  }
</script>

<BlockWrapper type="svg" {title}>
  <svelte:fragment slot="actions">
    <button class="action-btn" onclick={handleCopySvg} title="Copy SVG">
      <i class="ti {copied ? 'ti-check' : 'ti-copy'}"></i>
      <span>{copied ? 'Tersalin' : 'Copy'}</span>
    </button>
    <button class="action-btn" onclick={handleDownloadSvg} title="Download SVG">
      <i class="ti ti-download"></i>
      <span>Download</span>
    </button>
  </svelte:fragment>

  <div bind:this={container} class="svg-container"></div>
</BlockWrapper>

<style>
  .svg-container { width: 100%; }

  .action-btn {
    display: flex;
    align-items: center;
    gap: 4px;
    background: transparent;
    border: 0.5px solid var(--color-border-secondary);
    border-radius: 6px;
    padding: 3px 8px;
    font-size: 12px;
    color: var(--color-text-secondary);
    cursor: pointer;
    white-space: nowrap;
  }

  .action-btn:hover {
    background: var(--color-background-tertiary);
    color: var(--color-text-primary);
  }

  .action-btn i { font-size: 13px; }
</style>
```

---

## 8. Render HTML via Sandboxed iframe

```svelte
<!-- src/lib/components/Renderer/HtmlBlock.svelte -->
<script lang="ts">
  import { onMount, onDestroy, createEventDispatcher } from 'svelte';
  import { copyToClipboard } from '$lib/utils/actions';
  import BlockWrapper from './BlockWrapper.svelte';

  export let content: string;
  export let title: string;

  const dispatch = createEventDispatcher<{ sendprompt: { text: string } }>();

  let iframe: HTMLIFrameElement;
  let height = 300;
  let copied = false;
  let blobUrl = '';

  function buildHtml(html: string): string {
    const bridge = `<script>
      window.sendPrompt = (text) =>
        window.parent.postMessage({ type: 'sendPrompt', text }, '*');
      function reportHeight() {
        const h = document.documentElement.scrollHeight;
        window.parent.postMessage({ type: 'resize', height: h }, '*');
      }
      window.addEventListener('load', reportHeight);
      new ResizeObserver(reportHeight).observe(document.body);
    <\/script>`;

    return html.includes('</head>')
      ? html.replace('</head>', bridge + '</head>')
      : bridge + html;
  }

  onMount(() => {
    const finalHtml = buildHtml(content);
    // Pakai blob URL agar sandbox lebih ketat
    const blob = new Blob([finalHtml], { type: 'text/html' });
    blobUrl = URL.createObjectURL(blob);
    iframe.src = blobUrl;

    const handler = (e: MessageEvent) => {
      if (e.source !== iframe?.contentWindow) return;
      if (e.data?.type === 'sendPrompt' && typeof e.data.text === 'string') {
        dispatch('sendprompt', { text: e.data.text });
      }
      if (e.data?.type === 'resize') {
        height = Math.max(100, Math.min(2000, e.data.height + 24));
      }
    };

    window.addEventListener('message', handler);
    return () => window.removeEventListener('message', handler);
  });

  onDestroy(() => {
    if (blobUrl) URL.revokeObjectURL(blobUrl);
  });

  async function handleCopyHtml() {
    await copyToClipboard(content);
    copied = true;
    setTimeout(() => (copied = false), 2000);
  }

  function handleDownloadHtml() {
    const blob = new Blob([content], { type: 'text/html' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = `${title || 'widget'}.html`;
    a.click();
    URL.revokeObjectURL(url);
  }

  function handleOpenTab() {
    window.open(blobUrl, '_blank');
  }
</script>

<BlockWrapper type="html" {title}>
  <svelte:fragment slot="actions">
    <button class="action-btn" onclick={handleCopyHtml} title="Copy HTML">
      <i class="ti {copied ? 'ti-check' : 'ti-copy'}"></i>
      <span>{copied ? 'Tersalin' : 'Copy'}</span>
    </button>
    <button class="action-btn" onclick={handleDownloadHtml} title="Download HTML">
      <i class="ti ti-download"></i>
      <span>Download</span>
    </button>
    <button class="action-btn" onclick={handleOpenTab} title="Buka di tab baru">
      <i class="ti ti-external-link"></i>
    </button>
  </svelte:fragment>

  <iframe
    bind:this={iframe}
    {title}
    style="height: {height}px"
    sandbox="allow-scripts allow-forms"
    referrerpolicy="no-referrer"
    loading="lazy"
  ></iframe>
</BlockWrapper>

<style>
  iframe {
    width: 100%;
    border: none;
    display: block;
    transition: height 0.15s ease;
  }

  .action-btn {
    display: flex;
    align-items: center;
    gap: 4px;
    background: transparent;
    border: 0.5px solid var(--color-border-secondary);
    border-radius: 6px;
    padding: 3px 8px;
    font-size: 12px;
    color: var(--color-text-secondary);
    cursor: pointer;
    white-space: nowrap;
  }

  .action-btn:hover {
    background: var(--color-background-tertiary);
    color: var(--color-text-primary);
  }

  .action-btn i { font-size: 13px; }
</style>
```

---

## 9. Render Text / Markdown

```svelte
<!-- src/lib/components/Renderer/TextBlock.svelte -->
<script lang="ts">
  import { copyToClipboard } from '$lib/utils/actions';
  import { marked } from 'marked';

  export let content: string;

  let copied = false;

  $: html = marked.parse(content, { breaks: true });

  async function handleCopy() {
    await copyToClipboard(content);
    copied = true;
    setTimeout(() => (copied = false), 2000);
  }
</script>

<div class="text-block">
  <div class="text-content prose">
    {@html html}
  </div>
  <div class="text-footer">
    <button class="copy-text-btn" onclick={handleCopy}>
      <i class="ti {copied ? 'ti-check' : 'ti-copy'}"></i>
      <span>{copied ? 'Tersalin' : 'Salin teks'}</span>
    </button>
  </div>
</div>

<style>
  .text-block { position: relative; }

  .text-content {
    font-size: 15px;
    line-height: 1.7;
    color: var(--color-text-primary);
  }

  /* Prose styles */
  .prose :global(h1) { font-size: 20px; font-weight: 500; margin: 1.2rem 0 0.5rem; }
  .prose :global(h2) { font-size: 17px; font-weight: 500; margin: 1rem 0 0.4rem; }
  .prose :global(h3) { font-size: 15px; font-weight: 500; margin: 0.8rem 0 0.3rem; }
  .prose :global(p)  { margin: 0 0 0.75rem; }
  .prose :global(ul), .prose :global(ol) { padding-left: 1.5rem; margin: 0 0 0.75rem; }
  .prose :global(li) { margin-bottom: 0.25rem; }
  .prose :global(code) {
    font-family: var(--font-mono);
    font-size: 13px;
    background: var(--color-background-secondary);
    padding: 1px 5px;
    border-radius: 4px;
    border: 0.5px solid var(--color-border-tertiary);
  }
  .prose :global(pre) {
    background: var(--color-background-secondary);
    padding: 1rem;
    border-radius: 8px;
    overflow-x: auto;
    border: 0.5px solid var(--color-border-tertiary);
    margin: 0.75rem 0;
  }
  .prose :global(pre code) {
    background: none;
    border: none;
    padding: 0;
    font-size: 13px;
  }
  .prose :global(blockquote) {
    border-left: 3px solid var(--color-border-secondary);
    padding-left: 1rem;
    color: var(--color-text-secondary);
    margin: 0.75rem 0;
  }
  .prose :global(table) {
    width: 100%;
    border-collapse: collapse;
    font-size: 14px;
    margin: 0.75rem 0;
  }
  .prose :global(th), .prose :global(td) {
    border: 0.5px solid var(--color-border-tertiary);
    padding: 6px 10px;
    text-align: left;
  }
  .prose :global(th) {
    background: var(--color-background-secondary);
    font-weight: 500;
  }

  .text-footer {
    margin-top: 4px;
    display: flex;
    justify-content: flex-end;
  }

  .copy-text-btn {
    display: flex;
    align-items: center;
    gap: 4px;
    background: transparent;
    border: none;
    padding: 3px 6px;
    font-size: 12px;
    color: var(--color-text-tertiary);
    cursor: pointer;
    border-radius: 6px;
  }

  .copy-text-btn:hover {
    color: var(--color-text-secondary);
    background: var(--color-background-secondary);
  }

  .copy-text-btn i { font-size: 13px; }
</style>
```

---

## 10. Render Image Grid

```svelte
<!-- src/lib/components/Renderer/ImageGrid.svelte -->
<script lang="ts">
  import BlockWrapper from './BlockWrapper.svelte';
  import type { ImageItem } from '$lib/utils/parser';

  export let query: string;
  export let items: ImageItem[];

  let failed = new Set<string>();
  let lightbox: ImageItem | null = null;

  function onError(url: string) {
    failed = new Set([...failed, url]);
  }

  function openLightbox(item: ImageItem) {
    lightbox = item;
  }

  function closeLightbox() {
    lightbox = null;
  }

  $: visible = items.filter(i => !failed.has(i.url));
</script>

<BlockWrapper type="images" title={`"${query}"`}>
  <svelte:fragment slot="actions">
    <span class="count-badge">{visible.length} foto</span>
  </svelte:fragment>

  <div class="image-grid">
    {#each visible as item (item.url)}
      <button class="image-card" onclick={() => openLightbox(item)}>
        <img
          src={item.url}
          alt={item.title}
          loading="lazy"
          onerror={() => onError(item.url)}
        />
        <div class="image-caption">
          <span class="img-title">{item.title}</span>
          <span class="img-source">{item.source}</span>
        </div>
      </button>
    {/each}
  </div>
</BlockWrapper>

<!-- Lightbox (klik gambar untuk perbesar) -->
{#if lightbox}
  <div class="lightbox-overlay" onclick={closeLightbox} role="dialog" aria-modal="true">
    <div class="lightbox-box" onclick={(e) => e.stopPropagation()}>
      <button class="lightbox-close" onclick={closeLightbox} aria-label="Tutup">
        <i class="ti ti-x"></i>
      </button>
      <img src={lightbox.url} alt={lightbox.title} />
      <div class="lightbox-caption">
        <span>{lightbox.title}</span>
        <a href={lightbox.url} target="_blank" rel="noopener">
          <i class="ti ti-external-link"></i> Buka original
        </a>
      </div>
    </div>
  </div>
{/if}

<style>
  .image-grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(150px, 1fr));
    gap: 8px;
    padding: 0.75rem;
  }

  .image-card {
    all: unset;
    cursor: pointer;
    border-radius: 8px;
    overflow: hidden;
    border: 0.5px solid var(--color-border-tertiary);
    background: var(--color-background-secondary);
    display: block;
    transition: opacity 0.12s;
  }

  .image-card:hover { opacity: 0.85; }

  .image-card img {
    width: 100%;
    aspect-ratio: 16/10;
    object-fit: cover;
    display: block;
  }

  .image-caption {
    padding: 5px 8px;
    display: flex;
    flex-direction: column;
    gap: 2px;
  }

  .img-title {
    font-size: 12px;
    color: var(--color-text-primary);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .img-source {
    font-size: 11px;
    color: var(--color-text-tertiary);
  }

  .count-badge {
    font-size: 11px;
    color: var(--color-text-secondary);
    padding: 2px 8px;
    background: var(--color-background-tertiary);
    border-radius: 10px;
  }

  /* Lightbox */
  .lightbox-overlay {
    position: fixed; inset: 0;
    background: rgba(0,0,0,0.7);
    display: flex; align-items: center; justify-content: center;
    z-index: 1000;
    padding: 1rem;
  }

  .lightbox-box {
    background: var(--color-background-primary);
    border-radius: 12px;
    overflow: hidden;
    max-width: 90vw;
    max-height: 90vh;
    display: flex;
    flex-direction: column;
    position: relative;
  }

  .lightbox-box img {
    max-width: 100%;
    max-height: 75vh;
    object-fit: contain;
    display: block;
  }

  .lightbox-caption {
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: 10px 14px;
    font-size: 13px;
    color: var(--color-text-secondary);
    border-top: 0.5px solid var(--color-border-tertiary);
  }

  .lightbox-caption a {
    color: var(--color-text-info);
    display: flex; align-items: center; gap: 4px;
    text-decoration: none;
    font-size: 12px;
  }

  .lightbox-close {
    position: absolute;
    top: 8px; right: 8px;
    background: rgba(0,0,0,0.4);
    border: none;
    border-radius: 50%;
    width: 30px; height: 30px;
    display: flex; align-items: center; justify-content: center;
    color: white;
    cursor: pointer;
    font-size: 16px;
    z-index: 1;
  }
</style>
```

---

## 11. Action Toolbar per Tipe

Ringkasan action yang tersedia per tipe konten:

| Tipe    | Actions                                      |
|---------|----------------------------------------------|
| `text`  | Copy teks (di footer, subtle)                |
| `svg`   | Copy SVG code, Download .svg                 |
| `html`  | Copy HTML code, Download .html, Buka tab baru|
| `images`| Badge jumlah foto, klik gambar → lightbox    |

Action utilities:

```typescript
// src/lib/utils/actions.ts

export async function copyToClipboard(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    // Fallback untuk browser lama
    const el = document.createElement('textarea');
    el.value = text;
    el.style.position = 'absolute';
    el.style.left = '-9999px';
    document.body.appendChild(el);
    el.select();
    const ok = document.execCommand('copy');
    document.body.removeChild(el);
    return ok;
  }
}

export function downloadFile(content: string, filename: string, mimeType: string) {
  const blob = new Blob([content], { type: mimeType });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  document.body.removeChild(a);
  URL.revokeObjectURL(url);
}
```

---

## 12. Toggle & Expand per Blok

Toggle sudah built-in di `BlockWrapper` via state `collapsed`. Tambahan — **expand to fullscreen** untuk SVG dan HTML:

```svelte
<!-- Tambahkan di SvgBlock dan HtmlBlock -->
<script lang="ts">
  let fullscreen = false;

  function toggleFullscreen() {
    fullscreen = !fullscreen;
  }
</script>

<!-- Di slot actions -->
<button class="action-btn" onclick={toggleFullscreen}>
  <i class="ti {fullscreen ? 'ti-minimize' : 'ti-maximize'}"></i>
</button>

<!-- Wrapper dengan fullscreen mode -->
<div class:fullscreen-overlay={fullscreen}>
  {#if fullscreen}
    <button class="fs-close" onclick={toggleFullscreen}>
      <i class="ti ti-x"></i>
    </button>
  {/if}
  <!-- konten -->
</div>

<style>
  .fullscreen-overlay {
    position: fixed;
    inset: 0;
    z-index: 999;
    background: var(--color-background-primary);
    padding: 2rem;
    overflow: auto;
  }

  .fs-close {
    position: absolute;
    top: 1rem; right: 1rem;
    background: var(--color-background-secondary);
    border: 0.5px solid var(--color-border-secondary);
    border-radius: 8px;
    padding: 6px 10px;
    cursor: pointer;
    font-size: 16px;
    color: var(--color-text-secondary);
    z-index: 1;
  }
</style>
```

---

## 13. Keamanan (Security)

### Sandbox iframe

```
// Paling aman — tanpa same-origin
sandbox="allow-scripts allow-forms"

// Kenapa TIDAK pakai allow-same-origin:
// allow-scripts + allow-same-origin = script bisa escape sandbox
// via window.parent, document.cookie, localStorage
```

### Blob URL (lebih aman dari srcdoc)

Dengan blob URL, iframe dianggap origin berbeda (`blob:null/...`), sehingga:
- Tidak bisa akses `window.parent.document`
- Tidak bisa akses cookie/localStorage parent
- Komunikasi hanya via `postMessage`

```typescript
const blob = new Blob([html], { type: 'text/html' });
const url = URL.createObjectURL(blob);
// iframe.src = url  ← lebih aman dari srcdoc
```

### Sanitize SVG

```typescript
// src/lib/utils/sanitize.ts

export function sanitizeSvg(raw: string): string {
  const parser = new DOMParser();
  const doc = parser.parseFromString(raw, 'image/svg+xml');

  // Hapus tag berbahaya
  ['script', 'foreignObject', 'use', 'animate'].forEach(tag =>
    doc.querySelectorAll(tag).forEach(el => el.remove())
  );

  // Hapus semua event handler kecuali onclick (untuk sendPrompt)
  doc.querySelectorAll('*').forEach(el => {
    Array.from(el.attributes).forEach(attr => {
      // Hapus on* kecuali onclick
      if (attr.name.startsWith('on') && attr.name !== 'onclick') {
        el.removeAttribute(attr.name);
      }
      // Hapus onclick yang bukan sendPrompt
      if (attr.name === 'onclick') {
        const val = attr.value.trim();
        if (!/^sendPrompt\(['"][^'"]*['"]\)$/.test(val)) {
          el.removeAttribute('onclick');
        }
      }
      // Hapus javascript: di href
      if (['href', 'xlink:href', 'src'].includes(attr.name) &&
          attr.value.trim().toLowerCase().startsWith('javascript:')) {
        el.removeAttribute(attr.name);
      }
    });
  });

  return doc.documentElement.outerHTML;
}
```

### CSP di SvelteKit

```typescript
// src/hooks.server.ts
export const handle = async ({ event, resolve }) => {
  const response = await resolve(event);
  response.headers.set('Content-Security-Policy', [
    "default-src 'self'",
    "script-src 'self' 'unsafe-inline' cdnjs.cloudflare.com cdn.jsdelivr.net esm.sh",
    "style-src 'self' 'unsafe-inline' fonts.googleapis.com",
    "img-src 'self' data: https: blob:",
    "font-src 'self' fonts.gstatic.com",
    "frame-src blob:",
    "connect-src 'self' https://api.anthropic.com",
  ].join('; '));
  return response;
};
```

---

## 14. State Management

```typescript
// src/lib/stores/chat.ts
import { writable, derived } from 'svelte/store';

export interface Message {
  id: string;
  role: 'user' | 'assistant';
  content: string;
  streaming: boolean;
  timestamp: number;
}

function createChatStore() {
  const { subscribe, update, set } = writable<Message[]>([]);

  return {
    subscribe,
    addMessage: (msg: Omit<Message, 'id' | 'timestamp'>) =>
      update(msgs => [...msgs, {
        ...msg, id: crypto.randomUUID(), timestamp: Date.now(),
      }]),
    appendToLast: (chunk: string) =>
      update(msgs => {
        const last = msgs[msgs.length - 1];
        if (!last || last.role !== 'assistant') return msgs;
        return [...msgs.slice(0, -1), { ...last, content: last.content + chunk }];
      }),
    finishStreaming: () =>
      update(msgs => {
        const last = msgs[msgs.length - 1];
        if (!last) return msgs;
        return [...msgs.slice(0, -1), { ...last, streaming: false }];
      }),
    clear: () => set([]),
  };
}

export const chatStore = createChatStore();
export const isStreaming = derived(chatStore, $m => $m.some(m => m.streaming));
```

---

## 15. Komponen Utama: ChatWindow

```svelte
<!-- src/lib/components/Chat/ChatWindow.svelte -->
<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { chatStore, isStreaming } from '$lib/stores/chat';
  import { streamChat } from '$lib/utils/stream';
  import ContentRenderer from '../Renderer/ContentRenderer.svelte';
  import ChatInput from './ChatInput.svelte';

  let scrollEl: HTMLDivElement;

  async function scrollToBottom() {
    await tick();
    scrollEl?.scrollTo({ top: scrollEl.scrollHeight, behavior: 'smooth' });
  }

  async function handleSend(text: string) {
    if (!text.trim() || $isStreaming) return;

    chatStore.addMessage({ role: 'user', content: text, streaming: false });
    chatStore.addMessage({ role: 'assistant', content: '', streaming: true });
    await scrollToBottom();

    const history = $chatStore
      .filter(m => !m.streaming)
      .slice(-20)
      .map(m => ({ role: m.role, content: m.content }));

    await streamChat(
      history,
      (chunk) => { chatStore.appendToLast(chunk); scrollToBottom(); },
      () => chatStore.finishStreaming(),
      (err) => {
        console.error(err);
        chatStore.finishStreaming();
      }
    );
  }
</script>

<div class="chat-window">
  <div class="message-list" bind:this={scrollEl}>
    {#each $chatStore as msg (msg.id)}
      <div class="row row-{msg.role}">
        {#if msg.role === 'user'}
          <div class="user-bubble">{msg.content}</div>
        {:else}
          <div class="assistant-content">
            <ContentRenderer
              raw={msg.content}
              streaming={msg.streaming}
              on:sendprompt={(e) => handleSend(e.detail.text)}
            />
          </div>
        {/if}
      </div>
    {/each}
  </div>

  <ChatInput
    disabled={$isStreaming}
    on:send={(e) => handleSend(e.detail)}
  />
</div>

<style>
  .chat-window {
    display: flex; flex-direction: column;
    height: 100vh; max-width: 760px; margin: 0 auto;
  }

  .message-list {
    flex: 1; overflow-y: auto;
    padding: 1.5rem 1rem;
    display: flex; flex-direction: column; gap: 1.25rem;
  }

  .row { display: flex; }
  .row-user { justify-content: flex-end; }
  .row-assistant { justify-content: flex-start; }

  .user-bubble {
    max-width: 80%;
    background: var(--color-background-info);
    color: var(--color-text-primary);
    padding: 10px 14px;
    border-radius: 14px 14px 4px 14px;
    font-size: 15px; line-height: 1.6;
  }

  .assistant-content { width: 100%; }
</style>
```

### ContentRenderer (Router)

```svelte
<!-- src/lib/components/Renderer/ContentRenderer.svelte -->
<script lang="ts">
  import { createEventDispatcher } from 'svelte';
  import { parseBlocks } from '$lib/utils/parser';
  import TextBlock from './TextBlock.svelte';
  import SvgBlock from './SvgBlock.svelte';
  import HtmlBlock from './HtmlBlock.svelte';
  import ImageGrid from './ImageGrid.svelte';

  export let raw: string;
  export let streaming = false;

  const dispatch = createEventDispatcher<{ sendprompt: { text: string } }>();

  $: blocks = streaming ? null : parseBlocks(raw);
</script>

{#if streaming}
  <div class="streaming-preview">
    {raw}<span class="cursor">▋</span>
  </div>
{:else if blocks}
  {#each blocks as block}
    {#if block.type === 'text'}
      <TextBlock content={block.content} />
    {:else if block.type === 'svg'}
      <SvgBlock content={block.content} title={block.title}
        on:sendprompt={(e) => dispatch('sendprompt', e.detail)} />
    {:else if block.type === 'html'}
      <HtmlBlock content={block.content} title={block.title}
        on:sendprompt={(e) => dispatch('sendprompt', e.detail)} />
    {:else if block.type === 'images'}
      <ImageGrid query={block.query} items={block.items} />
    {/if}
  {/each}
{/if}

<style>
  .streaming-preview {
    white-space: pre-wrap; word-break: break-word;
    font-size: 15px; line-height: 1.7;
    color: var(--color-text-primary);
  }
  .cursor {
    animation: blink 1s step-end infinite;
    color: var(--color-text-secondary);
  }
  @keyframes blink { 50% { opacity: 0; } }
</style>
```

---

## 16. Tips & Gotchas

### Perbandingan tipe render

| Aspek          | Text     | SVG Inline  | HTML iframe   | Image Grid  |
|----------------|----------|-------------|---------------|-------------|
| Isolasi CSS    | Tidak    | Tidak       | Ya (penuh)    | Tidak       |
| JS scope       | Tidak    | Shared      | Isolated      | Tidak       |
| Resize         | Otomatis | Otomatis    | Manual (postMsg) | Otomatis  |
| Action utama   | Copy     | Copy+DL SVG | Copy+DL+Tab   | Lightbox    |
| Toggle         | —        | Ya          | Ya            | Ya          |

### Urutan implementasi yang disarankan

1. Buat `parser.ts` + unit test dulu — ini fondasi semua rendering
2. Buat `BlockWrapper` — shell konsisten untuk semua non-text blok
3. Implementasi `TextBlock` + markdown rendering
4. Implementasi `SvgBlock` + sanitize
5. Implementasi `HtmlBlock` + blob URL + postMessage bridge
6. Implementasi `ImageGrid` + lightbox
7. Wiring ke `ContentRenderer` → `ChatWindow`
8. Tambah streaming handler terakhir

### Streaming — jangan parse sebelum tag lengkap

```typescript
// Cek di onChunk sebelum update UI
if (!isStreamComplete(raw)) {
  // Tampilkan raw text saja (streaming mode)
  return;
}
// Baru parse dan render blok
```

### marked.js untuk markdown

```bash
npm install marked
npm install -D @types/marked
```

```typescript
import { marked } from 'marked';
marked.setOptions({ breaks: true, gfm: true });
const html = marked.parse(content);
```

---

## Referensi

- [Anthropic Claude API](https://docs.claude.com/en/api/overview)
- [SvelteKit Docs](https://kit.svelte.dev/docs)
- [MDN: iframe sandbox](https://developer.mozilla.org/en-US/docs/Web/HTML/Element/iframe#sandbox)
- [MDN: postMessage](https://developer.mozilla.org/en-US/docs/Web/API/Window/postMessage)
- [MDN: URL.createObjectURL](https://developer.mozilla.org/en-US/docs/Web/API/URL/createObjectURL)
- [marked.js](https://marked.js.org)
- [DOMPurify (alternatif sanitize)](https://github.com/cure53/DOMPurify)
