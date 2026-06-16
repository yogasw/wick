import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import { writable } from "svelte/store";
import App from "../../../App.svelte";
import * as api from "$lib/api.js";
import * as router from "$lib/router.js";

const routeStore = writable<string>("/");
const pushMock = vi.fn((p: string) => routeStore.set(p));

vi.mock("$lib/api.js");
vi.mock("$lib/router.js", async () => {
  const actual = await vi.importActual<typeof import("$lib/router.js")>("$lib/router.js");
  return {
    ...actual,
    push: (p: string) => pushMock(p),
    get route() {
      return routeStore;
    },
  };
});

function bar(): HTMLElement {
  return screen.getByLabelText("Breadcrumb");
}

beforeEach(() => {
  vi.clearAllMocks();
  routeStore.set("/");
  vi.mocked(api.listConnectors).mockResolvedValue([]);
  vi.mocked(api.getConnector).mockResolvedValue({
    key: "google_workspace",
    name: "Google Workspace",
    description: "",
    icon: "📁",
    fixed: false,
    op_count: 0,
    custom: false,
    rows: [],
  });
  vi.mocked(api.getConnectorRow).mockResolvedValue({
    key: "google_workspace",
    name: "Google Workspace",
    icon: "📁",
    id: "row-1",
    label: "Acme Workspace",
    disabled: false,
    rate_limit_rpm: 0,
    has_health_check: false,
    can_configure: false,
    is_admin: false,
    fields: [],
    operations: [],
    accounts: [],
    oauth: null,
    enable_sso: false,
    multi_account: false,
    allow_others_connect_sso: false,
    allow_others_configure: false,
    session_config_capable: false,
    session_config_allowed: false,
  });
  vi.mocked(api.getTestMeta).mockResolvedValue({
    key: "google_workspace",
    name: "Google Workspace",
    icon: "📁",
    id: "row-1",
    label: "Acme Workspace",
    ops: [],
    accounts: [],
  });
  vi.mocked(api.getConnectorHistory).mockResolvedValue({
    key: "google_workspace",
    name: "Google Workspace",
    id: "row-1",
    label: "Acme Workspace",
    runs: [],
    ops: [],
    users: [],
    page: 1,
    total_pages: 1,
    total: 0,
    page_size: 20,
  });
  vi.mocked(api.getJob).mockResolvedValue({
    key: "digest",
    name: "Daily Digest",
    description: "",
    icon: "📰",
    schedule: "",
    enabled: false,
    max_runs: 0,
    max_timeout_min: 30,
    total_runs: 0,
    last_status: "",
    can_configure: false,
    fields: [],
  });
  vi.mocked(api.getTool).mockResolvedValue({
    key: "summarize",
    name: "Summarizer",
    description: "",
    icon: "🧠",
    can_configure: false,
    fields: [],
  });
  vi.mocked(api.getAuditRuns).mockResolvedValue({
    runs: [],
    source: "",
    status: "",
    from: "",
    to: "",
    page: 1,
    total_pages: 1,
    total: 0,
    page_size: 20,
    summary: { total: 0, succeeded: 0, errored: 0, avg_latency_ms: 0 },
  });
});

describe("AppShell breadcrumb", () => {
  it("does not render a section-tab nav (Connectors/Jobs/Tools/Audit row)", () => {
    routeStore.set("/");
    render(App);
    expect(screen.queryByLabelText("Sections")).toBeNull();
  });

  it("connectors index shows Home / Connectors", async () => {
    routeStore.set("/");
    render(App);
    const b = bar();
    expect(b.querySelector("button")?.textContent).toBe("Home");
    expect(b.textContent).toContain("Connectors");
    expect(b.textContent).not.toContain("Jobs");
    expect(b.textContent).not.toContain("Audit");
  });

  it("connector list shows Home / <connector display name>", async () => {
    routeStore.set("/connectors/google_workspace");
    render(App);
    const b = bar();
    await vi.waitFor(() => expect(b.textContent).toContain("Google Workspace"));
    expect(b.textContent).toContain("Home");
    expect(b.textContent).not.toContain("google_workspace");
  });

  it("connector detail shows Home / <connector name> / <row label>", async () => {
    routeStore.set("/connectors/google_workspace/row-1");
    render(App);
    const b = bar();
    await vi.waitFor(() => expect(b.textContent).toContain("Acme Workspace"));
    expect(b.textContent).toContain("Home");
    expect(b.textContent).toContain("Google Workspace");
    expect(b.textContent).not.toContain("row-1");
  });

  it("connector test shows Home / <name> / <label> / Test", async () => {
    routeStore.set("/connectors/google_workspace/row-1/test");
    render(App);
    const b = await screen.findByLabelText("Breadcrumb");
    await vi.waitFor(() => expect(b.textContent).toContain("Acme Workspace"));
    expect(b.textContent).toContain("Home");
    expect(b.textContent).toContain("Google Workspace");
    expect(b.textContent).toContain("Test");
    /* Four items (Home / name / label / Test) render three separators. */
    expect(b.querySelectorAll("span[aria-hidden='true']")).toHaveLength(3);
    /* The current item (Test) is plain text, not a link. */
    expect(screen.queryByRole("button", { name: "Test" })).toBeNull();
  });

  it("connector history shows Home / <name> / <label> / History", async () => {
    routeStore.set("/connectors/google_workspace/row-1/history");
    render(App);
    const b = await screen.findByLabelText("Breadcrumb");
    await vi.waitFor(() => expect(b.textContent).toContain("Acme Workspace"));
    expect(b.textContent).toContain("Home");
    expect(b.textContent).toContain("History");
  });

  it("job detail shows Jobs / <job display name> and no Home link", async () => {
    routeStore.set("/jobs/digest");
    render(App);
    const b = await screen.findByLabelText("Breadcrumb");
    await vi.waitFor(() => expect(b.textContent).toContain("Daily Digest"));
    expect(b.textContent).toContain("Jobs");
    expect(b.textContent).not.toContain("Home");
    expect(b.textContent).not.toContain("digest /");
  });

  it("tool detail shows Tools / <tool display name> and no Home link", async () => {
    routeStore.set("/tools/summarize");
    render(App);
    const b = await screen.findByLabelText("Breadcrumb");
    await vi.waitFor(() => expect(b.textContent).toContain("Summarizer"));
    expect(b.textContent).toContain("Tools");
    expect(b.textContent).not.toContain("Home");
  });

  it("audit shows Home / Audit Log", async () => {
    routeStore.set("/audit");
    render(App);
    const b = bar();
    expect(b.textContent).toContain("Home");
    expect(b.textContent).toContain("Audit Log");
  });

  it("Home link navigates to the connectors index", async () => {
    routeStore.set("/audit");
    render(App);
    await fireEvent.click(screen.getByRole("button", { name: "Home" }));
    expect(pushMock).toHaveBeenCalledWith("/");
  });
});
