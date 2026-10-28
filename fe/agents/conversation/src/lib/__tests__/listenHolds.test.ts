import { describe, test, expect } from "vitest";
import { linkListenHolds, permalinkTs, isProblemHold } from "../listenHolds.js";

type T = { turn_id: string; role: string; kind?: string; text: string; extras?: Record<string, string>; sender?: { permalink?: string } };
const hold = (id: string, state: string, msg_ts = "", text = "Auto-reply · passed to the agent after 3s"): T =>
  ({ turn_id: id, role: "system", kind: "listen_held", text, extras: { hold_id: "S/" + id, state, mode: "auto_reply", delay_sec: "3", started_ms: "1000", msg_ts } });
const user = (id: string, permalink = ""): T => ({ turn_id: id, role: "user", text: "hi", sender: { permalink } });

describe("linkListenHolds", () => {
  test("reads the ts of a Slack permalink", () => {
    expect(permalinkTs("https://acme.slack.com/archives/C01TESTCHAN/p1700000000123456?thread_ts=1.1")).toBe("1700000000.123456");
    expect(permalinkTs("")).toBe("");
  });

  test("links on msg_ts first, and the hold row leaves the thread", () => {
    const turns = [user("u1", "https://acme.slack.com/archives/C01TESTCHAN/p1700000000000001"), hold("h1", "sent", "1700000000.000002"), user("u2", "https://acme.slack.com/archives/C01TESTCHAN/p1700000000000002")];
    const out = linkListenHolds(turns);
    expect(out.turns.map((t) => t.turn_id)).toEqual(["u1", "u2"]);
    expect(out.holds.u2).toMatchObject({ mode: "auto_reply", heldFor: "3s", startedMs: 1000 });
    expect(out.holds.u1).toBeUndefined();
  });

  test("falls back to the next user turn, never past another hold", () => {
    const out = linkListenHolds([hold("h1", "sent"), { turn_id: "a", role: "assistant", text: "x" }, user("u1")]);
    expect(Object.keys(out.holds)).toEqual(["u1"]);
    expect(linkListenHolds([hold("h1", "sent"), hold("h2", "held"), user("u1")]).holds).toEqual({});
  });

  test("a hold that ended badly stays a row", () => {
    const out = linkListenHolds([hold("h1", "skipped", "", "Listen · not passed on: someone else answered"), user("u1")]);
    expect(out.turns.map((t) => t.turn_id)).toEqual(["h1", "u1"]);
    expect(out.holds).toEqual({});
    expect(isProblemHold("cancelled")).toBe(true);
    expect(isProblemHold("sent")).toBe(false);
  });
});
