import { describe, test, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";
import type { AgentSlackSettings, SlackSettingField } from "../../../slackAccess.js";

const AC = "Access Control";
const ACD = "Who may trigger agents.";
/* The rest of SlackChannelConfig after Access Control, in schema order
   (internal/agents/config/slack.go), as the API sends it. */
const REST: SlackSettingField[] = [
  { key: "ask_user_enabled", value: "false", type: "bool", group: "Agent Behaviour" },
  { key: "hide_subagent_status", value: "false", type: "bool", group: "Agent Behaviour" },
  { key: "reaction_trigger_enabled", value: "false", type: "bool", group: "Reaction Auto-Reply" },
  { key: "reaction_channels_mode", value: "whitelist", type: "dropdown", options: "all|whitelist", visible_when: "reaction_trigger_enabled:true", group: "Reaction Auto-Reply" },
  { key: "reaction_channels", value: "", type: "picker", options: "slack.channels", visible_when: "reaction_channels_mode:whitelist", group: "Reaction Auto-Reply" },
  { key: "gate_approvers", value: "trigger_users", type: "dropdown", options: "trigger_users|admins|custom", group: "Approval Gates" },
  { key: "gate_approver_users", value: "", type: "picker", options: "slack.users", visible_when: "gate_approvers:custom", group: "Approval Gates" },
  { key: "gate_approver_groups", value: "", type: "picker", options: "slack.usergroups", visible_when: "gate_approvers:custom", group: "Approval Gates" },
  { key: "mode", value: "socket", type: "dropdown", group: "Connection", readonly: true, note: "Set by the connect wizard." },
  { key: "bot_token", value: "set", type: "text", group: "Connection", readonly: true, note: "Managed with Replace tokens on this card." },
  { key: "app_token", value: "set", type: "text", group: "Connection", readonly: true, note: "Managed with Replace tokens on this card." },
  { key: "signing_secret", value: "", type: "text", group: "Connection", readonly: true, note: "HTTP mode only." },
  { key: "project_id", value: "Ops", type: "dropdown", group: "Routing", readonly: true, note: "Sessions run in the agent's own project." },
  { key: "public_url", value: "https://wick.example.test", type: "text", group: "Routing", readonly: true, note: "Set globally in Channels › Slack › Routing." },
];
/* Every key of SlackChannelConfig, schema order — the Channels page fields. */
const SCHEMA_KEYS = [
  "mode", "bot_token", "app_token", "signing_secret",
  "users_mode", "allowed_users", "groups_mode", "allowed_groups", "bots_mode", "allowed_bots", "channels_mode", "allowed_channels",
  "ask_user_enabled", "hide_subagent_status",
  "reaction_trigger_enabled", "reaction_channels_mode", "reaction_channels",
  "gate_approvers", "gate_approver_users", "gate_approver_groups",
  "project_id", "public_url",
];
const f = (key: string, value: string, type: string, extra: Partial<SlackSettingField> = {}): SlackSettingField =>
  ({ key, value, type, group: AC, group_desc: ACD, ...extra });
const fields = (over: Record<string, string> = {}): SlackSettingField[] => [
  f("users_mode", over.users_mode ?? "all", "dropdown", { options: "all|whitelist" }),
  f("allowed_users", over.allowed_users ?? "", "picker", { options: "slack.users", visible_when: "users_mode:whitelist" }),
  f("groups_mode", over.groups_mode ?? "all", "dropdown", { options: "all|whitelist" }),
  f("allowed_groups", over.allowed_groups ?? "", "picker", { options: "slack.usergroups", visible_when: "groups_mode:whitelist" }),
  f("bots_mode", over.bots_mode ?? "none", "dropdown", { options: "none|whitelist|all" }),
  f("allowed_bots", over.allowed_bots ?? "", "picker", { options: "slack.bots", visible_when: "bots_mode:whitelist" }),
  f("channels_mode", over.channels_mode ?? "all", "dropdown", { options: "all|whitelist" }),
  f("allowed_channels", over.allowed_channels ?? "", "picker", { options: "slack.channels", visible_when: "channels_mode:whitelist" }),
  ...REST,
];
const ONLY_ME = JSON.stringify([{ id: "UOWNER", name: "Yoga" }]);
const LOOKUP: Record<string, { id: string; name: string }[]> = {
  "slack.bots": [{ id: "UBOT", name: "Deploy bot" }],
  "slack.users": [{ id: "UA", name: "User A" }],
  "slack.usergroups": [{ id: "SB", name: "Group B" }],
};
let current: AgentSlackSettings;
/* Saves like the API: the key is stored and the whole settings come back. */
const patch = vi.fn((k: string, v: string) => {
  current = { ...current, fields: current.fields.map((x) => (x.key === k ? { ...x, value: v } : x)) };
  return Promise.resolve(current);
});

vi.mock("../../../api/team.js", async (orig) => ({
  ...(await orig<typeof import("../../../api/team.js")>()),
  runApi: <T,>(p: Promise<T>) => p,
}));
vi.mock("../../../slackAccess.js", async (orig) => ({
  ...(await orig<typeof import("../../../slackAccess.js")>()),
  getAgentSlackSettings: () => Promise.resolve(current),
  setAgentSlackSetting: (_b: string, _id: string, k: string, v: string) => patch(k, v),
  lookupAgentSlack: (_b: string, _id: string, source: string) => Promise.resolve({ items: LOOKUP[source] ?? [] }),
}));

import SlackSettingsGroups from "../SlackSettingsGroups.svelte";
import { accessSummary } from "../../../slackAccess.js";
import type { AgentItem } from "../../../api/team.js";

const agent = { id: "a1", handle: "captain", name: "Captain" } as unknown as AgentItem;

describe("SlackSettingsGroups", () => {
  beforeEach(() => patch.mockClear());

  test("renders the four groups then Connection and Routing, all closed, with Bots inside Access Control", async () => {
    current = { fields: fields({ users_mode: "whitelist", allowed_users: ONLY_ME }), owner_slack_id: "UOWNER", owner_slack_name: "Yoga", owner_slack_handle: "yoga" };
    render(SlackSettingsGroups, { props: { base: "/b", agent } });
    await waitFor(() => expect(screen.getAllByTestId("slack-group").length).toBe(6));
    const groups = screen.getAllByTestId("slack-group") as HTMLDetailsElement[];
    expect(groups.map((g) => g.dataset.group)).toEqual([AC, "Agent Behaviour", "Reaction Auto-Reply", "Approval Gates", "Connection", "Routing"]);
    expect(groups.every((g) => !g.open)).toBe(true);
    expect(groups[0].querySelector('[data-field="bots_mode"]')).not.toBeNull();
    expect(screen.getByText("Who can use @captain")).toBeTruthy();
  });

  test("a new app reads as Only me with the owner's handle and id", async () => {
    current = { fields: fields({ users_mode: "whitelist", allowed_users: ONLY_ME }), owner_slack_id: "UOWNER", owner_slack_name: "Yoga", owner_slack_handle: "yoga" };
    const onSummary = vi.fn();
    render(SlackSettingsGroups, { props: { base: "/b", agent, onSummary } });
    const me = (await screen.findByLabelText("Only me (@yoga · UOWNER)")) as HTMLInputElement;
    expect(me.checked).toBe(true);
    await waitFor(() => expect(onSummary).toHaveBeenLastCalledWith("Access: only Yoga · all channels", false));
    // people fields stay hidden unless "Specific people & groups" is chosen
    expect(document.querySelector('[data-field="allowed_users"]')).toBeNull();
  });

  test("an existing open connection reports open, and Only me writes the owner", async () => {
    current = { fields: fields(), owner_slack_id: "UOWNER", owner_slack_name: "Yoga", owner_slack_handle: "yoga" };
    const onSummary = vi.fn();
    render(SlackSettingsGroups, { props: { base: "/b", agent, onSummary } });
    const all = (await screen.findByLabelText("Everyone in the workspace")) as HTMLInputElement;
    expect(all.checked).toBe(true);
    await waitFor(() => expect(onSummary).toHaveBeenLastCalledWith("Access: everyone · all channels", true));
    await fireEvent.click(screen.getByLabelText("Only me (@yoga · UOWNER)"));
    await waitFor(() => expect(patch).toHaveBeenCalledWith("users_mode", "whitelist"));
    expect(patch).toHaveBeenCalledWith("allowed_users", ONLY_ME);
  });

  test("owner not found in Slack: closed by default, Only me disabled, the user picker asked for", async () => {
    current = { fields: fields({ users_mode: "whitelist", allowed_users: "[]" }) };
    const onSummary = vi.fn();
    render(SlackSettingsGroups, { props: { base: "/b", agent, onSummary } });
    expect((await screen.findByTestId("owner-unresolved")).textContent).toContain("pick people manually");
    expect((screen.getByLabelText("Only me") as HTMLInputElement).disabled).toBe(true);
    expect((screen.getByLabelText("Specific people & groups") as HTMLInputElement).checked).toBe(true);
    expect(screen.getByLabelText("Allowed users")).toBeTruthy();
    await waitFor(() => expect(onSummary).toHaveBeenLastCalledWith("Access: nobody yet · all channels", false));
  });

  test("picking a bot from the bots picker saves allowed_bots", async () => {
    current = { fields: fields({ bots_mode: "whitelist" }), owner_slack_id: "UOWNER", owner_slack_name: "Yoga", owner_slack_handle: "yoga" };
    render(SlackSettingsGroups, { props: { base: "/b", agent } });
    const box = await screen.findByLabelText("Allowed bots");
    await fireEvent.input(box, { target: { value: "dep" } });
    await fireEvent.click(await screen.findByText("Deploy bot"));
    await waitFor(() => expect(patch).toHaveBeenCalledWith("allowed_bots", JSON.stringify([{ id: "UBOT", name: "Deploy bot" }])));
  });
});

describe("SlackSettingsGroups parity with the Channels page", () => {
  beforeEach(() => patch.mockClear());
  const owner = { owner_slack_id: "UOWNER", owner_slack_name: "Yoga", owner_slack_handle: "yoga" };

  test("every schema key renders once its condition holds, editable keys in the same groups", async () => {
    const all = fields({ users_mode: "whitelist", groups_mode: "whitelist", bots_mode: "whitelist", channels_mode: "whitelist" })
      .map((x) => ({ ...x, value: ({ reaction_trigger_enabled: "true", gate_approvers: "custom" } as Record<string, string>)[x.key] ?? x.value }));
    expect(all.map((x) => x.key).sort()).toEqual([...SCHEMA_KEYS].sort());
    current = { fields: all, ...owner };
    render(SlackSettingsGroups, { props: { base: "/b", agent } });
    await waitFor(() => expect(screen.getAllByTestId("slack-group").length).toBe(6));
    const rendered = [...document.querySelectorAll<HTMLElement>("[data-field]")].map((e) => e.dataset.field);
    expect([...rendered].sort()).toEqual([...SCHEMA_KEYS].sort());
    for (const k of ["mode", "bot_token", "app_token", "signing_secret", "project_id", "public_url"]) {
      const el = document.querySelector(`[data-field="${k}"]`)!;
      expect(el.querySelector("input,select")).toBeNull();
      expect(el.querySelector("[data-readonly]")).not.toBeNull();
      expect(el.textContent).toContain("Set elsewhere");
    }
    expect(document.querySelector('[data-field="bot_token"]')!.textContent).toContain("•••••••• (set)");
    expect(document.querySelector('[data-field="public_url"]')!.textContent).toContain("https://wick.example.test");
    expect(screen.getAllByTestId("group-readonly").length).toBe(2);
  });

  test("Access Control splits into People, Bots and Channels", async () => {
    current = { fields: fields({ users_mode: "whitelist", groups_mode: "whitelist", bots_mode: "whitelist", channels_mode: "whitelist" }), ...owner };
    render(SlackSettingsGroups, { props: { base: "/b", agent } });
    await waitFor(() => expect(screen.getAllByTestId("access-dim").length).toBe(3));
    const keys = (d: string) => [...document.querySelectorAll<HTMLElement>(`[data-dim="${d}"] [data-field]`)].map((e) => e.dataset.field);
    expect(keys("people")).toEqual(["users_mode", "allowed_users", "groups_mode", "allowed_groups"]);
    expect(keys("bots")).toEqual(["bots_mode", "allowed_bots"]);
    expect(keys("channels")).toEqual(["channels_mode", "allowed_channels"]);
  });

  test("Yoga's scenario 1 is set through Specific people & groups, Bots specific and Channels all", async () => {
    current = { fields: fields(), ...owner };
    const onSummary = vi.fn();
    render(SlackSettingsGroups, { props: { base: "/b", agent, onSummary } });
    await fireEvent.click(await screen.findByLabelText("Specific people & groups"));
    await waitFor(() => expect(patch).toHaveBeenCalledWith("users_mode", "whitelist"));
    await fireEvent.change(screen.getByLabelText("Groups mode"), { target: { value: "whitelist" } });
    await waitFor(() => expect(patch).toHaveBeenCalledWith("groups_mode", "whitelist"));
    await fireEvent.input(screen.getByLabelText("Allowed users"), { target: { value: "user" } });
    await fireEvent.click(await screen.findByText("User A"));
    await fireEvent.input(screen.getByLabelText("Allowed groups"), { target: { value: "grp" } });
    await fireEvent.click(await screen.findByText("Group B"));
    await fireEvent.change(screen.getByLabelText("Bots mode"), { target: { value: "whitelist" } });
    await fireEvent.input(await screen.findByLabelText("Allowed bots"), { target: { value: "dep" } });
    await fireEvent.click(await screen.findByText("Deploy bot"));
    await waitFor(() => expect(patch).toHaveBeenCalledWith("allowed_bots", JSON.stringify([{ id: "UBOT", name: "Deploy bot" }])));
    expect(patch).toHaveBeenCalledWith("allowed_users", JSON.stringify([{ id: "UA", name: "User A" }]));
    expect(patch).toHaveBeenCalledWith("allowed_groups", JSON.stringify([{ id: "SB", name: "Group B" }]));
    expect(patch).toHaveBeenCalledWith("bots_mode", "whitelist");
    expect((screen.getByLabelText("Channels mode") as HTMLSelectElement).value).toBe("all");
    await waitFor(() => expect(onSummary).toHaveBeenLastCalledWith("Access: 1 user · 1 group · 1 bot · all channels", false));
  });

  test("Only me with groups still stored says so instead of hiding them silently", async () => {
    current = { fields: fields({ users_mode: "whitelist", allowed_users: ONLY_ME, groups_mode: "all", allowed_groups: JSON.stringify([{ id: "SB", name: "Group B" }]) }), ...owner };
    render(SlackSettingsGroups, { props: { base: "/b", agent } });
    expect(((await screen.findByLabelText("Only me (@yoga · UOWNER)")) as HTMLInputElement).checked).toBe(true);
    expect(screen.getByTestId("people-hidden").textContent).toContain("Groups: 1 chosen");
    expect(document.querySelector('[data-field="allowed_groups"]')).toBeNull();
  });

  test("Only me preset fills users with the owner", async () => {
    current = { fields: fields({ users_mode: "whitelist", allowed_users: JSON.stringify([{ id: "UA", name: "User A" }]) }), ...owner };
    render(SlackSettingsGroups, { props: { base: "/b", agent } });
    await fireEvent.click(await screen.findByLabelText("Only me (@yoga · UOWNER)"));
    await waitFor(() => expect(patch).toHaveBeenCalledWith("allowed_users", ONLY_ME));
    expect(patch).toHaveBeenCalledWith("users_mode", "whitelist");
    expect(patch).toHaveBeenCalledWith("groups_mode", "all");
  });
});

describe("accessSummary", () => {
  test("only owner, two channels, one bot", () => {
    const v = {
      users_mode: "whitelist", allowed_users: ONLY_ME, groups_mode: "all",
      channels_mode: "whitelist", allowed_channels: JSON.stringify([{ id: "C1", name: "a" }, { id: "C2", name: "b" }]),
      bots_mode: "whitelist", allowed_bots: JSON.stringify([{ id: "UB", name: "bot" }]),
    };
    expect(accessSummary(v, "UOWNER", "Yoga")).toBe("Access: only Yoga · 1 bot · 2 channels");
  });

  test("Yoga's scenario 1: one user, one group, one bot, every channel", () => {
    const v = {
      users_mode: "whitelist", allowed_users: JSON.stringify([{ id: "UA", name: "User A" }]),
      groups_mode: "whitelist", allowed_groups: JSON.stringify([{ id: "SB", name: "Group B" }]),
      bots_mode: "whitelist", allowed_bots: JSON.stringify([{ id: "UBA", name: "Bot A" }]), channels_mode: "all",
    };
    expect(accessSummary(v, "UOWNER", "Yoga")).toBe("Access: 1 user · 1 group · 1 bot · all channels");
  });

  test("owner plus a group keeps 'only Yoga'", () => {
    const v = { users_mode: "whitelist", allowed_users: ONLY_ME, groups_mode: "whitelist", allowed_groups: JSON.stringify([{ id: "SB", name: "B" }]), channels_mode: "all" };
    expect(accessSummary(v, "UOWNER", "Yoga")).toBe("Access: only Yoga · 1 group · all channels");
  });
});
