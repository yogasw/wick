import { describe, test, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";
import type { AgentSlackSettings, SlackSettingField } from "../../../slackAccess.js";

const AC = "Access Control";
const ACD = "Who may trigger agents.";
const f = (key: string, value: string, type: string, extra: Partial<SlackSettingField> = {}): SlackSettingField =>
  ({ key, value, type, group: AC, group_desc: ACD, ...extra });
const fields = (over: Record<string, string> = {}): SlackSettingField[] => [
  f("users_mode", over.users_mode ?? "all", "dropdown", { options: "all|whitelist" }),
  f("allowed_users", over.allowed_users ?? "", "picker", { options: "slack.users", visible_when: "users_mode:whitelist" }),
  f("groups_mode", over.groups_mode ?? "all", "dropdown", { options: "all|whitelist" }),
  f("allowed_groups", "", "picker", { options: "slack.usergroups", visible_when: "groups_mode:whitelist" }),
  f("bots_mode", over.bots_mode ?? "none", "dropdown", { options: "none|whitelist|all" }),
  f("allowed_bots", over.allowed_bots ?? "", "picker", { options: "slack.bots", visible_when: "bots_mode:whitelist" }),
  f("channels_mode", over.channels_mode ?? "all", "dropdown", { options: "all|whitelist" }),
  f("allowed_channels", over.allowed_channels ?? "", "picker", { options: "slack.channels", visible_when: "channels_mode:whitelist" }),
  { key: "ask_user_enabled", value: "false", type: "bool", group: "Agent Behaviour" },
  { key: "reaction_trigger_enabled", value: "false", type: "bool", group: "Reaction Auto-Reply" },
  { key: "gate_approvers", value: "trigger_users", type: "dropdown", options: "trigger_users|admins|custom", group: "Approval Gates" },
];
const ONLY_ME = JSON.stringify([{ id: "UOWNER", name: "Yoga" }]);
let current: AgentSlackSettings;
const patch = vi.fn((_k: string, _v: string) => Promise.resolve(current));

vi.mock("../../../api/team.js", async (orig) => ({
  ...(await orig<typeof import("../../../api/team.js")>()),
  runApi: <T,>(p: Promise<T>) => p,
}));
vi.mock("../../../slackAccess.js", async (orig) => ({
  ...(await orig<typeof import("../../../slackAccess.js")>()),
  getAgentSlackSettings: () => Promise.resolve(current),
  setAgentSlackSetting: (_b: string, _id: string, k: string, v: string) => patch(k, v),
  lookupAgentSlack: () => Promise.resolve({ items: [{ id: "UBOT", name: "Deploy bot" }] }),
}));

import SlackSettingsGroups from "../SlackSettingsGroups.svelte";
import { accessSummary } from "../../../slackAccess.js";
import type { AgentItem } from "../../../api/team.js";

const agent = { id: "a1", handle: "captain", name: "Captain" } as unknown as AgentItem;

describe("SlackSettingsGroups", () => {
  beforeEach(() => patch.mockClear());

  test("renders the four groups, all closed, with Bots inside Access Control", async () => {
    current = { fields: fields({ users_mode: "whitelist", allowed_users: ONLY_ME }), owner_slack_id: "UOWNER", owner_slack_name: "Yoga", owner_slack_handle: "yoga" };
    render(SlackSettingsGroups, { props: { base: "/b", agent } });
    await waitFor(() => expect(screen.getAllByTestId("slack-group").length).toBe(4));
    const groups = screen.getAllByTestId("slack-group") as HTMLDetailsElement[];
    expect(groups.map((g) => g.dataset.group)).toEqual([AC, "Agent Behaviour", "Reaction Auto-Reply", "Approval Gates"]);
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

describe("accessSummary", () => {
  test("only owner, two channels, one bot", () => {
    const v = {
      users_mode: "whitelist", allowed_users: ONLY_ME, groups_mode: "all",
      channels_mode: "whitelist", allowed_channels: JSON.stringify([{ id: "C1", name: "a" }, { id: "C2", name: "b" }]),
      bots_mode: "whitelist", allowed_bots: JSON.stringify([{ id: "UB", name: "bot" }]),
    };
    expect(accessSummary(v, "UOWNER", "Yoga")).toBe("Access: only Yoga · 2 channels · 1 bot");
  });
});
