/* Ranking for "go to file". The server returns candidates for a term; which
   of them a person meant is a client question, and the answer is mostly about
   WHERE the term matched: the file name beats the folders above it, the start
   of a name beats the middle, and a literal run beats letters that merely
   appear in order. */

import type { SessionFileEntry } from "./file-browser-types.js";

/** A scored candidate. Exported so the ordering can be asserted directly
    rather than inferred from a rendered list. */
export type PathHit = SessionFileEntry & { score: number };

/** Do `term`'s characters appear in `s`, in order but not necessarily
    adjacent? This is what lets "scmgit" find "internal/agents/scm/git.go". */
export function subsequence(s: string, term: string): boolean {
  if (term === "") return true;
  let i = 0;
  for (const ch of s) {
    if (ch === term[i]) i++;
    if (i === term.length) return true;
  }
  return false;
}

/* The tiers. Spaced far enough apart that the length tie-break below can
   never lift a weaker kind of match above a stronger one. */
const EXACT_NAME = 1000;
const NAME_PREFIX = 800;
const NAME_CONTAINS = 600;
const PATH_CONTAINS = 400;
const NAME_SUBSEQ = 200;
const PATH_SUBSEQ = 100;

/** Score one candidate. 0 means "not a match", including for a blank term.
    Both path and name are taken so a caller that already has the basename
    does not pay to re-derive it for every entry. */
export function scorePath(path: string, name: string, term: string): number {
  const t = term.toLowerCase().trim();
  if (t === "") return 0;
  const p = path.toLowerCase();
  const n = (name || path.slice(path.lastIndexOf("/") + 1)).toLowerCase();

  let base: number;
  if (t.includes("/")) {
    // A slash in the query means the person is describing WHERE the file
    // lives, so the name tiers do not apply — "agents/scm" is not a
    // filename, and scoring it against one would rank by accident.
    if (p.includes(t)) base = PATH_CONTAINS;
    else if (subsequence(p, t)) base = PATH_SUBSEQ;
    else return 0;
  } else if (n === t) base = EXACT_NAME;
  else if (n.startsWith(t)) base = NAME_PREFIX;
  else if (n.includes(t)) base = NAME_CONTAINS;
  else if (p.includes(t)) base = PATH_CONTAINS;
  else if (subsequence(n, t)) base = NAME_SUBSEQ;
  else if (subsequence(p, t)) base = PATH_SUBSEQ;
  else return 0;

  // Among equals, prefer the shorter path: a hit five folders deep is
  // rarely the one someone typed three letters to reach.
  return base - Math.min(p.length, 200) / 10;
}

/** Rank candidates for a term, dropping folders and non-matches. A blank
    term finds nothing rather than everything — an empty quick-open box
    should show no list, not the whole repo. */
export function rankPathHits(
  entries: SessionFileEntry[],
  term: string,
  limit?: number,
): SessionFileEntry[] {
  if (term.trim() === "") return [];
  const scored: PathHit[] = [];
  for (const e of entries) {
    if (e.isDir) continue;
    const score = scorePath(e.path, e.name, term);
    if (score > 0) scored.push({ ...e, score });
  }
  scored.sort((a, b) => b.score - a.score);
  return typeof limit === "number" ? scored.slice(0, limit) : scored;
}

/* A deep search returns files, not the folders above them. A caller that
   wants to hang hits on a tree needs those folders to exist as rows, so this
   fills them in — a directory entry for every missing ancestor, with the
   hits themselves left exactly as they came. */
export function withAncestorDirs(entries: SessionFileEntry[]): SessionFileEntry[] {
  const seen = new Set(entries.map((e) => e.path));
  const out = [...entries];
  for (const e of entries) {
    const segs = e.path.split("/");
    for (let i = 1; i < segs.length; i++) {
      const dir = segs.slice(0, i).join("/");
      if (dir === "" || seen.has(dir)) continue;
      seen.add(dir);
      out.push({ path: dir, name: segs[i - 1], size: 0, isDir: true, mtime: 0 });
    }
  }
  return out;
}
