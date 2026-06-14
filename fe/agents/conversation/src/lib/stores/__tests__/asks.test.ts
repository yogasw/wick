import { describe, test, expect, beforeEach } from "vitest";
import { get } from "svelte/store";
import { currentAsk, showAsk, hideAsk } from "../asks.js";
import type { AskRequest } from "../../types/agents.js";

const REQ_A: AskRequest = {
  id: "ask-1",
  question: "Pick one?",
  options: [{ label: "Yes", value: "yes" }],
};

const REQ_B: AskRequest = {
  id: "ask-2",
  question: "Another?",
  options: [{ label: "No", value: "no" }],
};

beforeEach(() => {
  hideAsk();
});

describe("currentAsk store", () => {
  test("showAsk sets currentAsk", () => {
    showAsk(REQ_A);
    expect(get(currentAsk)).toEqual(REQ_A);
  });

  test("hideAsk clears currentAsk when no payload", () => {
    showAsk(REQ_A);
    hideAsk();
    expect(get(currentAsk)).toBeNull();
  });

  test("hideAsk clears currentAsk when id matches", () => {
    showAsk(REQ_A);
    hideAsk({ id: "ask-1" });
    expect(get(currentAsk)).toBeNull();
  });

  test("hideAsk is a no-op when id does not match", () => {
    showAsk(REQ_A);
    hideAsk({ id: "ask-2" });
    expect(get(currentAsk)).toEqual(REQ_A);
  });

  test("showAsk replaces existing ask", () => {
    showAsk(REQ_A);
    showAsk(REQ_B);
    expect(get(currentAsk)).toEqual(REQ_B);
  });
});
