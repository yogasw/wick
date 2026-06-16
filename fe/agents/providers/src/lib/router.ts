import { readable, writable } from "svelte/store";

function getBase(): string {
  return document.getElementById("app")?.dataset.base ?? "";
}

export function routeFromPath(pathname: string, base: string): string {
  const prefix = base + "/providers";
  if (pathname === prefix || pathname === prefix + "/") return "/";
  if (pathname.startsWith(prefix + "/")) {
    return "/" + pathname.slice(prefix.length + 1);
  }
  return "/";
}

const _route = writable<string>(routeFromPath(window.location.pathname, getBase()));

window.addEventListener("popstate", () => {
  _route.set(routeFromPath(window.location.pathname, getBase()));
});

export const route = readable<string>(
  routeFromPath(window.location.pathname, getBase()),
  (set) => _route.subscribe(set),
);

export function push(path: string): void {
  if (!path.startsWith("/")) path = "/" + path;
  const base = getBase();
  const fullUrl = path === "/" ? `${base}/providers` : `${base}/providers${path}`;
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
