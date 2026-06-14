import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import ComposerToolbar from "../ComposerToolbar.svelte";
import type { ProviderOption, ProjectOption } from "../../types/agents.js";

const PROVIDERS: ProviderOption[] = [
  { type: "anthropic", name: "Claude Sonnet", version: "claude-sonnet-4" },
  { type: "openai", name: "GPT-4o", version: "gpt-4o" },
];

const PROJECTS: ProjectOption[] = [
  { id: "proj-1", name: "My Project", path: "/home/user/project" },
  { id: "proj-2", name: "Other Project", path: "/home/user/other" },
];

describe("ComposerToolbar", () => {
  test("renders provider button with active provider label", () => {
    render(ComposerToolbar, {
      props: {
        providers: PROVIDERS,
        projects: PROJECTS,
        activeProvider: "anthropic",
        activeProjectId: "proj-1",
        onProviderChange: vi.fn(),
        onProjectChange: vi.fn(),
      },
    });
    expect(screen.getByText("anthropic")).toBeDefined();
  });

  test("provider dropdown opens when provider button is clicked", async () => {
    render(ComposerToolbar, {
      props: {
        providers: PROVIDERS,
        projects: PROJECTS,
        activeProvider: "anthropic",
        activeProjectId: null,
        onProviderChange: vi.fn(),
        onProjectChange: vi.fn(),
      },
    });
    const providerBtn = screen.getByRole("button", { name: /select provider/i });
    await fireEvent.click(providerBtn);
    expect(screen.getByText("openai")).toBeDefined();
  });

  test("clicking a provider option calls onProviderChange with provider type", async () => {
    const onProviderChange = vi.fn();
    render(ComposerToolbar, {
      props: {
        providers: PROVIDERS,
        projects: PROJECTS,
        activeProvider: "anthropic",
        activeProjectId: null,
        onProviderChange,
        onProjectChange: vi.fn(),
      },
    });
    const providerBtn = screen.getByRole("button", { name: /select provider/i });
    await fireEvent.click(providerBtn);

    const openaiOption = screen.getByRole("button", { name: /openai/i });
    await fireEvent.click(openaiOption);

    expect(onProviderChange).toHaveBeenCalledOnce();
    expect(onProviderChange).toHaveBeenCalledWith("openai");
  });

  test("renders project button with active project name", () => {
    render(ComposerToolbar, {
      props: {
        providers: PROVIDERS,
        projects: PROJECTS,
        activeProvider: "anthropic",
        activeProjectId: "proj-1",
        onProviderChange: vi.fn(),
        onProjectChange: vi.fn(),
      },
    });
    expect(screen.getByText("My Project")).toBeDefined();
  });

  test("clicking a project option calls onProjectChange with project id", async () => {
    const onProjectChange = vi.fn();
    render(ComposerToolbar, {
      props: {
        providers: PROVIDERS,
        projects: PROJECTS,
        activeProvider: "anthropic",
        activeProjectId: "proj-1",
        onProviderChange: vi.fn(),
        onProjectChange,
      },
    });
    const projectBtn = screen.getByRole("button", { name: /select project/i });
    await fireEvent.click(projectBtn);

    const otherProject = screen.getByRole("button", { name: /Other Project/i });
    await fireEvent.click(otherProject);

    expect(onProjectChange).toHaveBeenCalledOnce();
    expect(onProjectChange).toHaveBeenCalledWith("proj-2");
  });

  test("notification bell button is rendered", () => {
    render(ComposerToolbar, {
      props: {
        providers: PROVIDERS,
        projects: PROJECTS,
        activeProvider: "anthropic",
        activeProjectId: null,
        onProviderChange: vi.fn(),
        onProjectChange: vi.fn(),
      },
    });
    const bell = screen.getByRole("button", { name: /notifications/i });
    expect(bell).toBeDefined();
  });

  test("project dropdown shows (none) option", async () => {
    render(ComposerToolbar, {
      props: {
        providers: PROVIDERS,
        projects: PROJECTS,
        activeProvider: "anthropic",
        activeProjectId: "proj-1",
        onProviderChange: vi.fn(),
        onProjectChange: vi.fn(),
      },
    });
    const projectBtn = screen.getByRole("button", { name: /select project/i });
    await fireEvent.click(projectBtn);
    expect(screen.getByText(/no project/i)).toBeDefined();
  });

  test("clicking (none) project option calls onProjectChange with null", async () => {
    const onProjectChange = vi.fn();
    render(ComposerToolbar, {
      props: {
        providers: PROVIDERS,
        projects: PROJECTS,
        activeProvider: "anthropic",
        activeProjectId: "proj-1",
        onProviderChange: vi.fn(),
        onProjectChange,
      },
    });
    const projectBtn = screen.getByRole("button", { name: /select project/i });
    await fireEvent.click(projectBtn);

    const noneBtn = screen.getByRole("button", { name: /no project/i });
    await fireEvent.click(noneBtn);

    expect(onProjectChange).toHaveBeenCalledWith(null);
  });
});
