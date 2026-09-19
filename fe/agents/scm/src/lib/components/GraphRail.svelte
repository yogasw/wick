<script lang="ts">
  // One row's worth of rail: the vertical lanes passing through, the
  // diagonals where branches leave or merge, and this commit's dot.
  //
  // Geometry is fixed per row (ROW_H) because the lanes have to line up
  // with the rows above and below — a rail that stretched with its row
  // would bend every diagonal by a different amount.
  import type { GraphRow } from "$lib/graph";

  type Props = { row: GraphRow; lanes: number };
  let { row, lanes }: Props = $props();

  const LANE_W = 12;
  const ROW_H = 44;
  const R = 3.5;

  const x = (lane: number) => lane * LANE_W + LANE_W / 2;
  const width = $derived(Math.max(lanes, 1) * LANE_W);

  // Tailwind cannot see class names built at runtime, so the palette is
  // literal stroke colours. They are the chart hues, readable on both
  // themes without a per-theme swap.
  const PALETTE = [
    "#22b07d", "#6aa9ff", "#d98cff", "#ffb454",
    "#ff7b72", "#4dd0e1", "#c3e88d", "#f78fb3",
  ];
  const color = (i: number) => PALETTE[i % PALETTE.length];
</script>

<svg
  {width}
  height={ROW_H}
  viewBox={`0 0 ${width} ${ROW_H}`}
  class="shrink-0"
  aria-hidden="true"
>
  <!-- Lines arriving from the row above. -->
  {#each row.incoming as l (l.lane)}
    <line x1={x(l.lane)} y1="0" x2={x(l.lane)} y2={ROW_H / 2} stroke={color(l.color)} stroke-width="1.5" />
  {/each}
  <!-- Lines continuing into the row below. -->
  {#each row.outgoing as l (l.lane)}
    <line x1={x(l.lane)} y1={ROW_H / 2} x2={x(l.lane)} y2={ROW_H} stroke={color(l.color)} stroke-width="1.5" />
  {/each}
  <!-- Branches merging into this commit, coming down from above. -->
  {#each row.collapses as e (e.lane)}
    <path
      d={`M ${x(e.lane)} 0 L ${x(e.lane)} ${ROW_H / 4} Q ${x(e.lane)} ${ROW_H / 2} ${x(row.lane)} ${ROW_H / 2}`}
      fill="none"
      stroke={color(e.color)}
      stroke-width="1.5"
    />
  {/each}
  <!-- Extra parents branching away below. -->
  {#each row.merges as e (e.lane)}
    <path
      d={`M ${x(row.lane)} ${ROW_H / 2} Q ${x(e.lane)} ${ROW_H / 2} ${x(e.lane)} ${(ROW_H * 3) / 4} L ${x(e.lane)} ${ROW_H}`}
      fill="none"
      stroke={color(e.color)}
      stroke-width="1.5"
    />
  {/each}
  <circle cx={x(row.lane)} cy={ROW_H / 2} r={R} fill={color(row.color)} />
</svg>
