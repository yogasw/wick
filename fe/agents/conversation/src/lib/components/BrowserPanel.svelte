<script lang="ts">
  import { onDestroy } from "svelte";
  import { Effect } from "effect";
  import { WickClientLayer } from "@wick-fe/common-api";
  import {
    listInstances,
    listSessions,
    openSession,
    closeSession,
    newTab,
    closeTab,
    wsURL,
    type BrowserInstance,
    type BrowserSession,
  } from "../api/browser.js";

  /* onError bubbles failures to the parent's toast (panels stay presentational
   * about chrome but own their own WS lifecycle). */
  type Props = { onError: (msg: string) => void };
  let { onError }: Props = $props();

  const run = <A,>(e: Effect.Effect<A, unknown, never>) => Effect.runPromise(e);
  const errMsg = (e: unknown) => (e instanceof Error ? e.message : String(e));

  /* ── selection state ──────────────────────────────────────────── */
  let instances = $state<BrowserInstance[]>([]);
  let instanceId = $state("");
  let sessions = $state<BrowserSession[]>([]);
  let maxTabs = $state(0); // per-session tab cap from the connector (0 = unlimited)
  let sessionId = $state("");
  let tabIndex = $state(0);

  let loadingInstances = $state(true);
  let busy = $state(false);

  /* ── live view state ──────────────────────────────────────────── */
  type Mode = "view" | "full";
  let mode = $state<Mode>("view");
  let connected = $state(false);
  let statusText = $state("");

  /* Zoom = CSS→device scale for the emulated viewport. Lower = more page fits
   * (zoomed out); higher = larger content. The browser viewport is sized to the
   * canvas box / zoom, so there's no letterbox and content is real size. */
  let zoom = $state(1);
  const ZOOM_MIN = 0.5;
  const ZOOM_MAX = 2;
  let viewportEl = $state<HTMLDivElement | undefined>();
  let resizeObs: ResizeObserver | null = null;

  /* ── expand / floating overlay ────────────────────────────────── */
  /* docked = inline in the rail panel; float = draggable+resizable overlay;
   * max = fill the viewport (fullscreen-ish, still same-window). */
  type ViewState = "docked" | "float" | "max";
  let view = $state<ViewState>("docked");

  /* The overlay is position:fixed and can render outside the app's themed
   * container, so Tailwind dark: variants aren't reliable there. Resolve the
   * theme from <html class="dark"> ourselves and drive overlay colors via inline
   * style, so the chrome always matches the wick theme. Re-checked when the
   * overlay opens (view change) since the user can flip theme anytime. */
  let isDark = $state(true);
  function refreshTheme() {
    isDark = typeof document !== "undefined" && document.documentElement.classList.contains("dark");
  }
  // Surface + text tokens for the overlay chrome, resolved from the live theme.
  const ovBg = $derived(isDark ? "#0f1729" : "#ffffff"); // navy-900 / white
  const ovBar = $derived(isDark ? "#1b2438" : "#f1f3f7"); // navy-800 / white-200
  const ovBorder = $derived(isDark ? "#2a3450" : "#d9dde6");
  const ovText = $derived(isDark ? "#f5f7fa" : "#0f1729");
  const ovSub = $derived(isDark ? "#8a93a6" : "#5b6472");
  /* Floating window geometry (px). Seeded once when first floated. */
  let fx = $state(120);
  let fy = $state(90);
  let fw = $state(900);
  let fh = $state(600);

  type Drag = { kind: "move" | "resize"; startX: number; startY: number; ox: number; oy: number; ow: number; oh: number };
  let drag: Drag | null = null;

  function beginDrag(kind: "move" | "resize", ev: PointerEvent) {
    ev.preventDefault();
    (ev.target as HTMLElement).setPointerCapture?.(ev.pointerId);
    drag = { kind, startX: ev.clientX, startY: ev.clientY, ox: fx, oy: fy, ow: fw, oh: fh };
  }
  function onDragMove(ev: PointerEvent) {
    if (!drag) return;
    const dx = ev.clientX - drag.startX;
    const dy = ev.clientY - drag.startY;
    if (drag.kind === "move") {
      fx = Math.max(0, Math.min(window.innerWidth - 120, drag.ox + dx));
      fy = Math.max(0, Math.min(window.innerHeight - 40, drag.oy + dy));
    } else {
      fw = Math.max(320, drag.ow + dx);
      fh = Math.max(240, drag.oh + dy);
    }
  }
  function endDrag(ev: PointerEvent) {
    if (drag) (ev.target as HTMLElement).releasePointerCapture?.(ev.pointerId);
    drag = null;
  }

  let canvasEl = $state<HTMLCanvasElement | undefined>();
  let ws: WebSocket | null = null;
  let cdpId = 1; // monotonic CDP command id
  let ackTimer: number | null = null;

  const selectedSession = $derived(sessions.find((s) => s.session_id === sessionId));
  const tabs = $derived(selectedSession?.tabs ?? []);
  // "+" is blocked when the session already holds the max tabs (0 = unlimited).
  const tabLimitReached = $derived(maxTabs > 0 && tabs.length >= maxTabs);

  /* ── loaders ──────────────────────────────────────────────────── */
  async function loadInstances() {
    loadingInstances = true;
    try {
      instances = await run(listInstances().pipe(Effect.provide(WickClientLayer)));
      // Auto-pick when there's exactly one usable instance.
      const usable = instances.filter((i) => !i.disabled);
      if (!instanceId && usable.length === 1) {
        instanceId = usable[0].id;
        await loadSessions();
      }
    } catch (e) {
      onError(`Load browser instances: ${errMsg(e)}`);
    } finally {
      loadingInstances = false;
    }
  }

  async function loadSessions() {
    if (!instanceId) {
      sessions = [];
      return;
    }
    try {
      const res = await run(listSessions(instanceId).pipe(Effect.provide(WickClientLayer)));
      sessions = res.sessions;
      maxTabs = res.maxTabs;
      // Keep a valid selection.
      if (sessionId && !sessions.some((s) => s.session_id === sessionId)) {
        disconnect();
        sessionId = "";
      }
    } catch (e) {
      onError(`List sessions: ${errMsg(e)}`);
    }
  }

  async function spawn() {
    if (!instanceId || busy) return;
    busy = true;
    try {
      const res = await run(openSession(instanceId).pipe(Effect.provide(WickClientLayer)));
      await loadSessions();
      sessionId = res.session_id;
      tabIndex = 0;
      connect();
    } catch (e) {
      onError(`Open session: ${errMsg(e)}`);
    } finally {
      busy = false;
    }
  }

  async function kill(sid: string) {
    if (busy) return;
    busy = true;
    try {
      if (sid === sessionId) disconnect();
      await run(closeSession(instanceId, sid).pipe(Effect.provide(WickClientLayer)));
      await loadSessions();
    } catch (e) {
      onError(`Close session: ${errMsg(e)}`);
    } finally {
      busy = false;
    }
  }

  // Open a real new tab in the session, then reload the tab list and switch the
  // view to it.
  async function openTab() {
    if (!sessionId || busy) return;
    busy = true;
    try {
      const res = await run(newTab(instanceId, sessionId, "").pipe(Effect.provide(WickClientLayer)));
      await loadSessions();
      if (typeof res?.index === "number") {
        tabIndex = res.index;
        connect();
      }
    } catch (e) {
      onError(`New tab: ${errMsg(e)}`);
    } finally {
      busy = false;
    }
  }

  // Close a tab by index; reload the list and clamp the shown tab.
  async function removeTab(index: number) {
    if (!sessionId || busy) return;
    busy = true;
    try {
      await run(closeTab(instanceId, sessionId, index).pipe(Effect.provide(WickClientLayer)));
      await loadSessions();
      if (tabIndex >= tabs.length) tabIndex = Math.max(0, tabs.length - 1);
      connect();
    } catch (e) {
      onError(`Close tab: ${errMsg(e)}`);
    } finally {
      busy = false;
    }
  }

  /* ── CDP screencast over the proxied WebSocket ────────────────── */
  function send(method: string, params: Record<string, unknown> = {}) {
    if (ws?.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify({ id: cdpId++, method, params }));
    }
  }

  function connect() {
    disconnect();
    if (!instanceId || !sessionId) return;
    statusText = "connecting…";
    const sock = new WebSocket(wsURL(instanceId, sessionId, tabIndex));
    ws = sock;
    sock.onopen = () => {
      connected = true;
      statusText = "live";
      send("Page.enable");
      applyViewport(); // sizes the viewport, then starts the screencast
    };
    sock.onmessage = (ev) => onCdpMessage(ev.data);
    sock.onerror = () => {
      statusText = "connection error";
    };
    sock.onclose = () => {
      connected = false;
      if (statusText === "live") statusText = "disconnected";
    };
  }

  function disconnect() {
    if (ws) {
      const s = ws;
      // Stop the screencast and pause+mute page media BEFORE closing, so a
      // playing video on the server doesn't keep going (with audio) after we
      // drop the view. Best-effort: only if the socket is still open.
      if (s.readyState === WebSocket.OPEN) {
        try {
          s.send(JSON.stringify({ id: cdpId++, method: "Page.stopScreencast", params: {} }));
          s.send(
            JSON.stringify({
              id: cdpId++,
              method: "Runtime.evaluate",
              params: {
                expression:
                  "document.querySelectorAll('video,audio').forEach(m=>{try{m.pause()}catch(e){}m.muted=true;});",
              },
            }),
          );
        } catch { /* ignore */ }
      }
      ws = null;
      try {
        s.close();
      } catch { /* ignore */ }
    }
    connected = false;
    paused = false;
    if (ackTimer) {
      clearTimeout(ackTimer);
      ackTimer = null;
    }
  }

  const startScreencast = () =>
    send("Page.startScreencast", { format: "jpeg", quality: 60, everyNthFrame: 1 });

  // Paused = we stopped consuming frames because the panel isn't visible (tab
  // hidden, panel closed). We stop the screencast AND mute the page's media so a
  // background video/audio doesn't keep playing while nobody's watching.
  let paused = false;

  function pauseStream() {
    if (!connected || paused) return;
    paused = true;
    send("Page.stopScreencast");
    // Mute + pause any playing media so audio stops when you look away.
    send("Runtime.evaluate", {
      expression:
        "document.querySelectorAll('video,audio').forEach(m=>{m.muted=true;try{m.pause()}catch(e){}});",
    });
  }

  function resumeStream() {
    if (!connected || !paused) return;
    paused = false;
    // Unmute media we muted on pause (leave playback where it is — don't auto-play).
    send("Runtime.evaluate", {
      expression: "document.querySelectorAll('video,audio').forEach(m=>{m.muted=false;});",
    });
    applyViewport(); // re-emulate + restart screencast for a fresh frame
  }

  function onVisibility() {
    if (document.hidden) pauseStream();
    else resumeStream();
  }

  // Emulate a viewport matching the on-screen box (÷ zoom), so the page renders
  // at the right size with NO letterbox — the frame fills the canvas. Lower zoom
  // = larger emulated viewport = more page visible (zoomed out). deviceScaleF is
  // kept at 1: we control apparent size via the viewport dimensions, not DPR.
  function applyViewport() {
    if (!connected || !viewportEl) return;
    const rect = viewportEl.getBoundingClientRect();
    const w = Math.max(320, Math.round(rect.width / zoom));
    const h = Math.max(240, Math.round(rect.height / zoom));
    send("Emulation.setDeviceMetricsOverride", {
      width: w,
      height: h,
      deviceScaleFactor: 1,
      mobile: false,
    });
    applyColorScheme();
    // Re-arm screencast so a fresh frame arrives at the new size immediately.
    send("Page.stopScreencast");
    requestAnimationFrame(startScreencast);
  }

  // Make headless Chrome honor the app's theme: emulate prefers-color-scheme so
  // theme-aware sites (YouTube, etc.) render dark when wick is dark instead of
  // defaulting to light. Mirrors document.documentElement.classList "dark".
  function applyColorScheme() {
    const dark = document.documentElement.classList.contains("dark");
    send("Emulation.setEmulatedMedia", {
      features: [{ name: "prefers-color-scheme", value: dark ? "dark" : "light" }],
    });
  }

  function setZoom(z: number) {
    zoom = Math.min(ZOOM_MAX, Math.max(ZOOM_MIN, Math.round(z * 100) / 100));
    applyViewport();
  }

  function onCdpMessage(raw: string) {
    let msg: { method?: string; params?: any };
    try {
      msg = JSON.parse(raw);
    } catch {
      return;
    }
    if (msg.method === "Page.screencastFrame" && msg.params) {
      const { data, sessionId: frameSid, metadata } = msg.params;
      drawFrame(data, metadata);
      // Ack so Chrome keeps sending frames.
      send("Page.screencastFrameAck", { sessionId: frameSid });
    } else if (msg.method === "Page.frameNavigated" && msg.params?.frame && !msg.params.frame.parentId) {
      // Top-frame navigation → reflect the new URL in the address bar (unless
      // the user is mid-edit).
      if (!urlFocused) urlInput = msg.params.frame.url ?? urlInput;
    }
  }

  /* ── address bar (manual navigation) ──────────────────────────── */
  let urlInput = $state("");
  let urlFocused = $state(false);

  function navigate() {
    let u = urlInput.trim();
    if (!u) return;
    if (!/^[a-z]+:\/\//i.test(u)) u = "https://" + u; // bare domain → https
    send("Page.navigate", { url: u });
    urlInput = u;
  }

  // Open a chrome:// internal page in the live session (extensions, settings…).
  function gotoChrome(page: string) {
    if (!connected) return;
    const url = "chrome://" + page;
    urlInput = url;
    send("Page.navigate", { url });
    shortcutsOpen = false;
  }

  // chrome:// shortcuts, shown as an inline expandable chip row under the
  // address bar (not a floating menu) so nothing can overflow or clip in the
  // narrow docked panel. New destinations are cheap to add here.
  const CHROME_PAGES = [
    { label: "Extensions", page: "extensions" },
    { label: "Settings", page: "settings" },
    { label: "History", page: "history" },
    { label: "Downloads", page: "downloads" },
  ];
  let shortcutsOpen = $state(false);

  // Shorten a long session id for the dropdown (keep head + tail); full id is in
  // the option's title tooltip.
  function shortSid(id: string): string {
    return id.length > 20 ? id.slice(0, 10) + "…" + id.slice(-6) : id;
  }

  /* Last frame's CSS-pixel page size, from screencast metadata. CDP input events
   * are in PAGE CSS pixels, not canvas device pixels — we map clicks into this. */
  let frameCssW = 0;
  let frameCssH = 0;

  function drawFrame(
    b64: string,
    metadata: { deviceWidth?: number; deviceHeight?: number; pageScaleFactor?: number },
  ) {
    const canvas = canvasEl;
    if (!canvas) return;
    if (metadata?.deviceWidth) frameCssW = metadata.deviceWidth;
    if (metadata?.deviceHeight) frameCssH = metadata.deviceHeight;
    const img = new Image();
    img.onload = () => {
      // Only resize the backing store when the frame size actually changes.
      // Assigning canvas.width/height clears the surface AND blurs the element,
      // which — if done every frame — stole focus mid-typing (had to click per
      // keystroke). Guarding the assignment keeps keyboard focus stable.
      if (canvas.width !== img.naturalWidth) canvas.width = img.naturalWidth;
      if (canvas.height !== img.naturalHeight) canvas.height = img.naturalHeight;
      const ctx = canvas.getContext("2d");
      ctx?.drawImage(img, 0, 0);
    };
    img.src = "data:image/jpeg;base64," + b64;
  }

  /* ── interactive input (Full mode only) ───────────────────────── */
  // Map a DOM pointer event to CDP page CSS coordinates. The canvas is
  // object-fill (frame stretched to fill the whole element, no letterbox), so
  // it's a straight fraction of the element rect → page CSS pixel space
  // (deviceWidth/Height from screencast metadata), which Input.dispatch* expects.
  function canvasPoint(ev: MouseEvent): { x: number; y: number } | null {
    const canvas = canvasEl;
    if (!canvas || !canvas.width || !canvas.height) return null;
    const rect = canvas.getBoundingClientRect();
    if (!rect.width || !rect.height) return null;

    const fracX = (ev.clientX - rect.left) / rect.width;
    const fracY = (ev.clientY - rect.top) / rect.height;
    if (fracX < 0 || fracY < 0 || fracX > 1 || fracY > 1) return null;

    const pageW = frameCssW || canvas.width;
    const pageH = frameCssH || canvas.height;
    return {
      x: Math.round(fracX * pageW),
      y: Math.round(fracY * pageH),
    };
  }

  // Name of the button currently held (for drag: press → move → release), and
  // its CDP `buttons` bitmask (left=1, right=2, middle=4). "" / 0 = none.
  let heldName = "";
  let heldMask = 0;

  // Map DOM MouseEvent.button (0=left,1=middle,2=right) to CDP name + bitmask.
  function domButton(b: number): { name: string; mask: number } {
    if (b === 2) return { name: "right", mask: 2 };
    if (b === 1) return { name: "middle", mask: 4 };
    return { name: "left", mask: 1 };
  }

  function mouseEvent(type: "mousePressed" | "mouseReleased" | "mouseMoved", ev: MouseEvent) {
    if (mode !== "full" || !connected) return;
    const p = canvasPoint(ev);
    if (!p) return;

    if (type === "mousePressed") {
      canvasEl?.focus(); // focus canvas so keys route to the browser
      const b = domButton(ev.button);
      heldName = b.name;
      heldMask = b.mask;
    }

    // On move, keep reporting the held button (+ bitmask) so Chrome sees a drag
    // (text selection, drag-and-drop). On press/release, use that event's button.
    let name: string;
    let mask: number;
    if (type === "mouseMoved") {
      name = heldName || "none";
      mask = heldMask;
    } else {
      const b = domButton(ev.button);
      name = b.name;
      mask = type === "mousePressed" ? b.mask : 0; // release clears the bitmask
    }

    send("Input.dispatchMouseEvent", {
      type,
      x: p.x,
      y: p.y,
      button: name,
      buttons: mask,
      clickCount: type === "mouseMoved" ? 0 : 1,
    });

    if (type === "mouseReleased") {
      heldName = "";
      heldMask = 0;
    }
  }

  // Keys go to the browser ONLY while the canvas itself holds DOM focus. The
  // canvas is focusable (tabindex) and no longer blurs on every frame (drawFrame
  // only resizes on change), so focus is stable. Anchoring on activeElement ===
  // canvas means we can NEVER steal keystrokes meant for the chat composer,
  // address bar, or anything else — if it's not the canvas, we don't touch it.
  function onCanvasKey(ev: KeyboardEvent, type: "keyDown" | "keyUp") {
    if (mode !== "full" || !connected) return;
    ev.preventDefault();
    // The chat composer has a window-level keydown listener that force-focuses
    // its textarea on any printable key when focus isn't already in an editable
    // element (Composer.svelte). Our <canvas> isn't "editable", so that listener
    // was yanking focus to the composer on every letter (backspace slipped
    // through because it's not length-1). stopPropagation keeps the key from
    // ever reaching that window listener, so focus stays on the canvas.
    ev.stopPropagation();
    const base = {
      key: ev.key,
      code: ev.code,
      windowsVirtualKeyCode: ev.keyCode,
      modifiers:
        (ev.altKey ? 1 : 0) | (ev.ctrlKey ? 2 : 0) | (ev.metaKey ? 4 : 0) | (ev.shiftKey ? 8 : 0),
    };
    if (type === "keyUp") {
      send("Input.dispatchKeyEvent", { type: "keyUp", ...base });
      return;
    }
    // keyDown: send the raw key event, then — for printable characters — a
    // separate "char" event carrying the text. Chrome only commits typed text
    // on the char event; a keyDown with `text` alone does NOT insert the letter
    // (which is why backspace worked but typing didn't).
    const printable = ev.key.length === 1 && !ev.ctrlKey && !ev.metaKey && !ev.altKey;
    send("Input.dispatchKeyEvent", { type: "rawKeyDown", ...base });
    if (printable) {
      send("Input.dispatchKeyEvent", { type: "char", text: ev.key, ...base });
    }
  }

  function onWheel(ev: WheelEvent) {
    if (mode !== "full" || !connected) return;
    ev.preventDefault();
    const p = canvasPoint(ev);
    if (!p) return;
    send("Input.dispatchMouseEvent", {
      type: "mouseWheel",
      x: p.x,
      y: p.y,
      deltaX: -ev.deltaX,
      deltaY: -ev.deltaY,
    });
  }

  /* Reconnect when the selected session or tab changes; seed the address bar
   * from the tab's known URL. */
  function seedURL() {
    const t = tabs.find((x) => x.index === tabIndex);
    if (t && !urlFocused) urlInput = t.url ?? "";
  }
  function onSessionChange() {
    tabIndex = 0;
    seedURL();
    connect();
  }
  function onTabChange() {
    seedURL();
    connect();
  }

  $effect(() => {
    // Load instances once on mount.
    loadInstances();
    // Pause the stream when the tab/window is hidden so a background video
    // doesn't keep streaming (and playing audio) while you're on another page.
    document.addEventListener("visibilitychange", onVisibility);
    return () => {
      document.removeEventListener("visibilitychange", onVisibility);
      disconnect();
      resizeObs?.disconnect();
    };
  });

  // Re-emulate the viewport (and re-arm the screencast) whenever the on-screen
  // box changes size — view-mode switch, float resize, or window resize. The
  // ResizeObserver is (re)bound to whichever canvas box is currently mounted.
  $effect(() => {
    view; // track: canvas remounts on view change
    refreshTheme(); // overlay opens on view change — resolve theme for its colors
    resizeObs?.disconnect();
    if (viewportEl) {
      resizeObs = new ResizeObserver(() => applyViewport());
      resizeObs.observe(viewportEl);
    }
    applyViewport();
  });

  onDestroy(disconnect);
</script>

<div class="flex-1 overflow-y-auto p-3 space-y-3">
  <!-- Instance + session controls -->
  <div class="rounded-xl border border-white-300 dark:border-navy-600 p-3 space-y-2">
    {#if loadingInstances}
      <p class="text-xs text-black-700 dark:text-black-600">Loading…</p>
    {:else if instances.length === 0}
      <p class="text-xs text-black-700 dark:text-black-600">
        No <span class="font-mono">playwright_browser</span> connector configured. Install the plugin and add an instance to use the live browser.
      </p>
    {:else}
      <label class="block text-xs font-medium text-black-800 dark:text-white-200">Connector instance</label>
      <select
        class="w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-sm text-black-900 dark:text-white-100 focus:border-green-500 focus:ring-2 focus:ring-green-200 dark:focus:ring-green-800 focus:outline-none"
        bind:value={instanceId}
        onchange={() => { disconnect(); sessionId = ""; loadSessions(); }}
        data-testid="browser-instance"
      >
        <option value="">Select an instance…</option>
        {#each instances as inst (inst.id)}
          <option value={inst.id} disabled={inst.disabled}>{inst.label}{inst.disabled ? " (disabled)" : ""}</option>
        {/each}
      </select>

      {#if instanceId}
        <div class="flex items-center gap-2 pt-1">
          <button
            type="button"
            class="rounded-lg bg-green-500 px-3 py-1.5 text-xs font-medium text-white-100 hover:bg-green-600 active:bg-green-700 transition-colors disabled:opacity-50"
            disabled={busy}
            onclick={spawn}
            data-testid="browser-open"
          >{busy ? "Working…" : "New session"}</button>
          <button
            type="button"
            class="rounded-lg border border-white-400 dark:border-navy-600 px-3 py-1.5 text-xs font-medium text-black-700 dark:text-white-200 hover:border-green-400 transition-colors"
            onclick={loadSessions}
          >Refresh</button>
        </div>

        {#if sessions.length > 0}
          <label class="block text-xs font-medium text-black-800 dark:text-white-200 pt-1">Live session</label>
          <select
            class="w-full max-w-full truncate rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-sm text-black-900 dark:text-white-100 focus:border-green-500 focus:outline-none"
            bind:value={sessionId}
            onchange={onSessionChange}
            title={sessionId}
            data-testid="browser-session"
          >
            <option value="">Select a session…</option>
            {#each sessions as s (s.session_id)}
              <option value={s.session_id} title={`${s.session_id} · ${s.browser}`}>{shortSid(s.session_id)} · {s.browser}</option>
            {/each}
          </select>
        {:else}
          <p class="text-xs text-black-700 dark:text-black-600 pt-1">No live sessions. Open one to start.</p>
        {/if}
      {/if}
    {/if}
  </div>

  <!-- Live view (only when docked; float/max render in the overlay below) -->
  {#if sessionId && view === "docked"}
    <div class="rounded-xl border border-white-300 dark:border-navy-600 p-3 space-y-2">
      {@render toolbar()}
      {@render addressBar()}
      {@render tabSwitcher()}
      {@render screen("max-h-[60vh]")}
      {@render fullHint()}
    </div>
  {/if}
</div>

<!-- Floating / maximized overlay -->
{#if sessionId && view !== "docked"}
  <!-- Backdrop only in fullscreen (max), where the browser view should own the
       whole screen. In float mode there is NO backdrop, so the rest of the UI
       (chat, rail, etc.) stays clickable behind the floating window. -->
  {#if view === "max"}
    <div class="fixed inset-0 z-40 bg-black-900/60" aria-hidden="true"></div>
  {/if}
  <div
    class={view === "max"
      ? "fixed inset-0 z-50 flex flex-col"
      : "fixed z-50 flex flex-col rounded-xl border shadow-2xl overflow-hidden"}
    style={`background:${ovBg}; border-color:${ovBorder}; color:${ovText};` +
      (view === "float" ? ` left:${fx}px; top:${fy}px; width:${fw}px; height:${fh}px;` : "")}
    data-testid="browser-overlay"
  >
    <!-- Title bar — solid background so it clearly reads as a window chrome.
         Draggable in float mode. Colors resolved from the live theme (inline,
         not dark: variants) because a fixed overlay may sit outside the themed
         container. -->
    <div
      class={"flex items-center gap-2.5 px-3 py-2.5 border-b " + (view === "float" ? "cursor-move select-none" : "")}
      style={`background:${ovBar}; border-color:${ovBorder};`}
      onpointerdown={view === "float" ? (e) => beginDrag("move", e) : undefined}
      onpointermove={onDragMove}
      onpointerup={endDrag}
    >
      <span class={"h-2 w-2 shrink-0 rounded-full " + (connected ? "bg-green-500" : "bg-black-500")}></span>
      <span class="text-sm font-semibold shrink-0" style={`color:${ovText};`}>Live browser</span>
      <span class="font-mono text-[11px] truncate" style={`color:${ovSub};`}>{sessionId}</span>
      <div class="ml-auto flex items-center gap-1">
        {@render viewBtn("docked", "Dock into panel", "M3 3h10v10H3z")}
        {@render viewBtn("float", "Floating window", "M2 4h12v8H2z M2 4l4 3")}
        {@render viewBtn("max", "Fullscreen", "M2 6V2h4 M14 6V2h-4 M2 10v4h4 M14 10v4h-4")}
      </div>
    </div>

    <!-- Toolbar + screen fill the overlay body -->
    <div class="flex-1 min-h-0 flex flex-col p-3 gap-2 overflow-hidden" style={`background:${ovBg}; color:${ovText};`}>
      {@render toolbar()}
      {@render addressBar()}
      {@render tabSwitcher()}
      <div class="flex-1 min-h-0">
        {@render screen("h-full")}
      </div>
      {@render fullHint()}
    </div>

    <!-- Resize handle (float only) -->
    {#if view === "float"}
      <div
        class="absolute bottom-0 right-0 h-4 w-4 cursor-nwse-resize"
        onpointerdown={(e) => beginDrag("resize", e)}
        onpointermove={onDragMove}
        onpointerup={endDrag}
        aria-label="Resize"
      >
        <svg viewBox="0 0 16 16" class="h-4 w-4 text-black-600"><path d="M11 15L15 11M6 15L15 6" stroke="currentColor" stroke-width="1.5" fill="none"/></svg>
      </div>
    {/if}
  </div>
{/if}

<!-- ── Reusable snippets ──────────────────────────────────────────── -->

{#snippet toolbar()}
  <div class="flex items-center gap-2 flex-wrap">
    <div class="inline-flex rounded-lg border border-white-400 dark:border-navy-600 overflow-hidden text-xs font-medium">
      <button
        type="button"
        class={"px-3 py-1.5 transition-colors " + (mode === "view" ? "bg-green-500 text-white-100" : "bg-white-100 dark:bg-navy-800 text-black-700 dark:text-white-200")}
        onclick={() => (mode = "view")}
        data-testid="browser-mode-view"
      >View only</button>
      <button
        type="button"
        class={"px-3 py-1.5 transition-colors " + (mode === "full" ? "bg-green-500 text-white-100" : "bg-white-100 dark:bg-navy-800 text-black-700 dark:text-white-200")}
        onclick={() => (mode = "full")}
        data-testid="browser-mode-full"
      >Full</button>
    </div>

    <span class={"rounded-full px-2 py-0.5 text-[10px] font-medium " + (connected ? "bg-green-100 text-green-700 dark:bg-green-900 dark:text-green-300" : "bg-white-300 text-black-600 dark:bg-navy-700 dark:text-black-600")}>
      {statusText || "idle"}
    </span>

    <!-- Zoom: sizes the emulated viewport (out = more page fits). -->
    <div class="flex items-center gap-0.5 rounded-lg border border-white-400 dark:border-navy-600 p-0.5">
      <button type="button" title="Zoom out" aria-label="Zoom out" class="rounded-md px-1.5 py-1 text-xs font-medium text-black-700 dark:text-white-200 hover:bg-white-300 dark:hover:bg-navy-700 disabled:opacity-40" disabled={zoom <= ZOOM_MIN} onclick={() => setZoom(zoom - 0.25)}>−</button>
      <button type="button" title="Reset zoom" class="rounded-md px-1.5 py-1 text-[10px] font-mono text-black-700 dark:text-white-200 hover:bg-white-300 dark:hover:bg-navy-700 tabular-nums" onclick={() => setZoom(1)}>{Math.round(zoom * 100)}%</button>
      <button type="button" title="Zoom in" aria-label="Zoom in" class="rounded-md px-1.5 py-1 text-xs font-medium text-black-700 dark:text-white-200 hover:bg-white-300 dark:hover:bg-navy-700 disabled:opacity-40" disabled={zoom >= ZOOM_MAX} onclick={() => setZoom(zoom + 0.25)}>+</button>
    </div>

    <!-- Expand controls: shown docked; the overlay has its own in the header. -->
    {#if view === "docked"}
      <div class="flex items-center gap-0.5 rounded-lg border border-white-400 dark:border-navy-600 p-0.5">
        {@render viewBtn("float", "Pop out to window", "M2 4h12v8H2z M2 4l4 3")}
        {@render viewBtn("max", "Fullscreen", "M2 6V2h4 M14 6V2h-4 M2 10v4h4 M14 10v4h-4")}
      </div>
    {/if}

    <button
      type="button"
      class="ml-auto text-[11px] font-medium text-neg-400 hover:underline"
      onclick={() => kill(sessionId)}
      data-testid="browser-close"
    >Close session</button>
  </div>
{/snippet}

{#snippet viewBtn(target: ViewState, label: string, path: string)}
  <button
    type="button"
    title={label}
    aria-label={label}
    class={"rounded-md p-1.5 transition-colors " + (view === target ? "bg-green-500 text-white-100" : "text-black-700 dark:text-white-200 hover:bg-white-300 dark:hover:bg-navy-700")}
    onclick={() => (view = target)}
  >
    <svg viewBox="0 0 16 16" class="h-3.5 w-3.5"><path d={path} stroke="currentColor" stroke-width="1.3" fill="none" stroke-linecap="round" stroke-linejoin="round"/></svg>
  </button>
{/snippet}

{#snippet addressBar()}
  <div class="space-y-1.5">
    <!-- URL row: globe · input · Go, all one height. -->
    <div class="flex h-8 items-stretch gap-1.5">
      <div class="flex flex-1 items-center rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 focus-within:border-green-500 focus-within:ring-2 focus-within:ring-green-200 dark:focus-within:ring-green-800">
        <svg viewBox="0 0 16 16" class="ml-2.5 h-3.5 w-3.5 shrink-0 text-black-600"><circle cx="8" cy="8" r="6" stroke="currentColor" stroke-width="1.2" fill="none"/><path d="M2 8h12M8 2c2 2 2.5 4 2.5 6S10 12 8 14M8 2C6 4 5.5 6 5.5 8S6 12 8 14" stroke="currentColor" stroke-width="1" fill="none"/></svg>
        <input
          class="min-w-0 flex-1 bg-transparent px-2 text-xs text-black-900 dark:text-white-100 placeholder-black-600 focus:outline-none"
          bind:value={urlInput}
          onfocus={() => (urlFocused = true)}
          onblur={() => (urlFocused = false)}
          onkeydown={(e) => { if (e.key === "Enter") { e.preventDefault(); navigate(); } }}
          placeholder="Type a URL and press Enter…"
          data-testid="browser-url"
        />
      </div>
      <button
        type="button"
        class="shrink-0 rounded-lg bg-green-500 px-3 text-xs font-medium text-white-100 hover:bg-green-600 active:bg-green-700 transition-colors disabled:opacity-50"
        disabled={!connected}
        onclick={navigate}
        data-testid="browser-go"
      >Go</button>
    </div>

    <!-- Shortcuts: a toggle that reveals chrome:// chips inline (no floating
         menu, so nothing can overflow/clip in the narrow docked panel). -->
    <div>
      <button
        type="button"
        class="inline-flex items-center gap-1 text-[11px] font-medium text-black-700 dark:text-black-500 hover:text-black-900 dark:hover:text-white-200 disabled:opacity-50"
        disabled={!connected}
        onclick={() => (shortcutsOpen = !shortcutsOpen)}
        aria-expanded={shortcutsOpen}
        data-testid="browser-shortcuts"
      >
        <svg viewBox="0 0 16 16" class={"h-3 w-3 transition-transform " + (shortcutsOpen ? "rotate-90" : "")}><path d="M6 4l4 4-4 4" stroke="currentColor" stroke-width="1.5" fill="none" stroke-linecap="round" stroke-linejoin="round"/></svg>
        Shortcuts
      </button>
      {#if shortcutsOpen}
        <div class="mt-1.5 flex flex-wrap gap-1.5">
          {#each CHROME_PAGES as p (p.page)}
            <button
              type="button"
              class="rounded-full border border-white-400 dark:border-navy-600 px-2.5 py-1 text-[11px] text-black-800 dark:text-white-200 hover:border-green-400 hover:text-green-600 dark:hover:text-green-400 transition-colors"
              onclick={() => gotoChrome(p.page)}
            >{p.label}</button>
          {/each}
        </div>
      {/if}
    </div>
  </div>
{/snippet}

{#snippet tabSwitcher()}
  <!-- Hidden when there's nothing to manage: a single tab that also can't grow
       (single-tab session). Shown once there are 2+ tabs, or "+" is available. -->
  {#if tabs.length > 1 || (tabs.length > 0 && !tabLimitReached)}
    <div class="flex items-center gap-1.5">
      <select
        class="min-w-0 flex-1 rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-2 py-1 text-xs text-black-900 dark:text-white-100 focus:outline-none"
        bind:value={tabIndex}
        onchange={onTabChange}
        title="Switch the tab shown in the view"
      >
        {#each tabs as t (t.index)}
          <option value={t.index} title={t.url}>Tab {t.index + 1}: {t.title || t.url || "(blank)"}</option>
        {/each}
      </select>
      <!-- Open a real new tab in the session. -->
      <!-- Hidden entirely at the tab cap (0 = unlimited → always shown). -->
      {#if !tabLimitReached}
        <button
          type="button"
          title="New tab"
          aria-label="New tab"
          class="shrink-0 rounded-md border border-white-400 dark:border-navy-600 p-1 text-black-700 dark:text-white-200 hover:border-green-400 transition-colors disabled:opacity-50"
          disabled={busy || !connected}
          onclick={openTab}
          data-testid="browser-tab-new"
        >
          <svg viewBox="0 0 16 16" class="h-3 w-3"><path d="M8 3v10M3 8h10" stroke="currentColor" stroke-width="1.5" fill="none" stroke-linecap="round"/></svg>
        </button>
      {/if}
      <!-- Close the currently shown tab (only when more than one). -->
      {#if tabs.length > 1}
        <button
          type="button"
          title="Close this tab"
          aria-label="Close tab"
          class="shrink-0 rounded-md border border-white-400 dark:border-navy-600 p-1 text-neg-400 hover:border-neg-400 transition-colors disabled:opacity-50"
          disabled={busy}
          onclick={() => removeTab(tabIndex)}
          data-testid="browser-tab-close"
        >
          <svg viewBox="0 0 16 16" class="h-3 w-3"><path d="M4 4l8 8M12 4l-8 8" stroke="currentColor" stroke-width="1.5" fill="none" stroke-linecap="round"/></svg>
        </button>
      {/if}
      <button
        type="button"
        title="Refresh tab list"
        aria-label="Refresh tabs"
        class="shrink-0 rounded-md border border-white-400 dark:border-navy-600 p-1 text-black-700 dark:text-white-200 hover:border-green-400 transition-colors"
        onclick={() => loadSessions()}
      >
        <svg viewBox="0 0 16 16" class="h-3 w-3"><path d="M13 3v3h-3M3 13v-3h3M13 6a5 5 0 00-9-1M3 10a5 5 0 009 1" stroke="currentColor" stroke-width="1.3" fill="none" stroke-linecap="round" stroke-linejoin="round"/></svg>
      </button>
    </div>
  {/if}
{/snippet}

{#snippet screen(sizeCls: string)}
  <!-- Screencast canvas. tabindex makes it focusable for keyboard input. -->
  <div bind:this={viewportEl} class={"relative rounded-lg overflow-hidden bg-black-900 border border-white-400 dark:border-navy-700 " + sizeCls}>
    <canvas
      bind:this={canvasEl}
      class={"w-full h-full block object-fill bg-transparent " + (mode === "full" ? "cursor-crosshair" : "cursor-default")}
      tabindex={mode === "full" ? 0 : -1}
      onmousedown={(e) => mouseEvent("mousePressed", e)}
      onmouseup={(e) => mouseEvent("mouseReleased", e)}
      onmousemove={(e) => mouseEvent("mouseMoved", e)}
      onwheel={onWheel}
      onkeydown={(e) => onCanvasKey(e, "keyDown")}
      onkeyup={(e) => onCanvasKey(e, "keyUp")}
      oncontextmenu={(e) => e.preventDefault()}
      data-testid="browser-canvas"
    ></canvas>
    {#if !connected}
      <div class="absolute inset-0 flex items-center justify-center text-xs text-white-300 pointer-events-none">
        {statusText || "not connected"}
      </div>
    {/if}
  </div>
{/snippet}

{#snippet fullHint()}
  {#if mode === "full"}
    <p class="text-[11px] text-black-700 dark:text-black-600">
      Full mode: click and type directly on the view. Click the view first to capture keyboard.
    </p>
  {/if}
{/snippet}
