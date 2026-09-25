import { describe, expect, test, vi } from "vitest";
import { render, screen } from "@testing-library/svelte";

import Settings from "../Settings.svelte";
import { emptySettings, externalExample, externalStatus, rejectionLine } from "../settings.js";
import type { ExternalState } from "../types.js";

/* External access as the user meets it.

   The switch behind this block makes a store of client session transcripts
   readable from off the machine, so the assertions are about what the screen
   SAYS before the click and what it refuses to show afterwards — not about
   whether a function returns a string. */

const noop = () => {};

const baseProps = {
  form: emptySettings(),
  stored: emptySettings(),
  lock: null,
  defaultPort: 49374,
  running: true,
  restartPending: false,
  saving: false,
  busy: false,
  test: null,
  sweep: null,
  sweepError: "",
  advanced: false,
  canManage: true,
  onField: vi.fn(),
  onSave: noop,
  onReset: noop,
  onTest: noop,
  onCaptureAssistant: noop,
  onPreviewSweep: noop,
  onRunSweep: noop,
  onToggleAdvanced: noop,
};

const CLOSED: ExternalState = {
  enabled: false,
  has_token: false,
  url: "https://wick.example/agentmemory/ai-memory",
  paths: ["/api/", "/mcp"],
  allowed_total: 0,
  rejected_total: 0,
  recent: [],
};

const OPEN: ExternalState = { ...CLOSED, enabled: true, has_token: true };

describe("the sentence next to the switch", () => {
  test("the block is there for an admin, and says what it holds BEFORE the click", () => {
    render(Settings, { props: { ...baseProps, external: CLOSED } });
    expect(screen.getByText("External access")).toBeDefined();
    // The consequence, stated on the screen rather than learned later.
    expect(screen.getAllByText(/inside client sessions/i).length).toBeGreaterThan(0);
  });

  test("a viewer gets no control at all — the admin-only line stands in for it", () => {
    render(Settings, { props: { ...baseProps, canManage: false, external: OPEN } });
    expect(screen.queryByText("External access")).toBeNull();
    expect(screen.queryByText("Create token")).toBeNull();
    expect(screen.queryByText("Revoke")).toBeNull();
    expect(screen.queryAllByText(/restricted to admins/i).length).toBeGreaterThan(0);
  });

  test("the status line distinguishes the three states", () => {
    expect(externalStatus(null)).toMatch(/loading/i);
    expect(externalStatus(CLOSED)).toMatch(/no access token/i);
    expect(externalStatus({ ...CLOSED, has_token: true })).toMatch(/switch is off/i);
    expect(externalStatus(OPEN)).toMatch(/^Open\./);
  });

  test("the address is a call that can be copied, not a description", () => {
    expect(externalExample(OPEN)).toContain("Authorization: Bearer");
    expect(externalExample(OPEN)).toContain("https://wick.example/agentmemory/ai-memory");
    expect(externalExample(null)).toBe("");
  });
});

describe("the token", () => {
  test("it is rendered once, when it is minted", () => {
    render(Settings, { props: { ...baseProps, external: OPEN, mintedToken: "agmem_abc123" } });
    expect(screen.getByTestId("minted-token").textContent).toContain("agmem_abc123");
    expect(screen.getAllByText(/only time it is shown/i).length).toBeGreaterThan(0);
  });

  test("a later render with a token stored never echoes it", () => {
    const { container } = render(Settings, { props: { ...baseProps, external: OPEN } });
    expect(screen.queryByTestId("minted-token")).toBeNull();
    expect(container.textContent).not.toContain("agmem_");
    // …and the page still says one exists, so nobody mints a second by
    // mistake.
    expect(screen.getAllByText(/A token exists/i).length).toBeGreaterThan(0);
  });

  test("revoke is offered only while a token exists, and clears the one on screen", async () => {
    const onRevokeToken = vi.fn();
    const { rerender } = render(Settings, {
      props: { ...baseProps, external: OPEN, mintedToken: "agmem_abc123", onRevokeToken },
    });
    screen.getByText("Revoke").click();
    expect(onRevokeToken).toHaveBeenCalled();

    // What the app does with that: the token is gone, the switch is closed,
    // and the value is no longer anywhere on the page.
    await rerender({ ...baseProps, external: CLOSED, mintedToken: "", onRevokeToken });
    expect(screen.queryByTestId("minted-token")).toBeNull();
    expect(screen.queryByText("Revoke")).toBeNull();
    expect(screen.getAllByText(/No token yet/i).length).toBeGreaterThan(0);
  });
});

describe("refusals", () => {
  test("a refusal reads as a reason, not a status code", () => {
    const line = rejectionLine({
      time_ms: 1_790_000_000_000,
      method: "GET",
      path: "/api/v1/projects",
      client_ip: "203.0.113.7",
      reason: "wrong-token",
      status: 401,
    });
    expect(line).toContain("203.0.113.7");
    expect(line).toContain("the token did not match");
  });

  test("an unknown reason is shown as the server sent it rather than swallowed", () => {
    expect(
      rejectionLine({ time_ms: 1, method: "POST", path: "/mcp", client_ip: "10.0.0.2", reason: "something-new", status: 403 }),
    ).toContain("something-new");
  });

  test("the panel lists what was refused", () => {
    const refused: ExternalState = {
      ...OPEN,
      allowed_total: 2,
      rejected_total: 1,
      recent: [
        {
          time_ms: 1_790_000_000_000,
          method: "GET",
          path: "/api/v1/projects",
          client_ip: "203.0.113.7",
          reason: "wrong-token",
          status: 401,
        },
      ],
    };
    render(Settings, { props: { ...baseProps, external: refused } });
    expect(screen.getAllByText(/the token did not match/i).length).toBeGreaterThan(0);
  });
});
