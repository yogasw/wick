/* Per-instance MCP account login, opened from the connector list's row menu.

   Distinct from mcpOAuth.ts (the edit form's "Test now", which mints an
   unsaved login the form later submits) and from connectorOAuth.ts (the
   generic connector SSO flow): here the server row already exists and the
   callback persists the tokens onto the instance before the popup closes.
   So there is no login id to carry — the caller only needs to know the
   login finished, then re-fetch to pick up the new account.

   Completion is raced two ways, matching mcpOAuth: a same-origin
   BroadcastChannel (instant, and immune to the authorization server's COOP
   headers severing window.opener) and a popup-closed watch as the fallback
   that always lands. */

const WATCH_INTERVAL_MS = 800;
const LOGIN_TIMEOUT_MS = 180000;
const POPUP_FEATURES = "width=560,height=720";
const CHANNEL = "wick-mcp-oauth";

export interface InstanceOAuthConnect {
  promise: Promise<void>;
  cancel: () => void;
}

type BroadcastPayload = { type?: string; error?: string };

export function startInstanceOAuth(startURL: string): InstanceOAuthConnect {
  let watch: ReturnType<typeof setInterval> | null = null;
  let timer: ReturnType<typeof setTimeout> | null = null;
  let channel: BroadcastChannel | null = null;
  let popup: Window | null = null;
  let onMessage: ((e: MessageEvent) => void) | null = null;

  function cleanup() {
    if (watch !== null) {
      clearInterval(watch);
      watch = null;
    }
    if (timer !== null) {
      clearTimeout(timer);
      timer = null;
    }
    if (onMessage !== null) {
      window.removeEventListener("message", onMessage);
      onMessage = null;
    }
    if (channel !== null) {
      channel.close();
      channel = null;
    }
    if (popup !== null && !popup.closed) {
      popup.close();
    }
    popup = null;
  }

  const promise = new Promise<void>((resolve, reject) => {
    popup = window.open(startURL, CHANNEL, POPUP_FEATURES);
    if (!popup) {
      reject(new Error("Popup blocked — allow popups for this site and retry."));
      return;
    }
    try {
      channel = new BroadcastChannel(CHANNEL);
    } catch {
      channel = null;
    }

    function finish(err: string | null) {
      cleanup();
      if (err) {
        reject(new Error(err));
        return;
      }
      resolve();
    }

    timer = setTimeout(() => finish("Login timed out — try connecting again."), LOGIN_TIMEOUT_MS);

    function handle(payload: BroadcastPayload | null) {
      if (!payload || payload.type !== CHANNEL) return;
      finish(payload.error || null);
    }

    onMessage = (e: MessageEvent) => {
      if (e.origin !== window.location.origin) return;
      handle(e.data as BroadcastPayload);
    };
    window.addEventListener("message", onMessage);
    if (channel) {
      channel.onmessage = (e: MessageEvent) => handle(e.data as BroadcastPayload);
    }

    /* Fallback: the user closing the popup (or a signal that never arrived)
       still resolves, so the caller re-fetches and shows the real state. */
    watch = setInterval(() => {
      if (popup === null || popup.closed) {
        finish(null);
      }
    }, WATCH_INTERVAL_MS);
  });

  return { promise, cancel: cleanup };
}
