import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import AnswerBox from "../AnswerBox.svelte";

/* P48 slice 4: the answer box the Team blocks share (mockup .dans). */
const input = () => screen.getByLabelText("Answer @franky") as HTMLInputElement;
const sendBtn = () => screen.getByLabelText("Send answer") as HTMLButtonElement;

describe("AnswerBox", () => {
  test("renders the header line inside the box, the input and the send button", () => {
    render(AnswerBox, { props: { to: "franky", onSend: vi.fn() } });
    const to = screen.getByTestId("answer-to");
    // No note, no trailing separator.
    expect(to.textContent).toBe("Answer goes to @franky");
    expect(to.querySelector("svg")!.dataset.icon).toBe("reply");
    expect(to.querySelector("svg")!.getAttribute("width")).toBe("11");
    expect(to.querySelector("b")!.className).toContain("text-amber-700");
    // The header is the box's first row, above the input.
    const box = to.parentElement!;
    expect(box.tagName).toBe("FORM");
    expect(box.className).toContain("rounded-[10px]");
    expect(box.className).toContain("border-amber-500/[.45]");
    expect(box.contains(input())).toBe(true);
    expect(input().placeholder).toBe("Type an answer…");
    expect(sendBtn().querySelector("svg")!.dataset.icon).toBe("send");
    expect(sendBtn().className).toContain("h-7");
    expect(sendBtn().className).toContain("w-7");
    expect(screen.queryAllByTestId("quick-pill")).toHaveLength(0);
  });

  test("the note and the input's label can be set; ok tone colours the handle green", () => {
    render(AnswerBox, { props: { to: "halodev", note: "Cap sees it too", label: "Reply to Halodev", tone: "ok", onSend: vi.fn() } });
    expect(screen.getByTestId("answer-to").textContent).toBe("Answer goes to @halodev · Cap sees it too");
    expect(screen.getByTestId("answer-to").querySelector("b")!.className).toContain("text-green-600");
    expect(screen.getByLabelText("Reply to Halodev")).toBeDefined();
  });

  test("empty is off and disabled; typing turns it green and enables it", async () => {
    const onInput = vi.fn();
    render(AnswerBox, { props: { to: "franky", onSend: vi.fn(), onInput } });
    expect(sendBtn().disabled).toBe(true);
    expect(sendBtn().dataset.off).toBe("true");
    expect(sendBtn().className).toContain("bg-white-300");
    await fireEvent.input(input(), { target: { value: "   " } });
    expect(sendBtn().disabled).toBe(true);
    await fireEvent.input(input(), { target: { value: "both" } });
    expect(onInput).toHaveBeenLastCalledWith("both");
    expect(sendBtn().disabled).toBe(false);
    expect(sendBtn().dataset.off).toBe("false");
    expect(sendBtn().className).toContain("bg-green-500");
  });

  test("send (button or Enter) calls onSend with the trimmed text", async () => {
    const onSend = vi.fn();
    render(AnswerBox, { props: { to: "franky", onSend } });
    await fireEvent.input(input(), { target: { value: "  only 20:55 " } });
    await fireEvent.click(sendBtn());
    expect(onSend).toHaveBeenCalledWith("only 20:55");
    await fireEvent.submit(input().form!);
    expect(onSend).toHaveBeenCalledTimes(2);
  });

  test("a quick pill sends its text straight away", async () => {
    const onSend = vi.fn();
    render(AnswerBox, { props: { to: "franky", quick: ["Both deploys", "Only 20:55"], onSend } });
    const pills = screen.getAllByTestId("quick-pill");
    expect(pills.map((p) => p.textContent)).toEqual(["Both deploys", "Only 20:55"]);
    await fireEvent.click(pills[1]);
    expect(onSend).toHaveBeenCalledWith("Only 20:55");
  });

  test("while sending: the button spins and stays disabled, pills too; typing still works", async () => {
    const onSend = vi.fn();
    render(AnswerBox, { props: { to: "franky", value: "both", quick: ["a", "b"], sending: true, onSend } });
    expect(sendBtn().disabled).toBe(true);
    expect(sendBtn().dataset.sending).toBe("true");
    expect(sendBtn().querySelector("svg")!.dataset.icon).toBe("spin");
    expect(sendBtn().className).toContain("bg-green-500");
    for (const p of screen.getAllByTestId("quick-pill")) expect((p as HTMLButtonElement).disabled).toBe(true);
    await fireEvent.submit(input().form!);
    await fireEvent.click(screen.getAllByTestId("quick-pill")[0]);
    expect(onSend).not.toHaveBeenCalled();
    expect(input().disabled).toBe(false);
  });

  test("disabled blocks sending, not typing", async () => {
    const onSend = vi.fn();
    render(AnswerBox, { props: { to: "franky", disabled: true, onSend } });
    await fireEvent.input(input(), { target: { value: "both" } });
    expect(input().value).toBe("both");
    expect(sendBtn().disabled).toBe(true);
    await fireEvent.submit(input().form!);
    expect(onSend).not.toHaveBeenCalled();
  });
});
