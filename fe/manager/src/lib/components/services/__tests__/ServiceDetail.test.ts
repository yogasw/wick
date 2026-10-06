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
    // Non-required fields say they may stay empty; required ones keep "*".
    const optional = screen.getAllByTestId("config-optional");
    expect(optional).toHaveLength(1);
    expect(optional[0].parentElement?.textContent).toContain("prefix");
    expect(screen.getByText("api_key").textContent).toContain("*");
    const secret = container.querySelector<HTMLInputElement>('input[type="password"]')!;
    expect(secret.value).toBe("");
    const save = screen.getByRole("button", { name: "Save configuration" }) as HTMLButtonElement;
    expect(save.disabled).toBe(true);
    await fireEvent.input(secret, { target: { value: "new-key" } });
    await waitFor(() => expect(save.disabled).toBe(false));
    await fireEvent.click(save);
    expect(api.setServiceConfig).toHaveBeenCalledWith("example_a2a_repeater", { api_key: "new-key" });
  });

  it("auto-off supported: badge, sleeping banner, Off override saves", async () => {
    const auto_off = { supported: true, default_idle_seconds: 900, mode: "default" as const, idle_seconds: 900, enabled: true, forced: false };
    vi.mocked(api.getServicePlugin).mockResolvedValue(makeSvc({
      auto_off,
      status: { state: "sleeping", restarts: 0, slept_at: "2026-10-06T01:00:00Z", last_active: "2026-10-06T00:45:00Z", last_wake_ms: 2300 },
    }));
    vi.mocked(api.setServiceAutoOff).mockResolvedValue(makeSvc({ auto_off: { ...auto_off, mode: "off", enabled: false, forced: true } }));
    render(ServiceDetail, { serviceKey: "example_a2a_repeater" });
    await waitFor(() => expect(screen.getByTestId("auto-off-badge").textContent).toBe("Auto-off: on (plugin default, idle 15m)"));
    expect(screen.getByTestId("sleeping-banner").textContent).toContain("Sleeping — wakes on the next request");
    expect(screen.getByTestId("sleeping-banner").textContent).toContain("last wake took ~2.3s");
    expect(screen.getByTestId("auto-off-plugin").textContent).toContain("supports auto-off");
    // Sleeping still offers Stop: an admin stop is not woken by requests.
    expect(screen.getByRole("button", { name: "Stop" })).toBeTruthy();
    await fireEvent.click(screen.getByRole("radio", { name: "Off" }));
    expect(screen.queryByTestId("auto-off-warning")).toBeNull();
    await fireEvent.click(screen.getByRole("button", { name: "Save auto-off" }));
    expect(api.setServiceAutoOff).toHaveBeenCalledWith("example_a2a_repeater", "off", 0, false);
    await waitFor(() => expect(screen.getByTestId("auto-off-badge").textContent).toBe("Auto-off: off (forced)"));
  });

  it("auto-off not supported: shows the reason; forcing On warns and needs a confirm", async () => {
    const auto_off = { supported: false, reason: "polls the job queue in the background", default_idle_seconds: 900, mode: "default" as const, idle_seconds: 900, enabled: false, forced: false };
    vi.mocked(api.getServicePlugin).mockResolvedValue(makeSvc({ auto_off }));
    vi.mocked(api.setServiceAutoOff).mockResolvedValue(makeSvc({ auto_off: { ...auto_off, mode: "on", idle_seconds: 1800, enabled: true, forced: true } }));
    render(ServiceDetail, { serviceKey: "example_a2a_repeater" });
    await waitFor(() => expect(screen.getByTestId("auto-off-badge").textContent).toBe("Auto-off: off (plugin default)"));
    expect(screen.getByTestId("auto-off-plugin").textContent).toContain("This service cannot auto-off");
    expect(screen.getByTestId("auto-off-plugin").textContent).toContain("polls the job queue in the background");
    await fireEvent.click(screen.getByRole("radio", { name: "On" }));
    expect(screen.getByTestId("auto-off-warning").textContent).toContain("The plugin says it cannot auto-off: polls the job queue in the background");
    const idle = screen.getByLabelText("Idle limit in minutes") as HTMLInputElement;
    await fireEvent.input(idle, { target: { value: "30" } });
    await fireEvent.click(screen.getByRole("button", { name: "Save auto-off" }));
    expect(api.setServiceAutoOff).not.toHaveBeenCalled();
    await fireEvent.click(await screen.findByRole("button", { name: "Force on" }));
    expect(api.setServiceAutoOff).toHaveBeenCalledWith("example_a2a_repeater", "on", 1800, true);
    await waitFor(() => expect(screen.getByTestId("auto-off-badge").textContent).toBe("Auto-off: on (forced, idle 30m)"));
  });

  it("no plugin declaration: default reason text", async () => {
    vi.mocked(api.getServicePlugin).mockResolvedValue(makeSvc({
      auto_off: { supported: false, default_idle_seconds: 900, mode: "default", idle_seconds: 900, enabled: false, forced: false },
    }));
    render(ServiceDetail, { serviceKey: "example_a2a_repeater" });
    await waitFor(() => expect(screen.getByTestId("auto-off-plugin").textContent).toContain("The plugin does not declare auto-off support."));
  });
});
