import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";
import ConnectorDetail from "../ConnectorDetail.svelte";
import * as api from "$lib/api.js";
import * as router from "$lib/router.js";
import type { AccountDetail as AccountDetailData, AccountOp, ConnectorDetail as RowDetail } from "$lib/types.js";

vi.mock("$lib/api.js");
vi.mock("$lib/router.js", () => ({ push: vi.fn() }));
vi.mock("@wick-fe/common-stores", () => ({ toastOk: vi.fn(), toastError: vi.fn() }));
vi.mock("../connectorOAuth.js", () => ({ startConnectorOAuth: vi.fn() }));

function makeOp(over: Partial<AccountOp> = {}): AccountOp {
  return {
    key: "send",
    name: "Send Message",
    description: "Post a message",
    destructive: false,
    enabled: true,
    state: "inherit",
    inherited: true,
    system_disabled: false,
    system_disabled_reason: "",
    ...over,
  };
}

function makeData(over: Partial<AccountDetailData> = {}): AccountDetailData {
  return {
    connector_key: "slack",
    connector_name: "Slack",
    row_id: "row-a",
    row_label: "Prod",
    account_id: "acc-1",
    display_name: "yoga.setiawan",
    can_manage: true,
    reconnect_url: "/manager/connectors/slack/oauth/start?connector_id=row-a",
    ops: [makeOp()],
    ...over,
  };
}

/* Account mode still loads the row — it IS the row's page — so both fetches
   are stubbed. can_configure is false so the instance-configuration sections
   stay out of the way of the assertions; account mode hides them anyway. */
function makeRow(over: Partial<RowDetail> = {}): RowDetail {
  return {
    key: "slack",
    name: "Slack",
    description: "",
    icon: "💬",
    id: "row-a",
    label: "Prod",
    disabled: false,
    can_configure: false,
    can_manage_policy: false,
    configs: [],
    operations: [],
    categories: [],
    accounts: [],
    oauth: null,
    mcp_auth: null,
    enable_sso: false,
    multi_account: false,
    allow_others_configure: false,
    allow_others_connect_sso: false,
    allow_others_see_accounts: false,
    rate_limit_rpm: 0,
    has_health_check: false,
    require_ai_description: false,
    ...over,
  } as RowDetail;
}

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(api.getConnectorRow).mockResolvedValue(makeRow());
});

/* The page renders the SHARED OperationsTable in account mode rather than a
   second list of its own — same grouping, search, pagination and Test /
   History actions the instance page has. What account mode adds is the
   indicator next to the toggle: "off because the instance is off" and "off
   because this account says so" are different facts, and only the second is
   fixed from here. */
describe("AccountDetail operation state indicator", () => {
  const cases: [string, Partial<AccountOp>, string][] = [
    ["inheriting an instance-on op", { state: "inherit", enabled: true, inherited: true }, "inherited"],
    ["inheriting an instance-off op", { state: "inherit", enabled: false, inherited: false }, "inherited"],
    ["an override on over an instance-off op", { state: "on", enabled: true, inherited: false }, "override"],
    ["an override off over an instance-on op", { state: "off", enabled: false, inherited: true }, "override"],
  ];

  for (const [name, over, label] of cases) {
    it(`labels ${name}`, async () => {
      vi.mocked(api.getConnectorAccount).mockResolvedValue(makeData({ ops: [makeOp(over)] }));
      render(ConnectorDetail, { connectorKey: "slack", connectorId: "row-a", accountId: "acc-1" });
      expect(await screen.findByText(label)).toBeTruthy();
    });
  }

  it("surfaces the health-check reason from the shared table", async () => {
    vi.mocked(api.getConnectorAccount).mockResolvedValue(
      makeData({ ops: [makeOp({ system_disabled: true, system_disabled_reason: "needs scope: chat:write" })] }),
    );
    render(ConnectorDetail, { connectorKey: "slack", connectorId: "row-a", accountId: "acc-1" });
    expect(await screen.findByText(/needs scope: chat:write/)).toBeTruthy();
  });

  it("counts the overrides in effect", async () => {
    vi.mocked(api.getConnectorAccount).mockResolvedValue(
      makeData({
        ops: [makeOp({ key: "a", state: "on" }), makeOp({ key: "b", state: "off" }), makeOp({ key: "c", state: "inherit" })],
      }),
    );
    render(ConnectorDetail, { connectorKey: "slack", connectorId: "row-a", accountId: "acc-1" });
    /* The first question when a call behaved differently here than elsewhere
       is whether this account follows the instance at all. */
    expect(await screen.findByText("2 overrides in effect.")).toBeTruthy();
  });
});

