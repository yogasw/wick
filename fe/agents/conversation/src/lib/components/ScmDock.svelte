<script lang="ts">
  /*
   * Purpose:    Slide-over dock that hosts the SCM Svelte island (window.WickSCM).
   * Caller:     conversation SPA App.svelte (Slice 9 cutover).
   * Deps:       window.WickSCM (lazy-loaded from assetUrl), localStorage.
   * Main fns:   open/close toggle, lazy bundle load, drag-resize (persisted).
   * Side fx:    mutates localStorage (wick.scm.width), injects <script> tag once.
   */

  const WIDTH_KEY = "wick.scm.width";
  const MIN_W = 240;
  const MAX_W = 640;
  const DEFAULT_W = 260;

  type MountOpts = { sessionID: string; mode: "sidebar" };

  type Props = {
    sessionId: string;
    assetUrl: string;
    open?: boolean;
    changeCount?: number;
    onOpenChange?: (open: boolean) => void;
    mountIsland?: (host: HTMLElement, opts: MountOpts) => void;
    unmountIsland?: (host: HTMLElement) => void;
    loadBundle?: (assetUrl: string) => Promise<void>;
  };

  let {
    sessionId,
    assetUrl,
    open = $bindable(false),
    changeCount = 0,
    onOpenChange,
    mountIsland = (host: HTMLElement, opts: MountOpts) => {
      (window as unknown as { WickSCM?: { mount: (h: HTMLElement, o: MountOpts) => void } }).WickSCM?.mount(host, opts);
    },
    unmountIsland = (host: HTMLElement) => {
      (window as unknown as { WickSCM?: { unmount: (h: HTMLElement) => void } }).WickSCM?.unmount(host);
    },
    loadBundle = (url: string): Promise<void> => {
      const w = window as unknown as { WickSCM?: unknown };
      if (w.WickSCM) return Promise.resolve();
      return new Promise<void>((resolve, reject) => {
        const s = document.createElement("script");
        s.type = "module";
        s.src = url;
        s.onload = () => {
          if ((window as unknown as { WickSCM?: unknown }).WickSCM) resolve();
          else reject(new Error("WickSCM not installed"));
        };
        s.onerror = () => reject(new Error("failed to load scm bundle"));
        document.head.appendChild(s);
      });
    },
  }: Props = $props();

  let hostEl: HTMLElement | undefined = $state(undefined);
  let mounted = false;
  let panelWidth = $state(savedWidth());
  let dragging = false;

  function savedWidth(): number {
    const raw = localStorage.getItem(WIDTH_KEY);
    const w = parseInt(raw ?? String(DEFAULT_W), 10);
    return isNaN(w) ? DEFAULT_W : Math.min(MAX_W, Math.max(MIN_W, w));
  }

  function applyWidth(w: number) {
    const clamped = Math.min(MAX_W, Math.max(MIN_W, w));
    panelWidth = clamped;
    localStorage.setItem(WIDTH_KEY, String(clamped));
  }

  async function doOpen() {
    open = true;
    onOpenChange?.(true);
    applyWidth(savedWidth());
    if (!hostEl || mounted) return;
    try {
      await loadBundle(assetUrl);
      mountIsland(hostEl, { sessionID: sessionId, mode: "sidebar" });
      mounted = true;
    } catch (_) {
      /* bundle load failure — island stays blank */
    }
  }

  function doClose() {
    open = false;
    onOpenChange?.(false);
    /* Island stays mounted (keep state); source JS also keeps it mounted on close */
  }

  function onToggle() {
    if (open) doClose();
    else doOpen();
  }

  function onMouseDown(e: MouseEvent) {
    dragging = true;
    e.preventDefault();
    document.body.style.userSelect = "none";
  }

  function onMouseMove(e: MouseEvent) {
    if (!dragging) return;
    const panel = (e.currentTarget as Window).document.querySelector<HTMLElement>("[data-scm-panel]");
    if (!panel) return;
    const rect = panel.getBoundingClientRect();
    applyWidth(rect.right - e.clientX);
  }

  function onMouseUp() {
    if (!dragging) return;
    dragging = false;
    document.body.style.userSelect = "";
  }

  const badgeText = $derived(changeCount > 99 ? "99+" : String(changeCount));
  const badgeVisible = $derived(changeCount > 0);
</script>

<svelte:window onmousemove={onMouseMove} onmouseup={onMouseUp} />

<!-- FAB open button -->
<button
  type="button"
  aria-label="Source Control"
  onclick={onToggle}
  class="relative inline-flex h-10 w-10 items-center justify-center rounded-full bg-navy-900 text-white-100 shadow-lg hover:bg-navy-700 transition-colors"
>
  <!-- Branch icon -->
  <svg viewBox="0 0 16 16" class="h-5 w-5" fill="none" stroke="currentColor" stroke-width="1.5">
    <circle cx="4" cy="4" r="1.5" />
    <circle cx="4" cy="12" r="1.5" />
    <circle cx="12" cy="4" r="1.5" />
    <path d="M4 5.5v5M4 5.5C4 8 12 8 12 5.5" stroke-linecap="round" stroke-linejoin="round" />
  </svg>

  <!-- Change count badge -->
  <span
    data-scm-badge
    class={badgeVisible ? "absolute -top-1 -right-1 inline-flex h-4 min-w-4 items-center justify-center rounded-full bg-green-500 px-1 text-[10px] font-semibold text-white-100" : "hidden"}
    aria-hidden={!badgeVisible}
  >{badgeText}</span>
</button>

<!-- Slide-over panel -->
<aside
  data-scm-panel
  class={open ? "fixed inset-y-0 right-0 flex flex-col bg-white-100 dark:bg-navy-900 shadow-xl z-50 border-l border-white-300 dark:border-navy-700" : "hidden"}
  style="width: {panelWidth}px"
>
  <!-- Resize handle (left edge of panel) — drag only, no keyboard alternative -->
  <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
  <div
    data-scm-resize
    role="separator"
    aria-label="Resize dock"
    aria-orientation="vertical"
    class="absolute left-0 inset-y-0 w-1.5 cursor-col-resize hover:bg-green-400/40 transition-colors"
    onmousedown={onMouseDown}
  ></div>

  <!-- Panel header -->
  <div class="flex items-center justify-between px-4 py-3 border-b border-white-300 dark:border-navy-700 shrink-0">
    <span class="text-sm font-medium text-black-900 dark:text-white-100">Source Control</span>
    <button
      type="button"
      aria-label="Close"
      onclick={doClose}
      class="inline-flex h-7 w-7 items-center justify-center rounded-lg text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800 transition-colors"
    >
      <svg viewBox="0 0 16 16" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="1.5">
        <path d="M4 4l8 8M12 4l-8 8" stroke-linecap="round" />
      </svg>
    </button>
  </div>

  <!-- SCM island host -->
  <div class="flex-1 overflow-hidden" bind:this={hostEl}></div>
</aside>
