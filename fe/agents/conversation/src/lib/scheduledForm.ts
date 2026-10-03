/* Pure helpers of the Scheduled drawer: how a schedule reads to a person,
   and the form ↔ API body mapping. Kept out of the component so they are
   testable without rendering. */
import type { AgentSchedule, AgentScheduleRun, AgentScheduleWrite, AgentTelegramChat } from "./api/team.js";

export type WhenMode = "once" | "every" | "cron";
export type EveryUnit = "m" | "h" | "d";
/** Where the result lands; "other" is a chat the drawer can't pick (a
    schedule the agent made elsewhere) and is left as it is. */
export type DraftDest = "main" | "telegram" | "other";
export type ScheduleDraft = {
  message: string;
  dest: DraftDest;
  /** The Telegram chat of a "telegram" destination. */
  tgSession: string;
  mode: WhenMode;
  /** datetime-local value (server tz is shown beside it). */
  at: string;
  every: number;
  unit: EveryUnit;
  cron: string;
};

const UNIT_WORD: Record<EveryUnit, string> = { m: "minute", h: "hour", d: "day" };
const DAY_NAMES = ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"];

const pad = (n: number) => String(n).padStart(2, "0");

/** everyLabel: 3_600_000 → "Every hour", 5_400_000 → "Every 90 minutes". */
export function everyLabel(ms: number): string {
  const min = Math.round(ms / 60_000);
  const [n, unit]: [number, EveryUnit] =
    min % 1440 === 0 ? [min / 1440, "d"] : min % 60 === 0 ? [min / 60, "h"] : [min, "m"];
  return n === 1 ? `Every ${UNIT_WORD[unit]}` : `Every ${n} ${UNIT_WORD[unit]}s`;
}

/** cronLabel reads the common 5-field shapes in words; anything else is
    shown as the expression itself. */
export function cronLabel(expr: string, tz = ""): string {
  const f = expr.trim().split(/\s+/);
  const zone = tz ? ` (${tz})` : "";
  if (f.length !== 5) return `Cron ${expr}${zone}`;
  const [mi, h, dom, mon, dow] = f;
  const fixed = /^\d+$/.test(mi) && /^\d+$/.test(h);
  const time = fixed ? `${pad(+h)}:${pad(+mi)}` : "";
  if (fixed && dom === "*" && mon === "*") {
    if (dow === "*") return `Every day ${time}${zone}`;
    if (dow === "1-5") return `Weekdays ${time}${zone}`;
    if (/^[0-6]$/.test(dow)) return `Every ${DAY_NAMES[+dow]} ${time}${zone}`;
  }
  if (/^\d+$/.test(mi) && h === "*" && dom === "*" && mon === "*" && dow === "*") return `Every hour at :${pad(+mi)}${zone}`;
  return `Cron ${expr}${zone}`;
}

/** whenLabel is the row's "how often" line. */
export function whenLabel(s: Pick<AgentSchedule, "kind" | "interval_ms" | "cron" | "cron_timezone" | "next_run_at">): string {
  if (s.kind === "recurring") {
    if (s.cron) return cronLabel(s.cron, s.cron_timezone);
    if (s.interval_ms) return everyLabel(s.interval_ms);
  }
  return s.next_run_at ? `Once at ${fmtTime(s.next_run_at)}` : "Once";
}

/** fmtTime: an RFC3339 instant in the viewer's locale, minute precision. */
export function fmtTime(iso: string | undefined): string {
  if (!iso) return "—";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" });
}

/** statusOf is the row's state chip. */
export function statusOf(s: Pick<AgentSchedule, "status" | "paused" | "held_by_agent" | "last_error">): { label: string; tone: "ok" | "muted" | "warn" | "error" } {
  if (s.held_by_agent) return { label: "Held — agent off", tone: "muted" };
  if (s.paused || s.status === "paused") return { label: "Paused", tone: "muted" };
  switch (s.status) {
    case "pending":
    case "active":
      return { label: "On", tone: "ok" };
    case "done":
      return { label: "Done", tone: "muted" };
    case "failed":
      return { label: "Failed", tone: "error" };
    default:
      return { label: s.status.charAt(0).toUpperCase() + s.status.slice(1), tone: "muted" };
  }
}

