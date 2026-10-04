import { describe, it, expect, vi } from "vitest";
import { orderChats, pinChat, pinChatVia, startDraftChat } from "../agentChats.js";
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

describe("startDraftChat", () => {
  it("creates the chat only on Send, then sends into it", async () => {
    const calls: string[] = [];
    const open = vi.fn(async () => (calls.push("open"), { session_id: "s1" }));
    const send = vi.fn(async (id: string) => calls.push("send " + id));
    // Opening a draft is local: nothing is created until Send.
    expect(open).not.toHaveBeenCalled();
    const r = await startDraftChat({ text: "hi", files: [] }, open, send);
    expect(r).toEqual({ sessionId: "s1" });
    expect(calls).toEqual(["open", "send s1"]);
  });
  it("still lands in the new chat when the send fails", async () => {
    const r = await startDraftChat({ text: "hi", files: [] }, async () => ({ session_id: "s2" }), () => Promise.reject(new Error("busy")));
    expect(r).toEqual({ sessionId: "s2", error: "busy" });
  });
});
