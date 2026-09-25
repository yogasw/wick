import { describe, test, expect } from "vitest";
import {
  collisionFinding,
  contaminationFinding,
  contaminationRows,
  doctorFindings,
  embeddingFinding,
  healthFindings,
  healthVerdict,
  levelClasses,
  zeroLLMFinding,
} from "../health.js";
import type { DoctorReport, StoreStatus } from "../types.js";

// DOCTOR is a real `ai-memory 2.4.0 doctor --json` response, captured on
// 2026-09-25 from a directory where both harnesses had run and neither hook
// was installed. Nothing in this fixture is invented.
const DOCTOR: DoctorReport = {
  workspace: "default",
  project: "files",
  since_days: 30,
  rows: [
    { agent: "claude-code", local_total: 451, local_recent: 451, captured: 0, uncaptured: true },
    { agent: "codex", local_total: 9, local_recent: 9, captured: 0, uncaptured: true },
  ],
  uncaptured: ["claude-code", "codex"],
};

const store = (llm: string, embeddings = 7, missing = 0): StoreStatus =>
  ({
    llm: { status: llm },
    embedding: { status: "ok" },
    index: { embedding_rows: embeddings, latest_pages_missing_embeddings: missing },
  }) as StoreStatus;

describe("doctorFindings", () => {
  // The whole reason this check exists: the harness works, the store looks
  // fine, and nothing it does is ever remembered (PLAN §7.1).
  test("a harness that ran here with nothing captured is critical and carries its command", () => {
    const f = doctorFindings(DOCTOR);
    expect(f).toHaveLength(2);
    expect(f[0].level).toBe("critical");
    expect(f[0].title).toBe("claude-code ran here but nothing was captured");
    expect(f[0].body).toContain("being lost as it happens");
    expect(f[0].fix?.command).toBe("ai-memory install-hooks --agent claude-code --apply");
    expect(f[1].fix?.command).toBe("ai-memory install-hooks --agent codex --apply");
  });

  test("partial capture is a warning pointing at backfill, not a hook problem", () => {
    const f = doctorFindings({ ...DOCTOR, rows: [{ agent: "codex", local_total: 9, local_recent: 9, captured: 4, uncaptured: false }] });
    expect(f[0].level).toBe("warn");
    expect(f[0].title).toContain("only partly captured");
    expect(f[0].fix?.where).toBe("Projects");
  });

  test("full capture passes and says what it compared", () => {
    const f = doctorFindings({ ...DOCTOR, rows: [{ agent: "codex", local_total: 9, local_recent: 9, captured: 9, uncaptured: false }] });
    expect(f[0].level).toBe("ok");
    expect(f[0].body).toContain("the store holds 9");
  });

  // Nothing having run is not a clean bill of health and not a failure —
  // it is the check having nothing to say, which is its own sentence.
  test("no harness having run is info, not a pass and not an alarm", () => {
    const f = doctorFindings({ ...DOCTOR, rows: [] });
    expect(f[0].level).toBe("info");
    expect(f[0].body).toContain("last 30 days");
  });

  test("a failed check reports itself instead of disappearing", () => {
    const f = doctorFindings({ error: "project not found" });
    expect(f[0].level).toBe("warn");
    expect(f[0].body).toBe("project not found");
  });

  test("no report at all yields no findings", () => {
    expect(doctorFindings(undefined)).toEqual([]);
  });
});

describe("contaminationFinding", () => {
  test("a clean audit passes", () => {
    const f = contaminationFinding({ sessions_misbucketed: 0, findings: [] });
    expect(f.level).toBe("ok");
  });

  // This one is weighted harder here than upstream, and the body has to say
  // why: a wick session holds client data (PLAN §13.3), and the cause is the
  // folder-name scoping from §14.3.
  test("misbucketed sessions are critical, and the body says why it matters here", () => {
    const f = contaminationFinding({ sessions_misbucketed: 3 });
    expect(f.level).toBe("critical");
    expect(f.title).toContain("3 sessions are filed under the wrong project");
    expect(f.body).toContain("client data");
    expect(f.body).toContain("FOLDER NAME");
    expect(f.fix?.command).toBe("ai-memory reorg");
  });

  test("an audit that could not run is a warning, not a clean result", () => {
    expect(contaminationFinding({ sessions_misbucketed: 0, error: "db locked" }).level).toBe("warn");
  });
});

describe("contaminationRows", () => {
  // The finding row shape is unverified — the only run available to read off
  // was empty — so the table renders whatever keys arrive rather than
  // dropping the rows that matter most.
  test("renders whatever keys arrive, stringifying nested values", () => {
    expect(
      contaminationRows({
        sessions_misbucketed: 1,
        findings: [{ session_id: "abc", cwd: null, meta: { a: 1 } }],
      }),
    ).toEqual([{ session_id: "abc", cwd: "—", meta: '{"a":1}' }]);
  });

  test("a missing or malformed findings list is empty, not a crash", () => {
    expect(contaminationRows(undefined)).toEqual([]);
    expect(contaminationRows({ sessions_misbucketed: 0, findings: null })).toEqual([]);
  });
});

