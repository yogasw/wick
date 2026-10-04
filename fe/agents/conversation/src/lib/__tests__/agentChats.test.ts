import { describe, it, expect, vi } from "vitest";
import { orderChats, pinChat, pinChatVia } from "../agentChats.js";
import type { AgentSessionItem } from "../api/team.js";

const chat = (id: string, main = false): AgentSessionItem => ({ id, label: id, last_active: null, agent_main: main, status: "idle" });

describe("orderChats", () => {
  it("puts the main chat first and keeps the rest in order", () => {
    expect(orderChats([chat("b"), chat("m", true), chat("a")]).map((s) => s.id)).toEqual(["m", "b", "a"]);
  });
});

describe("pinChat", () => {
  it("moves the pin and keeps the old main as a chat", () => {
    const next = pinChat([chat("m", true), chat("b"), chat("a")], "a");
    expect(next.map((s) => [s.id, s.agent_main])).toEqual([["a", true], ["m", false], ["b", false]]);
  });
});

describe("pinChatVia", () => {
  it("calls the pin endpoint, then reorders", async () => {
    const save = vi.fn().mockResolvedValue({ session_id: "b" });
    const next = await pinChatVia([chat("m", true), chat("b")], "b", save);
    expect(save).toHaveBeenCalledWith("b");
    expect(next.map((s) => s.id)).toEqual(["b", "m"]);
  });
  it("leaves the list alone when the server refuses", async () => {
    const items = [chat("m", true), chat("b")];
    await expect(pinChatVia(items, "b", () => Promise.reject(new Error("chat not found")))).rejects.toThrow("chat not found");
    expect(items.map((s) => s.agent_main)).toEqual([true, false]);
  });
});
