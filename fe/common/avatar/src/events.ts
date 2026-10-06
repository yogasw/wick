/* What happens to an agent decides how its avatar looks: a tool that reads
   gets a curious face, one that writes an attentive one, a failed tool a
   sad one, and so on. Deterministic, read off things that already happen
   (the tool the turn waits on, ask_user, a remote's wait), no AI.

   Every pose is temporary: the moment the event is over the avatar goes
   back to the expression the owner picked. The owner can change or turn
   off any row per agent (Settings › Avatar, Avatar.events); a row that is
   off, an unknown tool or an event the UI has never heard of falls back
   to the plain cues every avatar had before: orbit while a tool runs,
   thinking while a turn has none, alert / notify / idle otherwise. */
import { EXPRESSIONS, STATES, type BlobExpression, type BlobState } from "./blob/core/types";
import type { AvatarState } from "./shape.js";

/** The events with a row in the table, in the order Settings lists them. */
export const AVATAR_EVENTS = ["read", "run", "write", "delegate", "ask_user", "remote_wait", "error", "done", "compact"] as const;
export type AvatarEvent = (typeof AVATAR_EVENTS)[number];

export const AVATAR_EVENT_LABELS: Record<AvatarEvent, string> = {
  read: "Reading / searching",
  run: "Running a command / build",
  write: "Writing / editing",
  delegate: "Delegating to a sub-agent",
  ask_user: "Waiting for you",
  remote_wait: "Waiting on a remote agent",
  error: "A tool failed",
  done: "Turn finished",
  compact: "Compacting context",
};

/** A row's pose. No expression = the agent's own (base) expression. */
export type EventPose = { state: BlobState; expression?: BlobExpression };

/** One row of an agent's override (team.Avatar.events): a different pose,
    or off. Fields left out keep the default. */
export type EventOverride = { state?: string; expression?: string; off?: boolean };
export type EventOverrides = Partial<Record<string, EventOverride>>;

export const DEFAULT_EVENT_POSES: Record<AvatarEvent, EventPose> = {
  read: { state: "wide", expression: "curious" },
  run: { state: "thinking" },
  write: { state: "orbit", expression: "attentive" },
  delegate: { state: "orbit" },
  ask_user: { state: "alert" },
  remote_wait: { state: "wide", expression: "attentive" },
  error: { state: "idle", expression: "sad" },
  done: { state: "notify", expression: "happy" },
  compact: { state: "swirl" },
};

export const isAvatarEvent = (e: string | null | undefined): e is AvatarEvent => AVATAR_EVENTS.includes(e as AvatarEvent);

/* Tool names that say what they do without a verb guess. Matched on the
   bare name (no mcp__<server>__ prefix), lowercased. */
const KNOWN_TOOLS: Record<string, AvatarEvent> = {
  read: "read", grep: "read", glob: "read", ls: "read", webfetch: "read", websearch: "read", notebookread: "read",
  read_file: "read", wick_list: "read", wick_search: "read", wick_get: "read", wick_info: "read", wick_agents: "read",
  bash: "run", bashoutput: "run", shell: "run", monitor: "run", killshell: "run",
  edit: "write", write: "write", multiedit: "write", notebookedit: "write", write_file: "write", edit_file: "write", apply_patch: "write",
  task: "delegate", agent: "delegate", sendmessage: "delegate", wick_delegate: "delegate", delegate: "delegate", wick_agent_delegate: "delegate", wick_agent_message: "delegate", spawn_agent: "delegate",
  ask_user: "ask_user", askuserquestion: "ask_user", request_user_input: "ask_user",
  wick_compact: "compact", compact: "compact",
};

/* The fallback's verb guess: the first word of the name that is one of
   these (an MCP connector op is named by its verb: query_range,
   send_message). */
const VERBS: [AvatarEvent, string[]][] = [
  ["ask_user", ["ask"]],
  ["delegate", ["delegate", "spawn"]],
  ["compact", ["compact"]],
  ["read", ["read", "get", "list", "search", "fetch", "find", "grep", "glob", "query", "show", "view", "lookup", "load", "describe", "inspect", "count", "check", "download", "browse", "info"]],
  ["run", ["run", "exec", "execute", "build", "test", "bash", "shell", "deploy", "start", "restart", "invoke", "compile", "lint"]],
  ["write", ["write", "edit", "create", "update", "delete", "remove", "send", "post", "put", "patch", "set", "add", "upload", "commit", "push", "insert", "append", "reply", "save", "move", "rename", "merge", "replace", "publish"]],
];

/** toolWords splits a tool name into lowercase words: snake, kebab, dots,
    slashes and camelCase all break. */
export function toolWords(name: string): string[] {
  return name
    .replace(/([a-z0-9])([A-Z])/g, "$1 $2")
    .toLowerCase()
    .split(/[^a-z0-9]+/)
    .filter(Boolean);
}

/** toolEvent names the event of a tool call: a known tool's own, else the
    first verb in its name, else null (a generic tool). */
