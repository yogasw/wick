<script lang="ts">
  import { onDestroy } from "svelte";
  import { Modal, Button, TextInput } from "@wick-fe/common-ui";
  import { toastError, toastOk } from "@wick-fe/common-stores";
  import {
    apiLoginTTYExtend,
    apiLoginTTYKill,
    loginTTYWSURL,
    fmtCountdown,
    applyFrame,
    type TermFeedState,
    type LoginTTYFrame,
    type LoginAccount,
    type LoginTTYSession,
  } from "$lib/logintty.js";
  import "@xterm/xterm/css/xterm.css";

  type Props = {
    base: string;
    type: string;
    name: string;
    session: LoginTTYSession;
    extendS: number;
    onClose: () => void;
    /** Called once when the session reaches a terminal state. */
    onFinished: (account: LoginAccount | null) => void;
  };
  let { base, type, name, session, extendS, onClose, onFinished }: Props = $props();

  let feed = $state<TermFeedState>({
    links: [],
    success: null,
    failure: null,
    remainingS: session.remainingS,
    capS: session.capS,
    state: session.state || "running",
    exitErr: "",
    account: null,
  });
  let code = $state("");
  let extending = $state(false);
  let stopping = $state(false);
  let wsOpen = $state(false);
  /* Optional: open a detected login link in THIS browser. Per-open
     setting — always starts off (the user may want another browser)
     and is never persisted. */
  let autoOpen = $state(false);
  let autoOpened = false;

  let running = $derived(feed.state === "running");
  let atCap = $derived(feed.remainingS >= feed.capS);

  let ws: WebSocket | null = null;
  let term: { write: (d: Uint8Array) => void; dispose: () => void; cols: number; rows: number; onData: (cb: (d: string) => void) => void; focus: () => void } | null = null;
  let fit: { fit: () => void } | null = null;
  let tick: ReturnType<typeof setInterval> | null = null;
  let finishedNotified = false;

  function b64ToBytes(b64: string): Uint8Array {
    return Uint8Array.from(atob(b64), (c) => c.charCodeAt(0));
  }
  function strToB64(s: string): string {
    const bytes = new TextEncoder().encode(s);
    let bin = "";
    for (const b of bytes) bin += String.fromCharCode(b);
    return btoa(bin);
  }

  function send(msg: unknown) {
    if (ws && ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify(msg));
  }

  function handleFrame(fr: LoginTTYFrame) {
    if (fr.t === "out" && fr.data && term) {
      term.write(b64ToBytes(fr.data));
      return;
    }
    feed = applyFrame(feed, fr, Date.now());
    if (fr.t === "link" && fr.url && autoOpen && !autoOpened) {
      autoOpened = true;
      window.open(fr.url, "_blank", "noopener");
    }
    if (fr.t === "state" && feed.state !== "running" && !finishedNotified) {
      finishedNotified = true;
      onFinished(feed.account);
    }
  }

  /* boot mounts xterm into the container and opens the websocket. Runs
     once per modal open; guarded so jsdom (tests) stays quiet. */
  async function boot(el: HTMLDivElement) {
    try {
      const [{ Terminal }, { FitAddon }] = await Promise.all([
        import("@xterm/xterm"),
        import("@xterm/addon-fit"),
      ]);
      const t = new Terminal({
        convertEol: false,
        cursorBlink: true,
        fontSize: 12,
        fontFamily: "ui-monospace, SFMono-Regular, Menlo, Consolas, monospace",
        theme: { background: "#0b1220" },
      });
      const f = new FitAddon();
      t.loadAddon(f);
      t.open(el);
      f.fit();
      t.onData((d: string) => send({ t: "in", data: strToB64(d) }));
      term = t;
      fit = f;
      t.focus();
    } catch {
      /* jsdom / renderer failure — the parsed link + code input still work */
    }

    // jsdom ships a WebSocket that throws asynchronously (ws browser
    // stub) — skip the live socket under vitest; frame handling is
    // covered by the applyFrame unit tests.
    if (typeof WebSocket === "undefined" || import.meta.env?.MODE === "test") return;
    let sock: WebSocket;
    try {
      sock = new WebSocket(loginTTYWSURL(base, type, name));
    } catch {
      return;
    }
    sock.onopen = () => {
      wsOpen = true;
      if (term) send({ t: "resize", cols: term.cols, rows: term.rows });
    };
    sock.onmessage = (ev) => {
      try {
        handleFrame(JSON.parse(ev.data as string) as LoginTTYFrame);
      } catch {
        /* skip malformed frame */
      }
    };
    sock.onclose = () => {
      wsOpen = false;
    };
    ws = sock;

    // Local fallback tick between server ttl frames so the countdown
    // never looks frozen.
    tick = setInterval(() => {
      if (running && feed.remainingS > 0) feed = { ...feed, remainingS: feed.remainingS - 1 };
    }, 1000);
  }

  function termContainer(el: HTMLDivElement) {
    void boot(el);
  }

  function onWindowResize() {
    if (fit && term) {
      fit.fit();
      send({ t: "resize", cols: term.cols, rows: term.rows });
    }
  }

  onDestroy(() => {
    if (tick) clearInterval(tick);
    ws?.close();
    term?.dispose();
  });

  async function extend() {
    extending = true;
    try {
      const r = await apiLoginTTYExtend(base, type, name);
      if (r === null) {
        toastError("Cannot extend — session cap reached");
      } else {
        feed = { ...feed, remainingS: r.remainingS, capS: r.capS };
      }
    } catch (e) {
      toastError(e instanceof Error ? e.message : "Extend failed");
    } finally {
      extending = false;
    }
  }

  async function stop() {
    stopping = true;
    try {
      await apiLoginTTYKill(base, type, name);
    } catch (e) {
      toastError(e instanceof Error ? e.message : "Stop failed");
    } finally {
      stopping = false;
    }
  }

  function submitCode() {
    const v = code.trim();
    if (v === "") return;
    send({ t: "in", data: strToB64(v) });
    // Enter goes as a separate keypress after a beat — bundled with the
    // code, ink's paste detection treats the \r as a pasted newline and
    // never submits.
    setTimeout(() => send({ t: "in", data: strToB64("\r") }), 200);
    code = "";
    toastOk("Code submitted");
  }

  async function copyLink(url: string) {
    try {
      await navigator.clipboard.writeText(url);
      toastOk("Link copied");
    } catch {
      toastError("Copy failed — select it in the terminal instead");
    }
  }

  function stateBadge(state: string): { label: string; cls: string } {
    switch (state) {
      case "exited":
        return { label: "Finished", cls: "bg-white-300 dark:bg-navy-600 text-black-800 dark:text-black-600" };
      case "expired":
        return { label: "Expired — session killed", cls: "bg-cau-100 dark:bg-cau-400/20 text-cau-400" };
      case "killed":
        return { label: "Stopped", cls: "bg-white-300 dark:bg-navy-600 text-black-800 dark:text-black-600" };
      default:
        return { label: state, cls: "bg-white-300 dark:bg-navy-600 text-black-800 dark:text-black-600" };
    }
  }

  function fmtAt(at: number): string {
    return new Date(at).toLocaleTimeString();
  }
