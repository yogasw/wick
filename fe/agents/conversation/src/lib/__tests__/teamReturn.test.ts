import { describe, it, expect } from "vitest";
import { returnHref, classicHref, newSessionHref, agentsHomeHref } from "../teamReturn.js";

const O = "https://wick.example";
const B = "/tools/agents";

describe("returnHref", () => {
  it("prefers the page stored by the nav click", () => {
    expect(returnHref("/tools/agents/overview?x=1#y", `${O}/tools/agents/sessions/abc`, O, B)).toBe("/tools/agents/overview?x=1#y");
  });
  it("falls back to a same-origin referrer", () => {
    expect(returnHref(null, `${O}/tools/agents/sessions/abc?tab=rail`, O, B)).toBe("/tools/agents/sessions/abc?tab=rail");
  });
  it("ignores a referrer from another site", () => {
    expect(returnHref(null, "https://evil.example/tools/agents/overview", O, B)).toBe("/tools/agents/sessions");
  });
  it("ignores stored values that are not plain local paths", () => {
    expect(returnHref("//evil.example/x", "", O, B)).toBe("/tools/agents/sessions");
    expect(returnHref("https://evil.example/x", "", O, B)).toBe("/tools/agents/sessions");
  });
  it("rejects a backslash after the leading slash (browsers treat /\\ like //)", () => {
    expect(returnHref("/\\evil.example/x", "", O, B)).toBe("/tools/agents/sessions");
    expect(returnHref("/\\evil.example", `${O}/tools/agents/connectors`, O, B)).toBe("/tools/agents/connectors");
  });
  it("never points back into the Team app", () => {
    expect(returnHref("/tools/agents/team/ops", `${O}/tools/agents/team?panel=new`, O, B)).toBe("/tools/agents/sessions");
    expect(returnHref("/tools/agents/team", "", O, B)).toBe("/tools/agents/sessions");
    expect(returnHref("/tools/agents/team?panel=new", "", O, B)).toBe("/tools/agents/sessions");
    expect(returnHref("/tools/agents/team#x", "", O, B)).toBe("/tools/agents/sessions");
  });
  it("does not mistake a sibling path for the app", () => {
    expect(returnHref("/tools/agents/teams-report", "", O, B)).toBe("/tools/agents/teams-report");
  });
  it("skips a bad stored value but still uses the referrer", () => {
    expect(returnHref("//x", `${O}/tools/agents/connectors`, O, B)).toBe("/tools/agents/connectors");
  });
  it("never leaves the Agents space for another wick tool", () => {
    expect(returnHref("/tools/work-schedule", `${O}/tools/work-schedule`, O, B)).toBe("/tools/agents/sessions");
    expect(returnHref(null, `${O}/tools/text-counter?x=1`, O, B)).toBe("/tools/agents/sessions");
    expect(returnHref("/tools/agentsx", "", O, B)).toBe("/tools/agents/sessions");
  });
  it("tolerates an unparsable referrer", () => {
    expect(returnHref(null, "not a url", O, B)).toBe("/tools/agents/sessions");
  });
});

describe("classicHref", () => {
  it("marks the bare Agents landing so it does not redirect back to Team", () => {
    expect(classicHref("/tools/agents", B)).toBe("/tools/agents?view=classic");
    expect(classicHref("/tools/agents/", B)).toBe("/tools/agents/?view=classic");
    expect(classicHref("/tools/agents?project=p1#x", B)).toBe("/tools/agents?project=p1&view=classic#x");
  });
  it("leaves other pages and an already-marked landing alone", () => {
    expect(classicHref("/tools/agents/sessions", B)).toBe("/tools/agents/sessions");
    expect(classicHref("/tools/agents/overview?x=1", B)).toBe("/tools/agents/overview?x=1");
    expect(classicHref("/tools/agents?view=classic", B)).toBe("/tools/agents?view=classic");
  });
});

describe("newSessionHref", () => {
  it("marks the landing classic so Open Team cannot redirect it", () => {
    expect(newSessionHref(B)).toBe("/tools/agents/?view=classic");
  });
});

describe("agentsHomeHref", () => {
  const base = "/tools/agents", origin = "https://wick.example";
  it("opens the Agents home with the project the user came from", () => {
    expect(agentsHomeHref("/tools/agents/projects/p-1", "", origin, base)).toBe("/tools/agents/?view=classic&project=p-1");
    expect(agentsHomeHref(null, "https://wick.example/tools/agents/projects/p-2?tab=files", origin, base)).toBe("/tools/agents/?view=classic&project=p-2");
  });
  it("is the plain Agents home from any other page, never back to it", () => {
    expect(agentsHomeHref("/tools/agents/sessions/s-1", "", origin, base)).toBe("/tools/agents/?view=classic");
    expect(agentsHomeHref(null, "", origin, base)).toBe("/tools/agents/?view=classic");
    expect(agentsHomeHref("//evil/projects/x", "", origin, base)).toBe("/tools/agents/?view=classic");
  });
});
