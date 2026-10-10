import { describe, test, expect } from "vitest";
import { render } from "@testing-library/svelte";
import RailTasksIcon from "../RailTasksIcon.svelte";
import RailTasksTip from "../RailTasksTip.svelte";
import { railTabName } from "../../../railPrefs.js";

const ICON = '<path d="M2 4h12"/>';

/* P48 rail: the Tasks tab's marks and tooltip, as the combined mockup §5. */
describe("RailTasksIcon", () => {
  test("one working + one needing input: green badge, amber dot, ring", () => {
    const { getByTestId } = render(RailTasksIcon, { icon: ICON, count: 2, needsInput: 1, working: true });
    const badge = getByTestId("rail-badge-tasks");
    expect(badge.textContent).toBe("2");
    // Always green: the amber dot, not the badge, says a task needs input.
    expect(badge.className).toContain("bg-green-500");
    expect(badge.className).not.toContain("bg-amber-500");
    expect(badge.className).toContain("text-white-100");
    expect(badge.className).toContain("text-[9px]");
    expect(badge.className).toContain("font-semibold");
    // Out to -6px around the ring.
    expect(badge.className).toContain("-top-1.5");
    expect(badge.className).toContain("-right-1.5");
    const dot = getByTestId("rail-dot-tasks");
    expect(dot.className).toContain("bg-amber-500");
    expect(dot.className).toContain("-left-1.5");
    expect(dot.className).toContain("-top-1.5");
    expect(dot.className).toContain("shadow-[0_0_0_2px_theme(colors.white.100)]");
    expect(dot.className).toContain("dark:shadow-[0_0_0_2px_theme(colors.navy.700)]");
    expect(getByTestId("rail-ring-tasks").className).toContain("animate-spin");
  });

  test("nobody working: no ring, the badge sits at -4px", () => {
    const { getByTestId, queryByTestId } = render(RailTasksIcon, { icon: ICON, count: 1, needsInput: 1, working: false });
    expect(queryByTestId("rail-ring-tasks")).toBeNull();
    const badge = getByTestId("rail-badge-tasks");
    expect(badge.className).toContain("-top-1");
    expect(badge.className).not.toContain("-top-1.5");
    expect(badge.className).toContain("bg-green-500");
    expect(getByTestId("rail-dot-tasks")).toBeTruthy();
  });

  test("nothing waits on the person: no dot", () => {
    const { queryByTestId, getByTestId } = render(RailTasksIcon, { icon: ICON, count: 3, needsInput: 0, working: true });
    expect(queryByTestId("rail-dot-tasks")).toBeNull();
    expect(getByTestId("rail-ring-tasks")).toBeTruthy();
  });

  test("caps the count at 99+", () => {
    const { getByTestId } = render(RailTasksIcon, { icon: ICON, count: 120, needsInput: 0, working: false });
    expect(getByTestId("rail-badge-tasks").textContent).toBe("99+");
  });
});

describe("RailTasksTip", () => {
  test("bold name, active count, the needs-input part in the warn colour", () => {
    const { container, getByTestId } = render(RailTasksTip, { label: "Tasks", count: 2, needsInput: 1 });
    expect(container.textContent).toBe("Tasks · 2 active · 1 needs input");
    // The tooltip and the tab's aria-label (railTabName) say the same words.
    expect(container.textContent).toBe(railTabName("Tasks", 2, 1));
    expect(container.querySelector("b")?.textContent).toBe("Tasks");
    const em = getByTestId("rail-tip-tasks-needs");
    expect(em.textContent).toBe("1 needs input");
    expect(em.className).toContain("text-amber-700");
    expect(em.className).toContain("dark:text-amber-300");
    expect(em.className).toContain("not-italic");
  });

  test("no needs-input part when nothing waits", () => {
    const { container, queryByTestId } = render(RailTasksTip, { label: "Tasks", count: 2, needsInput: 0 });
    expect(container.textContent).toBe("Tasks · 2 active");
    expect(container.textContent).toBe(railTabName("Tasks", 2, 0));
    expect(queryByTestId("rail-tip-tasks-needs")).toBeNull();
  });

  test("no tasks: just the name", () => {
    const { container } = render(RailTasksTip, { label: "Tasks", count: 0, needsInput: 0 });
    expect(container.textContent).toBe("Tasks");
    expect(container.textContent).toBe(railTabName("Tasks", 0, 0));
  });
});
