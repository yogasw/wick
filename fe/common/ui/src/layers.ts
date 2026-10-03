/* Overlay layers: which modal / drawer is on top, and who answers Escape.

   Every overlay used to put its own `keydown` listener on the window, and a
   window listener runs in REGISTRATION order — so the side panel, mounted
   first, saw an Escape before the file modal opened on top of it did, and one
   press closed both. Asking "was it defaultPrevented?" cannot fix that: the
   panel runs before the modal has had the chance to prevent anything.

   So the layers live on one stack and ONE listener answers for them, on the
   document in the bubble phase. That position is the point:
   - it runs AFTER the element the key went to — a Select, an autocomplete,
     an inline rename — so a widget that consumed Escape (preventDefault)
     keeps it, and the layer under it stays open;
   - it runs BEFORE every window listener, and stops the event there, so
     whatever sits underneath (the side panel) never hears the press that
     closed the modal on top of it.

   The stack is kept on globalThis, not in this module: the Source panel is
   its own bundle with its own copy of this file, and two stacks would each
   believe they were on top. */

export type LayerOptions = {
  /* Called for an Escape that reached the layer while it is on top. Return
     false to say "not mine" — the event then goes on to whatever is below. */
  onEscape: () => boolean | void;
  /* Keep Tab inside the layer's node. Default true; a non-modal drawer
     passes false. */
  trap?: boolean;
  /* Move focus into the node when the layer opens (if it is not already
     there), and give it back to the opener when it closes. Default true. */
  focus?: boolean;
};

type Layer = LayerOptions & { id: number; node: HTMLElement | null; returnTo: HTMLElement | null };

// installedOn is the document the listener sits on, not a flag: a test
// runner swaps the document between files while globalThis survives.
type Registry = { stack: Layer[]; seq: number; installedOn: Document | null };

const KEY = "__wickLayers";

function registry(): Registry {
  const g = globalThis as unknown as Record<string, Registry | undefined>;
  let r = g[KEY];
  if (!r) {
    r = { stack: [], seq: 0, installedOn: null };
    g[KEY] = r;
  }
  return r;
}

const FOCUSABLE =
  'a[href], button:not([disabled]), input:not([disabled]):not([type="hidden"]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"]), [contenteditable="true"]';

function focusables(node: HTMLElement): HTMLElement[] {
  return Array.from(node.querySelectorAll<HTMLElement>(FOCUSABLE)).filter(
    (el) => !el.hasAttribute("inert") && el.getAttribute("aria-hidden") !== "true",
  );
}

function onKeydown(e: KeyboardEvent): void {
  const { stack } = registry();
  const top = stack[stack.length - 1];
  if (!top) return;
  if (e.key === "Escape") {
    // A widget inside the layer (autocomplete, select, inline edit) took it.
    if (e.defaultPrevented) return;
    if (top.onEscape() === false) return;
    e.preventDefault();
    e.stopPropagation();
    return;
  }
  if (e.key === "Tab" && top.trap !== false && top.node) trapTab(e, top.node);
}

function trapTab(e: KeyboardEvent, node: HTMLElement): void {
  const list = focusables(node);
  const active = document.activeElement as HTMLElement | null;
  if (list.length === 0) {
    e.preventDefault();
    node.focus();
    return;
  }
  const first = list[0];
  const last = list[list.length - 1];
  const inside = !!active && node.contains(active);
  if (e.shiftKey && (!inside || active === first || active === node)) {
    e.preventDefault();
    last.focus();
  } else if (!e.shiftKey && (!inside || active === last)) {
    e.preventDefault();
    first.focus();
  }
}

function install(r: Registry): void {
  if (typeof document === "undefined" || r.installedOn === document) return;
  document.addEventListener("keydown", onKeydown);
  // A key dispatched at the window itself never passes the document (tests
  // do this, and so does a synthetic shortcut); answer those here too.
  window.addEventListener("keydown", (e) => {
    // Anything aimed at a node already passed the document listener.
    if (!(e.target instanceof Node)) onKeydown(e);
  });
  r.installedOn = document;
}

/* pushLayer puts an overlay on top and returns its release function.
   Release is idempotent and may run out of order (a layer under the top
   closing first is fine — it just leaves the stack). */
export function pushLayer(node: HTMLElement | null, opts: LayerOptions): () => void {
  const r = registry();
  install(r);
  const active = typeof document !== "undefined" ? (document.activeElement as HTMLElement | null) : null;
  const layer: Layer = {
    ...opts,
    id: ++r.seq,
    node,
    returnTo: active && active !== document.body ? active : null,
  };
  r.stack.push(layer);
  if (node && opts.focus !== false && !node.contains(active)) {
    if (!node.hasAttribute("tabindex")) node.setAttribute("tabindex", "-1");
    node.focus({ preventScroll: true });
  }
  let released = false;
  return () => {
    if (released) return;
    released = true;
    r.stack = r.stack.filter((l) => l.id !== layer.id);
    if (opts.focus === false) return;
    // Give focus back only when it is still ours to give: focus that went
    // somewhere else on purpose stays where it went.
    const now = document.activeElement;
    const lost = !now || now === document.body || (!!node && (node.contains(now) || !node.isConnected));
    if (lost && layer.returnTo?.isConnected) layer.returnTo.focus({ preventScroll: true });
  };
}

/* topLayerNode is the node of the layer on top — for a key handler that is
   not Escape but must still only act when its own overlay is the top one. */
export function topLayerNode(): HTMLElement | null {
  const { stack } = registry();
  return stack[stack.length - 1]?.node ?? null;
}

export function layerDepth(): number {
  return registry().stack.length;
}

/* `use:layer={{ onEscape }}` — the action form, for an overlay whose node
   exists exactly as long as it is open. */
export function layer(node: HTMLElement, opts: LayerOptions) {
  let current = opts;
  const release = pushLayer(node, {
    onEscape: () => current.onEscape(),
    trap: opts.trap,
    focus: opts.focus,
  });
  return {
    update(next: LayerOptions) {
      current = next;
    },
    destroy() {
      release();
    },
  };
}
