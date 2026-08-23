import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import RailMore from "../RailMore.svelte";

const tab = (id: string, label: string) => ({ id, label, icon: "<path d='M0 0h16v16H0z'/>" });

const all = [
  tab("ticket", "Ticket"),
  tab("notes", "Notes"),
  tab("context", "Context"),
  tab("process", "Process"),
  tab("browser", "Browser"),
];

function renderMore(over: Record<string, unknown> = {}) {
  const onSelect = vi.fn();
  const onMove = vi.fn();
  const onReorder = vi.fn();
  const onToggleHidden = vi.fn();
  const onDropHere = vi.fn();
  const onDragOut = vi.fn();
  const utils = render(RailMore, {
    props: {
      overflow: [all[3], all[4]],
      all,
      hiddenCount: 0,
      hiddenBusy: false,
      activeId: null,
      countFor: () => 0,
      onSelect,
      onMove,
      onReorder,
      onToggleHidden,
      onDropHere,
      onDragOut,
      ...over,
    },
  });
  return { ...utils, onSelect, onMove, onReorder, onToggleHidden, onDropHere, onDragOut };
}

/* A DataTransfer stand-in: jsdom does not implement it. */
function dt() {
  const store: Record<string, string> = {};
  return {
    setData: (k: string, v: string) => { store[k] = v; },
    getData: (k: string) => store[k] ?? "",
    effectAllowed: "",
  };
}

/* Opening the button is the whole thing now — there is no second mode to
   step into before the rows can be acted on. */
const openPanel = () => fireEvent.click(screen.getByLabelText("More panels"));

describe("RailMore", () => {
  // The badge answers "how much of the rail is folded away". It used to sum
  // the hidden tabs' own badges — and since a tab with a badge is promoted
  // into the strip, that sum was ~always 0 while several tabs were hidden.
  test("the badge counts hidden tabs, not their badges", () => {
    renderMore({ overflow: [all[3], all[4]], hiddenCount: 0 });
    expect(screen.getByTestId("rail-more-count").textContent?.trim()).toBe("2");
  });

  test("the badge still counts tabs when those tabs carry badges", () => {
    renderMore({ overflow: [all[3], all[4]], hiddenCount: 46 });
    expect(screen.getByTestId("rail-more-count").textContent?.trim()).toBe("2");
  });

  test("nothing hidden means no badge", () => {
    renderMore({ overflow: [] });
    expect(screen.queryByTestId("rail-more-count")).toBeNull();
  });

  /* ── the panel list ── */

  // The list holds the HIDDEN panels and nothing else. Listing all of them
  // contradicted the badge on the button: it said 2 while the list showed 5.
  test("the list holds only the hidden panels", async () => {
    renderMore();
    await openPanel();
    expect(screen.getByTestId("rail-row-process")).toBeTruthy();
    expect(screen.getByTestId("rail-row-browser")).toBeTruthy();
    expect(screen.queryByTestId("rail-row-ticket")).toBeNull();
    expect(screen.queryByTestId("rail-row-notes")).toBeNull();
  });

  test("the list length matches the button's badge", async () => {
    renderMore();
    const badge = Number(screen.getByTestId("rail-more-count").textContent!.trim());
    await openPanel();
    expect(screen.getAllByTestId(/^rail-row-/)).toHaveLength(badge);
  });

  // Every row here is hidden, so the eye has exactly one meaning.
  test("the eye puts a hidden panel back in the strip", async () => {
    const { onToggleHidden } = renderMore();
    await openPanel();
    await fireEvent.click(screen.getByTestId("rail-unfold-process"));
    expect(onToggleHidden).toHaveBeenCalledWith("process");
  });

  // The rows are a subset of the order, so a drop has to be translated into
  // a full-order position. Without that, a drag inside a 2-row list would
  // move panels to positions 0-2 of a 5-panel rail.
  test("dropping a row translates to a position in the full order", async () => {
    const { onReorder } = renderMore();
    await openPanel();
    const src = screen.getByTestId("rail-row-browser");
    const target = screen.getByTestId("rail-row-process");
    const transfer = dt();
    await fireEvent.dragStart(src, { dataTransfer: transfer });
    // Top half of the Process row → land where Process sits in `all`, which
    // is index 3, not index 0 of this two-row list.
    await fireEvent.dragOver(target, { dataTransfer: transfer, clientY: 0 });
    await fireEvent.drop(target, { dataTransfer: transfer });
    expect(onReorder).toHaveBeenCalledWith("browser", 3);
  });

  // The arrows move a panel in the full order too, so their bounds come from
  // that order — not from this row's place in the visible slice.
  test("the arrows are bounded by the full order, not the list", async () => {
    renderMore();
    await openPanel();
    // Process is 4th of 5 overall, so both directions are live even though it
    // is the FIRST row shown here.
    expect((screen.getByLabelText("Move Process up") as HTMLButtonElement).disabled).toBe(false);
    // Browser is last overall, so down is dead.
    expect((screen.getByLabelText("Move Browser down") as HTMLButtonElement).disabled).toBe(true);
  });

  /* ── drop onto the button itself ── */

  test("a strip tab dropped on the button is folded away", async () => {
    const { onDropHere } = renderMore({ dragging: true });
    const transfer = dt();
    transfer.setData("text/plain", "railtab:ticket");
    await fireEvent.drop(screen.getByLabelText("More panels"), { dataTransfer: transfer });
    expect(onDropHere).toHaveBeenCalledWith("ticket");
  });

  // The payload is namespaced, so an unrelated drag passing over the rail
  // does not rearrange it.
  test("a drop that is not a rail tab is ignored", async () => {
    const { onDropHere } = renderMore({ dragging: true });
    const transfer = dt();
    transfer.setData("text/plain", "session:abc");
    await fireEvent.drop(screen.getByLabelText("More panels"), { dataTransfer: transfer });
    expect(onDropHere).not.toHaveBeenCalled();
  });

  /* ── folding is a drag from the strip ── */

  // Folding a VISIBLE panel is not in this panel at all: it is the drag onto
  // the button. That gesture already existed and needed no list of its own.
  test("dropping a strip tab into the list folds it", async () => {
    const { onDropHere } = renderMore({ dragging: true });
    await openPanel();
    const transfer = dt();
    transfer.setData("text/plain", "railtab:notes");
    const list = screen.getByTestId("rail-row-process").closest("ul")!;
    await fireEvent.drop(list, { dataTransfer: transfer });
    expect(onDropHere).toHaveBeenCalledWith("notes");
  });

  // Dragging a hidden panel out onto the strip is the other direction, and it
  // carries the strip's own payload so the strip needs no special case.
  test("dragging a hidden panel out announces it, with the strip's payload", async () => {
    const { onDragOut } = renderMore();
    await openPanel();
    const transfer = dt();
    await fireEvent.dragStart(screen.getByTestId("rail-row-process"), { dataTransfer: transfer });
    expect(onDragOut).toHaveBeenCalledWith("process");
    expect(transfer.getData("text/plain")).toBe("railtab:process");
  });

  /* ── opening a panel ── */

  test("picking a hidden panel selects it", async () => {
    const { onSelect } = renderMore();
    await openPanel();
    await fireEvent.click(screen.getByTestId("rail-more-process"));
    expect(onSelect).toHaveBeenCalledWith("process");
  });
});