export function toolEvent(toolName: string | null | undefined): AvatarEvent | null {
  let bare = (toolName ?? "").trim();
  if (!bare) return null;
  const mcp = bare.lastIndexOf("__");
  if (bare.startsWith("mcp__") && mcp > 3) bare = bare.slice(mcp + 2);
  const known = KNOWN_TOOLS[bare.toLowerCase()];
  if (known) return known;
  for (const w of toolWords(bare)) {
    for (const [ev, verbs] of VERBS) if (verbs.includes(w)) return ev;
  }
  return null;
}

/** What the avatar knows about its agent right now. */
export type PoseInput = {
  disabled?: boolean;
  hatching?: boolean;
  /** Waiting on the owner: ask_user or an approval. */
  attention?: boolean;
  working?: boolean;
  /** The tool the running turn waits on (a name, "" = none). */
  toolName?: string;
  /** A tool is running but its name is unknown here. */
  tool?: boolean;
  /** The newest tool of the running turn failed. */
  toolError?: boolean;
  /** A remote agent: the turn waits on the other side. */
  remote?: boolean;
  /** The turn just ended (a few seconds). */
  done?: boolean;
  /** Something new to read. */
  notify?: boolean;
  overrides?: EventOverrides | null;
};

/** eventOf picks the event that wins, by priority: needs-attention, a
    failed tool, the tool in flight (or a remote's wait), then a finished
    turn. A disabled or hatching agent has none. */
export function eventOf(i: PoseInput): AvatarEvent | null {
  if (i.disabled || i.hatching) return null;
  if (i.attention) return "ask_user";
  if (i.working) {
    if (i.toolError) return "error";
    const t = toolEvent(i.toolName);
    if (t) return t;
    if (i.remote) return "remote_wait";
    return null;
  }
  return i.done ? "done" : null;
}

/** poseFor is a row's pose with the agent's override folded in; null when
    the row is off. An override naming an unknown state or expression keeps
    the default for that field. */
export function poseFor(ev: AvatarEvent, overrides?: EventOverrides | null): EventPose | null {
  const d = DEFAULT_EVENT_POSES[ev];
  const o = overrides?.[ev];
  if (!o) return d;
  if (o.off) return null;
  const state = STATES.includes(o.state as BlobState) ? (o.state as BlobState) : d.state;
  const expression = o.expression === "" ? undefined : EXPRESSIONS.includes(o.expression as BlobExpression) ? (o.expression as BlobExpression) : d.expression;
  return expression ? { state, expression } : { state };
}

/** fallbackState is the pose with no table row to go by: the cues every
    avatar had before events. */
export function fallbackState(i: PoseInput): AvatarState {
  if (i.hatching) return "egg";
  if (i.disabled) return "sleep";
  if (i.attention) return "alert";
  if (i.working) return i.tool || !!(i.toolName ?? "").trim() || i.remote ? "orbit" : "thinking";
  if (i.done || i.notify) return "notify";
  return "idle";
}

export type ResolvedPose = {
  /** The blob motion. */
  state: BlobState;
  /** The classic avatar's state (it has no wide / swirl / …). */
  classic: AvatarState;
  /** Temporary expression; absent = the agent's own. */
  expression?: BlobExpression;
  /** The event behind it, null for the fallback. */
  event: AvatarEvent | null;
};

/* The blob states the classic avatar can also draw. */
const CLASSIC: Partial<Record<BlobState, AvatarState>> = { idle: "idle", thinking: "thinking", orbit: "orbit", alert: "alert", notify: "notify", sleep: "sleep" };

/** resolvePose: disabled > needs-attention > error > tool in flight >
    thinking > idle, each event through the agent's table, a row that is
    off through the fallback. */
export function resolvePose(i: PoseInput): ResolvedPose {
  const fb = fallbackState(i);
  const ev = eventOf(i);
  const pose = ev ? poseFor(ev, i.overrides) : null;
  if (!ev || !pose) return { state: fb === "egg" ? "idle" : fb, classic: fb, event: null };
  const classic = CLASSIC[pose.state] ?? fb;
  return pose.expression ? { state: pose.state, classic, expression: pose.expression, event: ev } : { state: pose.state, classic, event: ev };
}

/** cleanOverrides drops rows that say nothing and unknown events, so a
    table edited back to its defaults saves as no override at all. */
export function cleanOverrides(o: EventOverrides | null | undefined): Record<string, EventOverride> | undefined {
  const out: Record<string, EventOverride> = {};
  for (const ev of AVATAR_EVENTS) {
    const r = o?.[ev];
    if (!r) continue;
    if (r.off) {
      out[ev] = { off: true };
      continue;
    }
    const d = DEFAULT_EVENT_POSES[ev];
    const row: EventOverride = {};
    if (r.state && r.state !== d.state && STATES.includes(r.state as BlobState)) row.state = r.state;
    if (r.expression !== undefined && r.expression !== (d.expression ?? "") && (r.expression === "" || EXPRESSIONS.includes(r.expression as BlobExpression))) row.expression = r.expression;
    if (row.state || row.expression !== undefined) out[ev] = row;
  }
  return Object.keys(out).length ? out : undefined;
}
