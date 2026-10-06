/* aigen.ts — client for the queued generate-job API (/api/ai-gen).
 *
 * A generate call never runs inside the request that asked for it: the
 * server queues it behind the agent pool and hands back a job id. AIGenRun
 * wraps the submit → poll → done/failed loop so every "✨ Generate" button
 * shows the same states (Queued · #2 / Writing… / error + retry) and can
 * cancel. Polling, not a stream: a job lives for seconds and a poll a
 * second is one small GET. */

export type AIGenStatus = "queued" | "working" | "done" | "failed" | "canceled";

export type AIGenInput = {
  /** The brief or paste the kind works from. */
  text: string;
  /** Provider instance name; omit for the operator's default. */
  provider?: string;
  /** Kind-specific context, e.g. the current value to improve. */
  fields?: Record<string, string>;
};

export type AIGenJob<T = unknown> = {
  id: string;
  kind: string;
  status: AIGenStatus;
  /** 1-based place in line while queued. */
  position?: number;
  provider?: string;
  result?: T;
  error?: string;
};

export const AIGEN_ENDPOINT = "/api/ai-gen";

async function call<T>(method: "GET" | "POST", path: string, body?: unknown): Promise<T> {
  const res = await fetch(path, {
    method,
    credentials: "same-origin",
    headers: body === undefined
      ? { Accept: "application/json" }
      : { Accept: "application/json", "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const text = await res.text();
  let parsed: unknown = undefined;
  try { parsed = text ? JSON.parse(text) : undefined; } catch { /* not JSON */ }
  if (!res.ok) {
    const msg = (parsed as { error?: string } | undefined)?.error;
    throw new Error(msg || text || `generate: ${res.status}`);
  }
  return parsed as T;
}

export const submitAIGen = <T = unknown>(kind: string, input: AIGenInput) =>
  call<AIGenJob<T>>("POST", AIGEN_ENDPOINT, { kind, input });
export const getAIGen = <T = unknown>(id: string) =>
  call<AIGenJob<T>>("GET", `${AIGEN_ENDPOINT}/${encodeURIComponent(id)}`);
export const cancelAIGen = <T = unknown>(id: string) =>
  call<AIGenJob<T>>("POST", `${AIGEN_ENDPOINT}/${encodeURIComponent(id)}/cancel`);

export type AIGenPhase = "idle" | "queued" | "working" | "done" | "failed";

export type AIGenState<T = unknown> = {
  phase: AIGenPhase;
  /** Place in line while queued (0 otherwise). */
  position: number;
  result?: T;
  error: string;
  jobId: string;
};

export const idleAIGenState = <T = unknown>(): AIGenState<T> =>
  ({ phase: "idle", position: 0, error: "", jobId: "" });

/** The three calls AIGenRun makes; injectable so tests need no fetch. */
export type AIGenAPI<T> = {
  submit: (kind: string, input: AIGenInput) => Promise<AIGenJob<T>>;
  get: (id: string) => Promise<AIGenJob<T>>;
  cancel: (id: string) => Promise<AIGenJob<T>>;
};

export type AIGenRunOptions<T> = {
  kind: string;
  onChange: (s: AIGenState<T>) => void;
  pollMs?: number;
  api?: AIGenAPI<T>;
};

/** AIGenRun drives one job at a time for one button. start() while a job
 *  is in flight cancels the old one first; dispose() stops polling for an
 *  unmounted button (and cancels its job so it does not hold a slot). */
export class AIGenRun<T = unknown> {
  private state: AIGenState<T> = idleAIGenState<T>();
  private timer: ReturnType<typeof setTimeout> | undefined;
  private gen = 0; // bumps on every start/cancel so stale polls are dropped
  private readonly api: AIGenAPI<T>;
  private readonly pollMs: number;

  constructor(private readonly opts: AIGenRunOptions<T>) {
    this.api = opts.api ?? { submit: submitAIGen, get: getAIGen, cancel: cancelAIGen };
    this.pollMs = opts.pollMs ?? 1000;
  }

  get current(): AIGenState<T> { return this.state; }

  get busy(): boolean { return this.state.phase === "queued" || this.state.phase === "working"; }

  async start(input: AIGenInput): Promise<void> {
    if (this.busy) await this.cancel();
    const gen = ++this.gen;
    this.set({ ...idleAIGenState<T>(), phase: "queued", position: 0 });
    try {
      const job = await this.api.submit(this.opts.kind, input);
      if (gen !== this.gen) return;
      this.apply(job, gen);
    } catch (e) {
      if (gen !== this.gen) return;
      this.set({ ...idleAIGenState<T>(), phase: "failed", error: errText(e) });
    }
  }

  /** cancel stops the job (server-side too) and returns to idle. */
  async cancel(): Promise<void> {
    const id = this.state.jobId;
    const wasBusy = this.busy;
    this.gen++;
    this.clearTimer();
    this.set(idleAIGenState<T>());
    if (id && wasBusy) {
      try { await this.api.cancel(id); } catch { /* already gone */ }
    }
  }

  /** reset drops a finished result or error without touching the server. */
  reset(): void {
    this.gen++;
    this.clearTimer();
    this.set(idleAIGenState<T>());
  }

  dispose(): void {
    if (this.busy) void this.cancel();
    else this.reset();
  }

  private apply(job: AIGenJob<T>, gen: number): void {
    switch (job.status) {
      case "queued":
      case "working":
        this.set({ phase: job.status, position: job.position ?? 0, error: "", jobId: job.id });
        this.timer = setTimeout(() => this.poll(job.id, gen), this.pollMs);
        return;
      case "done":
        this.set({ phase: "done", position: 0, result: job.result, error: "", jobId: job.id });
        return;
      case "canceled":
        this.set(idleAIGenState<T>());
        return;
      default:
        this.set({ phase: "failed", position: 0, error: job.error || "Generate failed.", jobId: job.id });
    }
  }

  private async poll(id: string, gen: number): Promise<void> {
    this.timer = undefined;
    try {
      const job = await this.api.get(id);
      if (gen !== this.gen) return;
      this.apply(job, gen);
    } catch (e) {
      if (gen !== this.gen) return;
      this.set({ phase: "failed", position: 0, error: errText(e), jobId: id });
    }
  }

  private clearTimer(): void {
    if (this.timer !== undefined) clearTimeout(this.timer);
    this.timer = undefined;
  }

  private set(s: AIGenState<T>): void {
    this.state = s;
    this.opts.onChange(s);
  }
}

function errText(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}
