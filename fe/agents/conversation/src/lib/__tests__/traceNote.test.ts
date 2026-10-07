import { describe, test, expect } from "vitest";
import { tidyTraceNote } from "../traceNote.js";

describe("tidyTraceNote", () => {
  test("drops newlines at the start and end", () => {
    expect(tidyTraceNote("\n\nMenarik — clone wick lokal sudah ada.\n\n")).toBe("Menarik — clone wick lokal sudah ada.");
  });

  test("a note made only of whitespace becomes empty, so nothing is rendered", () => {
    expect(tidyTraceNote("\n\n")).toBe("");
    expect(tidyTraceNote("  \n \t\n")).toBe("");
    expect(tidyTraceNote(undefined)).toBe("");
  });

  test("caps a run of blank lines at one, keeps a single paragraph break", () => {
    expect(tidyTraceNote("satu\n\n\n\ndua")).toBe("satu\n\ndua");
    expect(tidyTraceNote("satu\n\ndua")).toBe("satu\n\ndua");
    expect(tidyTraceNote("satu\ndua")).toBe("satu\ndua");
  });

  test("blank lines that only hold spaces count as blank", () => {
    expect(tidyTraceNote("satu\n  \n \n\ndua")).toBe("satu\n\ndua");
  });

  test("normalises CRLF", () => {
    expect(tidyTraceNote("satu\r\n\r\n\r\ndua\r\n")).toBe("satu\n\ndua");
  });

  test("leaves inner spacing of a line alone", () => {
    expect(tidyTraceNote("a  b   c")).toBe("a  b   c");
  });
});
