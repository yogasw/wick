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
  test("three decorative dots in currentColor, no test id (a list can hold many)", () => {
    const { container } = render(Dots);
    const d = container.querySelector<HTMLElement>(".dots")!;
    expect(d.dataset.testid).toBeUndefined();
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
    expect(danger.className).toContain("text-red-600");
    expect(danger.className).toContain("dark:text-red-400");
    expect((danger as HTMLButtonElement).disabled).toBe(true);
  });

  test("warn has a hover state", () => {
    render(SmBtn, { variant: "warn", children: label("Answer") });
    const warn = screen.getByRole("button", { name: "Answer" });
    expect(warn.className).toContain("hover:bg-[rgba(251,191,36,.22)]");
    expect(warn.className).toContain("dark:hover:bg-[rgba(251,191,36,.18)]");
  });

  test("class takes a string, an array or an object, never [object Object]", () => {
    const a = render(SmBtn, { class: "shrink-0", children: label("A") });
    expect(screen.getByRole("button", { name: "A" }).className).toContain("shrink-0");
    a.unmount();
    const b = render(SmBtn, { class: ["ml-auto", { "opacity-50": true, hidden: false }], children: label("B") });
    const btn = screen.getByRole("button", { name: "B" });
    expect(btn.className).not.toContain("[object Object]");
    expect(btn.className).toContain("ml-auto");
    expect(btn.className).toContain("opacity-50");
    expect(btn.className).not.toMatch(/(^|\s)hidden(\s|$)/);
    expect(btn.className).toContain("rounded");
    b.unmount();
    render(SmBtn, { class: { "w-full": true }, children: label("C") });
    expect(screen.getByRole("button", { name: "C" }).className).toContain("w-full");
  });
});
