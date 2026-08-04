import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import { emptyAgentProfile, type AgentProfile } from "@wick-fe/common-api";
import AgentModelQuickChange from "../AgentModelQuickChange.svelte";

function profile(overrides: Partial<AgentProfile> = {}): AgentProfile {
  return { ...emptyAgentProfile(), key: "researcher", name: "Researcher", ...overrides };
}

const LIST = [
  { type: "wick", name: "x", models: [] },
  { type: "claude", name: "claude", models: [] },
];

describe("AgentModelQuickChange", () => {
  // Nothing picked yet is not a change: saving would rewrite the role with
  // the values it already has.
  test("Save is disabled until the pick actually differs", () => {
    render(AgentModelQuickChange, {
      profile: profile({ provider: "wick/x", model: "set1@gpt-5" }),
      providerList: LIST,
      onsave: vi.fn(),
      onclose: vi.fn(),
    });
    const save = screen.getByRole("button", { name: /^Save$/ }) as HTMLButtonElement;
    expect(save.disabled).toBe(true);
  });

  // Provider and model are committed as one pair. Saving them from different
  // levels is how a pin ends up addressed to the wrong instance's registry.
  test("saves the provider and model together", async () => {
    const onsave = vi.fn();
    const loadModels = vi
      .fn()
      .mockResolvedValue([{ id: "m1", label: "Model One", default: true }]);
    render(AgentModelQuickChange, {
      profile: profile({ provider: "claude/claude", model: "" }),
      providerList: LIST,
      onsave,
      onclose: vi.fn(),
      loadModels,
    });

    // Open the picker, drill into wick/x, choose its model.
    await fireEvent.click(screen.getByRole("button", { name: /claude/i }));
    await fireEvent.click(screen.getByRole("button", { name: /wick/i }));
    await fireEvent.click(await screen.findByText("Model One"));
    await fireEvent.click(screen.getByRole("button", { name: /^Save$/ }));

    expect(onsave).toHaveBeenCalledOnce();
    const saved = onsave.mock.calls[0][0] as AgentProfile;
    expect(saved.provider).toBe("wick/x");
    expect(saved.model).toBe("m1");
  });

  // Only the two fields this dialog is about may change — it is a shortcut
  // past the full editor, not a partial rewrite of the role.
  test("leaves the rest of the role untouched", async () => {
    const onsave = vi.fn();
    const loadModels = vi.fn().mockResolvedValue([{ id: "m1", label: "Model One", default: true }]);
    render(AgentModelQuickChange, {
      profile: profile({
        provider: "claude/claude",
        model: "",
        system_prompt: "keep me",
        default_max_turns: 9,
      }),
      providerList: LIST,
      onsave,
      onclose: vi.fn(),
      loadModels,
    });

    await fireEvent.click(screen.getByRole("button", { name: /claude/i }));
    await fireEvent.click(screen.getByRole("button", { name: /wick/i }));
    await fireEvent.click(await screen.findByText("Model One"));
    await fireEvent.click(screen.getByRole("button", { name: /^Save$/ }));

    const saved = onsave.mock.calls[0][0] as AgentProfile;
    expect(saved.system_prompt).toBe("keep me");
    expect(saved.default_max_turns).toBe(9);
    expect(saved.key).toBe("researcher");
  });

  test("Cancel closes without saving", async () => {
    const onsave = vi.fn();
    const onclose = vi.fn();
    render(AgentModelQuickChange, {
      profile: profile({ provider: "wick/x" }),
      providerList: LIST,
      onsave,
      onclose,
    });
    await fireEvent.click(screen.getByRole("button", { name: /cancel/i }));
    expect(onclose).toHaveBeenCalledOnce();
    expect(onsave).not.toHaveBeenCalled();
  });

  // A role saved before instances existed holds a bare type; it has to match
  // a real option rather than render as "(unavailable)".
  test("promotes a legacy bare provider to its canonical instance", () => {
    render(AgentModelQuickChange, {
      profile: profile({ provider: "claude", model: "" }),
      providerList: LIST,
      onsave: vi.fn(),
      onclose: vi.fn(),
    });
    expect(screen.queryByText(/unavailable/i)).toBeNull();
  });
});