describe("AccountDetail toggle writes an override", () => {
  it("flipping the toggle pins this account's answer", async () => {
    vi.mocked(api.getConnectorAccount).mockResolvedValue(makeData());
    vi.mocked(api.setAccountOpState).mockResolvedValue(undefined);
    render(ConnectorDetail, { connectorKey: "slack", connectorId: "row-a", accountId: "acc-1" });

    const sw = await screen.findByRole("switch", { name: "Enable Send Message for this account" });
    expect(sw.getAttribute("aria-checked")).toBe("true");
    await fireEvent.click(sw);
    await waitFor(() =>
      expect(api.setAccountOpState).toHaveBeenCalledWith("slack", "row-a", "acc-1", "send", "off"),
    );
  });

  it("offers reset only while an override is in effect", async () => {
    vi.mocked(api.getConnectorAccount).mockResolvedValue(makeData({ ops: [makeOp({ state: "inherit" })] }));
    render(ConnectorDetail, { connectorKey: "slack", connectorId: "row-a", accountId: "acc-1" });
    await screen.findByText("inherited");
    /* Nothing to reset when the account has said nothing. */
    expect(screen.queryByRole("button", { name: "reset" })).toBeNull();
  });

  it("reset clears the override back to inheriting", async () => {
    vi.mocked(api.getConnectorAccount).mockResolvedValue(makeData({ ops: [makeOp({ state: "off", enabled: false })] }));
    vi.mocked(api.setAccountOpState).mockResolvedValue(undefined);
    render(ConnectorDetail, { connectorKey: "slack", connectorId: "row-a", accountId: "acc-1" });
    await fireEvent.click(await screen.findByRole("button", { name: "reset" }));
    await waitFor(() =>
      expect(api.setAccountOpState).toHaveBeenCalledWith("slack", "row-a", "acc-1", "send", "inherit"),
    );
  });

  it("locks the toggle when the health check has", async () => {
    vi.mocked(api.getConnectorAccount).mockResolvedValue(makeData({ ops: [makeOp({ system_disabled: true })] }));
    render(ConnectorDetail, { connectorKey: "slack", connectorId: "row-a", accountId: "acc-1" });
    /* Offering a switch that cannot take effect is worse than offering none:
       the credential genuinely lacks the permission. */
    const sw = await screen.findByRole("switch", { name: "Enable Send Message for this account" });
    expect(sw.hasAttribute("disabled")).toBe(true);
  });
});

