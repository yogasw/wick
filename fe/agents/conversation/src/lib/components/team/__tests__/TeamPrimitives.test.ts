import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import { createRawSnippet } from "svelte";
import Dots from "../Dots.svelte";
import SmBtn from "../SmBtn.svelte";
import TeamIcon from "../TeamIcon.svelte";
import { TEAM_ICONS, TEAM_ICON_NAMES } from "../icons.js";

/* P48 slice 1: the primitives the Team blocks share. */
const label = (text: string) => createRawSnippet(() => ({ render: () => `<span>${text}</span>` }));

describe("Dots", () => {
  test("three decorative dots in currentColor", () => {
    render(Dots);
    const d = screen.getByTestId("dots");
    expect(d.getAttribute("aria-hidden")).toBe("true");
    expect(d.querySelectorAll("i")).toHaveLength(3);
    expect(d.textContent).toBe("");
  });
});

describe("TeamIcon", () => {
  test("every mockup icon is there, drawn on the 16px grid", () => {
    expect(TEAM_ICON_NAMES).toEqual(["tasks", "spin", "check", "chevron", "reply", "open", "send", "msg", "jump", "retry", "recover"]);
    for (const name of TEAM_ICON_NAMES) expect(TEAM_ICONS[name]).toMatch(/stroke="currentColor"/);
  });

  test("renders the named icon at 12px by default, hidden from readers; spin turns it", () => {
    const r = render(TeamIcon, { name: "reply" });
    const svg = r.container.querySelector("svg")!;
    expect(svg.dataset.icon).toBe("reply");
    expect(svg.getAttribute("width")).toBe("12");
    expect(svg.getAttribute("viewBox")).toBe("0 0 16 16");
    expect(svg.getAttribute("aria-hidden")).toBe("true");
    expect(svg.innerHTML).toContain("M6.5 4L2.5 8l4 4");
    expect(svg.classList.contains("team-icon-spin")).toBe(false);
    r.unmount();
    const s = render(TeamIcon, { name: "spin", size: 10, spin: true });
    const spin = s.container.querySelector("svg")!;
    expect(spin.getAttribute("width")).toBe("10");
    expect(spin.classList.contains("team-icon-spin")).toBe(true);
  });
});

describe("SmBtn", () => {
  test("default: the mockup's .btn.sm with an optional icon before the label, a button by type", async () => {
    const onclick = vi.fn();
    const r = render(SmBtn, { icon: "reply", onclick, children: label("Reply in thread") });
    const b = screen.getByRole("button", { name: "Reply in thread" });
    expect(b.getAttribute("type")).toBe("button");
    expect(b.dataset.variant).toBe("default");
    expect(b.className).toMatch(/(^|\s)rounded(\s|$)/);
    for (const c of ["border", "px-2", "py-[3px]", "text-[10.5px]", "font-medium", "border-white-300", "dark:border-navy-600", "bg-white-100", "dark:bg-navy-800"]) {
      expect(b.className).toContain(c);
    }
    expect(b.querySelector("svg")?.dataset.icon).toBe("reply");
    await fireEvent.click(b);
    expect(onclick).toHaveBeenCalledOnce();
    r.unmount();
  });

  test("warn is amber (answer), danger is red text (cancel), no icon unless given; disabled passes through", () => {
    const w = render(SmBtn, { variant: "warn", children: label("Answer") });
    const warn = screen.getByRole("button", { name: "Answer" });
    for (const c of ["border-amber-500/[.45]", "bg-[rgba(251,191,36,.12)]", "text-amber-700", "dark:border-[rgba(251,191,36,.40)]", "dark:bg-[rgba(251,191,36,.10)]", "dark:text-amber-300"]) {
      expect(warn.className).toContain(c);
    }
    expect(warn.querySelector("svg")).toBeNull();
    w.unmount();
    render(SmBtn, { variant: "danger", disabled: true, children: label("Cancel") });
    const danger = screen.getByRole("button", { name: "Cancel" });
    expect(danger.className).toContain("text-red-500");
    expect((danger as HTMLButtonElement).disabled).toBe(true);
  });
});
