import { startMcpOAuth, getMcpOAuthStatus } from "$lib/api.js";
import type { McpServerForm } from "$lib/types.js";

/* MCP OAuth popup login. Mirrors the legacy custom_mcp_form.js flow:
   POST oauth/start to discover the authorization URL, open it in a
   popup, then race two completion signals — a same-origin
   BroadcastChannel (instant; survives the COOP-severed popup handle)
   and a server-side status poll (the truth that always lands). On
   success the resolved login_id rides the form session into test/save.

   The returned handle exposes cancel() so callers can tear down every
   timer, listener, channel, and the popup on unmount — no leaks. */

const LOGIN_TIMEOUT_MS = 180000;
const POLL_INTERVAL_MS = 2500;
const POPUP_FEATURES = "width=560,height=720";

export interface OAuthLogin {
  promise: Promise<string>;
  cancel: () => void;
}

type BroadcastPayload = {
  type?: string;
  error?: string;
};

export function startOAuthLogin(form: McpServerForm): OAuthLogin {
  let cancelled = false;
  let timer: ReturnType<typeof setTimeout> | null = null;
  let poll: ReturnType<typeof setInterval> | null = null;
  let channel: BroadcastChannel | null = null;
  let popup: Window | null = null;
  let onMessage: ((e: MessageEvent) => void) | null = null;

  function cleanup() {
    if (timer !== null) {
      clearTimeout(timer);
      timer = null;
    }
    if (poll !== null) {
      clearInterval(poll);
      poll = null;
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

  const promise = new Promise<string>((resolve, reject) => {
    startMcpOAuth(form)
      .then((data) => {
        if (cancelled) {
          reject(new Error("cancelled"));
          return;
        }
        popup = window.open(data.auth_url, "wick-mcp-oauth", POPUP_FEATURES);
        if (!popup) {
          reject(new Error("Popup blocked — allow popups for this site and retry."));
          return;
        }
        const loginID = data.login_id;
        try {
          channel = new BroadcastChannel("wick-mcp-oauth");
        } catch {
          channel = null;
        }

        function finish(err: string | null) {
          cleanup();
          if (err) {
            reject(new Error(err));
            return;
          }
          resolve(loginID);
        }

        timer = setTimeout(() => {
          finish("Login timed out — click Test now to try again.");
        }, LOGIN_TIMEOUT_MS);

        poll = setInterval(() => {
          getMcpOAuthStatus(loginID)
            .then((st) => {
              if (st.status === "done") {
                finish(null);
              } else if (st.status === "expired") {
                finish("Login session expired — click Test now to try again.");
              }
            })
            .catch(() => {});
        }, POLL_INTERVAL_MS);

        function handle(payload: BroadcastPayload | null) {
          if (!payload || payload.type !== "wick-mcp-oauth") return;
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
      })
      .catch((e) => {
        cleanup();
        reject(e instanceof Error ? e : new Error(String(e)));
      });
  });

  return {
    promise,
    cancel: () => {
      cancelled = true;
      cleanup();
    },
  };
}
