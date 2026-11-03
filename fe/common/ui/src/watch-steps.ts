/* Shared by the conversation Schedule panel and the global Scheduled page:
   what type a schedule is, the type filter both lists offer, and one line per
   watch step. */

export type ScheduleType = "message" | "watch";

/* Rows written before watches existed carry no type: they are messages. */
export const scheduleType = (s: { type?: string }): ScheduleType => (s.type === "watch" ? "watch" : "message");

export type ScheduleTypeFilter = "all" | ScheduleType;

/* A Message wakes the agent every fire; a Watch polls without it. */
export const SCHEDULE_TYPE_FILTERS: { id: ScheduleTypeFilter; label: string }[] = [
  { id: "all", label: "All" },
  { id: "message", label: "Message" },
  { id: "watch", label: "Watch" },
];

export const matchesScheduleType = (s: { type?: string }, f: ScheduleTypeFilter): boolean =>
  f === "all" || scheduleType(s) === f;

export type WatchRule = { path: string; op: string; value?: unknown };

export type WatchStep = {
  name?: string;
  kind: "connector" | "bash" | "check" | "code";
  tool_id?: string;
  params?: Record<string, unknown>;
  script?: string;
  /* kind=code: inline TS/JS xor a project-relative script_path. */
  code?: string;
  script_path?: string;
  lang?: "ts" | "js";
  timeout_sec?: number;
  match?: "all" | "any";
  rules?: WatchRule[];
  fail_rules?: WatchRule[];
  extract?: Record<string, string>;
  on_ok?: "next" | "done";
  on_fail?: "done" | "pending";
};

/** A rule as people read it: `state.name in [COMPLETED]`. */
export function ruleText(r: WatchRule): string {
  if (r.op === "exists" || r.op === "not_exists") return `${r.path} ${r.op}`;
  const v = Array.isArray(r.value) ? `[${r.value.map(String).join(", ")}]` : String(r.value);
  return `${r.path} ${r.op} ${v}`;
}

const isComment = (l: string) => l.startsWith("//") || l.startsWith("/*") || l.startsWith("*");
const clip = (l: string) => (l.length > 80 ? l.slice(0, 80) + "…" : l);

/** One line describing what a step does. */
export function stepSummary(st: WatchStep): string {
  switch (st.kind) {
    case "connector":
      return st.tool_id ?? "";
    case "bash":
      return clip((st.script ?? "").split("\n")[0]);
    case "check": {
      const join = st.match === "any" ? " OR " : " AND ";
      const parts = [(st.rules ?? []).map(ruleText).join(join)];
      if (st.fail_rules?.length) parts.push("fail if " + st.fail_rules.map(ruleText).join(" OR "));
      return parts.filter(Boolean).join(" · ");
    }
    case "code": {
      const lang = st.lang || "ts";
      if (st.script_path) return `${lang} · ${st.script_path}`;
      const first = (st.code ?? "").split("\n").map((l) => l.trim()).find((l) => l && !isComment(l)) ?? "";
      return `${lang} · ${clip(first)}`;
    }
    default:
      return "";
  }
}

export const STEP_KIND_ICON: Record<string, string> = { connector: "🔌", bash: "⌨", check: "✓", code: "{}" };
