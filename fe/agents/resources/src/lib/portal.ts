// portal.ts — move an element to <body> so ancestors cannot clip it.
//
// A dropdown positioned inside a scrolling or `overflow-hidden` container
// is clipped by it, however it is styled. That is not something a CSS
// rule can undo from the outside: the containing block is what does the
// clipping, so the element has to leave it.
//
// Used by the row menu, which lives inside a table wrapped in both
// `overflow-hidden` (rounded card) and `overflow-x-auto` (wide table) —
// removing either would cost the rounded corners or the ability to scroll
// a wide table, so the menu moves instead.

/** Options for {@link portal}. */
export interface PortalOptions {
  /** Viewport coordinates the element should be anchored to. */
  x: number;
  y: number;
  /** Preferred width, used to keep the element inside the viewport. */
  width?: number;
  /**
   * Cover the whole viewport instead of anchoring to x/y — for the
   * click-away layer behind a menu, which needs to catch a click
   * anywhere on the page rather than sit at one point.
   */
  fill?: boolean;
}

/**
 * Svelte action: reparent the node to <body> and position it in viewport
 * coordinates.
 *
 * Flips upward or leftward when the element would otherwise run off the
 * screen — a menu on the last row of a long table opens above its trigger
 * rather than below the fold.
 */
export function portal(node: HTMLElement, opts: PortalOptions) {
  document.body.appendChild(node);
  node.style.position = "fixed";
  node.style.zIndex = "50";

  function place(o: PortalOptions): void {
    if (o.fill) {
      // Behind the anchored layer, never over it.
      node.style.zIndex = "40";
      node.style.inset = "0";
      return;
    }

    const w = o.width ?? node.offsetWidth ?? 176;
    const h = node.offsetHeight || 0;
    const margin = 8;

    // Right-align to the trigger, then pull back if that overflows.
    let left = o.x - w;
    if (left < margin) left = margin;
    if (left + w > window.innerWidth - margin) {
      left = Math.max(margin, window.innerWidth - w - margin);
    }

    // Below the trigger unless there is no room, in which case above it.
    let top = o.y;
    if (h > 0 && top + h > window.innerHeight - margin) {
      top = Math.max(margin, o.y - h - 24);
    }

    node.style.left = `${left}px`;
    node.style.top = `${top}px`;
  }

  place(opts);
  // Height is unknown until the node is in the document, so place again on
  // the next frame — otherwise the flip-up decision is made against 0.
  requestAnimationFrame(() => place(opts));

  return {
    update(next: PortalOptions) {
      place(next);
    },
    destroy() {
      node.remove();
    },
  };
}
