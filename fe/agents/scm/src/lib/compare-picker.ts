// The pure half of the Compare overlay: which strings may be a side, what
// the quick presets expand to, and how the file search matches. Kept out
// of the component so it can be tested without mounting Monaco.

// The working-tree sides the server understands (scm.WorktreeRef /
// scm.StagedRef). ':' is illegal in a ref name, so neither can collide
// with a branch.
export const WORKTREE = ":worktree";
export const STAGED = ":staged";

export const isWorkingSide = (ref: string) => ref === WORKTREE || ref === STAGED;

// What a side reads as in the UI. Shas and branch names show as typed.
export function sideLabel(ref: string): string {
  if (ref === WORKTREE) return "Working tree (uncommitted)";
  if (ref === STAGED) return "Staged changes";
  return ref;
}

// refShapeError is the client-side guard for a typed ref, mirroring the
// server's: never something git would read as an option, never a range or
// a pathspec. The server still checks the ref exists — this only keeps an
// obviously wrong string from becoming a request. "" means fine.
export function refShapeError(raw: string): string {
  const ref = raw.trim();
  if (!ref) return "Type a branch, tag or commit sha.";
  if (isWorkingSide(ref)) return "";
  if (ref.startsWith("-")) return "A ref may not start with '-'.";
  if (/\s/.test(ref)) return "A ref may not contain spaces.";
  if (ref.includes("..")) return "Pick each side on its own, not a range.";
  // ':' would make it a pathspec (HEAD:file); the rest are characters git
  // forbids in ref names anyway.
  if (/[:\\?*[\x00-\x1f\x7f]/.test(ref)) return "That is not a valid ref.";
  return "";
}

export type ComparePair = { base: string; head: string; threeDot: boolean };

export type Preset = "trunk-head" | "trunk-worktree" | "last-n";

// presetPair turns one of the quick presets into a pair. trunk is the
// detected default branch (origin/HEAD, else master/main); without one the
// trunk presets have nothing to stand on and return null.
//
// Trunk presets use the merge base — "what this branch adds", the way a
// PR reads. Last N is a straight HEAD~N..HEAD: those commits and nothing
// else.
export function presetPair(p: Preset, trunk: string, n = 3): ComparePair | null {
  switch (p) {
    case "trunk-head":
      return trunk ? { base: trunk, head: "HEAD", threeDot: true } : null;
    case "trunk-worktree":
      return trunk ? { base: trunk, head: WORKTREE, threeDot: true } : null;
    case "last-n": {
      const k = Math.floor(n);
      if (!Number.isFinite(k) || k < 1) return null;
      return { base: `HEAD~${k}`, head: "HEAD", threeDot: false };
    }
  }
}

// rangePair is the pair for a run of commits picked in the graph, oldest
// and newest in either order: the oldest one's parent up to the newest,
// so the oldest commit's own change is part of the diff.
export function rangePair(a: { sha: string; index: number }, b: { sha: string; index: number }): ComparePair {
  // The log lists newest first, so the larger index is the older commit.
  const [older, newer] = a.index >= b.index ? [a, b] : [b, a];
  return { base: `${older.sha}^`, head: newer.sha, threeDot: false };
}

// fuzzyScore says whether every character of q appears in text, in order,
// and how tightly: lower is better, -1 is no match. A plain substring
// always beats a scattered match, and a hit in the file name beats one in
// the folders above it.
export function fuzzyScore(text: string, q: string): number {
  const t = text.toLowerCase();
  const needle = q.trim().toLowerCase();
  if (!needle) return 0;
  const sub = t.indexOf(needle);
  if (sub >= 0) {
    const nameStart = t.lastIndexOf("/") + 1;
    return sub >= nameStart ? 0 : 1;
  }
  let at = -1;
  let gaps = 0;
  for (const ch of needle) {
    const next = t.indexOf(ch, at + 1);
    if (next < 0) return -1;
    if (at >= 0) gaps += next - at - 1;
    at = next;
  }
  return 2 + gaps;
}

// filterFiles keeps the files whose path matches q, best first; the input
// order breaks ties, so an exact-substring search keeps the list's order.
// An empty query returns the list untouched.
export function filterFiles<T extends { path: string; orig_path?: string }>(files: T[], q: string): T[] {
  if (!q.trim()) return files;
  const scored: { f: T; s: number; i: number }[] = [];
  files.forEach((f, i) => {
    let s = fuzzyScore(f.path, q);
    if (f.orig_path) {
      const o = fuzzyScore(f.orig_path, q);
      if (o >= 0 && (s < 0 || o < s)) s = o;
    }
    if (s >= 0) scored.push({ f, s, i });
  });
  scored.sort((a, b) => a.s - b.s || a.i - b.i);
  return scored.map((x) => x.f);
}
