import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import McpServerForm from "../McpServerForm.svelte";
import * as api from "$lib/api.js";
import type { McpServerForm as McpServerFormType, McpServerFormResult } from "$lib/types.js";

vi.mock("$lib/api.js");
vi.mock("$lib/router.js", () => ({ push: vi.fn() }));

function makeForm(over: Partial<McpServerFormType> = {}): McpServerFormType {
  return {
    label: "Internal MCP",
    icon: "🔌",
    description: "",
    url: "https://mcp.internal.example.com/v1",
    auth_scheme: "custom_header",
    auth_secret: "",
    auth_headers: [{ key: "X-API-Key", value: "wick_enc_abc", secret: true }],
    headers: [{ key: "X-Tenant", value: "acme", secret: false }],
    sso: { audience: "", ttl_seconds: 300 },
    oauth: { client_id: "", client_secret: "", scopes: "" },
    excluded: ["dangerous_tool"],
    oauth_login_id: "",
    ...over,
  };
}

function makeFormResult(over: Partial<McpServerFormResult> = {}): McpServerFormResult {
  return {
    id: "srv-1",
    form: makeForm(),
    tools: [
      { name: "list_items", description: "List items" },
      { name: "dangerous_tool", description: "Deletes everything" },
    ],
    info: { def_id: "def-1", disabled: false },
    ...over,
  };
}

beforeEach(() => {
  vi.clearAllMocks();
});

describe("McpServerForm — new mode (test gate)", () => {
  it("renders the register toolbar with Save disabled until a test passes", async () => {
    render(McpServerForm);
    expect(await screen.findByText("Register MCP server")).toBeTruthy();
    const save = screen.getByRole("button", { name: "Save & create →" }) as HTMLButtonElement;
    expect(save.disabled).toBe(true);
    /* No edit-only actions in new mode. */
    expect(screen.queryByRole("button", { name: "Delete" })).toBeNull();
  });

  it("enables Save after a successful test and lists discovered tools", async () => {
    vi.mocked(api.testMcpServer).mockResolvedValue({
      ok: true,
      latency_ms: 42,
      tools: [
        { name: "list_items", description: "List items" },
        { name: "create_item", description: "Create one" },
      ],
    });
    render(McpServerForm);
    await screen.findByText("Register MCP server");
    await fireEvent.click(screen.getByRole("button", { name: "▶ Test now" }));
    await screen.findByText(/Connected/);
    expect(screen.getByText("list_items")).toBeTruthy();
    expect(screen.getByText("create_item")).toBeTruthy();
    const save = screen.getByRole("button", { name: "Save & create →" }) as HTMLButtonElement;
    expect(save.disabled).toBe(false);
  });

  it("keeps Save disabled and surfaces the error on a failed test", async () => {
    vi.mocked(api.testMcpServer).mockResolvedValue({ ok: false, error: "connection refused" });
    render(McpServerForm);
    await screen.findByText("Register MCP server");
    await fireEvent.click(screen.getByRole("button", { name: "▶ Test now" }));
    expect(await screen.findByText("connection refused")).toBeTruthy();
    const save = screen.getByRole("button", { name: "Save & create →" }) as HTMLButtonElement;
    expect(save.disabled).toBe(true);
  });

  it("re-locks Save when a field changes after a successful test", async () => {
    vi.mocked(api.testMcpServer).mockResolvedValue({ ok: true, latency_ms: 10, tools: [] });
    render(McpServerForm);
    await screen.findByText("Register MCP server");
    await fireEvent.click(screen.getByRole("button", { name: "▶ Test now" }));
    await screen.findByText(/Connected/);
    let save = screen.getByRole("button", { name: "Save & create →" }) as HTMLButtonElement;
    expect(save.disabled).toBe(false);
    await fireEvent.input(screen.getByLabelText("Label"), { target: { value: "Renamed" } });
    save = screen.getByRole("button", { name: "Save & create →" }) as HTMLButtonElement;
    expect(save.disabled).toBe(true);
  });
});

describe("McpServerForm — auth panel toggle", () => {
  it("shows the bearer token input only when the bearer scheme is selected", async () => {
    render(McpServerForm);
    await screen.findByText("Register MCP server");
    /* none scheme by default — no bearer field. */
    expect(screen.queryByLabelText("Header name")).toBeNull();
    await fireEvent.click(screen.getByLabelText("Bearer token"));
    expect(await screen.findByText(/Stored encrypted/)).toBeTruthy();
  });

  it("shows the auth-header editor when the custom_header scheme is selected", async () => {
    render(McpServerForm);
    await screen.findByText("Register MCP server");
    await fireEvent.click(screen.getByLabelText("Custom header"));
    expect(await screen.findByText("Auth headers")).toBeTruthy();
  });

  it("shows the OAuth client fields when the oauth scheme is selected", async () => {
    render(McpServerForm);
    await screen.findByText("Register MCP server");
    await fireEvent.click(screen.getByLabelText("OAuth (login on the server)"));
    expect(await screen.findByText("Client ID (optional)")).toBeTruthy();
  });
});

describe("McpServerForm — SSO panel content (finding #15)", () => {
  it("shows claim-mapping pre, Why-SSO box, and server-requirement callout when SSO is selected", async () => {
    render(McpServerForm);
    await screen.findByText("Register MCP server");
    await fireEvent.click(screen.getByLabelText("SSO (forward caller's session)"));
    expect(await screen.findByText(/Claim mapping/)).toBeTruthy();
    expect(screen.getByText(/\.well-known\/wick-pubkey\.pem/)).toBeTruthy();
    expect(screen.getByText(/Why SSO/)).toBeTruthy();
  });
});

describe("McpServerForm — edit mode", () => {
  it("prefills from the JSON endpoint and shows enable/disable + delete", async () => {
    vi.mocked(api.getMcpServerForm).mockResolvedValue(makeFormResult());
    render(McpServerForm, { serverId: "srv-1" });
    expect(await screen.findByText("Edit MCP server")).toBeTruthy();
    expect(api.getMcpServerForm).toHaveBeenCalledWith("srv-1");
    expect(screen.getByRole("button", { name: "Save changes" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Delete" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Disable" })).toBeTruthy();
    /* Prefilled tool catalog renders for the exclude list. */
    expect(screen.getByText("list_items")).toBeTruthy();
  });

  it("toggles a tool's exclude membership", async () => {
    vi.mocked(api.getMcpServerForm).mockResolvedValue(makeFormResult());
    render(McpServerForm, { serverId: "srv-1" });
    await screen.findByText("Edit MCP server");
    /* dangerous_tool starts excluded → button reads Include. */
    const includeBtn = screen.getAllByRole("button", { name: "Include" });
    expect(includeBtn.length).toBe(1);
    /* list_items starts included → Exclude. Click to exclude it. */
    const excludeBtns = screen.getAllByRole("button", { name: "Exclude" });
    await fireEvent.click(excludeBtns[0]);
    expect(screen.getAllByRole("button", { name: "Include" }).length).toBe(2);
  });
});
