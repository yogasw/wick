import { describe, test, expect } from "vitest";
import { jumpLink, deliveryView } from "./slackDelivery.js";

describe("jumpLink", () => {
  test("keeps http(s) links only", () => {
    expect(jumpLink("https://example.slack.com/archives/C1/p1")).toBe("https://example.slack.com/archives/C1/p1");
    expect(jumpLink("javascript:alert(1)")).toBe("");
    expect(jumpLink(undefined)).toBe("");
    expect(jumpLink("  ")).toBe("");
  });
});

describe("deliveryView", () => {
  test("nothing for a turn no channel posted", () => {
    expect(deliveryView(undefined)).toBeNull();
    expect(deliveryView({ channel: "slack", status: "weird" as never })).toBeNull();
  });
  test("sending has no link yet", () => {
    expect(deliveryView({ channel: "slack", status: "sending" })).toEqual({ state: "sending", label: "Sending to Slack…", link: "" });
  });
  test("sent links to the posted reply", () => {
    expect(deliveryView({ channel: "slack", status: "sent", permalink: "https://example.slack.com/x" })).toEqual({
      state: "sent",
      label: "Sent to Slack",
      link: "https://example.slack.com/x",
    });
  });
  test("failed carries Slack's reason", () => {
    const v = deliveryView({ channel: "slack", status: "failed", error: "channel_not_found" });
    expect(v?.state).toBe("failed");
    expect(v?.label).toBe("Not sent to Slack: channel_not_found");
    expect(v?.link).toBe("");
  });
});
