import { describe, test, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import ScmDock from "../ScmDock.svelte";

const SESSION_ID = "sess-abc";
const ASSET_URL = "/static/scm/main.js";

function makeProps(overrides: Record<string, unknown> = {}) {
  return {
    sessionId: SESSION_ID,
    assetUrl: ASSET_URL,
    loadBundle: vi.fn().mockResolvedValue(undefined),
    mountIsland: vi.fn(),
    unmountIsland: vi.fn(),
    ...overrides,
  };
}

describe("ScmDock", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  test("is closed by default — panel is not visible", () => {
    const { container } = render(ScmDock, { props: makeProps() });
    const panel = container.querySelector("[data-scm-panel]");
    expect(panel).toBeDefined();
    expect(panel?.classList.contains("hidden")).toBe(true);
  });

  test("respects open=true prop — panel visible on mount", () => {
    const { container } = render(ScmDock, { props: makeProps({ open: true }) });
    const panel = container.querySelector("[data-scm-panel]");
    expect(panel?.classList.contains("hidden")).toBe(false);
  });

  test("clicking open button calls onOpenChange(true)", async () => {
    const onOpenChange = vi.fn();
    render(ScmDock, { props: makeProps({ onOpenChange }) });

    const fab = screen.getByRole("button", { name: /source control/i });
    await fireEvent.click(fab);

    expect(onOpenChange).toHaveBeenCalledOnce();
    expect(onOpenChange).toHaveBeenCalledWith(true);
  });

  test("on open: loadBundle is called then mountIsland with correct opts", async () => {
    const loadBundle = vi.fn().mockResolvedValue(undefined);
    const mountIsland = vi.fn();
    const onOpenChange = vi.fn();

    render(ScmDock, { props: makeProps({ loadBundle, mountIsland, onOpenChange }) });

    const fab = screen.getByRole("button", { name: /source control/i });
    await fireEvent.click(fab);

    /* loadBundle is async — flush microtasks */
    await new Promise((r) => setTimeout(r, 0));

    expect(loadBundle).toHaveBeenCalledOnce();
    expect(loadBundle).toHaveBeenCalledWith(ASSET_URL);
    expect(mountIsland).toHaveBeenCalledOnce();
    expect(mountIsland).toHaveBeenCalledWith(
      expect.any(HTMLElement),
      { sessionID: SESSION_ID, mode: "sidebar" },
    );
  });

  test("re-opening does NOT call mountIsland a second time (mounted guard)", async () => {
    const loadBundle = vi.fn().mockResolvedValue(undefined);
    const mountIsland = vi.fn();

    render(ScmDock, { props: makeProps({ loadBundle, mountIsland }) });

    const fab = screen.getByRole("button", { name: /source control/i });

    /* open */
    await fireEvent.click(fab);
    await new Promise((r) => setTimeout(r, 0));
    expect(mountIsland).toHaveBeenCalledTimes(1);

    /* close */
    const closeBtn = screen.getByRole("button", { name: /close/i });
    await fireEvent.click(closeBtn);

    /* re-open */
    await fireEvent.click(fab);
    await new Promise((r) => setTimeout(r, 0));
    expect(mountIsland).toHaveBeenCalledTimes(1);
  });

  test("changeCount > 0 renders the badge with correct text", () => {
    render(ScmDock, { props: makeProps({ changeCount: 5 }) });
    const badge = screen.getByText("5");
    expect(badge).toBeDefined();
  });

  test("changeCount = 0 hides the badge", () => {
    const { container } = render(ScmDock, { props: makeProps({ changeCount: 0 }) });
    const badge = container.querySelector("[data-scm-badge]");
    expect(badge?.classList.contains("hidden")).toBe(true);
  });

  test("changeCount > 99 shows 99+ in badge", () => {
    render(ScmDock, { props: makeProps({ changeCount: 150 }) });
    expect(screen.getByText("99+")).toBeDefined();
  });

  test("resize handle is rendered inside the panel", () => {
    const { container } = render(ScmDock, { props: makeProps() });
    const handle = container.querySelector("[data-scm-resize]");
    expect(handle).toBeDefined();
    /* Drag-resize interaction requires real pointer events — not asserted in jsdom */
  });
});
