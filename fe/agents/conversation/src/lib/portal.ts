/* Move a node to <body> so it escapes whatever stacking context it was
   declared in. The Context window panel floats above the composer as an
   `absolute z-30` box, and a z-index inside it only ever competes within
   that box — so its big chart, declared there, is painted under the
   roster sidebar (`z-40`) however high its own z-index. The chart is
   viewport-fixed anyway, so moving it to the body costs nothing and takes
   it out of the panel's context entirely. Same helper as the SCM panel's. */
export function portal(node: HTMLElement) {
  document.body.appendChild(node);
  return {
    destroy() {
      node.remove();
    },
  };
}
