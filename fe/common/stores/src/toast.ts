import { writable, get } from "svelte/store";

// Toast queue — mirror of v1's #wf-toast-host. Three states:
//   ok    — green, used for clean save / publish
//   warn  — amber, used for "Saved with N validation errors"
//   error — red, used for save / publish failures
//
// Auto-dismiss after `ttlMs` (default 3.5 s) or on click. Multiple
// toasts stack vertically; newest at top.

export type ToastState = "ok" | "warn" | "error";

export type Toast = {
  id: number;
  state: ToastState;
  title: string;
  body?: string;
  ttlMs: number;
};

let nextID = 1;

export const toasts = writable<Toast[]>([]);

export function pushToast(opts: {
  state: ToastState;
  title: string;
  body?: string;
  ttlMs?: number;
}): number {
  // Dedupe — if an identical toast is already queued, refresh its
  // dismiss timer instead of stacking a duplicate. Common case: a
  // rapid double-click on Publish would otherwise emit "Cannot
  // publish" twice. Falls through for distinct messages.
  const existing = get(toasts).find(
    (t) => t.state === opts.state && t.title === opts.title && t.body === opts.body,
  );
  if (existing) {
    return existing.id;
  }
  const id = nextID++;
  const t: Toast = {
    id,
    state: opts.state,
    title: opts.title,
    body: opts.body,
    ttlMs: opts.ttlMs ?? 3500,
  };
  toasts.update((q) => [t, ...q]);
  if (t.ttlMs > 0) {
    setTimeout(() => dismissToast(id), t.ttlMs);
  }
  return id;
}

export function dismissToast(id: number) {
  toasts.update((q) => q.filter((t) => t.id !== id));
}

// Helpers for common shapes — keep call sites terse at the source.
export const toastOk = (title: string, body?: string) =>
  pushToast({ state: "ok", title, body });
export const toastWarn = (title: string, body?: string) =>
  pushToast({ state: "warn", title, body });
export const toastError = (title: string, body?: string) =>
  pushToast({ state: "error", title, body });

// Debugging hook — read current queue without subscribing.
export function snapshotToasts(): Toast[] {
  return get(toasts);
}
