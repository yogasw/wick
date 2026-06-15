import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import { createRawSnippet } from "svelte";
import Button from "../Button.svelte";

const label = (text: string) =>
  createRawSnippet(() => ({ render: () => `<span>${text}</span>` }));

describe("Button", () => {
  test("renders children and fires onclick", async () => {
    const onclick = vi.fn();
    render(Button, { props: { onclick, children: label("Save") } });
    const btn = screen.getByRole("button");
    expect(btn.textContent).toContain("Save");
    await fireEvent.click(btn);
    expect(onclick).toHaveBeenCalled();
  });

  test("disabled prevents click and sets the attribute", async () => {
    const onclick = vi.fn();
    render(Button, { props: { onclick, disabled: true, children: label("X") } });
    const btn = screen.getByRole("button") as HTMLButtonElement;
    expect(btn.disabled).toBe(true);
    await fireEvent.click(btn);
    expect(onclick).not.toHaveBeenCalled();
  });

  test("variant + size apply distinct classes", () => {
    const { container } = render(Button, {
      props: { variant: "danger", size: "lg", children: label("Del") },
    });
    const btn = container.querySelector("button") as HTMLButtonElement;
    expect(btn.className).toContain("bg-rose-500");
    expect(btn.className).toContain("px-4");
  });

  test("type submit is honored", () => {
    render(Button, { props: { type: "submit", children: label("Go") } });
    expect((screen.getByRole("button") as HTMLButtonElement).type).toBe("submit");
  });
});
