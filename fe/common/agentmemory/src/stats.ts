// The shapes the shared analytics blocks render, and the one calculation they
// need (Yoga, 2026-09-26: "analytic all masukin ke per project juga, dan
// halaman ini sharing ke all juga").
//
// The global panel's Analytics tab and a project's own tab ask different
// questions of different sources, and that difference must survive — a
// store-wide total relabelled as a project's is the confusion PLAN §13.5 opens
// with. What they share is the DRAWING: a grid of labelled numbers, a list of
// proportional bars. So the components here take rows and know nothing about
// scope; each caller builds its own rows from its own source and states which
// scope they are (Section's `scope` header).

// StatItem is one labelled number in a grid. `note` is the sentence under it —
// what the number counts, or what it is measured against — because a figure
// under a two-word heading is only readable by whoever wrote it.
export type StatItem = { label: string; value: string; note?: string };

// MeterRow is one proportional bar: a label, its own figure, and its share of
// the whole. `share` is a fraction (0–1) rather than a percentage string so
// the bar and the caption cannot disagree.
export type MeterRow = {
  id: string;
  label: string;
  value: string;
  share: number;
  // bar overrides the fill class. Defaulted rather than required: most rows
  // are one accent colour, and only a breakdown whose slices MEAN different
  // things (accepted vs shed) needs to colour them apart.
  bar?: string;
  note?: string;
};

// meterWidth turns a share into a CSS percentage.
//
// A non-zero share never renders as an invisible sliver: a bar that rounds to
// 0% reads as "none", which is a different fact from "almost none".
export function meterWidth(share: number): number {
  if (!Number.isFinite(share) || share <= 0) return 0;
  const pct = Math.round(share * 100);
  return Math.min(100, Math.max(2, pct));
}
