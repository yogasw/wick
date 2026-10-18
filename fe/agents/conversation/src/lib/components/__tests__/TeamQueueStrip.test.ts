import { describe, expect, test } from "vitest";
import { fireEvent, render, screen } from "@testing-library/svelte";
import TeamQueueStrip from "../TeamQueueStrip.svelte";

const q = (pos: number, handle: string, title: string, revisions = 0) => ({
  task_id: "t" + pos, pos, from_handle: handle, from_name: handle, title, since: "2026-10-18T01:00:00Z", revisions,
});

describe("TeamQueueStrip", () => {
  test("nothing when the line is empty", () => {
    render(TeamQueueStrip, { props: { base: "", sessionId: "s", initial: [] } });
    expect(screen.queryByTestId("team-queue-strip")).toBeNull();
  });

  test("one clean line with the count and the next task; a click lists them", async () => {
    render(TeamQueueStrip, { props: { base: "", sessionId: "s", initial: [q(1, "yelan", "scan 12 agents", 1), q(2, "franky", "avatars")] } });
    const strip = screen.getByTestId("team-queue-strip");
    expect(strip.className).toContain("bg-white-200");
    expect(strip.className).toContain("rounded-xl");
    expect(strip.textContent).toContain("Queued (2)");
    expect(strip.textContent).toContain("next: @yelan — scan 12 agents");
    expect(screen.queryByTestId("team-queue-list")).toBeNull();
    await fireEvent.click(screen.getByTestId("team-queue-toggle"));
    const list = screen.getByTestId("team-queue-list");
    expect(list.textContent).toContain("#2");
    expect(list.textContent).toContain("@franky");
    expect(list.textContent).toContain("↻ 1");
  });
});
