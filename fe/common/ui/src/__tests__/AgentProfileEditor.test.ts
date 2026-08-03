import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import { emptyAgentProfile, type AgentProfile } from "@wick-fe/common-api";
import AgentProfileEditor from "../AgentProfileEditor.svelte";

function profile(overrides: Partial<AgentProfile> = {}): AgentProfile {
  return { ...emptyAgentProfile(), ...overrides };
}

describe("AgentProfileEditor", () => {
  // The server refuses an empty description, because it is what a leader
  // model reads to pick a role. Blocking the save locally keeps that
  // refusal on the field instead of arriving as an opaque 400.
  test("refuses to save without a description", async () => {
    const onsave = vi.fn();
    render(AgentProfileEditor, { profile: profile({ key: "researcher" }), onsave });

    await fireEvent.click(screen.getByRole("button", { name: /save/i }));
    expect(onsave).not.toHaveBeenCalled();
    expect(await screen.findByText(/required/i)).toBeTruthy();
  });

  test("saves once key and description are present", async () => {
    const onsave = vi.fn();
    render(AgentProfileEditor, {
      profile: profile({ key: "researcher", description: "Researches things." }),
      onsave,
    });

    await fireEvent.click(screen.getByRole("button", { name: /save/i }));
    expect(onsave).toHaveBeenCalledTimes(1);
    expect(onsave.mock.calls[0][0].key).toBe("researcher");
  });

  // The server forces can_delegate off for providers it cannot lead with.
  // A checkbox that accepts a value the server drops is worse than one
  // that plainly cannot be ticked.
  test("disables can_delegate on a provider that cannot lead", () => {
    const { container } = render(AgentProfileEditor, {
      profile: profile({ key: "k", description: "d", provider: "gemini" }),
      onsave: vi.fn(),
    });
    const boxes = container.querySelectorAll('input[type="checkbox"]');
    // Order follows the template: strict_mcp, can_delegate, allow_take_over, disabled.
    expect((boxes[1] as HTMLInputElement).disabled).toBe(true);
  });

  test("leaves can_delegate editable on a leader-capable provider", () => {
    const { container } = render(AgentProfileEditor, {
      profile: profile({ key: "k", description: "d", provider: "claude" }),
      onsave: vi.fn(),
    });
    const boxes = container.querySelectorAll('input[type="checkbox"]');
    expect((boxes[1] as HTMLInputElement).disabled).toBe(false);
  });

  // Never sends a can_delegate the server would discard, even if the
  // value arrived true on a provider that cannot lead.
  test("strips can_delegate for a non-leader provider on save", async () => {
    const onsave = vi.fn();
    render(AgentProfileEditor, {
      profile: profile({ key: "k", description: "d", provider: "gemini", can_delegate: true }),
      onsave,
    });
    await fireEvent.click(screen.getByRole("button", { name: /save/i }));
    expect(onsave.mock.calls[0][0].can_delegate).toBe(false);
  });

  // Read-only is how a non-admin sees a global role: no way to submit,
  // and nothing that looks editable.
  test("readonly hides Save and disables the fields", () => {
    const { container } = render(AgentProfileEditor, {
      profile: profile({ key: "k", description: "d" }),
      readonly: true,
      onsave: vi.fn(),
    });
    expect(screen.queryByRole("button", { name: /save/i })).toBeNull();
    for (const el of container.querySelectorAll("input, textarea, select")) {
      expect((el as HTMLInputElement).disabled).toBe(true);
    }
  });

  // The key is a role's identity and is referenced by every delegation
  // that named it, so an existing profile's key is not editable.
  test("locks the key on an existing profile but not a new one", () => {
    const existing = render(AgentProfileEditor, {
      profile: profile({ id: "p1", key: "researcher", description: "d" }),
      onsave: vi.fn(),
    });
    const keyInput = existing.container.querySelector("input[type='text']") as HTMLInputElement;
    expect(keyInput.disabled).toBe(true);

    const fresh = render(AgentProfileEditor, {
      profile: profile({ key: "", description: "" }),
      onsave: vi.fn(),
    });
    const freshKey = fresh.container.querySelector("input[type='text']") as HTMLInputElement;
    expect(freshKey.disabled).toBe(false);
  });

  // Provider and model used to be two controls: a type dropdown and a free
  // text box nobody could fill correctly. One picker that descends to the
  // model replaces both, and the packed value is split on save.
  test("saves a picked provider and model into their own fields", async () => {
    const onsave = vi.fn();
    render(AgentProfileEditor, {
      profile: profile({
        key: "k",
        description: "d",
        provider: "wick/wick",
        model: "cc/claude-fable-5",
      }),
      providerList: [
        {
          type: "wick",
          name: "wick",
          models: [{ id: "cc/claude-fable-5", label: "Fable", default: true }],
        },
      ],
      onsave,
    });

    await fireEvent.click(screen.getByRole("button", { name: /save/i }));
    const saved = onsave.mock.calls[0][0];
    expect(saved.provider).toBe("wick/wick");
    expect(saved.model).toBe("cc/claude-fable-5");
  });

  // A role stored before instances existed holds a bare type. It must show
  // as the instance it means, not as a phantom "(unavailable)" option.
  test("renders a legacy bare provider as its canonical instance", () => {
    render(AgentProfileEditor, {
      profile: profile({ key: "k", description: "d", provider: "claude" }),
      providerList: [{ type: "claude", name: "claude" }],
      onsave: vi.fn(),
    });
    expect(screen.queryByText(/unavailable/i)).toBeNull();
  });

  test("there is no separate model field any more", () => {
    const { container } = render(AgentProfileEditor, {
      profile: profile({ key: "k", description: "d" }),
      providerList: [{ type: "claude", name: "claude" }],
      onsave: vi.fn(),
    });
    expect(container.textContent).not.toContain("Empty uses the provider default");
  });

  // can_delegate keys off the provider TYPE. A role pinned to an instance
  // and a model must not lose the checkbox.
  test("keeps can_delegate editable on a pinned leader-capable instance", () => {
    const { container } = render(AgentProfileEditor, {
      profile: profile({ key: "k", description: "d", provider: "wick/wick", model: "m1" }),
      providerList: [{ type: "wick", name: "wick" }],
      onsave: vi.fn(),
    });
    const boxes = container.querySelectorAll('input[type="checkbox"]');
    expect((boxes[1] as HTMLInputElement).disabled).toBe(false);
  });
});
