import { describe, test, expect, vi } from "vitest";
import { render, screen } from "@testing-library/svelte";
import type { ProjectOption, ProviderOption, SessionListItem } from "../../types/agents.js";

vi.mock("../../router.js", () => ({
  push: vi.fn(),
}));

import ProjectLanding from "../ProjectLanding.svelte";

const PROJECT: ProjectOption = { id: "proj-42", name: "Acme API", path: "" };

const PROVIDER: ProviderOption = { type: "anthropic", name: "Claude Sonnet", version: "claude-sonnet-4" };

function makeSession(id: string): SessionListItem {
  return {
    id,
    label: `Chat ${id}`,
    status: "idle",
    project_id: "proj-42",
    active_agent: "claude",
    created_at: "2026-01-01T00:00:00Z",
    last_active: "2026-01-02T00:00:00Z",
    lifecycle: "idle",
  };
}

describe("ProjectLanding — presentational rendering", () => {
  const baseProps = {
    base: "/tools/agents",
    project: PROJECT,
    providers: [PROVIDER],
    sessions: [makeSession("s1"), makeSession("s2"), makeSession("s3")],
    onPin: vi.fn(),
    onSelectSession: vi.fn(),
  };

  test("renders project name as heading", () => {
    render(ProjectLanding, { props: baseProps });
    expect(screen.getByRole("heading", { name: "Acme API" })).toBeDefined();
  });

  test("renders chat count derived from sessions length", () => {
    render(ProjectLanding, { props: baseProps });
    expect(screen.getByText(/3 chats/)).toBeDefined();
  });

  test("shows 'managed' when project path is empty", () => {
    render(ProjectLanding, { props: baseProps });
    expect(screen.getByText(/managed/)).toBeDefined();
  });

  test("shows path label when project has a non-empty path", () => {
    const customProject = { ...PROJECT, path: "/home/user/acme" };
    render(ProjectLanding, { props: { ...baseProps, project: customProject } });
    expect(screen.getByText(/\/home\/user\/acme/)).toBeDefined();
  });

  test("renders compose form with action pointing to base + '/'", () => {
    const { container } = render(ProjectLanding, { props: baseProps });
    const form = container.querySelector("form");
    expect(form).not.toBeNull();
    expect(form?.getAttribute("action")).toBe("/tools/agents/");
    expect(form?.getAttribute("method")).toBe("POST");
  });

  test("compose form contains hidden project_id input with correct value", () => {
    const { container } = render(ProjectLanding, { props: baseProps });
    const hidden = container.querySelector("input[name='project_id']") as HTMLInputElement | null;
    expect(hidden).not.toBeNull();
    expect(hidden?.type).toBe("hidden");
    expect(hidden?.value).toBe("proj-42");
  });

  test("compose form contains a message textarea", () => {
    const { container } = render(ProjectLanding, { props: baseProps });
    const ta = container.querySelector("textarea[name='message']");
    expect(ta).not.toBeNull();
  });

  test("renders a Pin as default button", () => {
    render(ProjectLanding, { props: baseProps });
    expect(screen.getByRole("button", { name: /pin as default/i })).toBeDefined();
  });

  test("renders a Settings link pointing to the project settings page", () => {
    const { container } = render(ProjectLanding, { props: baseProps });
    const link = container.querySelector(`a[href='/tools/agents/projects/proj-42']`);
    expect(link).not.toBeNull();
  });

  test("renders a session list item for each session", () => {
    render(ProjectLanding, { props: baseProps });
    expect(screen.getByText("Chat s1")).toBeDefined();
    expect(screen.getByText("Chat s2")).toBeDefined();
    expect(screen.getByText("Chat s3")).toBeDefined();
  });

  test("shows 0 chats when sessions array is empty", () => {
    render(ProjectLanding, { props: { ...baseProps, sessions: [] } });
    expect(screen.getByText(/0 chats/)).toBeDefined();
  });
});
