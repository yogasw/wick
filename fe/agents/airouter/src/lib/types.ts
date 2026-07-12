// Server-reported install + run state. `state` is the single source of
// truth for the status badge; the booleans are legacy fallbacks.
export type Status = {
  installed: boolean;
  version: string;
  running: boolean;
  state: "not-installed" | "checking" | "starting" | "running" | "stopped";
  // True while the version is still being resolved for the first time —
  // render "Checking…" instead of a blank/stale version.
  checking?: boolean;
};

// One proxied /airouter/<id>/v1 request, streamed live over SSE. Carries the
// FULL request/response bodies — they live only in this browser tab.
export type ReqEvent = {
  time: string;
  method: string;
  path: string;
  host: string;
  remote_addr: string;
  client_ip: string;
  external: boolean;
  auth: string;
  user_agent: string;
  model: string;
  status: number;
  duration_ms: number;
  req_body: string;
  resp_body: string;
};

// A ReqEvent tagged with a stable client-side id for keyed rendering.
export type ReqRow = ReqEvent & { id: number };

// One registered router backend, from GET /airouter/routers. Drives the
// switcher and seeds the per-router Settings toggles.
export type RouterInfo = {
  id: string;
  name: string;
  blurb: string;
  icon: string; // inline SVG inner markup (rendered inside an <svg>)
  autostart: boolean;
  external: boolean;
};
