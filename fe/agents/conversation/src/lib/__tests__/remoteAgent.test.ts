import { describe, test, expect } from "vitest";
import {
  authReq, egressWarning, formatBytes, hostOf, isRemoteAgent, limitsError, remoteCaption, remoteSettingsTab,
  REMOTE_HIDDEN_TABS, remoteChatMode, remoteSubtitle, testSummary,
  isA2ARemote, isSlackRemote, remoteBadge, remoteSettingsTabs, REMOTE_SOURCES,
  clipLabel, remoteProgress, remoteWaitLabel, PROGRESS_MAX,
} from "../remoteAgent.js";
import type { LiveTurn } from "../types/agents.js";

describe("remoteAgent", () => {
  test("isRemoteAgent reads kind", () => {
    expect(isRemoteAgent({ kind: "a2a-remote" })).toBe(true);
    expect(isRemoteAgent({ kind: "" })).toBe(false);
    expect(isRemoteAgent(null)).toBe(false);
  });

  test("caption and warning name the host", () => {
    expect(remoteCaption("research.example.com")).toBe("via A2A · research.example.com · no local tools");
    expect(egressWarning("h.io")).toContain("leave wick for h.io.");
  });

  test("every rail tab, including Source/Files/Process, is hidden", () => {
    for (const t of ["source", "files", "process", "workspace", "todos"] as const) expect(REMOTE_HIDDEN_TABS).toContain(t);
  });

  test("tabs a remote agent lacks open Remote A2A", () => {
    expect(remoteSettingsTab("persona")).toBe("persona");
    expect(remoteSettingsTab("access")).toBe("remote");
    expect(remoteSettingsTab("captain")).toBe("remote");
    expect(remoteSettingsTab("mention")).toBe("mention");
    expect(remoteSettingsTab("advanced")).toBe("advanced");
  });

  test("authReq: none, bearer, api_key with default header, blank secret = incomplete", () => {
    expect(authReq("none", "x", "")).toEqual({ type: "none" });
    expect(authReq("bearer", " t ", "")).toEqual({ type: "bearer", secret: "t" });
    expect(authReq("api_key", "k", "")).toEqual({ type: "api_key", header: "X-API-Key", secret: "k" });
    expect(authReq("api_key", "k", "X-Key")).toEqual({ type: "api_key", header: "X-Key", secret: "k" });
    expect(authReq("bearer", "  ", "")).toBeUndefined();
  });

  test("hostOf only accepts http(s)", () => {
    expect(hostOf("https://a.example.com:8443/x")).toBe("a.example.com:8443");
    expect(hostOf("ftp://a")).toBe("");
    expect(hostOf("nope")).toBe("");
  });

  test("limitsError mirrors the server bounds", () => {
    expect(limitsError(120, 2097152)).toBe("");
    expect(limitsError(0, 2097152)).toContain("Timeout");
    expect(limitsError(901, 2097152)).toContain("Timeout");
    expect(limitsError(120, 100)).toContain("Max response");
    expect(limitsError(120, 33554433)).toContain("Max response");
  });

  test("formatBytes and testSummary", () => {
    expect(formatBytes(2097152)).toBe("2 MB");
    expect(formatBytes(2048)).toBe("2 KB");
    expect(testSummary({ ok: true, state: "message", latency_ms: 40, reply: "pong", error: "" })).toBe("Replied in 40 ms — “pong”");
    expect(testSummary({ ok: false, state: "card_failed", latency_ms: 0, reply: "", error: "404" })).toBe("Agent card failed: 404");
  });
});