export const isLive = (s: Pick<AgentSchedule, "status">) => s.status === "pending" || s.status === "active" || s.status === "paused";

/** emptyDraft: every day by default, an hour from now for "once". */
export function emptyDraft(now = new Date()): ScheduleDraft {
  const at = new Date(now.getTime() + 3_600_000);
  return { message: "", dest: "main", tgSession: "", mode: "cron", at: localInput(at), every: 1, unit: "h", cron: "0 9 * * *" };
}

/** localInput formats a Date for <input type="datetime-local">. */
export function localInput(d: Date): string {
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

/** draftOf turns an existing schedule into the edit form. */
export function draftOf(s: AgentSchedule): ScheduleDraft {
  const d = emptyDraft();
  d.message = s.message;
  d.dest = s.destination === "main" || s.destination === "telegram" ? s.destination : "other";
  d.tgSession = s.telegram_session ?? "";
  if (s.kind === "recurring" && s.cron) return { ...d, mode: "cron", cron: s.cron };
  if (s.kind === "recurring" && s.interval_ms) {
    const min = Math.round(s.interval_ms / 60_000);
    if (min % 1440 === 0) return { ...d, mode: "every", every: min / 1440, unit: "d" };
    if (min % 60 === 0) return { ...d, mode: "every", every: min / 60, unit: "h" };
    return { ...d, mode: "every", every: min, unit: "m" };
  }
  return { ...d, mode: "once", at: s.next_run_at ? localInput(new Date(s.next_run_at)) : d.at };
}

/** draftError is why the form can't be sent yet, "" when it can. */
export function draftError(d: ScheduleDraft): string {
  if (!d.message.trim()) return "Write what the agent should do.";
  if (d.mode === "once" && !d.at) return "Pick a time.";
  if (d.mode === "every" && (!Number.isInteger(d.every) || d.every < 1)) return "Every needs a whole number of 1 or more.";
  if (d.mode === "cron" && d.cron.trim().split(/\s+/).length !== 5) return "Cron needs 5 fields: minute hour day month weekday.";
  if (d.dest === "telegram" && !d.tgSession) return "Pick a Telegram chat.";
  return "";
}

/** bodyOf is the API body of a draft. */
export function bodyOf(d: ScheduleDraft): AgentScheduleWrite {
  const b: AgentScheduleWrite = { message: d.message.trim() };
  if (d.mode === "once") b.run_at = new Date(d.at).toISOString();
  else if (d.mode === "every") b.every = `${d.every}${d.unit}`;
  else b.cron = d.cron.trim();
  if (d.dest === "main") b.destination = "main";
  else if (d.dest === "telegram") {
    b.destination = "telegram";
    b.telegram_session = d.tgSession;
  }
  return b;
}

/** destLabel is the row's "where it lands" in words. */
export function destLabel(s: Pick<AgentSchedule, "destination" | "telegram_session">, chats: AgentTelegramChat[] = []): string {
  switch (s.destination) {
    case "main":
      return "Main chat";
    case "telegram": {
      const chat = chats.find((c) => c.session_id === s.telegram_session);
      return chat ? `Telegram · ${chat.title}` : "Telegram";
    }
    case "new_chat":
      return "New chat each run";
    default:
      return "Another chat";
  }
}

/** runStatus is a history row's chip. */
export function runStatus(r: Pick<AgentScheduleRun, "status">): { label: string; tone: "ok" | "muted" | "error" } {
  if (r.status === "ok") return { label: "OK", tone: "ok" };
  if (r.status === "failed") return { label: "Failed", tone: "error" };
  return { label: "Running", tone: "muted" };
}

/** kindOf: a draft's mode as the schedule kind it creates. The server
    can't change a schedule's kind, so the edit form keeps to it. */
export const kindOf = (mode: WhenMode): AgentSchedule["kind"] => (mode === "once" ? "once" : "recurring");
