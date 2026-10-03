import { describe, test, expect } from "vitest";
import {
  authReq, egressWarning, formatBytes, hostOf, isRemoteAgent, limitsError, remoteCaption, remoteSettingsTab,
  REMOTE_HIDDEN_TABS, testSummary,
} from "../remoteAgent.js";

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
    expect(remoteSettingsTab("persona")).toBe("remote");
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
