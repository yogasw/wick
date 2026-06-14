import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import AskUserModal from "../AskUserModal.svelte";
import type { AskRequest, AskAnswer } from "../../types/agents.js";

const SINGLE_Q: AskRequest = {
  id: "ask-1",
  question: "Which environment?",
  options: [
    { label: "Production", value: "prod" },
    { label: "Staging", value: "staging" },
  ],
};

const SINGLE_Q_FREEFORM: AskRequest = {
  id: "ask-2",
  question: "Name?",
  options: [],
  allow_freeform: true,
};

const WIZARD_TEXT: AskRequest = {
  id: "ask-3",
  fields: [
    { key: "name", label: "Your name", type: "text", required: true },
    { key: "email", label: "Your email", type: "text" },
  ],
};

const WIZARD_CHOICE: AskRequest = {
  id: "ask-4",
  fields: [
    {
      key: "env",
      label: "Environment",
      type: "choice",
      options: [
        { label: "Production", value: "prod" },
        { label: "Staging", value: "staging" },
      ],
    },
  ],
};

const WIZARD_RANK: AskRequest = {
  id: "ask-5",
  fields: [
    {
      key: "priority",
      label: "Rank priority",
      type: "rank",
      options: [
        { label: "Speed", value: "speed" },
        { label: "Cost", value: "cost" },
      ],
    },
  ],
};

describe("AskUserModal", () => {
  test("renders nothing when request is null", () => {
    const { container } = render(AskUserModal, {
      props: { request: null, onSubmit: vi.fn() },
    });
    expect(container.querySelector("div")).toBeNull();
  });

  test("single-question: renders question and option buttons", () => {
    render(AskUserModal, {
      props: { request: SINGLE_Q, onSubmit: vi.fn() },
    });
    expect(screen.getByText("Which environment?")).toBeDefined();
    expect(screen.getByText("Production")).toBeDefined();
    expect(screen.getByText("Staging")).toBeDefined();
  });

  test("single-question: clicking an option calls onSubmit with {id, value}", async () => {
    const onSubmit = vi.fn();
    render(AskUserModal, {
      props: { request: SINGLE_Q, onSubmit },
    });
    await fireEvent.click(screen.getByText("Production"));
    expect(onSubmit).toHaveBeenCalledOnce();
    expect(onSubmit).toHaveBeenCalledWith({ id: "ask-1", value: "prod" } satisfies AskAnswer);
  });

  test("single-question with allow_freeform: typing + submit calls onSubmit with {id, text}", async () => {
    const onSubmit = vi.fn();
    render(AskUserModal, {
      props: { request: SINGLE_Q_FREEFORM, onSubmit },
    });
    const input = screen.getByPlaceholderText("Type your answer…");
    await fireEvent.input(input, { target: { value: "my answer" } });
    const form = input.closest("form")!;
    await fireEvent.submit(form);
    expect(onSubmit).toHaveBeenCalledOnce();
    expect(onSubmit).toHaveBeenCalledWith({ id: "ask-2", text: "my answer" } satisfies AskAnswer);
  });

  test("single-question with allow_freeform: empty freeform shows error and does NOT submit", async () => {
    const onSubmit = vi.fn();
    render(AskUserModal, {
      props: { request: SINGLE_Q_FREEFORM, onSubmit },
    });
    const input = screen.getByPlaceholderText("Type your answer…");
    const form = input.closest("form")!;
    await fireEvent.submit(form);
    expect(onSubmit).not.toHaveBeenCalled();
    expect(screen.getByText("Type an answer, or click one of the options above.")).toBeDefined();
  });

  test("wizard: renders label of first field", () => {
    render(AskUserModal, {
      props: { request: WIZARD_TEXT, onSubmit: vi.fn() },
    });
    expect(screen.getByText(/Your name/)).toBeDefined();
  });

  test("wizard: required text field empty → Next shows error and does not advance", async () => {
    render(AskUserModal, {
      props: { request: WIZARD_TEXT, onSubmit: vi.fn() },
    });
    expect(screen.getByText(/Your name/)).toBeDefined();
    const nextBtn = screen.getByText("Next");
    await fireEvent.click(nextBtn);
    expect(screen.getByText(/"Your name" is required/)).toBeDefined();
    expect(screen.queryByText(/Your email/)).toBeNull();
  });

  test("wizard: filling required text field + Next advances to next step", async () => {
    render(AskUserModal, {
      props: { request: WIZARD_TEXT, onSubmit: vi.fn() },
    });
    const input = screen.getByRole<HTMLInputElement>("textbox");
    await fireEvent.input(input, { target: { value: "Alice" } });
    await fireEvent.click(screen.getByText("Next"));
    expect(screen.getByText(/Your email/)).toBeDefined();
  });

  test("wizard: last-step Submit calls onSubmit with {id, values}", async () => {
    const onSubmit = vi.fn();
    render(AskUserModal, {
      props: { request: WIZARD_TEXT, onSubmit },
    });
    const input = screen.getByRole<HTMLInputElement>("textbox");
    await fireEvent.input(input, { target: { value: "Alice" } });
    await fireEvent.click(screen.getByText("Next"));
    const emailInput = screen.getByRole<HTMLInputElement>("textbox");
    await fireEvent.input(emailInput, { target: { value: "alice@example.com" } });
    await fireEvent.click(screen.getByText("Submit"));
    expect(onSubmit).toHaveBeenCalledOnce();
    const call = onSubmit.mock.calls[0][0] as { id: string; values: Record<string, string> };
    expect(call.id).toBe("ask-3");
    expect(call.values).toEqual({ name: "Alice", email: "alice@example.com" });
  });

  test("wizard choice: clicking option records it and calls onSubmit on single-field form", async () => {
    const onSubmit = vi.fn();
    render(AskUserModal, {
      props: { request: WIZARD_CHOICE, onSubmit },
    });
    expect(screen.getByText(/Environment/)).toBeDefined();
    await fireEvent.click(screen.getByText("Production"));
    await new Promise((r) => setTimeout(r, 200));
    expect(onSubmit).toHaveBeenCalledOnce();
    const call = onSubmit.mock.calls[0][0] as { id: string; values: Record<string, string> };
    expect(call.id).toBe("ask-4");
    expect(call.values.env).toBe("prod");
  });

  test("wizard rank: renders draggable items (drag-drop not exhaustively tested)", () => {
    render(AskUserModal, {
      props: { request: WIZARD_RANK, onSubmit: vi.fn() },
    });
    expect(screen.getByText("Speed")).toBeDefined();
    expect(screen.getByText("Cost")).toBeDefined();
  });
});
