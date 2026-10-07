/* Move a node to <body> so it escapes whatever stacking context it was
   declared in. The SCM panel is mounted inside the session page's dock, an
   <aside class="fixed … z-50">, and a z-index inside that aside can only ever
   compete *within* it — so an overlay declared there is painted under any
   sibling of the aside that sits higher, e.g. the session's left rail. The
   overlay is viewport-fixed anyway, so moving it to the body costs nothing
   and takes it out of the dock's context entirely. */
export function portal(node: HTMLElement) {
  document.body.appendChild(node);
  return {
    destroy() {
      node.remove();
    },
  };
}
