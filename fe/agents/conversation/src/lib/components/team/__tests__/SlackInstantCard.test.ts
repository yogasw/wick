import { describe, test, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";
import type { AgentSlackInstantStatus, AgentSlackInstantUpdate } from "../../../slackInstant.js";

const off: AgentSlackInstantStatus = {
  enabled: false, shared_channel: "", bound_channels: [], prefix_enabled: false, username: "",
  avatar_ready: false, shared_online: false, customize_scope: "unknown",
};
const on: AgentSlackInstantStatus = {
  enabled: true, shared_channel: "slack:__owner__", bound_channels: ["C0123ABCD"], prefix_enabled: true,
  username: "Nanda", avatar_ready: true, shared_online: true, customize_scope: "ok", warnings: [],
};
class APIErr extends Error { constructor(public status: number, msg: string) { super(msg); } }

const update = vi.fn((_b: string, _id: string, body: AgentSlackInstantUpdate): Promise<AgentSlackInstantStatus> =>
  Promise.resolve({ ...on, ...body, enabled: true }));
const disable = vi.fn(() => Promise.resolve({ status: "disabled" }));
const rotate = vi.fn(() => Promise.resolve(on));
vi.mock("../../../api/team.js", async (orig) => ({
  ...(await orig<typeof import("../../../api/team.js")>()),
  runApi: <T,>(p: Promise<T>) => p,
}));
vi.mock("../../../slackInstant.js", async (orig) => ({
  ...(await orig<typeof import("../../../slackInstant.js")>()),
  listSlackInstantApps: () => Promise.resolve({ apps: [{ key: "slack:__owner__", bot_name: "wick", team_name: "Acme", online: true, shared: true }] }),
  updateAgentSlackInstant: (b: string, id: string, body: AgentSlackInstantUpdate) => update(b, id, body),
  disableAgentSlackInstant: () => disable(),
  rotateAgentSlackInstantAvatar: () => rotate(),
}));

import SlackInstantCard from "../SlackInstantCard.svelte";
import { channelOf, instantStatusLine, prefixExample } from "../../../slackInstant.js";
import type { AgentItem } from "../../../api/team.js";

const agent = { id: "a1", handle: "nanda", name: "Nanda", avatar: { shape: "circle", color: "#6366f1" } } as unknown as AgentItem;
const props = (status: AgentSlackInstantStatus, customConnected = false) => ({ base: "/tools/agents", agent, status, customConnected });

describe("SlackInstantCard", () => {
  beforeEach(() => { update.mockClear(); disable.mockClear(); rotate.mockClear(); });

  test("picking the shared app turns Instant on and shows the preview, limits and prefix example", async () => {
    render(SlackInstantCard, { props: props(off) });
    const sel = (await screen.findByLabelText("Shared Slack app")) as HTMLSelectElement;
    await waitFor(() => expect(sel.options.length).toBe(2));
    expect(screen.queryByTestId("instant-preview")).toBeNull();
    await fireEvent.change(sel, { target: { value: "slack:__owner__" } });
    await waitFor(() => expect(update).toHaveBeenCalledWith("/tools/agents", "a1", { shared_channel: "slack:__owner__" }));
    expect((await screen.findByTestId("instant-username")).textContent).toBe("Nanda");
    expect(screen.getByTestId("instant-prefix-example").textContent).toBe("@wick nanda: summarize this thread");
    expect(screen.getByTestId("instant-limits").textContent).toContain("can't DM it directly");
    expect(screen.getByTestId("instant-scope").dataset.scope).toBe("ok");
  });

  test("bind takes a pasted message link as its channel id, unbind drops it, junk is refused locally", async () => {
    render(SlackInstantCard, { props: props(on) });
    const box = screen.getByLabelText("Channel ID or link");
    await fireEvent.input(box, { target: { value: "#general" } });
    await fireEvent.click(screen.getByRole("button", { name: "Bind" }));
    expect(screen.getByTestId("channel-error")).toBeTruthy();
    expect(update).not.toHaveBeenCalled();
    await fireEvent.input(box, { target: { value: "https://acme.slack.com/archives/C0999ZZZZ/p1700000000123456" } });
    await fireEvent.click(screen.getByRole("button", { name: "Bind" }));
    await waitFor(() => expect(update).toHaveBeenCalledWith("/tools/agents", "a1", { bound_channels: ["C0123ABCD", "C0999ZZZZ"] }));
    await waitFor(() => expect((box as HTMLInputElement).value).toBe(""));
    await fireEvent.click(screen.getByRole("button", { name: "Unbind C0123ABCD" }));
    await waitFor(() => expect(update).toHaveBeenLastCalledWith("/tools/agents", "a1", { bound_channels: ["C0999ZZZZ"] }));
  });

  test("409: the owning agent's name is shown and the channel list stays as it was", async () => {
    update.mockImplementationOnce(() => Promise.reject(new APIErr(409, "channel C0777AAAA is already bound to @rekap — one channel answers as one agent")));
    render(SlackInstantCard, { props: props(on) });
    await fireEvent.input(screen.getByLabelText("Channel ID or link"), { target: { value: "c0777aaaa" } });
    await fireEvent.click(screen.getByRole("button", { name: "Bind" }));
    expect((await screen.findByTestId("instant-error")).textContent).toContain("@rekap");
    expect(screen.getByTestId("instant-save").textContent).toBe("Couldn't save");
    expect(screen.getByTestId("instant-channels").textContent).not.toContain("C0777AAAA");
    expect((screen.getByLabelText("Channel ID or link") as HTMLInputElement).value).toBe("c0777aaaa");
  });

  test("Custom mode connected: the picker is locked and the exclusivity is explained", async () => {
    render(SlackInstantCard, { props: props(off, true) });
    expect(screen.getByTestId("instant-exclusive").textContent).toContain("Disconnect it");
    expect(((await screen.findByLabelText("Shared Slack app")) as HTMLSelectElement).disabled).toBe(true);
  });

  test("rotate issues a new avatar link; prefix toggle and turn off autosave", async () => {
    render(SlackInstantCard, { props: props(on) });
    await fireEvent.click(screen.getByRole("button", { name: "Rotate avatar link" }));
    await waitFor(() => expect(rotate).toHaveBeenCalled());
    expect(await screen.findByText("New link issued — the old one no longer works.")).toBeTruthy();
    await fireEvent.click(screen.getByRole("switch", { name: "Answer to its handle" }));
    await waitFor(() => expect(update).toHaveBeenCalledWith("/tools/agents", "a1", { prefix_enabled: false }));
    await fireEvent.click(screen.getByRole("button", { name: "Turn off Instant" }));
    await waitFor(() => expect(disable).toHaveBeenCalled());
    await waitFor(() => expect(screen.queryByTestId("instant-preview")).toBeNull());
  });

  test("scope missing: warning with the fix; server warnings are listed", async () => {
    render(SlackInstantCard, { props: props({ ...on, customize_scope: "missing", warnings: ["Replies go out as the shared bot."] }) });
    const scope = screen.getByTestId("instant-scope");
    expect(scope.dataset.scope).toBe("missing");
    expect(scope.textContent).toContain("Bot Token Scopes");
    expect(scope.textContent).toContain("reinstall");
    expect(screen.getByTestId("instant-warnings").textContent).toContain("as the shared bot");
  });
});

describe("slackInstant", () => {
  test("channelOf mirrors the server: ids, # and lower case, archive and client links", () => {
    expect(channelOf(" #c0123abcd ")).toBe("C0123ABCD");
    expect(channelOf("https://x.slack.com/archives/G0123ABCD")).toBe("G0123ABCD");
    expect(channelOf("https://app.slack.com/client/T0001/C0999ZZZZ/thread/C0999ZZZZ-1.2")).toBe("C0999ZZZZ");
    expect(channelOf("#general")).toBe("");
    expect(channelOf("CGENERALX")).toBe("");
    expect(prefixExample(undefined, "nanda")).toBe("@bot nanda: summarize this thread");
    expect(instantStatusLine(null)).toBe("Not connected");
    expect(instantStatusLine({ ...on, bound_channels: ["C1", "C2"] })).toBe("Instant — 2 channels");
    expect(instantStatusLine({ ...on, bound_channels: [], shared_online: false })).toBe("Instant · shared app offline — prefix only");
  });
});
