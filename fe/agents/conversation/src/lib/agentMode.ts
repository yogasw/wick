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
    persona's vocabulary and the tabs are DetailView's. */
export const FEATURE_TABS: { feature: keyof AgentFeatures; tab: RailTab; label: string }[] = [
  { feature: "source", tab: "source", label: "Source" },
  { feature: "files", tab: "files", label: "Files" },
  { feature: "process", tab: "process", label: "Process" },
  { feature: "workspace", tab: "workspace", label: "Workspace" },
  { feature: "schedule", tab: "scheduled", label: "Scheduled" },
  { feature: "browser", tab: "browser", label: "Browser" },
  { feature: "subagents", tab: "subagents", label: "Sub-agents" },
  { feature: "tickets", tab: "ticket", label: "Ticket" },
  { feature: "notes", tab: "notes", label: "Notes" },
  { feature: "todos", tab: "todos", label: "Todos" },
];

/** hiddenTabsFor lists the rail tabs to hide for a feature set. A missing
    features object hides nothing — an agent loaded before the server sent
    flags should look like a normal session, not an empty rail. */
export function hiddenTabsFor(f: Partial<AgentFeatures> | null | undefined): RailTab[] {
  if (!f) return [];
  return FEATURE_TABS.filter((m) => f[m.feature] === false).map((m) => m.tab);
}
