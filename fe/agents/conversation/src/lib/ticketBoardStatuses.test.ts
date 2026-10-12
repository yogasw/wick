import { describe, expect, test } from "vitest";
import { activeStatusKeys, terminalKeys, toggledStatuses } from "./ticketBoardStatuses.js";

const NONE = "\u0000none";
const keys = ["open", "in_progress", "done"];
const term = ["done"];

describe("ticketBoardStatuses", () => {
  test("terminal keys are the flagged ones, else the last column", () => {
    expect(terminalKeys([{ key: "a" }, { key: "b", terminal: true }, { key: "c" }])).toEqual(["b"]);
    expect(terminalKeys([{ key: "a", terminal: true }, { key: "b", terminal: true }])).toEqual(["a", "b"]);
    expect(terminalKeys([{ key: "a" }, { key: "z" }])).toEqual(["z"]);
    expect(terminalKeys([])).toEqual([]);
  });

  test("nothing saved = every column but the terminal one", () => {
    expect(activeStatusKeys(undefined, keys, term, NONE)).toEqual(["open", "in_progress"]);
    expect(activeStatusKeys([], keys, term, NONE)).toEqual(["open", "in_progress"]);
  });

  test("a saved choice is kept, in board order, minus keys the board lacks", () => {
    expect(activeStatusKeys(["done", "open"], keys, term, NONE)).toEqual(["open", "done"]);
    expect(activeStatusKeys(["open", "gone"], keys, term, NONE)).toEqual(["open"]);
    expect(activeStatusKeys([NONE], keys, term, NONE)).toEqual([]);
  });

  test("toggling saves the explicit list, the default as [], nothing as the sentinel", () => {
    expect(toggledStatuses(["open", "in_progress"], "done", keys, term, NONE)).toEqual(keys);
    expect(toggledStatuses(keys, "done", keys, term, NONE)).toEqual([]);
    expect(toggledStatuses(["open", "in_progress"], "open", keys, term, NONE)).toEqual(["in_progress"]);
    expect(toggledStatuses(["open"], "open", keys, term, NONE)).toEqual([NONE]);
  });
});
