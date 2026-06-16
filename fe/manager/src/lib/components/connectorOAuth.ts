/* Connector OAuth connect popup. Unlike the MCP server flow (which has a
   dedicated JSON status endpoint), the generic connector OAuth handler is a
   full-page browser redirect: GET /manager/connectors/{key}/oauth/start →
   provider consent → /oauth/callback → redirect back with ?oauth=success.

   We can't navigate the SPA away mid-edit, so we open the start URL in a
   popup and resolve once the popup closes — the caller then reloads the
   detail to pick up the newly connected account / token. The handle exposes
   cancel() so the watcher interval + popup tear down on unmount (no leaks). */

const WATCH_INTERVAL_MS = 800;
const POPUP_FEATURES = "width=560,height=720";

export interface OAuthConnect {
  promise: Promise<void>;
  cancel: () => void;
}

export function startConnectorOAuth(startURL: string): OAuthConnect {
  let watch: ReturnType<typeof setInterval> | null = null;
  let popup: Window | null = null;
  let onMessage: ((e: MessageEvent) => void) | null = null;

  function cleanup() {
    if (watch !== null) {
      clearInterval(watch);
      watch = null;
    }
    if (onMessage !== null) {
      window.removeEventListener("message", onMessage);
      onMessage = null;
    }
    if (popup !== null && !popup.closed) {
      popup.close();
    }
    popup = null;
  }

  const promise = new Promise<void>((resolve, reject) => {
    popup = window.open(startURL, "wick-connector-oauth", POPUP_FEATURES);
    if (!popup) {
      reject(new Error("Popup blocked — allow popups for this site and retry."));
      return;
    }
    function finish() {
      cleanup();
      resolve();
    }
    onMessage = (e: MessageEvent) => {
      if (e.origin !== window.location.origin) return;
      const data = e.data as { type?: string } | null;
      if (data && data.type === "wick-connector-oauth") {
        finish();
      }
    };
    window.addEventListener("message", onMessage);
    watch = setInterval(() => {
      if (popup === null || popup.closed) {
        finish();
      }
    }, WATCH_INTERVAL_MS);
  });

  return {
    promise,
    cancel: () => {
      cleanup();
    },
  };
}
