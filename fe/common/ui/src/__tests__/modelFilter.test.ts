import { describe, it, expect } from "vitest";
import { matchModelFilter } from "../modelFilter.js";

describe("matchModelFilter", () => {
  const cases: [string, string, boolean][] = [
    ["openai/gpt-5.5", "", true],
    ["openai/gpt-5.5", "GPT openai", true],
    ["openai/gpt-5.5", "gpt claude", false],
    ["openai/gpt-5.5-mini", "-mini", false],
    ["openai/gpt-5.5", "- !", true],
    ["anthropic/claude-sonnet", "claude|gpt", true],
    ["google/gemini-3", "claude|gpt", false],
    ["openai/gpt-5.5-mini", "claude|gpt !mini", false],
    ["openai/gpt-5.5", "claude|gpt !mini", true],
    ["anthropic/claude", "!claude|gpt", false],
    ["google/gemini-3", "!claude|gpt", true],
    ["google/gemini-3", "gemini|| |", true],
  ];
  it.each(cases)("%s ~ %s → %s", (hay, q, want) => {
    expect(matchModelFilter(hay, q)).toBe(want);
  });
});