describe("AccountDetail access", () => {
  it("renders read-only for a viewer who may not manage the account", async () => {
    vi.mocked(api.getConnectorAccount).mockResolvedValue(makeData({ can_manage: false }));
    render(ConnectorDetail, { connectorKey: "slack", connectorId: "row-a", accountId: "acc-1" });

    /* Seeing which operations apply to an identity is not privileged, so the
       page renders rather than 403-ing — but nothing is writable. */
    expect(await screen.findByText(/Read-only/)).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Actions for @yoga.setiawan" })).toBeNull();
    const sw = screen.getByRole("switch", { name: "Enable Send Message for this account" });
    expect(sw.hasAttribute("disabled")).toBe(true);
  });

  it("offers Re-connect and Disconnect behind the account's kebab", async () => {
    vi.mocked(api.getConnectorAccount).mockResolvedValue(makeData());
    render(ConnectorDetail, { connectorKey: "slack", connectorId: "row-a", accountId: "acc-1" });
    await fireEvent.click(await screen.findByRole("button", { name: "Actions for @yoga.setiawan" }));
    expect(screen.getByRole("menuitem", { name: "Re-connect" })).toBeTruthy();
    expect(screen.getByRole("menuitem", { name: "Disconnect" })).toBeTruthy();
  });

  it("links History pre-filtered to this credential", async () => {
    vi.mocked(api.getConnectorAccount).mockResolvedValue(makeData());
    render(ConnectorDetail, { connectorKey: "slack", connectorId: "row-a", accountId: "acc-1" });
    /* Two History buttons now: the header's (whole account) and the shared
       table's per-operation one. The header's comes first. */
    const headerHistory = (await screen.findAllByRole("button", { name: "History" }))[0];
    await fireEvent.click(headerHistory);
    expect(router.push).toHaveBeenCalledWith("/connectors/slack/row-a/history?credential=acc-1");
  });

  it("scopes the per-operation History action to this account too", async () => {
    vi.mocked(api.getConnectorAccount).mockResolvedValue(makeData());
    render(ConnectorDetail, { connectorKey: "slack", connectorId: "row-a", accountId: "acc-1" });
    /* The row action inside the shared table must carry the credential, or
       "History" next to an account's operation shows every identity's calls. */
    const rowHistory = (await screen.findAllByRole("button", { name: "History" }))[1];
    await fireEvent.click(rowHistory);
    expect(router.push).toHaveBeenCalledWith(
      "/connectors/slack/row-a/history?op=send&credential=acc-1",
    );
  });
});

/* Both complaints were one cause: the toggle triggered a full page reload,
   which flipped `loading` and unmounted the table. That is the flicker, and
   it is why a change looked like it needed a refresh — the row you touched
   was replaced by "Loading…" and rebuilt. */
describe("AccountDetail updates in place", () => {
  it("does not refetch the page when a toggle is flipped", async () => {
    vi.mocked(api.getConnectorAccount).mockResolvedValue(makeData());
    vi.mocked(api.setAccountOpState).mockResolvedValue(undefined);
    render(ConnectorDetail, { connectorKey: "slack", connectorId: "row-a", accountId: "acc-1" });

    const sw = await screen.findByRole("switch", { name: "Enable Send Message for this account" });
    expect(api.getConnectorAccount).toHaveBeenCalledTimes(1);
    await fireEvent.click(sw);
    await waitFor(() => expect(api.setAccountOpState).toHaveBeenCalled());
    /* One fetch, at mount. A second one would mean the table was torn down
       and rebuilt on a click. */
    expect(api.getConnectorAccount).toHaveBeenCalledTimes(1);
    expect(screen.queryByText("Loading…")).toBeNull();
  });

  it("moves the caption with the switch, not a round-trip later", async () => {
    vi.mocked(api.getConnectorAccount).mockResolvedValue(makeData({ ops: [makeOp({ state: "inherit", enabled: true, inherited: true })] }));
    vi.mocked(api.setAccountOpState).mockResolvedValue(undefined);
    render(ConnectorDetail, { connectorKey: "slack", connectorId: "row-a", accountId: "acc-1" });

    expect(await screen.findByText("inherited")).toBeTruthy();
    await fireEvent.click(screen.getByRole("switch", { name: "Enable Send Message for this account" }));
    /* The switch and the caption are one fact; showing "inherited" next to a
       switch the user just overrode is a lie. */
    await waitFor(() => expect(screen.getByText("override")).toBeTruthy());
    expect(screen.queryByText("inherited")).toBeNull();
  });

  it("reset snaps the switch back to what the instance says", async () => {
    vi.mocked(api.getConnectorAccount).mockResolvedValue(
      // Overridden ON over an instance that says off.
      makeData({ ops: [makeOp({ state: "on", enabled: true, inherited: false })] }),
    );
    vi.mocked(api.setAccountOpState).mockResolvedValue(undefined);
    render(ConnectorDetail, { connectorKey: "slack", connectorId: "row-a", accountId: "acc-1" });

    await fireEvent.click(await screen.findByRole("button", { name: "reset" }));
    await waitFor(() => expect(screen.getByText("inherited")).toBeTruthy());
    const sw = screen.getByRole("switch", { name: "Enable Send Message for this account" });
    expect(sw.getAttribute("aria-checked")).toBe("false");
  });

  it("puts the switch and the caption back when the write fails", async () => {
    vi.mocked(api.getConnectorAccount).mockResolvedValue(makeData());
    vi.mocked(api.setAccountOpState).mockRejectedValue(new Error("nope"));
    render(ConnectorDetail, { connectorKey: "slack", connectorId: "row-a", accountId: "acc-1" });

    const sw = await screen.findByRole("switch", { name: "Enable Send Message for this account" });
    await fireEvent.click(sw);
    /* An optimistic update that survives a failed write is worse than none:
       the page would claim a change that is not stored. */
    await waitFor(() => expect(screen.getByText("inherited")).toBeTruthy());
    expect(screen.getByRole("switch", { name: "Enable Send Message for this account" }).getAttribute("aria-checked")).toBe("true");
  });
});

