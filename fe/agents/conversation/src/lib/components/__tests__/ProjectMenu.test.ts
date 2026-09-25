import { describe, test, expect, vi, afterEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";

import ProjectMenu from "../ProjectMenu.svelte";

/* The "⋯" menu's Agent Memory entry (PLAN §22).

   What is worth testing is the gate and the target, because both fail
   silently: an entry shown on an install where the feature is off leads to a
   404, and an entry carrying anything other than the wick project id would
   open the panel on a bucket nobody writes to. */

const PROJECT = { id: "proj-42", name: "Acme API", path: "/managed/path", managed: true, pinned: false };

const baseProps = { base: "/tools/agents", project: PROJECT, chatCount: 3, onPin: vi.fn() };

// The flag rides on #app, the way the Go shell inlines it.
function setAgentMemory(on: boolean | null): void {
  document.getElementById("app")?.remove();
  const el = document.createElement("div");
  el.id = "app";
  if (on !== null) el.dataset.agentMemory = String(on);
  document.body.appendChild(el);
}

async function openMenu(): Promise<void> {
  await fireEvent.click(screen.getByTestId("project-menu"));
}

afterEach(() => {
  document.getElementById("app")?.remove();
});

describe("ProjectMenu — Agent Memory entry", () => {
  test("is absent when the shell says the feature is off or unreadable", async () => {
    setAgentMemory(false);
    render(ProjectMenu, { props: baseProps });
    await openMenu();
    expect(screen.queryByTestId("project-menu-agent-memory")).toBeNull();
  });

  test("is absent when the shell says nothing at all", async () => {
    setAgentMemory(null);
    render(ProjectMenu, { props: baseProps });
    await openMenu();
    expect(screen.queryByTestId("project-menu-agent-memory")).toBeNull();
  });

  test("links into the project's own Agent Memory tab when the feature is on", async () => {
    setAgentMemory(true);
    render(ProjectMenu, { props: baseProps });
    await openMenu();
    const link = screen.getByTestId("project-menu-agent-memory");
    // Inside the project, not the global panel with a filter on it.
    expect(link.getAttribute("href")).toBe("/tools/agents/projects/proj-42?tab=memory");
    expect(link.textContent).toContain("Agent Memory");
  });

  test("the entries it sits between are untouched", async () => {
    setAgentMemory(true);
    render(ProjectMenu, { props: baseProps });
    await openMenu();
    expect(screen.getByText("Project settings").getAttribute("href")).toBe("/tools/agents/projects/proj-42");
    expect(screen.getByText("All chats").getAttribute("href")).toBe("/tools/agents/sessions");
  });
});
