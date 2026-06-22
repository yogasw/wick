import { readable, writable } from "svelte/store";

function getBase(): string {
  return document.getElementById("app")?.dataset.base ?? "";
}

export function routeFromPath(pathname: string, base: string): string {
  if (pathname === base || pathname === base + "/") return "/";
  if (pathname.startsWith(base + "/")) {
    return "/" + pathname.slice(base.length + 1);
  }
  return "/";
}

// When the SPA is hosted outside its own base (the Agents shell mounts it
// at /tools/agents/connectors while base stays /manager), window.location
// won't match base, so routeFromPath would always yield "/". The host
// forwards the intended client route via data-deep — honour it for the
// initial route only. After boot, push() drives navigation normally.
function initialRoute(): string {
  const deep = document.getElementById("app")?.dataset.deep ?? "";
  if (deep) return deep.startsWith("/") ? deep : "/" + deep;
  return routeFromPath(window.location.pathname, getBase());
}

const _route = writable<string>(initialRoute());

window.addEventListener("popstate", () => {
  _route.set(routeFromPath(window.location.pathname, getBase()));
});

export const route = readable<string>(initialRoute(), (set) =>
  _route.subscribe(set),
);

export function push(path: string): void {
  if (!path.startsWith("/")) path = "/" + path;
  const base = getBase();
  const fullUrl = path === "/" ? base : `${base}${path}`;
  history.pushState({}, "", fullUrl);
  _route.set(routeFromPath(window.location.pathname, base));
}

export function match(
  pattern: string,
  path: string,
): Record<string, string> | null {
  const pSegs = pattern.split("/").filter(Boolean);
  const sSegs = path.split("/").filter(Boolean);
  if (pSegs.length !== sSegs.length) return null;
  const params: Record<string, string> = {};
  for (let i = 0; i < pSegs.length; i++) {
    if (pSegs[i].startsWith(":")) {
      params[pSegs[i].slice(1)] = decodeURIComponent(sSegs[i]);
    } else if (pSegs[i] !== sSegs[i]) {
      return null;
    }
  }
  return params;
}
