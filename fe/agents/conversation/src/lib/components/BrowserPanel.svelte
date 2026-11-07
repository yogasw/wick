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
   * about chrome but own their own WS lifecycle). view is bindable so the
   * parent can hide its side panel while the browser floats or fills the
   * screen, and dock it back from its own rail button. */
  type ViewState = "docked" | "float" | "max";
  type Props = { onError: (msg: string) => void; view?: ViewState };
  let { onError, view = $bindable("docked") }: Props = $props();

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
  let loadingSessions = $state(false);
  // Bumped per loadSessions call so a slow, older response can't overwrite a
  // newer one (e.g. after switching instance mid-request).
  let sessionsSeq = 0;
  let busy = $state(false);

  /* ── live view state ──────────────────────────────────────────── */
  /* The panel shows the AGENT'S point of view in every mode: it never resizes
   * the remote viewport, never changes its colour scheme and never runs page
   * scripts on its own, so opening the panel cannot shift the agent's
   * coordinates or interrupt a step. The frame is letterboxed into the box.
   * view: watch only (no input at all). full: a person may click, type and
   * navigate. The proxy enforces the same split (connectors_browser_proxy.go),
   * so an older tab cannot do more than its mode allows. */
  type Mode = "view" | "full";
  let mode = $state<Mode>("view");
  let connected = $state(false);
  let statusText = $state("");

  /* Right-click context menu on the live view — a small HTML menu we render
   * ourselves (the remote's native menu can't be streamed), giving discoverable
   * Copy/Paste plus the cross-machine PC clipboard actions. Positioned at the
   * click in client coords; open=false hides it. */
  let ctxMenu = $state<{ open: boolean; x: number; y: number }>({ open: false, x: 0, y: 0 });

  let viewportEl = $state<HTMLDivElement | undefined>();
  let resizeObs: ResizeObserver | null = null;

  /* ── expand / floating overlay ────────────────────────────────── */
  /* docked = inline in the rail panel; float = draggable+resizable overlay;
   * max = fill the viewport (fullscreen-ish, still same-window). Leaving max
   * goes back to where it was opened from. */
  let prevView: ViewState = "docked";
  function setView(next: ViewState) {
    if (next === view) return;
    if (next === "max") prevView = view;
    if (next === "float") seedFloat();
    view = next;
    menuOpen = false;
  }
  function exitMax() {
    setView(prevView === "max" ? "docked" : prevView);
  }

  /* The ⋯ menu holds what the toolbar has no room for: the address bar,
   * chrome:// shortcuts, copying the session id and closing the session. */
  let menuOpen = $state(false);
  let showAddress = $state(false);

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
  /* Floating window geometry (px). Seeded when floated: on a phone it spans
   * the screen width under the app header (a 16:10 frame plus the title bar);
   * on a desktop it keeps the last spot, pulled back inside a smaller window. */
  let fx = $state(120);
  let fy = $state(90);
  let fw = $state(900);
  let fh = $state(600);
  const FLOAT_BAR_PX = 44;
  function seedFloat() {
    if (typeof window === "undefined") return;
    const W = window.innerWidth;
    const H = window.innerHeight;
    if (W < 640) {
      fw = W - 16;
      fh = Math.round(fw * 10 / 16) + FLOAT_BAR_PX;
      fx = 8;
      fy = 64;
      return;
    }
    fw = Math.min(fw, W - 40);
    fh = Math.min(fh, H - 40);
    fx = Math.max(0, Math.min(fx, W - fw));
    fy = Math.max(0, Math.min(fy, H - fh));
  }

  type Drag = { kind: "move" | "resize"; startX: number; startY: number; ox: number; oy: number; ow: number; oh: number };
  let drag: Drag | null = null;

  function beginDrag(kind: "move" | "resize", ev: PointerEvent) {
    // The title bar holds buttons too: a press on one is a click, not a drag.
    if ((ev.target as HTMLElement).closest("button")) return;
    ev.preventDefault();
    (ev.target as HTMLElement).setPointerCapture?.(ev.pointerId);
    drag = { kind, startX: ev.clientX, startY: ev.clientY, ox: fx, oy: fy, ow: fw, oh: fh };
  }
  function onDragMove(ev: PointerEvent) {
    if (!drag) return;
    const dx = ev.clientX - drag.startX;
    const dy = ev.clientY - drag.startY;
    if (drag.kind === "move") {
      fx = Math.max(0, Math.min(window.innerWidth - Math.min(fw, 120), drag.ox + dx));
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

  // CDP commands awaiting a result (only sendCmd registers here), keyed by id.
  // Used to read the remote page's selection back for the copy→PC clipboard step.
  const pendingCmds = new Map<
    number,
    { resolve: (v: any) => void; reject: (e: any) => void; timer: number }
  >();

  const selectedSession = $derived(sessions.find((s) => s.session_id === sessionId));
  const tabs = $derived(selectedSession?.tabs ?? []);
  // "+" is blocked when the session already holds the max tabs (0 = unlimited).
  const tabLimitReached = $derived(maxTabs > 0 && tabs.length >= maxTabs);

  /* ── session cards ────────────────────────────────────────────── */
  function fmtDur(sec: number | undefined): string {
    if (sec === undefined || sec < 0) return "";
    if (sec < 60) return sec < 10 ? "just now" : `${sec}s`;
    const m = Math.round(sec / 60);
    return m < 60 ? `${m} min` : `${Math.floor(m / 60)}h ${m % 60}m`;
  }
  function hostOf(url: string | undefined): string {
    if (!url) return "";
    try {
      const u = new URL(url);
      return u.host || url;
    } catch {
      return url;
    }
  }
  function sessionName(s: BrowserSession | undefined): string {
    return s?.profile || "anonymous";
  }
  function sessionMeta(s: BrowserSession): string {
    const parts = [hostOf(s.tabs?.[0]?.url) || "no tab"];
    if (s.idle_seconds !== undefined) parts.push(`idle ${fmtDur(s.idle_seconds)}`);
    if (s.auto_close_in_seconds !== undefined) parts.push(`auto-close in ${fmtDur(s.auto_close_in_seconds)}`);
    return parts.join(" · ");
  }

  function openSession_(sid: string) {
    sessionId = sid;
    onSessionChange();
  }
  // Back to the session list: the view stops streaming, nothing is closed.
  function backToList() {
    disconnect();
    sessionId = "";
    menuOpen = false;
    showAddress = false;
    view = "docked";
  }

  async function copySessionId() {
    menuOpen = false;
    try {
      await navigator.clipboard.writeText(sessionId);
    } catch (e) {
      onError(`Copy session id: ${errMsg(e)}`);
    }
  }

  // The instance picked last time, so a panel with several instances opens
  // straight on its session list.
  const LAST_INSTANCE_KEY = "wick.browser.instance";
  function readLastInstance(): string {
    try {
      return localStorage.getItem(LAST_INSTANCE_KEY) ?? "";
    } catch {
      return "";
    }
  }

  /* ── loaders ──────────────────────────────────────────────────── */
  async function loadInstances() {
    loadingInstances = true;
    try {
      instances = await run(listInstances().pipe(Effect.provide(WickClientLayer)));
      // Auto-pick the only usable instance, else the one picked last time.
      const usable = instances.filter((i) => !i.disabled);
      const last = usable.find((i) => i.id === readLastInstance());
      if (!instanceId && (usable.length === 1 || last)) {
        instanceId = (last ?? usable[0]).id;
        await loadSessions();
      }
    } catch (e) {
      onError(`Load browser instances: ${errMsg(e)}`);
    } finally {
      loadingInstances = false;
    }
  }

  async function loadSessions(quiet = false) {
    const seq = ++sessionsSeq;
    if (!instanceId) {
      sessions = [];
      loadingSessions = false;
      return;
    }
    const inst = instanceId;
    if (!quiet) loadingSessions = true;
    try {
      const res = await run(listSessions(inst).pipe(Effect.provide(WickClientLayer)));
      if (seq !== sessionsSeq || inst !== instanceId) return; // superseded
      sessions = res.sessions;
      maxTabs = res.maxTabs;
      // Keep a valid selection.
      if (sessionId && !sessions.some((s) => s.session_id === sessionId)) {
        disconnect();
        sessionId = "";
      }
    } catch (e) {
      if (!quiet && seq === sessionsSeq) onError(`List sessions: ${errMsg(e)}`);
    } finally {
      if (seq === sessionsSeq) loadingSessions = false;
    }
  }

  // Pick an instance from the event's own value: the bind:value update and the
  // onchange handler aren't guaranteed to run in that order, so reading
  // instanceId here could still see the old (empty) value and list nothing.
  function selectInstance(id: string) {
    disconnect();
    instanceId = id;
    try {
      localStorage.setItem(LAST_INSTANCE_KEY, id);
    } catch { /* storage off: just not remembered */ }
    sessionId = "";
    sessions = [];
    void loadSessions();
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

  // send() + a Promise resolving with the command's CDP result — for the one
  // place we need a return value: reading the remote selection for copy→PC.
  function sendCmd(method: string, params: Record<string, unknown> = {}): Promise<any> {
    if (ws?.readyState !== WebSocket.OPEN) return Promise.reject(new Error("not connected"));
    const id = cdpId++;
    return new Promise((resolve, reject) => {
      const timer = window.setTimeout(() => {
        pendingCmds.delete(id);
        reject(new Error(`CDP ${method} timed out`));
      }, 3000);
      pendingCmds.set(id, { resolve, reject, timer });
      ws!.send(JSON.stringify({ id, method, params }));
    });
  }

  /* Auto-reconnect: a dropped socket (network blip, laptop sleep, wick
   * restart) is retried with backoff while the view is still wanted; a
   * socket that stays open but stops answering is caught by the heartbeat.
   * After the retries run out, clicking the status reconnects. */
  const RETRY_MAX = 8;
  const HEARTBEAT_MS = 15000;
  let wantLive = false; // the user picked a session/tab and has not left it
  let retries = 0;
  let retryTimer: number | null = null;
  let heartbeatTimer: number | null = null;
  let missedBeats = 0;

  function clearRetry() {
    if (retryTimer) {
      clearTimeout(retryTimer);
      retryTimer = null;
    }
  }

  function stopHeartbeat() {
    if (heartbeatTimer) {
      clearInterval(heartbeatTimer);
      heartbeatTimer = null;
    }
  }

  function startHeartbeat(sock: WebSocket) {
    stopHeartbeat();
    missedBeats = 0;
    heartbeatTimer = window.setInterval(() => {
      if (ws !== sock || paused) return;
      sendCmd("Runtime.evaluate", { expression: "1" }).then(
        () => (missedBeats = 0),
        () => {
          // Two silent beats: the socket is open but dead. Drop it; onclose
          // schedules the reconnect.
          if (++missedBeats >= 2 && ws === sock) sock.close();
        },
      );
    }, HEARTBEAT_MS);
  }

  function scheduleReconnect() {
    clearRetry();
    if (!wantLive || !instanceId || !sessionId) return;
    if (retries >= RETRY_MAX) {
      statusText = "disconnected";
      return;
    }
    const delay = Math.min(15000, 1000 * 2 ** retries);
    retries++;
    statusText = `reconnecting… (${retries}/${RETRY_MAX})`;
    retryTimer = window.setTimeout(() => {
      retryTimer = null;
      openSocket();
    }, delay);
  }

  // Manual reconnect (click on the status): start over with a fresh budget.
  function reconnectNow() {
    if (!instanceId || !sessionId) return;
    retries = 0;
    connect();
  }

  function connect() {
    disconnect();
    if (!instanceId || !sessionId) return;
    wantLive = true;
    openSocket();
  }

  function openSocket() {
    closeSocket();
    statusText = "connecting…";
    const sock = new WebSocket(wsURL(instanceId, sessionId, tabIndex, mode));
    ws = sock;
    sock.onopen = () => {
      if (ws !== sock) return;
      connected = true;
      retries = 0;
      statusText = "live";
      send("Page.enable");
      applyViewport(); // sizes the viewport, then starts the screencast
      startHeartbeat(sock);
    };
    sock.onmessage = (ev) => onCdpMessage(ev.data);
    sock.onerror = () => {
      if (ws === sock) statusText = "connection error";
    };
    sock.onclose = () => {
      if (ws !== sock) return; // replaced or closed on purpose
      ws = null;
      connected = false;
      stopHeartbeat();
      rejectPending();
      if (wantLive) scheduleReconnect();
      else statusText = "disconnected";
    };
  }

  function rejectPending() {
    for (const [, p] of pendingCmds) {
      clearTimeout(p.timer);
      p.reject(new Error("disconnected"));
    }
    pendingCmds.clear();
  }

  // Leaving the view on purpose (session change, close, unmount): no retries.
  function disconnect() {
    wantLive = false;
    clearRetry();
    closeSocket();
  }

  function closeSocket() {
    stopHeartbeat();
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
    rejectPending();
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
    // Nothing runs in the page: the agent may be mid-step (a voice note, a
    // video it is checking), and looking away must not pause it.
  }

  function resumeStream() {
    if (!connected || !paused) return;
    paused = false;
    applyViewport(); // restart the screencast for a fresh frame
  }

  function onVisibility() {
    if (document.hidden) pauseStream();
    else if (wantLive && !connected) reconnectNow(); // dropped while away (laptop sleep)
    else resumeStream();
  }

  function onOnline() {
    if (wantLive && !connected) reconnectNow();
  }

  // Re-arm the screencast so a fresh frame arrives (after a resize, a resume or
  // a reconnect). Deliberately nothing else: no Emulation.* — sizing or theming
  // the remote page from here would change what the agent sees.
  function applyViewport() {
    if (!connected) return;
    send("Page.stopScreencast");
    requestAnimationFrame(startScreencast);
  }

  function onCdpMessage(raw: string) {
    let msg: { id?: number; result?: any; error?: any; method?: string; params?: any };
    try {
      msg = JSON.parse(raw);
    } catch {
      return;
    }
    // Reply to a sendCmd() we're awaiting.
    if (typeof msg.id === "number" && pendingCmds.has(msg.id)) {
      const p = pendingCmds.get(msg.id)!;
      pendingCmds.delete(msg.id);
      clearTimeout(p.timer);
      if (msg.error) p.reject(msg.error);
      else p.resolve(msg.result);
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
    if (mode !== "full") return;
    let u = urlInput.trim();
    if (!u) return;
    if (!/^[a-z]+:\/\//i.test(u)) u = "https://" + u; // bare domain → https
    send("Page.navigate", { url: u });
    urlInput = u;
  }

  // Open a chrome:// internal page in the live session (extensions, settings…).
  function gotoChrome(page: string) {
    if (!connected || mode !== "full") return;
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
  // object-contain (frame letterboxed at its own aspect ratio), so it's a
  // fraction of the drawn area → page CSS pixel space
  // (deviceWidth/Height from screencast metadata), which Input.dispatch* expects.
  function canvasPoint(ev: MouseEvent): { x: number; y: number } | null {
    const canvas = canvasEl;
    if (!canvas || !canvas.width || !canvas.height) return null;
    const rect = canvas.getBoundingClientRect();
    if (!rect.width || !rect.height) return null;

    // object-contain: the frame is centred at its aspect ratio inside the
    // element, with bars on two sides. Map into the drawn area only.
    const scale = Math.min(rect.width / canvas.width, rect.height / canvas.height);
    const drawnW = canvas.width * scale;
    const drawnH = canvas.height * scale;
    const left = rect.left + (rect.width - drawnW) / 2;
    const top = rect.top + (rect.height - drawnH) / 2;
    const fracX = (ev.clientX - left) / drawnW;
    const fracY = (ev.clientY - top) / drawnH;
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

    // clickCount drives Chrome's double/triple-click (select word / line). The
    // browser already counts consecutive clicks in MouseEvent.detail (1=single,
    // 2=double, 3=triple), so forward that — hardcoding 1 meant the remote never
    // saw a double-click. A move isn't a click → 0; press/release clamp to ≥1.
    const clickCount = type === "mouseMoved" ? 0 : Math.max(1, ev.detail || 1);

    send("Input.dispatchMouseEvent", {
      type,
      x: p.x,
      y: p.y,
      button: name,
      buttons: mask,
      clickCount,
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

    // Clipboard keys are NOT intercepted — Ctrl/Cmd+C/V flow straight to the
    // remote so its clipboard works natively. The PC↔remote bridge instead syncs
    // the two clipboards when focus crosses the canvas boundary (see the focus/
    // blur handlers), so plain copy/paste "just works" across machines.
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

  /* ── right-click context menu ─────────────────────────────────── */

  // Open our own menu at the click position (the remote's native context menu
  // can't be captured by the screencast). Full mode + connected only.
  function onCanvasContextMenu(ev: MouseEvent) {
    ev.preventDefault();
    if (mode !== "full" || !connected) return;
    ctxMenu = { open: true, x: ev.clientX, y: ev.clientY };
  }

  function closeCtxMenu() {
    if (ctxMenu.open) ctxMenu = { ...ctxMenu, open: false };
  }

  // Dispatch a plain Ctrl/Cmd+<key> into the remote (used for the menu's native
  // Copy/Paste, which act on the REMOTE clipboard just like a keyboard shortcut).
  function sendRemoteCombo(key: string, code: string, keyCode: number) {
    canvasEl?.focus();
    // metaKey on mac (modifier 4), ctrlKey elsewhere (modifier 2). Send both bits
    // set is fine — Chrome uses whichever the platform expects.
    const base = { key, code, windowsVirtualKeyCode: keyCode, modifiers: 2 };
    send("Input.dispatchKeyEvent", { type: "rawKeyDown", ...base });
    send("Input.dispatchKeyEvent", { type: "keyUp", ...base });
  }
  const remoteCopy = () => sendRemoteCombo("c", "KeyC", 67);
  const remotePaste = () => sendRemoteCombo("v", "KeyV", 86);

  /* ── PC↔remote clipboard auto-sync ────────────────────────────────
   * Plain Ctrl+C/V are never touched — copy/paste stay native to the remote. To
   * make the PC clipboard "just work" across the boundary, we sync the two
   * clipboards when focus crosses the canvas:
   *   focus  (entering the view) → push the PC clipboard INTO the remote, so a
   *           Ctrl+V inside pastes what you copied on your PC.
   *   blur   (leaving the view)  → pull the remote clipboard/selection OUT to the
   *           PC, so a paste in a PC app gets what you copied in-page.
   * Best-effort: the async Clipboard API needs a secure context + permission, and
   * the remote read falls back to the current selection when its clipboard read
   * is blocked (common right as the page loses focus). */

  // focus: PC clipboard → remote clipboard.
  async function pushClipboardToRemote() {
    if (mode !== "full" || !connected) return;
    let text = "";
    try {
      text = await navigator.clipboard.readText();
    } catch {
      return;
    }
    if (!text) return;
    // Write into the remote clipboard (userGesture so Chromium allows it). Best-
    // effort — if it's rejected, an in-page paste just uses the remote's prior clip.
    void sendCmd("Runtime.evaluate", {
      expression: `navigator.clipboard.writeText(${JSON.stringify(text)}).catch(()=>{})`,
      awaitPromise: true,
      userGesture: true,
    }).catch(() => {});
  }

  // blur: remote clipboard (fallback selection) → PC clipboard.
  async function pullClipboardFromRemote() {
    if (mode !== "full" || !connected) return;
    let text = "";
    try {
      const res = await sendCmd("Runtime.evaluate", {
        expression:
          "(async()=>{const s=String(window.getSelection?window.getSelection().toString():'');" +
          "if(s)return s;try{return await navigator.clipboard.readText();}catch(e){return '';}})()",
        awaitPromise: true,
        returnByValue: true,
        userGesture: true,
      });
      text = res?.result?.value ?? "";
    } catch {
      return;
    }
    if (!text) return;
    try {
      await navigator.clipboard.writeText(text);
    } catch {
      /* local clipboard write blocked */
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
    window.addEventListener("online", onOnline);
    // Esc closes the right-click menu.
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape") closeCtxMenu(); };
    document.addEventListener("keydown", onKey);
    // Sessions opened elsewhere (an agent's tool call) appear without Refresh.
    const poll = setInterval(() => {
      if (!document.hidden && instanceId && !busy && !loadingSessions) void loadSessions(true);
    }, 5000);
    return () => {
      clearInterval(poll);
      document.removeEventListener("visibilitychange", onVisibility);
      window.removeEventListener("online", onOnline);
      document.removeEventListener("keydown", onKey);
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

  // The proxy decides what a socket may send from the mode it was opened with,
  // so switching mode reopens the socket instead of reusing it.
  let socketMode: Mode = "view"; // the initial mode; the effect tracks changes
  $effect(() => {
    const m = mode;
    if (m === socketMode) return;
    socketMode = m;
    if (wantLive) reconnectNow();
  });

  // A closed or vanished session leaves nothing to float: dock back so the
  // parent shows its panel again.
  $effect(() => {
    if (!sessionId && view !== "docked") view = "docked";
  });

  onDestroy(disconnect);
</script>

<div class="flex-1 min-h-0 overflow-y-auto" data-testid="browser-panel">
  {#if sessionId && view === "docked"}
    <!-- Watching a session: the viewer replaces the list; ← goes back. -->
    <div class="flex flex-col">
      {@render titleBar(false)}
      {@render extras()}
      <div class="p-2">
        {@render screen("w-full aspect-[16/10]")}
      </div>
      {@render fullHint()}
    </div>
  {:else}
    <div class="p-3 space-y-3">
      {#if loadingInstances}
        <p class="text-xs text-black-700 dark:text-black-600">Loading…</p>
      {:else if instances.length === 0}
        <p class="text-xs text-black-700 dark:text-black-600">
          No <span class="font-mono">playwright_browser</span> connector configured. Install the plugin and add an instance to use the live browser.
        </p>
      {:else}
        <div class="space-y-1.5">
          <p class="text-[11px] font-medium uppercase tracking-wide text-black-700 dark:text-black-600">Instance</p>
          <div class="flex flex-wrap gap-1.5" data-testid="browser-instance">
            {#each instances as inst (inst.id)}
              <button
                type="button"
                class={"max-w-full truncate rounded-full border px-3 py-1 text-xs font-medium transition-colors disabled:opacity-50 " +
                  (inst.id === instanceId
                    ? "border-green-500 text-green-600 dark:text-green-400"
                    : "border-white-400 dark:border-navy-600 text-black-800 dark:text-white-200 hover:border-green-400")}
                disabled={inst.disabled}
                onclick={() => selectInstance(inst.id)}
                data-testid={`browser-instance-${inst.id}`}
              >{inst.label}{inst.disabled ? " (disabled)" : ""}</button>
            {/each}
          </div>
        </div>

        {#if instanceId}
          <div class="flex items-center gap-1.5">
            <p class="text-[11px] font-medium uppercase tracking-wide text-black-700 dark:text-black-600">Live sessions ({sessions.length})</p>
            <button
              type="button"
              title="Refresh"
              aria-label="Refresh sessions"
              class="ml-auto rounded-lg border border-white-400 dark:border-navy-600 p-1.5 text-black-700 dark:text-white-200 hover:border-green-400 transition-colors disabled:opacity-50"
              disabled={loadingSessions}
              onclick={() => loadSessions()}
              data-testid="browser-refresh"
            >
              <svg viewBox="0 0 16 16" class="h-3 w-3"><path d="M13 3v3h-3M3 13v-3h3M13 6a5 5 0 00-9-1M3 10a5 5 0 009 1" stroke="currentColor" stroke-width="1.3" fill="none" stroke-linecap="round" stroke-linejoin="round"/></svg>
            </button>
            <button
              type="button"
              class="rounded-lg bg-green-500 px-3 py-1.5 text-xs font-medium text-white-100 hover:bg-green-600 active:bg-green-700 transition-colors disabled:opacity-50"
              disabled={busy}
              onclick={spawn}
              data-testid="browser-open"
            >{busy ? "Working…" : "+ New session"}</button>
          </div>

          {#if sessions.length > 0}
            <ul class="space-y-2">
              {#each sessions as s (s.session_id)}
                <li
                  class={"flex items-center gap-2 rounded-xl border p-3 bg-white-200 dark:bg-navy-800 " +
                    (s.session_id === sessionId ? "border-green-500" : "border-white-300 dark:border-navy-600")}
                  data-testid="browser-session-card"
                >
                  <div class="min-w-0 flex-1">
                    <div class="flex min-w-0 items-center gap-1.5">
                      <span class={"h-2 w-2 shrink-0 rounded-full " + ((s.idle_seconds ?? 0) > 60 ? "bg-amber-500" : "bg-green-500")}></span>
                      <span class="truncate text-sm font-semibold text-black-900 dark:text-white-100">{sessionName(s)}</span>
                      <span class="shrink-0 rounded-md border border-white-400 dark:border-navy-600 px-1.5 text-[10px] text-black-700 dark:text-black-600">{s.browser}</span>
                    </div>
                    <p class="mt-0.5 truncate text-xs text-black-700 dark:text-black-600" title={s.session_id}>{sessionMeta(s)}</p>
                  </div>
                  {#if s.session_id === sessionId}
                    <span class="shrink-0 rounded-md border border-green-500 px-1.5 text-[10px] text-green-600 dark:text-green-400">watching</span>
                  {:else}
                    <button
                      type="button"
                      class="shrink-0 rounded-lg bg-green-500 px-3 py-1.5 text-xs font-medium text-white-100 hover:bg-green-600 active:bg-green-700 transition-colors"
                      onclick={() => openSession_(s.session_id)}
                      data-testid="browser-session-open"
                    >Open</button>
                  {/if}
                  <button
                    type="button"
                    title="Close session"
                    aria-label="Close session"
                    class="shrink-0 rounded-md p-1.5 text-neg-400 hover:bg-white-300 dark:hover:bg-navy-700 transition-colors disabled:opacity-50"
                    disabled={busy}
                    onclick={() => kill(s.session_id)}
                    data-testid="browser-session-close"
                  >
                    <svg viewBox="0 0 16 16" class="h-3 w-3"><path d="M4 4l8 8M12 4l-8 8" stroke="currentColor" stroke-width="1.5" fill="none" stroke-linecap="round"/></svg>
                  </button>
                </li>
              {/each}
            </ul>
          {:else if loadingSessions}
            <p class="text-xs text-black-700 dark:text-black-600" data-testid="browser-sessions-loading">Loading sessions…</p>
          {:else}
            <p class="text-xs text-black-700 dark:text-black-600">No live sessions. Open one, or wait for an agent to start one.</p>
          {/if}
        {/if}
      {/if}
    </div>
  {/if}
</div>

<!-- Floating / fullscreen overlay. visibility and pointer-events are set
     explicitly: the parent hides its side panel (visibility:hidden) while the
     browser is detached, and this window must stay on screen. -->
{#if sessionId && view !== "docked"}
  {#if view === "max"}
    <div class="fixed inset-0 z-40 bg-black-900/60" style="visibility:visible" aria-hidden="true"></div>
  {/if}
  <div
    class={view === "max"
      ? "fixed inset-0 z-50 flex flex-col"
      : "fixed z-50 flex flex-col rounded-xl border shadow-2xl overflow-hidden"}
    style={`background:${ovBg}; border-color:${ovBorder}; color:${ovText}; visibility:visible; pointer-events:auto;` +
      (view === "float" ? ` left:${fx}px; top:${fy}px; width:${fw}px; height:${fh}px;` : "")}
    data-testid="browser-overlay"
  >
    {@render titleBar(true)}
    <div class="flex-1 min-h-0 flex flex-col gap-1.5 p-1.5 overflow-hidden" style={`background:${ovBg}; color:${ovText};`}>
      {@render extras()}
      <div class="flex-1 min-h-0">
        {@render screen("h-full")}
      </div>
      {@render fullHint()}
    </div>

    <!-- Resize handle (float only) -->
    {#if view === "float"}
      <div
        class="absolute bottom-0 right-0 h-4 w-4 cursor-nwse-resize"
        style="touch-action:none"
        role="presentation"
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

{#if menuOpen}
  <!-- Outside click closes the ⋯ menu. -->
  <div class="fixed inset-0 z-[65]" style="visibility:visible; pointer-events:auto" onclick={() => (menuOpen = false)} role="presentation"></div>
{/if}

<!-- Right-click menu on the live view. Rendered at the top level so its fixed
     position isn't clipped by the panel. A full-screen invisible backdrop closes
     it on any outside click / right-click. -->
{#if ctxMenu.open}
  <div
    class="fixed inset-0 z-[60]"
    onclick={closeCtxMenu}
    oncontextmenu={(e) => { e.preventDefault(); closeCtxMenu(); }}
    role="presentation"
  >
    <div
      class="ctx-menu absolute min-w-[184px] overflow-hidden rounded-lg border py-1 text-[13px] shadow-2xl"
      style={`left:${ctxMenu.x}px; top:${ctxMenu.y}px; --ctx-bg:${ovBg}; --ctx-hover:${ovBar}; --ctx-border:${ovBorder}; --ctx-text:${ovText}; --ctx-sub:${ovSub}; background:var(--ctx-bg); border-color:var(--ctx-border); color:var(--ctx-text);`}
      role="menu"
    >
      <button type="button" role="menuitem" class="ctx-item" onclick={() => { remoteCopy(); closeCtxMenu(); }}>
        <span>Copy</span><span class="ctx-key">Ctrl+C</span>
      </button>
      <button type="button" role="menuitem" class="ctx-item" onclick={() => { remotePaste(); closeCtxMenu(); }}>
        <span>Paste</span><span class="ctx-key">Ctrl+V</span>
      </button>
    </div>
  </div>
{/if}

<!-- ── Reusable snippets ──────────────────────────────────────────── -->

{#snippet titleBar(overlay: boolean)}
  <!-- One row: ← (docked) · status · name · View only/Full · window buttons · ⋯.
       In the floating window it is also the drag handle (mouse or finger). -->
  <div
    class={"relative flex items-center gap-1.5 px-2.5 py-2 border-b " +
      (overlay ? "" : "border-white-300 dark:border-navy-600 ") +
      (overlay && view === "float" ? "cursor-move select-none" : "")}
    style={overlay ? `background:${ovBar}; border-color:${ovBorder};` + (view === "float" ? " touch-action:none;" : "") : ""}
    role="toolbar"
    aria-label="Browser controls"
    tabindex="-1"
    onpointerdown={overlay && view === "float" ? (e) => beginDrag("move", e) : undefined}
    onpointermove={overlay ? onDragMove : undefined}
    onpointerup={overlay ? endDrag : undefined}
  >
    {#if !overlay}
      {@render iconBtn("Back to sessions", "M10 3L5 8l5 5", backToList, "browser-back")}
    {/if}
    <span class={"h-2 w-2 shrink-0 rounded-full " + (connected ? "bg-green-500" : "bg-black-500")}></span>
    <span
      class={"min-w-0 flex-1 truncate text-sm font-semibold " + (overlay ? "" : "text-black-900 dark:text-white-100")}
      style={overlay ? `color:${ovText};` : ""}
      title={sessionId}
    >{sessionName(selectedSession)}{hostOf(tabs.find((t) => t.index === tabIndex)?.url) ? ` · ${hostOf(tabs.find((t) => t.index === tabIndex)?.url)}` : ""}</span>

    {#if connected}
      <span class="shrink-0 rounded-full px-1.5 py-0.5 text-[10px] font-medium bg-green-50 text-green-700 dark:bg-green-900 dark:text-green-300">{statusText || "live"}</span>
    {:else}
      <button
        type="button"
        title="Reconnect to the live browser"
        class="shrink-0 inline-flex items-center gap-1 rounded-full px-1.5 py-0.5 text-[10px] font-medium bg-white-300 text-black-600 hover:bg-green-50 hover:text-green-700 dark:bg-navy-700 dark:text-black-600 dark:hover:bg-green-900 dark:hover:text-green-300"
        onclick={reconnectNow}
        data-testid="browser-reconnect"
      >
        <svg viewBox="0 0 16 16" class="h-3 w-3" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M13.5 8a5.5 5.5 0 1 1-1.6-3.9M13.5 2.5v3h-3" /></svg>
        {statusText || "disconnected"}
      </button>
    {/if}

    <div class="shrink-0 inline-flex rounded-lg border border-white-400 dark:border-navy-600 overflow-hidden text-[11px] font-medium">
      <button
        type="button"
        title="Watch without touching the browser: no resize, no theme, no input. What you see is what the agent sees."
        class={"px-2 py-1 transition-colors " + (mode === "view" ? "bg-green-500 text-white-100" : "bg-white-100 dark:bg-navy-800 text-black-700 dark:text-white-200")}
        onclick={() => (mode = "view")}
        data-testid="browser-mode-view"
      >View only</button>
      <button
        type="button"
        title="Click and type in the agent's browser"
        class={"px-2 py-1 transition-colors " + (mode === "full" ? "bg-green-500 text-white-100" : "bg-white-100 dark:bg-navy-800 text-black-700 dark:text-white-200")}
        onclick={() => (mode = "full")}
        data-testid="browser-mode-full"
      >Full</button>
    </div>

    {#if view === "float"}
      {@render iconBtn("Dock to panel", "M3 3h10v10H3z M3 6h10", () => setView("docked"), "browser-dock")}
      {@render iconBtn("Fullscreen", "M2 6V2h4 M14 6V2h-4 M2 10v4h4 M14 10v4h-4", () => setView("max"), "browser-max")}
    {:else if view === "max"}
      {@render iconBtn("Pop out", "M2 4h12v8H2z M2 4l4 3", () => setView("float"), "browser-float")}
      {@render iconBtn("Exit fullscreen", "M4 4l8 8M12 4l-8 8", exitMax, "browser-exit-max")}
    {:else}
      {@render iconBtn("Pop out", "M2 4h12v8H2z M2 4l4 3", () => setView("float"), "browser-float")}
      {@render iconBtn("Fullscreen", "M2 6V2h4 M14 6V2h-4 M2 10v4h4 M14 10v4h-4", () => setView("max"), "browser-max")}
    {/if}
    {@render iconBtn("More", "M4 8h.01M8 8h.01M12 8h.01", () => (menuOpen = !menuOpen), "browser-more")}

    {#if menuOpen}
      <div
        class="absolute right-2 top-full z-[70] mt-1 min-w-[200px] overflow-hidden rounded-lg border py-1 text-[13px] shadow-2xl"
        style={`background:${ovBg}; border-color:${ovBorder}; color:${ovText}; visibility:visible; pointer-events:auto;`}
        role="menu"
        data-testid="browser-menu"
      >
        <button type="button" role="menuitem" class="menu-item" onclick={() => { showAddress = !showAddress; menuOpen = false; }}>
          {showAddress ? "Hide address bar" : "Address bar"}
        </button>
        {#each CHROME_PAGES as p (p.page)}
          <button
            type="button"
            role="menuitem"
            class="menu-item disabled:opacity-50"
            disabled={!connected || mode !== "full"}
            title={mode !== "full" ? "Switch to Full to navigate" : `Open chrome://${p.page}`}
            onclick={() => { gotoChrome(p.page); menuOpen = false; }}
          >chrome://{p.page}</button>
        {/each}
        <button type="button" role="menuitem" class="menu-item" onclick={copySessionId}>Copy session id</button>
        <button
          type="button"
          role="menuitem"
          class="menu-item text-neg-400"
          onclick={() => { menuOpen = false; void kill(sessionId); }}
          data-testid="browser-close"
        >Close session</button>
      </div>
    {/if}
  </div>
{/snippet}

{#snippet iconBtn(label: string, path: string, action: () => void, testid: string)}
  <button
    type="button"
    title={label}
    aria-label={label}
    class="shrink-0 rounded-md p-1.5 text-black-700 dark:text-white-200 hover:bg-white-300 dark:hover:bg-navy-700 transition-colors"
    onclick={action}
    data-testid={testid}
  >
    <svg viewBox="0 0 16 16" class="h-3.5 w-3.5"><path d={path} stroke="currentColor" stroke-width="1.6" fill="none" stroke-linecap="round" stroke-linejoin="round"/></svg>
  </button>
{/snippet}

{#snippet extras()}
  {#if showAddress || tabs.length > 1 || (tabs.length > 0 && !tabLimitReached)}
    <div class="space-y-1.5 px-2 pt-2">
      {#if showAddress}{@render addressBar()}{/if}
      {@render tabSwitcher()}
    </div>
  {/if}
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
      class={"w-full h-full block bg-transparent " + "object-contain " + (mode === "full" ? "cursor-crosshair" : "cursor-default")}
      tabindex={mode === "full" ? 0 : -1}
      onmousedown={(e) => mouseEvent("mousePressed", e)}
      onmouseup={(e) => mouseEvent("mouseReleased", e)}
      onmousemove={(e) => mouseEvent("mouseMoved", e)}
      onwheel={onWheel}
      onkeydown={(e) => onCanvasKey(e, "keyDown")}
      onkeyup={(e) => onCanvasKey(e, "keyUp")}
      onfocus={() => void pushClipboardToRemote()}
      onblur={() => void pullClipboardFromRemote()}
      oncontextmenu={onCanvasContextMenu}
      data-testid="browser-canvas"
    ></canvas>
    {#if !connected}
      {#if sessionId}
        <button
          type="button"
          class="absolute inset-0 flex flex-col items-center justify-center gap-1 text-xs text-white-300 hover:text-white-100"
          onclick={reconnectNow}
          data-testid="browser-reconnect-overlay"
        >
          <span>{statusText || "disconnected"}</span>
          <span class="text-[11px] underline underline-offset-2">Click to reconnect</span>
        </button>
      {:else}
        <div class="absolute inset-0 flex items-center justify-center text-xs text-white-300 pointer-events-none">
          {statusText || "not connected"}
        </div>
      {/if}
    {/if}
  </div>
{/snippet}

{#snippet fullHint()}
  {#if mode === "full"}
    <p class="mx-2 mb-2 rounded-md bg-amber-50 px-2 py-1 text-[11px] text-amber-700 dark:bg-amber-900 dark:text-amber-300">
      Full: your clicks and typing go to the agent's browser. Click the view first to capture the keyboard.
    </p>
  {/if}
{/snippet}

<style>
  /* Right-click menu — themed via CSS vars set inline from the live theme, so it
     reads as one surface in both light and dark (like the overlay chrome). */
  .ctx-item {
    display: flex;
    width: 100%;
    align-items: center;
    justify-content: space-between;
    gap: 1.5rem;
    padding: 0.375rem 0.75rem;
    text-align: left;
    color: var(--ctx-text);
    background: transparent;
    cursor: pointer;
  }
  .ctx-item:hover {
    background: var(--ctx-hover);
  }
  .menu-item {
    display: block;
    width: 100%;
    padding: 0.375rem 0.75rem;
    text-align: left;
    background: transparent;
    cursor: pointer;
  }
  .menu-item:hover:not(:disabled) {
    background: var(--menu-hover, rgba(127, 127, 127, 0.15));
  }
  .ctx-key {
    font-size: 11px;
    color: var(--ctx-sub);
  }
</style>
