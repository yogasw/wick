import { mount, type Component } from "svelte";
import App from "./App.svelte";
import EditorShell from "./lib/components/workflow/EditorShell.svelte";

// Island registry — keys match the `data-island` attribute templ writes.
// Adding an island: drop the .svelte under src/lib + add one line here.
// Mirror of the sveltempl ComponentMap pattern (sveltempl/internal/codegen)
// but inline because we ship a single bundle, not one per island.
const islands: Record<string, Component<any>> = {
  WorkflowEditor: EditorShell as unknown as Component<any>,
};

type IslandAPI = {
  mount(name: string, target: HTMLElement, props?: Record<string, unknown>): void;
};

declare global {
  interface Window {
    WickIslands?: IslandAPI;
  }
}

window.WickIslands = {
  mount(name, target, props = {}) {
    const Comp = islands[name];
    if (!Comp) {
      console.error(`[WickIslands] unknown island: ${name}`);
      return;
    }
    target.innerHTML = "";
    mount(Comp, { target, props });
  },
};

// Auto-hydrate every `[data-island]` div on first paint. Props live in
// `data-props` as JSON (kept on the element so server-rendered HTML can
// still describe the island fully without a separate <script> block).
function hydrateAll() {
  document.querySelectorAll<HTMLElement>("[data-island]").forEach((el) => {
    const name = el.dataset.island;
    if (!name) return;
    let props: Record<string, unknown> = {};
    if (el.dataset.props) {
      try {
        props = JSON.parse(el.dataset.props);
      } catch (e) {
        console.error(`[WickIslands] bad data-props on ${name}:`, e);
      }
    }
    window.WickIslands!.mount(name, el, props);
  });
}

// Fallback: legacy standalone shell at /agents-v2/workflow/ still mounts
// the full App on #app (so the SPA URL keeps working for dev / direct
// links). Hydrate islands first; if there are none, fall through to the
// standalone App mount.
if (document.readyState === "loading") {
  document.addEventListener("DOMContentLoaded", bootstrap);
} else {
  bootstrap();
}

function bootstrap() {
  const islandTargets = document.querySelectorAll("[data-island]");
  if (islandTargets.length > 0) {
    hydrateAll();
    return;
  }
  const appTarget = document.getElementById("app");
  if (appTarget) mount(App, { target: appTarget });
}
