// Paths in the Files tab live in two coordinate systems and this module is
// the only place they meet.
//
// The tree the user browses is rooted at the repo selected in the Source
// panel, so everything on screen is REPO-relative ("src/main.ts"). The
// session files API only speaks SESSION-relative paths, i.e. relative to the
// session cwd ("wick/src/main.ts"), and the repo itself is just a folder
// inside it — "." meaning the cwd IS the repo. Every request converts on the
// way out and every entry converts on the way back, so a repo switch is a
// pure re-root: no other state has to know which repo it came from.

/** normalizeRel cleans a user- or server-supplied relative path: Windows
 *  separators, duplicate and edge slashes, "." segments, and ".." resolved
 *  against the path itself (never above the root — the API would reject it
 *  and there is nothing above a repo root worth browsing anyway). */
export function normalizeRel(p: string): string {
  const out: string[] = [];
  for (const seg of p.replace(/\\/g, "/").split("/")) {
    if (seg === "" || seg === ".") continue;
    if (seg === "..") {
      out.pop();
      continue;
    }
    out.push(seg);
  }
  return out.join("/");
}

/** repoRoot is the session-relative prefix of a repo. "." (the cwd itself)
 *  and "" both mean "no prefix". */
export function repoRoot(repo: string): string {
  const clean = normalizeRel(repo);
  return clean === "." ? "" : clean;
}

/** sessionPath turns a repo-relative path into the session-relative path the
 *  files API wants. The repo root itself maps to "" — which the list
 *  endpoint reads as the session cwd. */
export function sessionPath(repo: string, rel: string): string {
  const root = repoRoot(repo);
  const r = normalizeRel(rel);
  if (!root) return r;
  return r ? `${root}/${r}` : root;
}

/** toRepoRel is the inverse: it strips the repo prefix off a path the API
 *  returned. null means the path is outside the repo — a listing that
 *  arrived after the user switched repos, which must be dropped rather than
 *  attached to the wrong tree. */
export function toRepoRel(repo: string, sessionRel: string): string | null {
  const root = repoRoot(repo);
  const s = normalizeRel(sessionRel);
  if (!root) return s;
  if (s === root) return "";
  return s.startsWith(`${root}/`) ? s.slice(root.length + 1) : null;
}

/** joinRel appends one name (which may itself contain slashes — "a/b.ts" is
 *  how you create a nested file) to a directory. */
export function joinRel(dir: string, name: string): string {
  const d = normalizeRel(dir);
  const n = normalizeRel(name);
  if (!d) return n;
  return n ? `${d}/${n}` : d;
}

/** parentRel walks one level up; the root's parent is the root. */
export function parentRel(rel: string): string {
  const clean = normalizeRel(rel);
  const i = clean.lastIndexOf("/");
  return i < 0 ? "" : clean.slice(0, i);
}

/** ancestorRels lists every folder above a path, root first — what the tree
 *  has to expand (and re-list) to make a freshly created file visible. */
export function ancestorRels(rel: string): string[] {
  const clean = normalizeRel(rel);
  const out = [""];
  if (!clean) return out;
  const segs = clean.split("/");
  for (let i = 1; i < segs.length; i++) out.push(segs.slice(0, i).join("/"));
  return out;
}

/** breadcrumbs turns the folder the tree is rooted at into clickable crumbs.
 *  The repo root comes first and is always present, so there is a way back
 *  up from anywhere. */
export function breadcrumbs(repo: string, rel: string): { name: string; path: string }[] {
  const crumbs = [{ name: rootLabel(repo), path: "" }];
  const clean = normalizeRel(rel);
  if (!clean) return crumbs;
  const segs = clean.split("/");
  for (let i = 0; i < segs.length; i++) {
    crumbs.push({ name: segs[i], path: segs.slice(0, i + 1).join("/") });
  }
  return crumbs;
}

/** rootLabel names the repo in a crumb: its folder name, or "cwd" when the
 *  repo IS the session working directory. */
export function rootLabel(repo: string): string {
  const root = repoRoot(repo);
  if (!root) return "cwd";
  return root.slice(root.lastIndexOf("/") + 1);
}

/** baseName is the last segment — what a row displays. */
export function baseName(rel: string): string {
  const clean = normalizeRel(rel);
  return clean.slice(clean.lastIndexOf("/") + 1);
}

/** isWithin reports whether rel sits inside dir (a folder contains itself).
 *  Used to drop cached listings and the open file when their folder is
 *  deleted. */
export function isWithin(dir: string, rel: string): boolean {
  const d = normalizeRel(dir);
  const r = normalizeRel(rel);
  if (!d) return true;
  return r === d || r.startsWith(`${d}/`);
}

/** sortEntries puts folders before files and sorts each group by name, the
 *  order every file tree uses. Returns a new array — the server's order is
 *  plain readdir. */
export function sortEntries<T extends { name: string; isDir: boolean }>(xs: T[]): T[] {
  return [...xs].sort((a, b) => {
    if (a.isDir !== b.isDir) return a.isDir ? -1 : 1;
    return a.name.localeCompare(b.name);
  });
}