describe("remote chat mode", () => {
  const remote = {
    remote: { host: "research.example.com", card: { version: "1.4.0" } },
  } as unknown as Parameters<typeof remoteChatMode>[0];

  test("hides the rail, says why, and captions the composer", () => {
    const m = remoteChatMode(remote);
    expect(m.caption).toBe("via A2A · research.example.com · no local tools");
    expect(m.hideTabs).toEqual(expect.arrayContaining(["source", "files", "process"]));
    expect(m.railNote).toContain("No local tools");
  });

  test("the header subtitle carries the card version and host", () => {
    expect(remoteSubtitle(remote)).toBe("A2A remote · v1.4.0 · research.example.com");
    expect(remoteSubtitle({})).toBe("A2A remote");
  });

  test("a Slack remote agent is remote, with its own badge, caption, subtitle and Remote tab", () => {
    const slack = {
      kind: "slack-remote",
      slack_remote: { connector_id: "c1", identity: "bot", target: "channel", channel: "C1", target_name: "#ops", listen: "target" },
    } as unknown as Parameters<typeof remoteChatMode>[0];
    expect(isRemoteAgent(slack)).toBe(true);
    expect(isSlackRemote(slack)).toBe(true);
    expect(isA2ARemote(slack)).toBe(false);
    expect(remoteBadge(slack)).toBe("Slack remote");
    expect(remoteBadge({ kind: "a2a-remote" })).toBe("A2A remote");
    expect(remoteBadge({ kind: "" })).toBe("");
    const m = remoteChatMode(slack);
    expect(m.caption).toBe("via Slack · #ops · no local tools");
    expect(m.hideTabs).toEqual(REMOTE_HIDDEN_TABS);
    expect(remoteSubtitle(slack)).toBe("Slack remote · #ops");
    expect(remoteSettingsTabs(slack).map((t) => t.label)).toEqual(["Remote", "Persona", "Mention", "Avatar", "Advanced"]);
    expect(remoteSettingsTabs({ kind: "a2a-remote" })[0].label).toBe("Remote A2A");
  });

  test("sources: A2A, Slack and Plugin", () => {
    expect(REMOTE_SOURCES.map((s) => s.value)).toEqual(["a2a", "slack", "plugin"]);
  });
});

import { remoteWaitLabel } from "../remoteAgent.js";
describe("remote wait label", () => {
  const dm = { kind: "slack-remote" as const, handle: "halodev", slack_remote: { target: "dm", user: "U1", target_name: "@halodev", max_sec_effective: 180 } } as never;
  test("waits for the target instead of typing, with the max", () => {
    expect(remoteWaitLabel(dm, 12.7)).toBe("Waiting for @halodev's reply · 12s / 180s");
  });
  test("a channel target waits for a reply there", () => {
    const ch = { kind: "slack-remote", handle: "ops", slack_remote: { target: "channel", channel: "C1", target_name: "#ops", max_sec_effective: 60 } } as never;
    expect(remoteWaitLabel(ch, 3)).toBe("Waiting for a reply in #ops · 3s / 60s");
  });
  test("A2A without a known max shows the seconds only", () => {
    expect(remoteWaitLabel({ kind: "a2a-remote", handle: "res" } as never, 5)).toBe("Waiting for @res's reply · 5s");
  });
  test("header does not repeat a target named like the handle", () => {
    expect(remoteSubtitle({ ...(dm as object), handle: "halodev" } as never)).toBe("Slack remote");
    expect(remoteSubtitle({ ...(dm as object), handle: "other" } as never)).toBe("Slack remote · @halodev");
  });
});

describe("remote progress label", () => {
  const live = (...thinking: string[]): LiveTurn => ({ text: "", blocks: thinking.map((text) => ({ kind: "thinking" as const, text })) });

  test("is the last line of the last thinking block", () => {
    expect(remoteProgress(live("lagi baca thread…\n", "lagi pakai code read…\n"))).toBe("lagi pakai code read…");
    expect(remoteProgress(live("satu\ndua\n\n"))).toBe("dua");
  });

  test("none without thinking", () => {
    expect(remoteProgress(null)).toBeUndefined();
    expect(remoteProgress({ text: "hi", blocks: [{ kind: "text", text: "hi" }] })).toBeUndefined();
    expect(remoteProgress(live("  \n"))).toBeUndefined();
  });

  test("a long label is cut on a word with an ellipsis", () => {
    const long = "lagi membaca semua file konfigurasi webhook dan membandingkan dengan log produksi kemarin";
    const out = clipLabel(long);
    expect(out.length).toBeLessThanOrEqual(PROGRESS_MAX);
    expect(out.endsWith("…")).toBe(true);
    expect(long.startsWith(out.slice(0, -1))).toBe(true);
    expect(clipLabel("pendek")).toBe("pendek");
  });

  test("replaces Waiting for … in the header", () => {
    const a = { kind: "a2a-remote", handle: "halodev", remote: { timeout_sec: 180 } } as Parameters<typeof remoteWaitLabel>[0];
    expect(remoteWaitLabel(a, 12)).toBe("Waiting for @halodev's reply · 12s / 180s");
    expect(remoteWaitLabel(a, 12, "lagi pakai code read…")).toBe("lagi pakai code read… · 12s / 180s");
  });
});