describe("zeroLLMFinding", () => {
  // "Zero-LLM mode" says nothing to anyone. What it MEANS is that a stored
  // fact is cut at about 80 characters with no marker, so long identifiers
  // vanish silently and a later answer can be a guess at the rest (PLAN §9).
  test("names the real consequence: truncation and lost identifiers", () => {
    const f = zeroLLMFinding(store("disabled"));
    expect(f?.level).toBe("warn");
    expect(f?.body).toContain("80 characters");
    expect(f?.body).toContain("nothing marking that it was cut");
    expect(f?.body).toContain("app id");
    expect(f?.body).toContain("plausible guess");
    expect(f?.fix?.where).toContain("Settings");
  });

  // A provider stuck in an error state is not healthier than a disabled one —
  // the facts are truncated either way.
  test("a failing provider warns the same way a disabled one does", () => {
    const st = store("error");
    st.llm.last_error_message = "connection refused";
    const f = zeroLLMFinding(st);
    expect(f?.title).toContain("not usable");
    expect(f?.body).toContain("connection refused");
  });

  test("a working provider produces no finding", () => {
    expect(zeroLLMFinding(store("ok"))).toBeNull();
    expect(zeroLLMFinding(undefined)).toBeNull();
  });
});

describe("embeddingFinding", () => {
  test("zero embeddings explains the whole-token fallback with its example", () => {
    const f = embeddingFinding(store("ok", 0));
    expect(f?.body).toContain("kasir_prod_db");
    expect(f?.fix?.command).toBe("ai-memory embed");
  });

  test("partly embedded pages are reported as silently skipped", () => {
    expect(embeddingFinding(store("ok", 7, 4))?.title).toContain("4 latest pages have no embedding");
  });

  test("fully embedded produces no finding", () => {
    expect(embeddingFinding(store("ok"))).toBeNull();
  });
});

describe("healthFindings", () => {
  // Ordering is by consequence, not by source: a harness capturing nothing
  // and client data in the wrong project both cost more than a missing index.
  test("the things that need acting on sort to the top", () => {
    const all = healthFindings(DOCTOR, { sessions_misbucketed: 0, findings: [] }, store("disabled", 0));
    expect(all[0].level).toBe("critical");
    expect(all.at(-1)?.level).toBe("ok");
    expect(all.map((f) => f.id)).toContain("zero-llm");
    expect(all.map((f) => f.id)).toContain("no-embeddings");
  });

  test("the verdict counts only what needs attention, and never over-claims", () => {
    expect(healthVerdict([])).toBe("Nothing has been checked yet.");
    const clean = healthFindings(
      { ...DOCTOR, rows: [{ agent: "codex", local_total: 1, local_recent: 1, captured: 1, uncaptured: false }] },
      { sessions_misbucketed: 0, findings: [] },
      store("ok"),
    );
    expect(healthVerdict(clean)).toBe("Nothing needs attention — every check passed.");
    expect(healthVerdict(healthFindings(DOCTOR, { sessions_misbucketed: 2 }, store("ok")))).toBe(
      "3 of 3 checks need attention.",
    );
  });
});

describe("levelClasses", () => {
  // Wick palette only, and every light class paired with its dark: partner on
  // the same line (PLAN §17.3).
  test("every class is from the wick palette and carries a dark pair", () => {
    for (const level of ["critical", "warn", "ok", "info"] as const) {
      const c = levelClasses(level);
      for (const cls of [c.border, c.title, c.chip]) {
        expect(cls).not.toMatch(/#|slate-|gray-/);
        expect(cls).toMatch(/dark:/);
      }
    }
  });

  test("critical and warn share one visual weight — act, versus read", () => {
    expect(levelClasses("critical")).toEqual(levelClasses("warn"));
    expect(levelClasses("ok")).not.toEqual(levelClasses("info"));
  });
});

describe("collisionFinding", () => {
  // The whole point of the check: this failure has no other symptom. Both
  // projects work, the store looks healthy, and one client's agent recalls
  // another client's work.
  test("a collision is critical and names both projects and the scope they share", () => {
    const f = collisionFinding({
      checked: true,
      scanned: 4,
      collisions: [
        {
          workspace: "wick",
          project: "backend",
          fixable: true,
          members: [
            { id: "a", name: "Client A backend", folder: "/srv/a/backend", has_marker: false },
            { id: "b", name: "Client B backend", folder: "/srv/b/backend", has_marker: false },
          ],
        },
      ],
    });
    expect(f?.level).toBe("critical");
    expect(f?.body).toContain("Client A backend");
    expect(f?.body).toContain("Client B backend");
    expect(f?.body).toContain("wick/backend");
    expect(f?.body).toMatch(/different client/i);
    expect(f?.fix?.where).toMatch(/\.ai-memory\.toml/);
  });

  // Two markers agreeing is a human decision. wick must not offer to
  // overwrite a scope somebody pinned on purpose.
  test("an all-marked collision is reported without offering to write a marker", () => {
    const f = collisionFinding({
      checked: true,
      scanned: 2,
      collisions: [
        {
          workspace: "wick",
          project: "shared",
          fixable: false,
          members: [
            { id: "a", name: "A", folder: "/srv/a", has_marker: true },
            { id: "b", name: "B", folder: "/srv/b", has_marker: true },
          ],
        },
      ],
    });
    expect(f?.level).toBe("critical");
    expect(f?.fix?.where).toBeUndefined();
    expect(f?.fix?.label).toMatch(/nothing here is accidental/i);
  });

  test("a clean check says how many projects it compared", () => {
    const f = collisionFinding({ checked: true, scanned: 7, collisions: [] });
    expect(f?.level).toBe("ok");
    expect(f?.body).toContain("7 projects");
  });

  // "Not checked" is not "no collisions", and the wording has to keep them
  // apart — an unanswered question rendered as a pass is the worst outcome.
  test("an unchecked result is info, not ok, and says it is not a clean result", () => {
    const f = collisionFinding({ checked: false, scanned: 0, reason: "wick could not read its own project list." });
    expect(f?.level).toBe("info");
    expect(f?.level).not.toBe("ok");
    expect(f?.title).toMatch(/not checked/i);
  });

  test("no check at all yields no finding rather than a fabricated one", () => {
    expect(collisionFinding(undefined)).toBeNull();
  });
});
