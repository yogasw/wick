import { apiGetE, apiPostE } from "@wick-fe/common-api";
import { Effect } from "effect";
import type { MemoryReport, SeriesResponse, ProcessListResponse } from "./types.js";

// Effect-based per the FE module contract: the caller provides the
// HttpClient layer, which is what makes these mockable in unit tests
// without touching the network.

export const fetchReportE = (base: string) =>
  apiGetE<MemoryReport>(`${base}/api/memory`).pipe(
    // The backend omits empty slices, so normalise once here rather than
    // guarding every access in the page.
    Effect.map((r) => ({ ...r, agents: r.agents ?? [] })),
  );

export const fetchSeriesE = (base: string, minutes: number) =>
  apiGetE<SeriesResponse>(`${base}/api/memory/series?minutes=${minutes}`).pipe(
    Effect.map((r) => ({ ...r, machine: r.machine ?? [], agents: r.agents ?? [] })),
  );

export const applySuggestedE = (base: string) =>
  apiPostE<{ ok: boolean }>(`${base}/api/memory/apply-suggested`, {});

// Fetched only when the explorer is used — the dashboard polls every 10s,
// and shipping ~350 processes on every poll would be most of the payload
// for a table nobody is looking at.
export const fetchProcessesE = (
  base: string,
  opts: { q?: string; sort?: string; page?: number; perPage?: number } = {},
) => {
  const p = new URLSearchParams();
  if (opts.q) p.set("q", opts.q);
  if (opts.sort) p.set("sort", opts.sort);
  if (opts.page) p.set("page", String(opts.page));
  if (opts.perPage) p.set("per_page", String(opts.perPage));
  const qs = p.toString();
  return apiGetE<ProcessListResponse>(`${base}/api/processes${qs ? `?${qs}` : ""}`).pipe(
    Effect.map((r) => ({ ...r, groups: r.groups ?? [] })),
  );
};
