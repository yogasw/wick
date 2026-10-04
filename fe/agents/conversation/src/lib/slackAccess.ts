import { apiGetE, apiPatchE } from "@wick-fe/common-api";

/* Connections › Slack › settings groups (api_team_slack_access.go): the
   Access Control, Agent Behaviour, Reaction Auto-Reply and Approval Gates
   groups of the Channels page, rendered from the same config tags and saved
   one key at a time on the agent's own Slack connection. */
export type SlackSettingField = {
  key: string;
  value: string;
  type: string;
  options?: string;
  desc?: string;
  group: string;
  group_desc?: string;
  visible_when?: string;
};
export type AgentSlackSettings = { fields: SlackSettingField[]; owner_slack_id?: string; owner_slack_name?: string };
export type PickerItem = { id: string; name: string };

const enc = encodeURIComponent;
export const getAgentSlackSettings = (base: string, id: string) =>
  apiGetE<AgentSlackSettings>(`${base}/api/team/agents/${enc(id)}/slack/settings`);
export const setAgentSlackSetting = (base: string, id: string, key: string, value: string) =>
  apiPatchE<AgentSlackSettings>(`${base}/api/team/agents/${enc(id)}/slack/settings`, { key, value });
export const lookupAgentSlack = (base: string, id: string, source: string, q: string) =>
  apiGetE<{ items: PickerItem[] }>(`${base}/api/team/agents/${enc(id)}/slack/lookup?source=${enc(source)}&q=${enc(q)}`);

/** Groups in first-seen order, each with its description. */
export function settingGroups(fields: SlackSettingField[]): { title: string; desc: string; fields: SlackSettingField[] }[] {
  const out: { title: string; desc: string; fields: SlackSettingField[] }[] = [];
  for (const f of fields) {
    let g = out.find((x) => x.title === f.group);
    if (!g) out.push((g = { title: f.group, desc: f.group_desc ?? "", fields: [] }));
    g.fields.push(f);
  }
  return out;
}

/** Picker values are a JSON list of {id,name}; anything else reads as empty. */
export function pickerItems(raw: string | undefined): PickerItem[] {
  if (!raw) return [];
  try {
    const v = JSON.parse(raw);
    return Array.isArray(v) ? v.filter((x) => x && typeof x.id === "string").map((x) => ({ id: x.id, name: x.name || x.id })) : [];
  } catch {
    return [];
  }
}

/** visible_when "field:a|b" against the current values. */
export function isVisible(f: SlackSettingField, values: Record<string, string>): boolean {
  if (!f.visible_when) return true;
  const [k, want] = f.visible_when.split(":");
  return (want ?? "").split("|").includes(values[k] ?? "");
}

/** Friendlier labels for the access dropdowns; other options show as-is. */
export const OPTION_LABEL: Record<string, Record<string, string>> = {
  users_mode: { all: "Everyone", whitelist: "Specific people" },
  groups_mode: { all: "Any group", whitelist: "Specific groups" },
  channels_mode: { all: "Every channel the bot is in", whitelist: "Specific channels" },
  bots_mode: { none: "Ignore bots", whitelist: "Specific bots", all: "Any bot" },
};

export type PeopleChoice = "all" | "me" | "custom";

/** Which of the three "People" choices the stored values amount to. */
export function peopleChoice(values: Record<string, string>, ownerID?: string): PeopleChoice {
  const users = values.users_mode === "whitelist";
  const groups = values.groups_mode === "whitelist";
  if (!users && !groups) return "all";
  const ids = pickerItems(values.allowed_users).map((i) => i.id);
  if (users && !groups && ownerID && ids.length === 1 && ids[0] === ownerID) return "me";
  return "custom";
}

/** The values a People choice writes, in save order. */
export function peopleValues(choice: PeopleChoice, values: Record<string, string>, owner?: PickerItem): [string, string][] {
  switch (choice) {
    case "all":
      return [["users_mode", "all"], ["groups_mode", "all"]];
    case "me":
      return owner ? [["allowed_users", JSON.stringify([owner])], ["users_mode", "whitelist"], ["groups_mode", "all"]] : [];
    default:
      return values.users_mode === "whitelist" || values.groups_mode === "whitelist" ? [] : [["users_mode", "whitelist"]];
  }
}

/** Open = nobody is filtered: every person in the workspace may use it. */
export const isOpenToWorkspace = (values: Record<string, string>) => peopleChoice(values) === "all";

const plural = (n: number, one: string) => `${n} ${one}${n === 1 ? "" : "s"}`;

/** Header line of the Slack card, e.g. "Access: only Yoga · 2 channels · 1 bot". */
export function accessSummary(values: Record<string, string>, ownerID?: string, ownerName?: string): string {
  const parts: string[] = [];
  const choice = peopleChoice(values, ownerID);
  if (choice === "all") parts.push("everyone");
  else if (choice === "me") parts.push(`only ${ownerName || "you"}`);
  else {
    const n = (values.users_mode === "whitelist" ? pickerItems(values.allowed_users).length : 0) +
      (values.groups_mode === "whitelist" ? pickerItems(values.allowed_groups).length : 0);
    parts.push(n === 1 && values.users_mode === "whitelist" ? `only ${pickerItems(values.allowed_users)[0]?.name}` : `${n} people & groups`);
  }
  parts.push(values.channels_mode === "whitelist" ? plural(pickerItems(values.allowed_channels).length, "channel") : "all channels");
  if (values.bots_mode === "all") parts.push("any bot");
  else if (values.bots_mode === "whitelist") parts.push(plural(pickerItems(values.allowed_bots).length, "bot"));
  return "Access: " + parts.join(" · ");
}
