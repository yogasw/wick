/* What DetailView needs to know when it is hosted by the Agents app
   instead of the /sessions pages. Lives outside the component so the
   app shell and the tests can name the same RailTab ids DetailView uses. */

export type RailTab =
  | "files"
  | "process"
  | "workspace"
  | "scheduled"
  | "browser"
  | "source"
  | "subagents"
  | "ticket"
  | "notes"
  | "todos";

export type AgentMode = {
  /** Rail tabs the agent's features turn off. A switched-off feature
      hides its tab outright instead of showing a tab that fails on click. */
  hideTabs?: RailTab[];
  /** Called instead of DetailView's own push("/") after the session is
      deleted: in the Agents app that path is the /sessions list, i.e. it
      would throw the user out of the app. */
  onDeleted?: () => void;
  /** The host draws its own header (agent identity, not session title and
      provider), so DetailView skips ConversationHeader. Without that
      header there is no tab strip, so the view stays on the conversation. */
  hideHeader?: boolean;
  /** Drop the project and provider pickers from the composer: an agent's
      chat lives in the agent's project, and moving it elsewhere from here
      would detach it from the agent. */
  hidePickers?: boolean;
  /** Who the chat is with. Turns on the agent-flavoured empty state, the
      "Message {name}…" placeholder, the composer caption and the avatar
      as the typing indicator; absent, DetailView looks as on /sessions. */
  agent?: AgentIdentity;
  /** Told when this chat's turn starts or ends, off the same SSE stream
      that drives the typing bubble, so the host's "typing" cue follows the
      turn instead of waiting for its next roster poll. */
  onTurnChange?: (active: boolean) => void;
  /** The agent's "Allow provider switch in chat" setting. Off, the
      composer shows the provider read-only; on, the picker works but the
      provider itself is fixed once the chat has started. */
  providerSwitch?: boolean;
  /** Opens the agent's Settings (the locked-provider modal's button). */
  onOpenSettings?: () => void;
  /** Starts a new chat with the agent (the started-chat modal's button). */
  onNewChat?: () => void;
};

/** providerLocked says whether picking `next` must be refused: the chat
    has started and `next` names another provider. A model change within
    the same provider is fine. Values are "type/name[::model]"; a started
    chat on the wick default ("") counts as having a provider. */
export function providerLocked(started: boolean, current: string, next: string): boolean {
  if (!started) return false;
  const prov = (v: string) => {
    const i = v.indexOf("::");
    const p = i < 0 ? v : v.slice(0, i);
    return p.includes("/") ? p : `${p}/${p}`;
  };
  return prov(current) !== prov(next);
}

/** What the chat area shows of the agent it talks to. */
export type AgentIdentity = {
  /** The Team agent's id and handle: the `@` menu leaves the agent itself
      out of its own Team list. Unset outside the Team app. */
  id?: string;
  handle?: string;
  name: string;
  /** Short label shown beside the name ("The Critic"); unset = none. */
  tagline?: string;
  description: string;
  /** Avatar spec; unset falls back to AgentAvatar's defaults. */
  shape?: string;
  color?: string;
  /** Right-hand composer caption, e.g. "3 connector". */
  caption: string;
};

/** Persona feature flags as the server sends them (snake_case JSON). */
export type AgentFeatures = {
  source: boolean;
  schedule: boolean;
  browser: boolean;
  subagents: boolean;
  tickets: boolean;
  notes: boolean;
  todos: boolean;
  files: boolean;
  workspace: boolean;
  process: boolean;
};

/** Feature flag → the rail tab it gates. The names differ in two places
    (schedule/scheduled, tickets/ticket) because the flags are the
    persona's vocabulary and the tabs are DetailView's. Todos and Workspace
    are not here on purpose: they are always shown (plan §6.0b), so an old
    todos/workspace flag set to false is ignored. Files and Process stay
    flags until native tools land. */
export const FEATURE_TABS: { feature: keyof AgentFeatures; tab: RailTab; label: string; hint?: string }[] = [
  { feature: "source", tab: "source", label: "Source panel (git)" },
  { feature: "schedule", tab: "scheduled", label: "Routines / Schedule" },
  { feature: "browser", tab: "browser", label: "Browser", hint: "needs the Playwright connector checked" },
  { feature: "subagents", tab: "subagents", label: "Sub-agents / delegation" },
  { feature: "notes", tab: "notes", label: "Notes" },
  { feature: "tickets", tab: "ticket", label: "Tickets" },
  { feature: "files", tab: "files", label: "Files" },
  { feature: "process", tab: "process", label: "Process" },
];

/** Every rail tab with the name the Tools & features hint uses, in rail order. */
const RAIL_LABELS: { tab: RailTab; label: string }[] = [
  { tab: "source", label: "Source" },
  { tab: "scheduled", label: "Routines" },
  { tab: "files", label: "Files" },
  { tab: "process", label: "Process" },
  { tab: "browser", label: "Browser" },
  { tab: "subagents", label: "Sub-agents" },
  { tab: "notes", label: "Notes" },
  { tab: "ticket", label: "Ticket" },
  { tab: "workspace", label: "Workspace" },
  { tab: "todos", label: "Todos" },
];

/** railShownNote is the hint under the feature switches: which rail tabs
    the agent gets with these flags ("rail shows: Source, Files, …"). */
export function railShownNote(f: Partial<AgentFeatures> | null | undefined): string {
  const hidden = hiddenTabsFor(f);
  const shown = RAIL_LABELS.filter((r) => !hidden.includes(r.tab)).map((r) => r.label);
  return `rail shows: ${shown.join(", ")}`;
}

/** hiddenTabsFor lists the rail tabs to hide for a feature set. A missing
    features object hides nothing — an agent loaded before the server sent
    flags should look like a normal session, not an empty rail. */
export function hiddenTabsFor(f: Partial<AgentFeatures> | null | undefined): RailTab[] {
  if (!f) return [];
  return FEATURE_TABS.filter((m) => f[m.feature] === false).map((m) => m.tab);
}

/** hiddenTabNote is the rail footer that says why tabs are missing: a
    tab gone because the agent may not use it should not read as broken.
    Empty when nothing is hidden. Duplicates count once. */
export function hiddenTabNote(hidden: RailTab[] | null | undefined): string {
  const n = new Set(hidden ?? []).size;
  return n > 0 ? `${n} tab${n === 1 ? "" : "s"} hidden — feature not allowed` : "";
}

/** composerPlaceholder addresses the agent by name; a nameless one (still
    loading) falls back to a neutral word rather than "Message …". */
export function composerPlaceholder(name: string | null | undefined): string {
  const n = (name ?? "").trim();
  return `Message ${n || "agent"}…`;
}

/** connectorCaption counts the connectors the agent is granted, one per
    connector however many accounts the grant covers. Native tools are not
    a persona field yet, so they are not counted here. */
export function connectorCaption(grants: { connector_id: string }[] | null | undefined): string {
  const n = new Set((grants ?? []).map((g) => g.connector_id)).size;
  return `${n} connector${n === 1 ? "" : "s"}`;
}
