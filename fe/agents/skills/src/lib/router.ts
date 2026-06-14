import { readable, writable } from "svelte/store";

function getBase(): string {
  return document.getElementById("app")?.dataset.base ?? "";
}

export function routeFromPath(pathname: string, base: string): string {
  const prefix = base + "/skills";
  if (pathname === prefix || pathname === prefix + "/") return "/";
  if (pathname.startsWith(prefix + "/")) {
    return "/skills/" + pathname.slice(prefix.length + 1);
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
  const fullUrl = path === "/" ? `${base}/skills` : `${base}${path}`;
  history.pushState({}, "", fullUrl);
  _route.set(routeFromPath(window.location.pathname, base));
}

export function match(
  pattern: string,
  path: string,
): Record<string, string> | null {
  const pSegs = pattern.split("/").filter(Boolean);
  const sSegs = path.split("/").filter(Boolean);

  const catchAllIdx = pSegs.findIndex((s) => s.startsWith(":") && s.endsWith("..."));
  if (catchAllIdx !== -1) {
    if (sSegs.length < catchAllIdx) return null;
    const params: Record<string, string> = {};
    for (let i = 0; i < catchAllIdx; i++) {
      if (pSegs[i].startsWith(":")) {
        params[pSegs[i].slice(1)] = decodeURIComponent(sSegs[i]);
      } else if (pSegs[i] !== sSegs[i]) {
        return null;
      }
    }
    const key = pSegs[catchAllIdx].slice(1, -3);
    params[key] = sSegs.slice(catchAllIdx).map(decodeURIComponent).join("/");
    return params;
  }

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
