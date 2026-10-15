import { describe, test, it, expect } from "vitest";
import type { AgentSlackStatus } from "./api/team.js";
import { connectBody, formatExpiry, isRotatingAppToken, rotationLine, showRotation, tokenError, withoutCleared, botTakenByChannel, type TokenDraft } from "./slackConnection.js";

const blank: TokenDraft = { mode: "socket", bot_token: "", app_token: "", signing_secret: "", app_refresh_token: "", client_id: "", client_secret: "" };
const none: AgentSlackStatus = { connected: false, online: false, mode: "socket", dm_main_chat: true, secrets: {}, disabled: false };
const rotating: AgentSlackStatus = {
  ...none, connected: true, rotating: true, client_id: "123.456",
  secrets: { bot_token: true, app_token: true, app_refresh_token: true, client_secret: true },
};

describe("tokenError", () => {
  test("a legacy xapp- token needs nothing else", () => {
    const d = { ...blank, bot_token: "xoxb-1", app_token: "xapp-1" };
    expect(tokenError(d, none)).toBe("");
    expect(showRotation(d, none)).toBe(false);
  });

  test("rotating bot and app token prefixes are accepted", () => {
    const d = { ...blank, bot_token: "xoxe.xoxb-1", app_token: "xoxe.xapp-1", app_refresh_token: "xoxe-1-r", client_id: "1.2", client_secret: "s" };
    expect(tokenError(d, none)).toBe("");
  });

  test("bad prefixes are refused", () => {
    expect(tokenError({ ...blank, bot_token: "xoxp-1", app_token: "xapp-1" }, none)).toMatch(/xoxb-/);
    expect(tokenError({ ...blank, bot_token: "xoxe.xapp-1", app_token: "xapp-1" }, none)).toMatch(/xoxb-/);
    expect(tokenError({ ...blank, bot_token: "xoxb-1", app_token: "xoxb-1" }, none)).toMatch(/xoxe\.xapp-/);
    expect(tokenError({ ...blank, bot_token: "xoxb-1", app_token: "xoxe.xoxb-1" }, none)).toMatch(/app-level token/);
  });

  test("a new xoxe.xapp- token needs its refresh token and the client credentials", () => {
    const d = { ...blank, bot_token: "xoxb-1", app_token: "xoxe.xapp-1" };
    expect(showRotation(d, none)).toBe(true);
    expect(tokenError(d, none)).toMatch(/refresh token/);
    expect(tokenError({ ...d, app_refresh_token: "xoxb-r" }, none)).toMatch(/refresh token/);
    expect(tokenError({ ...d, app_refresh_token: "xoxe-1-r" }, none)).toMatch(/Client ID/);
    expect(tokenError({ ...d, app_refresh_token: "xoxe-1-r", client_id: "1.2" }, none)).toMatch(/Client Secret/);
  });

  test("a new rotating token needs a new refresh token even when one is held", () => {
    const d = { ...blank, app_token: "xoxe.xapp-2" };
    expect(tokenError(d, rotating)).toMatch(/refresh token/);
    // Client ID and secret held by the server may stay blank.
    expect(tokenError({ ...d, app_refresh_token: "xoxe-1-r2" }, rotating)).toBe("");
  });

  test("a kept rotating token may leave everything blank when held", () => {
    expect(showRotation(blank, rotating)).toBe(true);
    expect(tokenError(blank, rotating)).toBe("");
    expect(tokenError(blank, { ...rotating, client_id: "" })).toMatch(/Client ID/);
    expect(tokenError(blank, { ...rotating, secrets: { ...rotating.secrets, client_secret: false } })).toMatch(/Client Secret/);
    expect(tokenError(blank, { ...rotating, secrets: { ...rotating.secrets, app_refresh_token: false } })).toMatch(/refresh token/);
    expect(tokenError({ ...blank, app_refresh_token: "bad" }, rotating)).toMatch(/refresh token/);
  });

  test("HTTP mode ignores the app token", () => {
    expect(showRotation({ ...blank, mode: "http", app_token: "xoxe.xapp-1" }, none)).toBe(false);
    expect(tokenError({ ...blank, mode: "http", bot_token: "xoxb-1", signing_secret: "s" }, none)).toBe("");
  });
});

