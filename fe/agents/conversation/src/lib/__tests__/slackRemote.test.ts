import { describe, test, expect } from "vitest";
import type { SlackRemoteConfig } from "../api/team.js";
import {
  cleanConfig, configError, parseSlackLink, patchBody, secError, slackCaption, slackTestSummary, slackWarning, targetLabel,
} from "../slackRemote.js";

const base: SlackRemoteConfig = { connector_id: "c1", identity: "bot", target: "dm", user: "U1", listen: "target" };

describe("slackRemote", () => {
  test("parseSlackLink reads channel and thread from a permalink", () => {
    expect(parseSlackLink("https://acme.slack.com/archives/C0123/p1700000000123456")).toEqual({ channel: "C0123", thread_ts: "1700000000.123456" });
    expect(parseSlackLink("https://acme.slack.com/archives/C0123/p1700000099000001?thread_ts=1700000000.123456&cid=C0123"))
      .toEqual({ channel: "C0123", thread_ts: "1700000000.123456" });
    expect(parseSlackLink("not a link")).toBeNull();
    expect(parseSlackLink("https://acme.slack.com/team/U1")).toBeNull();
  });

  test("targetLabel prefers the display name, else the id with its sigil", () => {
    expect(targetLabel({ target: "dm", user: "U1" })).toBe("@U1");
    expect(targetLabel({ target: "dm", user: "U1", target_name: "@helper" })).toBe("@helper");
    expect(targetLabel({ target: "channel", channel: "C1" })).toBe("#C1");
    expect(targetLabel({ target: "thread", channel: "C1", target_name: "#ops" })).toBe("a thread in #ops");
  });

  test("caption and warning name the target and workspace", () => {
    expect(slackCaption("#ops")).toBe("via Slack · #ops · no local tools");
    expect(slackWarning("#ops", "Acme")).toMatch(/^Messages you send .* will be posted to #ops in Acme\.$/);
    expect(slackWarning("@x", "")).toContain("posted to @x in Slack.");
  });

  test("configError mirrors the server's checks", () => {
    expect(configError(base)).toBe("");
    expect(configError({ ...base, connector_id: "" })).toMatch(/workspace/);
    expect(configError({ ...base, user: " " })).toMatch(/user or bot ID/);
    expect(configError({ ...base, target: "channel" })).toMatch(/channel ID/);
    expect(configError({ ...base, target: "thread", channel: "C1" })).toMatch(/thread/);
    expect(configError({ ...base, identity: "user" })).toMatch(/account/);
    expect(configError({ ...base, idle_sec: 901 })).toMatch(/Idle must be 0–900/);
    expect(secError(0, -1)).toMatch(/Max/);
  });

  test("cleanConfig drops what the target and identity do not use", () => {
    const c = cleanConfig({ ...base, target: "channel", channel: " C1 ", user: "U1", thread_ts: "1.2", mention_id: "U9", account_id: "a1" });
    expect(c).toEqual({ connector_id: "c1", identity: "bot", target: "channel", channel: "C1", mention_id: "U9", listen: "target", marker: true, mention_target: true, idle_sec: 0, max_sec: 0 });
    expect(cleanConfig({ ...base, identity: "user", account_id: "a1", marker: false }).account_id).toBe("a1");
    expect(cleanConfig({ ...base, marker: false }).marker).toBe(false);
    expect(cleanConfig({ ...base, grace_sec: -1 }).grace_sec).toBe(-1);
    expect(cleanConfig({ ...base, grace_sec: 0 })).not.toHaveProperty("grace_sec");
  });

  test("slackTestSummary words each state", () => {
    expect(slackTestSummary({ ok: true, state: "replied", latency_ms: 812, reply: "pong" })).toBe("Replied in 812 ms — “pong”");
    expect(slackTestSummary({ ok: false, state: "no_reply", latency_ms: 30000 })).toMatch(/^Ping posted, no reply/);
    expect(slackTestSummary({ ok: false, state: "send_failed", latency_ms: 0, error: "not_in_channel" })).toBe("Ping failed: not_in_channel");
    expect(slackTestSummary({ ok: false, state: "auth_failed", latency_ms: 0, error: "invalid_auth" })).toBe("Slack sign-in failed: invalid_auth");
  });
});

describe("patchBody", () => {
  test("sends blanks for cleared and unused fields", () => {
    expect(patchBody({ connector_id: "c1", identity: "bot", target: "dm", user: "U1", listen: "target", mention_id: "U9" })).toEqual({
      connector_id: "c1", identity: "bot", target: "dm", user: "U1", listen: "target", marker: true, mention_target: true, idle_sec: 0, max_sec: 0,
      account_id: "", channel: "", mention_id: "", thread_ts: "", target_name: "",
    });
  });
});
