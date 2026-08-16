import { apiGetE, apiPostE } from "@wick-fe/common-api";
import { Effect } from "effect";
import type { MemoryReport, SeriesResponse } from "./types.js";

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
