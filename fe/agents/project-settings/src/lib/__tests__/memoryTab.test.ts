import { describe, test, expect, afterEach } from "vitest";
import { render, screen } from "@testing-library/svelte";

import App from "../../App.svelte";

/* The project's Agent Memory lives HERE, next to its other settings — not on
   the global panel with a filter on it (Yoga, 2026-09-25). What is worth
   pinning is the tab's gate and its deep link: the entry in the project's
   "⋯" menu points at ?tab=memory, and a reader who cannot see the feature
   must not be shown a tab that 404s. */

function mountShell(attrs: Record<string, string>): void {
  document.getElementById("app")?.remove();
  const el = document.createElement("div");
  el.id = "app";
  for (const [k, v] of Object.entries(attrs)) el.dataset[k] = v;
  document.body.appendChild(el);
}

function withURL(search: string): void {
  window.history.replaceState({}, "", `/tools/agents/projects/p1${search}`);
}

afterEach(() => {
  document.getElementById("app")?.remove();
  withURL("");
});

describe("project settings — the Agent Memory tab", () => {
  test("is offered when the shell says the feature is on for this reader", () => {
    mountShell({ base: "/tools/agents", projectId: "p1", agentMemory: "true", canManage: "true" });
    withURL("");
    render(App);
    expect(screen.getByTestId("tab-memory")).toBeDefined();
  });

  test("is absent when the feature is off, and ?tab=memory falls back to General", () => {
    mountShell({ base: "/tools/agents", projectId: "p1", agentMemory: "false", canManage: "false" });
    withURL("?tab=memory");
    render(App);
    expect(screen.queryByTestId("tab-memory")).toBeNull();
    // Not a blank page: the reader lands on the tab that does exist.
    expect(screen.getByText("General")).toBeDefined();
  });

  test("the project menu's deep link opens the tab directly", () => {
    mountShell({ base: "/tools/agents", projectId: "p1", agentMemory: "true", canManage: "true" });
    withURL("?tab=memory");
    render(App);
    const tab = screen.getByTestId("tab-memory");
    // The selected tab carries the underline class the other tabs drop.
    expect(tab.className).toContain("after:bg-green-500");
  });
});