/* The requirement, stated plainly: the SAME page, with the parts that do not
   apply hidden. A separate page drifted from this one — different width,
   different header, its own Operations list — which is the whole reason this
   mode exists instead of a second component. */
describe("account mode is the row's page with sections hidden", () => {
  it("keeps the row's own header and identity", async () => {
    vi.mocked(api.getConnectorRow).mockResolvedValue(makeRow({ label: "Prod", can_configure: true }));
    vi.mocked(api.getConnectorAccount).mockResolvedValue(makeData());
    render(ConnectorDetail, { connectorKey: "slack", connectorId: "row-a", accountId: "acc-1" });

    // Same title + row id as the instance page, not a bespoke account header.
    expect(await screen.findByRole("heading", { name: "Prod" })).toBeTruthy();
    expect(screen.getByText("row-a")).toBeTruthy();
    expect(screen.getByText("@yoga.setiawan")).toBeTruthy();
  });

  it("hides the instance-configuration sections even for someone who may configure", async () => {
    vi.mocked(api.getConnectorRow).mockResolvedValue(
      makeRow({ can_configure: true, can_manage_policy: true, oauth: { display_name: "Slack", start_url: "" }, enable_sso: true }),
    );
    vi.mocked(api.getConnectorAccount).mockResolvedValue(makeData());
    render(ConnectorDetail, { connectorKey: "slack", connectorId: "row-a", accountId: "acc-1" });
    await screen.findByText("@yoga.setiawan");

    /* These belong to the row, not to one account — showing them here invites
       edits that have nothing to do with the account being looked at. */
    expect(screen.queryByRole("heading", { name: "Label" })).toBeNull();
    expect(screen.queryByRole("heading", { name: "Credentials" })).toBeNull();
    expect(screen.queryByRole("heading", { name: "Rate limit" })).toBeNull();
    expect(screen.queryByRole("heading", { name: "Connected accounts" })).toBeNull();
  });

  it("still shows them on the instance page itself", async () => {
    vi.mocked(api.getConnectorRow).mockResolvedValue(makeRow({ can_configure: true }));
    render(ConnectorDetail, { connectorKey: "slack", connectorId: "row-a" });
    /* The hiding must be conditional on account mode, not a removal. */
    expect(await screen.findByRole("heading", { name: "Label" })).toBeTruthy();
    expect(screen.getByRole("heading", { name: "Rate limit" })).toBeTruthy();
    expect(api.getConnectorAccount).not.toHaveBeenCalled();
  });
});
