import { describe, test, expect } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import SlackListenFields from "../SlackListenFields.svelte";

const HINT = "Turn this on if the remote bot answers by editing an earlier message — without it, edits to messages from before the request are ignored.";

const props = (marker: boolean) => ({ listen: "target" as const, marker, mention: false, idleSec: 0, maxSec: 0, idPrefix: "t" });

describe("SlackListenFields marker hint", () => {
  test.each([true, false])("edited-reply hint shows with marker %s", (marker) => {
    render(SlackListenFields, { props: props(marker) });
    const hint = screen.getByTestId("t-marker-edit-hint");
    expect(hint.textContent?.trim()).toBe(HINT);
    expect(hint.className).toBe("text-xs text-black-800 dark:text-black-600");
  });

  test("hint stays when the marker is turned off", async () => {
    render(SlackListenFields, { props: props(true) });
    await fireEvent.click(screen.getByTestId("t-marker"));
    expect((screen.getByTestId("t-marker") as HTMLInputElement).checked).toBe(false);
    expect(screen.getByText(/No marker: the turn ends/)).toBeTruthy();
    expect(screen.getByTestId("t-marker-edit-hint").textContent?.trim()).toBe(HINT);
  });
});
