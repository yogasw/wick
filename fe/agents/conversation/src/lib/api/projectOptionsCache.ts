import { Effect, Either, Runtime } from "effect";
import type { ProjectOption } from "../types/agents.js";

/** One /projects/options row as the server sends it. */
export type ProjectOptionRow = ProjectOption & {
  default_provider?: string;
  default_model?: string;
  ticket_enabled?: boolean;
};

/* One page asks for /projects/options from several places at once (list,
   chat, ticket board, agent settings), so answers are shared: keyed by the
   full URL, one request in flight per key, and served stale-while-revalidate
   — a fresh entry is returned as is, an older one is returned at once while
   a background request refreshes it. invalidateProjectOptions drops it all;
   call it after anything that adds, renames, deletes or pins a project. */
export const PROJECT_OPTIONS_TTL_MS = 60_000;

type ProjectOptionsEntry = {
  at: number;
  rows?: ProjectOptionRow[];
  inflight?: Promise<Either.Either<ProjectOptionRow[], unknown>>;
};

const projectOptionsCache = new Map<string, ProjectOptionsEntry>();

export const invalidateProjectOptions = () => projectOptionsCache.clear();

export const cachedProjectRows = <E, R>(url: string, load: Effect.Effect<ProjectOptionRow[] | null, E, R>) =>
  Effect.gen(function* () {
    const rt = yield* Effect.runtime<R>();
    let entry = projectOptionsCache.get(url);
    if (!entry) {
      entry = { at: 0 };
      projectOptionsCache.set(url, entry);
    }
    const e = entry;
    const refresh = () => {
      if (!e.inflight) {
        e.inflight = Runtime.runPromise(rt)(Effect.either(load)).then((res) => {
          e.inflight = undefined;
          // An entry dropped by invalidation while this was in flight stays
          // dropped: its answer may predate the change.
          if (Either.isRight(res) && projectOptionsCache.get(url) === e) {
            e.rows = res.right ?? [];
            e.at = Date.now();
          }
          return Either.map(res, (r) => r ?? []);
        }, (defect: unknown) => {
          e.inflight = undefined;
          throw defect;
        });
      }
      return e.inflight;
    };
    if (e.rows) {
      if (Date.now() - e.at >= PROJECT_OPTIONS_TTL_MS) void refresh();
      return e.rows;
    }
    const res = yield* Effect.promise(refresh);
    return yield* Either.match(res as Either.Either<ProjectOptionRow[], E>, {
      onLeft: (err) => Effect.fail(err),
      onRight: (rows) => Effect.succeed(rows),
    });
  });
