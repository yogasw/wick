// graph.ts turns a flat commit list into lane geometry — the rails an
// editor's history view draws down the left of each row.
//
// Pure data in, pure data out: the component only has to map lanes to x
// positions and colours. Keeping the layout here is what makes it testable,
// since a lane assignment that is subtly wrong still *looks* like a graph.
//
// The walk is the usual one. Each lane holds the sha it is waiting for. A
// commit takes the lane expecting it (or opens a new one if nothing was),
// any other lane waiting for the same commit collapses into it, and its
// parents then take over: the first parent stays in the lane, the rest
// branch off into lanes of their own.

export type Commit = { sha: string; parents?: string[] };

export type LaneLine = { lane: number; color: number };
export type LaneEdge = { lane: number; color: number };

export type GraphRow = {
  sha: string;
  /** Column the commit's dot sits in. */
  lane: number;
  color: number;
  /** Lanes running into this row from above — drawn as the top half. */
  incoming: LaneLine[];
  /** Lanes continuing below this row — drawn as the bottom half. */
  outgoing: LaneLine[];
  /** Lanes that merged into this commit: a diagonal from above. */
  collapses: LaneEdge[];
  /** Extra parents branching away below: a diagonal downward. */
  merges: LaneEdge[];
  /** Widest lane index in use at this row, +1. Drives the rail width. */
  width: number;
};

/** How many distinct rail colours to cycle through. */
export const LANE_COLORS = 8;

export function buildGraph(commits: Commit[]): GraphRow[] {
  // lanes[i] = sha lane i is waiting for, or null when the lane is free.
  const lanes: (string | null)[] = [];
  const colors: number[] = [];
  let nextColor = 0;

  const allocate = (sha: string): number => {
    let i = lanes.indexOf(null);
    if (i === -1) {
      i = lanes.length;
      lanes.push(null);
      colors.push(0);
    }
    lanes[i] = sha;
    colors[i] = nextColor++ % LANE_COLORS;
    return i;
  };
  const snapshot = (): LaneLine[] =>
    lanes
      .map((s, i) => (s !== null ? { lane: i, color: colors[i] } : null))
      .filter((v): v is LaneLine => v !== null);

  const rows: GraphRow[] = [];

  for (const c of commits) {
    const parents = c.parents ?? [];

    // The lane this commit belongs to. A commit nothing was waiting for is
    // a tip — the newest commit of a branch — and opens its own lane.
    let lane = lanes.indexOf(c.sha);
    const isTip = lane === -1;
    if (isTip) lane = allocate(c.sha);

    // Everything else waiting for this same commit is a branch merging in.
    const collapses: LaneEdge[] = [];
    for (let i = 0; i < lanes.length; i++) {
      if (i !== lane && lanes[i] === c.sha) {
        collapses.push({ lane: i, color: colors[i] });
        lanes[i] = null;
      }
    }

    // Lines arriving from above. A tip has nothing above it in its own
    // lane, so it is excluded — otherwise every branch head sprouts a
    // stub going nowhere.
    const incoming = snapshot().filter((l) => !(isTip && l.lane === lane));

    // Hand the lane to the first parent; the others branch off.
    const merges: LaneEdge[] = [];
    if (parents.length === 0) {
      lanes[lane] = null; // root commit: the lane ends here
    } else {
      lanes[lane] = parents[0];
      for (const p of parents.slice(1)) {
        let pl = lanes.indexOf(p);
        if (pl === -1) pl = allocate(p);
        merges.push({ lane: pl, color: colors[pl] });
      }
    }

    const outgoing = snapshot();
    const width = Math.max(
      lane + 1,
      ...incoming.map((l) => l.lane + 1),
      ...outgoing.map((l) => l.lane + 1),
      ...collapses.map((l) => l.lane + 1),
      ...merges.map((l) => l.lane + 1),
    );

    rows.push({ sha: c.sha, lane, color: colors[lane], incoming, outgoing, collapses, merges, width });
  }

  return rows;
}

/** Widest rail across all rows — what the column is sized to. */
export function graphWidth(rows: GraphRow[]): number {
  return rows.reduce((m, r) => Math.max(m, r.width), 1);
}
