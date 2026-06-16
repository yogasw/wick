import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import Breadcrumb from "../Breadcrumb.svelte";

describe("Breadcrumb", () => {
  test("renders a labelled nav", () => {
    render(Breadcrumb, { props: { items: [{ label: "Home" }] } });
    const nav = screen.getByLabelText("Breadcrumb");
    expect(nav.tagName).toBe("NAV");
  });

  test("items with onClick render buttons that fire", async () => {
    const onClick = vi.fn();
    render(Breadcrumb, {
      props: { items: [{ label: "Home", onClick }, { label: "Current" }] },
    });
    const btn = screen.getByRole("button", { name: "Home" });
    await fireEvent.click(btn);
    expect(onClick).toHaveBeenCalled();
  });

  test("the current (last) item is plain text, not a button", () => {
    render(Breadcrumb, {
      props: { items: [{ label: "Home", onClick: vi.fn() }, { label: "Connectors" }] },
    });
    const buttons = screen.getAllByRole("button");
    expect(buttons).toHaveLength(1);
    expect(buttons[0].textContent).toBe("Home");
    const nav = screen.getByLabelText("Breadcrumb");
    expect(nav.textContent).toContain("Connectors");
  });

  test("renders a separator between items but not after the last", () => {
    const { container } = render(Breadcrumb, {
      props: {
        items: [
          { label: "Home", onClick: vi.fn() },
          { label: "Connectors", onClick: vi.fn() },
          { label: "Current" },
        ],
      },
    });
    const seps = container.querySelectorAll("span[aria-hidden='true']");
    expect(seps).toHaveLength(2);
    seps.forEach((s) => expect(s.textContent).toBe("/"));
  });

  test("a single item renders no separator", () => {
    const { container } = render(Breadcrumb, {
      props: { items: [{ label: "Only" }] },
    });
    expect(container.querySelectorAll("span[aria-hidden='true']")).toHaveLength(0);
  });

  test("truncate flag adds truncate classes to a link", () => {
    render(Breadcrumb, {
      props: {
        items: [
          { label: "Long Name", onClick: vi.fn(), truncate: true },
          { label: "Current" },
        ],
      },
    });
    const btn = screen.getByRole("button", { name: "Long Name" });
    expect(btn.className).toContain("truncate");
    expect(btn.className).toContain("max-w-[55vw]");
  });

  test("a link without truncate uses whitespace-nowrap and the green hover", () => {
    render(Breadcrumb, {
      props: { items: [{ label: "Home", onClick: vi.fn() }, { label: "Current" }] },
    });
    const btn = screen.getByRole("button", { name: "Home" });
    expect(btn.className).toContain("whitespace-nowrap");
    expect(btn.className).toContain("hover:text-green-600");
    expect(btn.className).not.toContain("truncate");
  });
});
