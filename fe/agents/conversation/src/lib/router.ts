import { readable } from "svelte/store";

function readHash(): string {
  const h = window.location.hash;
  if (!h || h === "#") return "/";
  return h.startsWith("#") ? h.slice(1) : h;
}

export const route = readable<string>(readHash(), (set) => {
  const onHash = () => set(readHash());
  window.addEventListener("hashchange", onHash);
  return () => window.removeEventListener("hashchange", onHash);
});

export function push(path: string): void {
  if (!path.startsWith("/")) path = "/" + path;
  window.location.hash = path;
}

export function initialRoute(hash: string, initialSession: string | null | undefined): string | null {
  if (hash && hash !== "/") return null;
  if (initialSession) return "/sessions/" + initialSession;
  return null;
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