</script>

<svelte:window onresize={onWindowResize} />

<Modal open={true} title={`Reconnect ${type}/${name}`} size="lg" onClose={onClose}>
  <div class="space-y-3">
    <!-- Countdown + session state row -->
    <div class="flex items-center gap-2 flex-wrap">
      {#if running}
        <span
          class="inline-flex items-center gap-1.5 rounded px-2 py-0.5 text-xs font-semibold {feed.remainingS <= 60 ? 'bg-neg-100 dark:bg-neg-400/20 text-neg-400' : 'bg-white-300 dark:bg-navy-600 text-black-900 dark:text-white-100'}"
          title="Session is killed when the countdown hits zero"
        >
          <svg class="w-4 h-4" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24" aria-hidden="true">
            <path stroke-linecap="round" stroke-linejoin="round" d="M12 8v4l3 3m6-3a9 9 0 11-18 0 9 9 0 0118 0z"/>
          </svg>
          {fmtCountdown(feed.remainingS)}
        </span>
        <Button variant="secondary" disabled={extending || atCap} onclick={extend} title={atCap ? "Hard cap reached" : `Up to ${fmtCountdown(feed.capS)} total`}>
          {extending ? "Extending…" : `Extend +${Math.round(extendS / 60)}m`}
        </Button>
        <span class="ml-auto"></span>
        <Button variant="secondary" disabled={stopping} onclick={stop}>{stopping ? "Stopping…" : "Stop session"}</Button>
      {:else}
        {@const b = stateBadge(feed.state)}
        <span class="rounded px-2 py-0.5 text-xs font-semibold {b.cls}">{b.label}</span>
        {#if feed.exitErr}
          <span class="text-[11px] font-mono text-black-700 dark:text-black-600 break-all">{feed.exitErr}</span>
        {/if}
      {/if}
    </div>

    <!-- Login link — plain row, no card. The CLI is told it runs over
         SSH so it never opens a tab itself; the user clicks Open. -->
    {#if feed.links.length > 0}
      <div class="flex items-center gap-2 text-xs">
        <span class="shrink-0 font-medium text-black-800 dark:text-black-600">Login link</span>
        <a
          href={feed.links[0]}
          target="_blank"
          rel="noopener noreferrer"
          class="min-w-0 flex-1 truncate font-mono text-link-400 hover:underline"
        >{feed.links[0]}</a>
        <Button variant="secondary" onclick={() => copyLink(feed.links[0])}>Copy</Button>
        <a
          href={feed.links[0]}
          target="_blank"
          rel="noopener noreferrer"
          class="rounded-lg bg-green-500 hover:bg-green-600 px-3 py-1.5 text-xs font-semibold text-white-100 transition-colors"
        >Open</a>
      </div>
    {/if}

    <!-- Success / failure result — one quiet line with the event time -->
    {#if feed.success}
      <p class="flex items-baseline gap-2 text-xs min-w-0">
        <span class="h-2 w-2 shrink-0 self-center rounded-full bg-pos-400"></span>
        <span class="shrink-0 font-semibold text-pos-400">Success</span>
        <span class="shrink-0 text-black-700 dark:text-black-600">{fmtAt(feed.success.at)}</span>
        <span class="min-w-0 truncate font-mono text-black-800 dark:text-black-600">{feed.success.line}</span>
      </p>
    {/if}
    {#if feed.failure}
      <p class="flex items-baseline gap-2 text-xs min-w-0">
        <span class="h-2 w-2 shrink-0 self-center rounded-full bg-neg-400"></span>
        <span class="shrink-0 font-semibold text-neg-400">Failed</span>
        <span class="shrink-0 text-black-700 dark:text-black-600">{fmtAt(feed.failure.at)}</span>
        <span class="min-w-0 truncate font-mono text-black-800 dark:text-black-600">{feed.failure.line}</span>
      </p>
    {/if}

    <!-- Terminal -->
    <div class="rounded-lg overflow-hidden border border-white-300 dark:border-navy-600 bg-[#0b1220]">
      <div use:termContainer class="h-80 w-full p-2"></div>
    </div>

    <!-- Paste authorization code -->
    <div class="flex items-center gap-2">
      <div class="min-w-0 flex-1">
        <TextInput
          value={code}
          onChange={(v: string) => { code = v; }}
          placeholder="Paste authorization code here, then Submit"
          disabled={!running || !wsOpen}
        />
      </div>
      <Button variant="primary" disabled={!running || !wsOpen || code.trim() === ""} onclick={submitCode}>Submit</Button>
    </div>
    <div class="flex items-center justify-between gap-4">
      <p class="text-[11px] text-black-700 dark:text-black-600">Terminal is live — typing directly also works.</p>
      <label class="flex shrink-0 items-center gap-2 text-[11px] text-black-800 dark:text-black-600 cursor-pointer">
        <button
          type="button"
          role="switch"
          aria-label="Auto-open login link in this browser"
          aria-checked={autoOpen}
          onclick={() => { autoOpen = !autoOpen; }}
          class="relative inline-flex h-5 w-9 shrink-0 items-center rounded-full transition-colors {autoOpen ? 'bg-green-500' : 'bg-white-400 dark:bg-navy-600'}"
        >
          <span class="inline-block h-4 w-4 transform rounded-full bg-white-100 shadow transition-transform {autoOpen ? 'translate-x-4' : 'translate-x-0.5'}"></span>
        </button>
        Auto-open login link in this browser
      </label>
    </div>
  </div>
  {#snippet footer()}
    <Button variant="secondary" onclick={onClose}>Close</Button>
  {/snippet}
</Modal>
