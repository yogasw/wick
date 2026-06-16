import { mount, unmount, type Component } from "svelte";
import App from "./App.svelte";

// Two entry modes:
//
//  1. Island — the session page mounts the SCM panel into a docked
//     <aside> (sidebar mode) or a modal (full mode) via
//     window.WickSCM.mount(host, opts). The bundle is injected lazily on
//     first use; mount/unmount cycle as the host opens/closes.
//
//  2. Standalone — direct hit of /tools/agents/workflow/scm/?session=<id>
//     mounts the full App on #app (dev / direct link).

export type SCMMountOpts = {
  sessionID: string;
  mode?: "sidebar" | "full";
  pinned?: boolean;
  onPinToggle?: () => void;
  onClose?: () => void;
};

type SCMApi = {
  mount(target: HTMLElement, opts: SCMMountOpts): void;
  unmount(target: HTMLElement): void;
};

declare global {
  interface Window {
    WickSCM?: SCMApi;
  }
}

const instances = new WeakMap<HTMLElement, ReturnType<typeof mount>>();

window.WickSCM = {
  mount(target, opts) {
    const prev = instances.get(target);
    if (prev) {
      void unmount(prev);
      instances.delete(target);
    }
    target.innerHTML = "";
    const inst = mount(App as unknown as Component<SCMMountOpts>, {
      target,
      props: {
        sessionID: opts.sessionID,
        mode: opts.mode ?? "sidebar",
        pinned: opts.pinned ?? false,
        onPinToggle: opts.onPinToggle,
        onClose: opts.onClose,
      },
    });
    instances.set(target, inst);
  },
  unmount(target) {
    const inst = instances.get(target);
    if (inst) {
      void unmount(inst);
      instances.delete(target);
    }
  },
};

// Standalone fallback — only on the genuine standalone shell (marked with
// data-scm-standalone), never when this bundle is lazy-loaded as an island.
const appTarget = document.getElementById("app");
if (appTarget && appTarget.dataset.scmStandalone !== undefined) {
  mount(App, { target: appTarget });
}