test("isRotatingAppToken", () => {
  expect(isRotatingAppToken(" xoxe.xapp-1-A")).toBe(true);
  expect(isRotatingAppToken("xapp-1-A")).toBe(false);
});

test("connectBody sends the rotation fields that were typed", () => {
  expect(connectBody({ ...blank, app_token: "xoxe.xapp-1", app_refresh_token: " xoxe-1-r ", client_id: "1.2" })).toEqual({
    mode: "socket", app_token: "xoxe.xapp-1", app_refresh_token: "xoxe-1-r", client_id: "1.2",
  });
});

describe("formatExpiry", () => {
  const now = new Date("2026-10-14T00:00:00Z");
  test("hours and minutes left", () => {
    expect(formatExpiry("2026-10-14T11:40:30Z", now)).toBe("expires in 11h 40m");
    expect(formatExpiry("2026-10-14T00:05:00Z", now)).toBe("expires in 5m");
  });
  test("expired and unknown", () => {
    expect(formatExpiry("2026-10-13T23:00:00Z", now)).toBe("expired");
    expect(formatExpiry("2026-10-14T00:00:00Z", now)).toBe("expired");
    expect(formatExpiry("", now)).toBe("");
    expect(formatExpiry("nope", now)).toBe("");
  });
});

test("rotationLine", () => {
  const now = new Date("2026-10-14T00:00:00Z");
  const last = new Date(2026, 9, 13, 9, 5);
  expect(rotationLine(none, now)).toBe("");
  expect(rotationLine({ ...rotating, app_token_expires_at: "2026-10-14T11:40:00Z", last_refresh: last.toISOString() }, now))
    .toBe("Rotating · expires in 11h 40m · last refreshed 09:05");
  expect(rotationLine(rotating, now)).toBe("Rotating");
});

describe("take over + clear", () => {
  const draft = { mode: "socket" as const, bot_token: "", app_token: "", signing_secret: "", app_refresh_token: "", client_id: "", client_secret: "" };

  it("reads the take-over offer out of a 409 body only", () => {
    const body = JSON.stringify({ error: "x", code: "bot_taken_by_channel", instance_key: "slack:u9", instance_name: "Slack · @bot · Yoga", can_take_over: true, workflow_refs: [], sessions: 3, session_prefix: "slack-u9-" });
    const offer = botTakenByChannel({ status: 409, body });
    expect(offer?.instance_key).toBe("slack:u9");
    expect(offer?.sessions).toBe(3);
    expect(botTakenByChannel({ status: 409, body: JSON.stringify({ error: "already connected to @rekap" }) })).toBeNull();
    expect(botTakenByChannel(new Error("boom"))).toBeNull();
    expect(botTakenByChannel({ body: "not json" })).toBeNull();
  });

  it("sends clear for blank cleared secrets and take_over when asked", () => {
    expect(connectBody(draft, ["signing_secret"], true)).toEqual({ mode: "socket", clear: ["signing_secret"], take_over: true });
    // A pasted value replaces the secret — no clear needed.
    expect(connectBody({ ...draft, app_token: "xapp-1" }, ["app_token"])).toEqual({ mode: "socket", app_token: "xapp-1" });
    expect(connectBody(draft)).toEqual({ mode: "socket" });
  });

  it("treats cleared secrets as not held", () => {
    const st = { connected: true, online: true, mode: "socket" as const, dm_main_chat: true, disabled: false, secrets: { bot_token: true, app_token: true } };
    expect(withoutCleared(st, ["app_token"])?.secrets.app_token).toBe(false);
    expect(tokenError(draft, withoutCleared(st, ["app_token"]))).toMatch(/app-level token/);
    expect(withoutCleared(st, [])).toBe(st);
  });
});
