import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import { emptyAgentProfile, type AgentProfile } from "@wick-fe/common-api";
import AgentProfileRow from "../AgentProfileRow.svelte";

function profile(overrides: Partial<AgentProfile> = {}): AgentProfile {
  return { ...emptyAgentProfile(), key: "researcher", name: "Researcher", ...overrides };
}

/** Open the row's ⋮ menu. */
async function openMenu() {
  await fireEvent.click(screen.getByRole("button", { name: /actions for/i }));
}

/** The row body — a role="button" div, named exactly, since the ⋮ trigger's
    aria-label also contains the role name. */
function rowButton() {
  return screen.getByRole("button", { name: /^Researcher/ });
}

/** A provider/model chip, which doubles as the quick-change shortcut. */
function chip(text: string | RegExp) {
  return screen.getByRole("button", { name: text });
}

/** This project has no jest-dom, so disabled state is read off the DOM. */
function item(name: string | RegExp): HTMLButtonElement {
  return screen.getByRole("menuitem", { name }) as HTMLButtonElement;
}

describe("AgentProfileRow", () => {
  // The row IS the edit affordance: one primary action per row, instead of
  // Edit and Delete competing side by side.
  test("clicking the row opens the role", async () => {
    const onedit = vi.fn();
    render(AgentProfileRow, { profile: profile(), onedit });
    await fireEvent.click(rowButton());
    expect(onedit).toHaveBeenCalledOnce();
  });

  // Which provider and model a role runs on is the fact most often checked,
  // and it used to require opening the editor to see.
  test("shows the instance and the pinned model", () => {
    render(AgentProfileRow, {
      profile: profile({ provider: "wick/x", model: "set1@gemini-3-pro" }),
      onedit: vi.fn(),
    });
    expect(screen.getByText("wick/x")).toBeTruthy();
    // The vendor half is what the user chose; the entry id is plumbing.
    expect(screen.getByText("gemini-3-pro")).toBeTruthy();
  });

  test("says so when no model is pinned", () => {
    render(AgentProfileRow, {
      profile: profile({ provider: "claude/claude", model: "" }),
      onedit: vi.fn(),
    });
    expect(screen.getByText(/provider default model/i)).toBeTruthy();
  });

  // A locked role stays readable — that is how you find out what it does —
  // but its destructive and state-changing actions are disabled rather than
  // hidden, so the lock is visible instead of mysterious.
  test("a locked role is still openable", async () => {
    const onedit = vi.fn();
    render(AgentProfileRow, { profile: profile({ locked: true }), onedit });
    expect(screen.getByText("Locked")).toBeTruthy();
    await fireEvent.click(rowButton());
    expect(onedit).toHaveBeenCalledOnce();
  });

  test("a locked role disables delete, disable and quick change", async () => {
    render(AgentProfileRow, {
      profile: profile({ locked: true }),
      onedit: vi.fn(),
      ondelete: vi.fn(),
      ontoggle: vi.fn(),
      onchangeModel: vi.fn(),
    });
    await openMenu();
    expect(item("Delete").disabled).toBe(true);
    expect(item("Disable").disabled).toBe(true);
    expect(item(/change provider/i).disabled).toBe(true);
    // Edit stays alive: reading a locked role is allowed.
    expect(item("Edit").disabled).toBe(false);
  });

  // Disabling the item must actually prevent the action, not just grey it
  // out — a lock that only looks like a lock is worse than none.
  test("a locked role's delete cannot be fired by clicking it", async () => {
    const ondelete = vi.fn();
    const ontoggle = vi.fn();
    render(AgentProfileRow, {
      profile: profile({ locked: true }),
      onedit: vi.fn(),
      ondelete,
      ontoggle,
    });
    await openMenu();
    await fireEvent.click(item("Delete"));
    await fireEvent.click(item("Disable"));
    expect(ondelete).not.toHaveBeenCalled();
    expect(ontoggle).not.toHaveBeenCalled();
  });

  test("an unlocked role can be deleted and disabled from the menu", async () => {
    const ondelete = vi.fn();
    const ontoggle = vi.fn();
    render(AgentProfileRow, { profile: profile(), onedit: vi.fn(), ondelete, ontoggle });

    await openMenu();
    await fireEvent.click(screen.getByRole("menuitem", { name: "Disable" }));
    expect(ontoggle).toHaveBeenCalledOnce();

    await openMenu();
    await fireEvent.click(screen.getByRole("menuitem", { name: "Delete" }));
    expect(ondelete).toHaveBeenCalledOnce();
  });

  // The label reflects current state, so the menu never offers "Disable" on
  // something already disabled.
  test("offers Enable for an already-disabled role", async () => {
    render(AgentProfileRow, {
      profile: profile({ disabled: true }),
      onedit: vi.fn(),
      ontoggle: vi.fn(),
    });
    await openMenu();
    expect(screen.getByRole("menuitem", { name: "Enable" })).toBeTruthy();
  });

  // A read-only scope (a non-admin viewing global roles) must not be handed
  // write actions at all.
  test("readonly disables the write actions", async () => {
    render(AgentProfileRow, {
      profile: profile(),
      readonly: true,
      onedit: vi.fn(),
      ondelete: vi.fn(),
      ontoggle: vi.fn(),
    });
    await openMenu();
    expect(item("Delete").disabled).toBe(true);
    expect(item("Disable").disabled).toBe(true);
  });

  test("omitted handlers hide their menu items", async () => {
    render(AgentProfileRow, { profile: profile(), onedit: vi.fn() });
    await openMenu();
    expect(screen.queryByRole("menuitem", { name: "Delete" })).toBeNull();
    expect(screen.queryByRole("menuitem", { name: /change provider/i })).toBeNull();
  });

  // Swapping a model is the frequent edit, so it must not cost a trip through
  // the ⋮ menu.
  test("clicking a provider chip opens quick change directly", async () => {
    const onchangeModel = vi.fn();
    const onedit = vi.fn();
    render(AgentProfileRow, {
      profile: profile({ provider: "wick/x", model: "set1@gemini-3-pro" }),
      onedit,
      onchangeModel,
    });

    await fireEvent.click(chip("wick/x"));
    expect(onchangeModel).toHaveBeenCalledOnce();
    // The chip must not ALSO open the editor, which would render the full
    // form behind the dialog.
    expect(onedit).not.toHaveBeenCalled();
  });

  test("the model chip is a shortcut too", async () => {
    const onchangeModel = vi.fn();
    render(AgentProfileRow, {
      profile: profile({ provider: "wick/x", model: "set1@gemini-3-pro" }),
      onedit: vi.fn(),
      onchangeModel,
    });
    await fireEvent.click(chip("gemini-3-pro"));
    expect(onchangeModel).toHaveBeenCalledOnce();
  });

  // "no model pinned" is the state most worth changing, so that chip is a
  // shortcut as well rather than inert text.
  test("the default-model placeholder is clickable", async () => {
    const onchangeModel = vi.fn();
    render(AgentProfileRow, {
      profile: profile({ provider: "claude/claude", model: "" }),
      onedit: vi.fn(),
      onchangeModel,
    });
    // Exact name: the row container's accessible name includes all of its
    // text, so a substring match would also hit the row itself.
    await fireEvent.click(chip("provider default model"));
    expect(onchangeModel).toHaveBeenCalledOnce();
  });

  // A locked role's chips are inert: the lock has to hold on the shortcut too,
  // or it is trivially bypassed.
  test("a locked role's chips are not clickable", () => {
    const onchangeModel = vi.fn();
    render(AgentProfileRow, {
      profile: profile({ provider: "wick/x", locked: true }),
      onedit: vi.fn(),
      onchangeModel,
    });
    expect(screen.queryByRole("button", { name: "wick/x" })).toBeNull();
    expect(screen.getByText("wick/x")).toBeTruthy();
  });

  test("marks a project role that shadows a global one", () => {
    render(AgentProfileRow, { profile: profile(), shadowsGlobal: true, onedit: vi.fn() });
    expect(screen.getByText(/shadows global/i)).toBeTruthy();
  });
});
