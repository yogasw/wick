import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";
import ServiceDetail from "../ServiceDetail.svelte";
import * as api from "$lib/api.js";
import type { ServicePlugin } from "$lib/api.js";

vi.mock("$lib/api.js");

function makeSvc(over: Partial<ServicePlugin> = {}): ServicePlugin {
  return {
    key: "example_a2a_repeater",
    name: "A2A repeater (echo bot)",
    version: "0.1.0",
    path: "/x/example_a2a_repeater/",
    status: { state: "running", restarts: 2, started_at: "2026-10-04T01:00:00Z", last_error: "process exited" },
    routes: [{ prefix: "/", auth: "public" }, { prefix: "/api", auth: "token" }],
    capabilities: ["remote_source"],
    callback_scopes: ["notify"],
    callback_revoked: false,
    tokens: [{ id: "t1", name: "partner", hint: "9f2c", created_at: "2026-10-04T01:00:00Z" }],
    logs: ["01:00:00 [wick] running", "listening"],
    ...over,
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(api.getServicePlugin).mockResolvedValue(makeSvc());
});

describe("ServiceDetail", () => {
  it("shows status, routes, tokens and the log; Stop calls the action", async () => {
    vi.mocked(api.serviceAction).mockResolvedValue(makeSvc({ status: { state: "stopped", restarts: 2 } }));
    render(ServiceDetail, { serviceKey: "example_a2a_repeater" });
    await waitFor(() => expect(screen.getByTestId("service-state").textContent).toBe("running"));
    expect(screen.getByText("/x/example_a2a_repeater/api")).toBeTruthy();
    expect(screen.getByText("Token")).toBeTruthy();
    expect(screen.getByTestId("service-log").textContent).toContain("listening");
    await fireEvent.click(screen.getByRole("button", { name: "Stop" }));
    expect(api.serviceAction).toHaveBeenCalledWith("example_a2a_repeater", "stop");
    await waitFor(() => expect(screen.getByRole("button", { name: "Start" })).toBeTruthy());
  });

  it("Generate shows the secret once; Rotate and Revoke hit the token", async () => {
    vi.mocked(api.generateServiceToken).mockResolvedValue({ token: { id: "t2", name: "ci", hint: "abcd", created_at: "" }, secret: "wick_svc_secret" });
    vi.mocked(api.rotateServiceToken).mockResolvedValue({ token: { id: "t1", name: "partner", hint: "eeee", created_at: "" }, secret: "wick_svc_rotated" });
    vi.mocked(api.revokeServiceToken).mockResolvedValue(undefined);
    render(ServiceDetail, { serviceKey: "example_a2a_repeater" });
    await waitFor(() => expect(screen.getByText("Generate token")).toBeTruthy());
    await fireEvent.click(screen.getByText("Generate token"));
    await waitFor(() => expect(screen.getByTestId("token-secret").textContent).toContain("wick_svc_secret"));
    expect(api.generateServiceToken).toHaveBeenCalledWith("example_a2a_repeater", "token");
    await fireEvent.click(screen.getByRole("button", { name: "Rotate" }));
    await waitFor(() => expect(screen.getByTestId("token-secret").textContent).toContain("wick_svc_rotated"));
    await fireEvent.click(screen.getAllByRole("button", { name: "Revoke" })[0]);
    expect(api.revokeServiceToken).toHaveBeenCalledWith("example_a2a_repeater", "t1");
  });

  it("config form: secret never prefilled, Save sends only edits", async () => {
    const configs = [
      { key: "prefix", value: "echo: ", description: "Reply prefix", is_secret: false, has_value: true, required: false },
      { key: "api_key", value: "", is_secret: true, has_value: true, required: true },
    ];
    vi.mocked(api.getServicePlugin).mockResolvedValue(makeSvc({ configs }));
    vi.mocked(api.setServiceConfig).mockResolvedValue(makeSvc({ configs }));
    const { container } = render(ServiceDetail, { serviceKey: "example_a2a_repeater" });
    await waitFor(() => expect(screen.getByTestId("service-config")).toBeTruthy());
    expect(screen.getByText("Stored — leave blank to keep.")).toBeTruthy();
    const secret = container.querySelector<HTMLInputElement>('input[type="password"]')!;
    expect(secret.value).toBe("");
    const save = screen.getByRole("button", { name: "Save configuration" }) as HTMLButtonElement;
    expect(save.disabled).toBe(true);
    await fireEvent.input(secret, { target: { value: "new-key" } });
    await waitFor(() => expect(save.disabled).toBe(false));
    await fireEvent.click(save);
    expect(api.setServiceConfig).toHaveBeenCalledWith("example_a2a_repeater", { api_key: "new-key" });
  });
});
