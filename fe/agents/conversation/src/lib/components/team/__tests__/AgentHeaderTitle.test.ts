import { describe, test, expect } from "vitest";
import { render, screen } from "@testing-library/svelte";
import AgentHeaderTitle from "../AgentHeaderTitle.svelte";
import { withSaved } from "../../../rosterList.js";
import type { AgentItem } from "../../../api/team.js";

/* P48 #9: the chat header's " · Team lead" follows the captain flag. */
const agent = (id: string, p: Partial<AgentItem> = {}) => ({ id, handle: id, name: id[0].toUpperCase() + id.slice(1), is_captain: false, disabled: false, ...p }) as AgentItem;

const title = (a: AgentItem) => {
  const r = render(AgentHeaderTitle, { agent: a });
  const text = screen.getByTestId("header-title").textContent?.replace(/ /g, " ");
  r.unmount();
  return text;
};

describe("AgentHeaderTitle", () => {
  test("the captain reads 'Name · Team lead' in a muted, normal-weight span", () => {
    render(AgentHeaderTitle, { agent: agent("captain", { is_captain: true }) });
    expect(screen.getByTestId("header-title").textContent?.replace(/ /g, " ")).toBe("Captain · Team lead");
    const s = screen.getByTestId("header-suffix");
    expect(s.className).toContain("font-normal");
    expect(s.className).toContain("text-black-700");
  });

  test("Team lead takes the tagline's place on the captain", () => {
    expect(title(agent("captain", { is_captain: true, tagline: "Ops lead" }))).toBe("Captain · Team lead");
  });

  test("other agents get no Team lead: just the name, or the tagline", () => {
    expect(title(agent("log"))).toBe("Log");
    expect(title(agent("log", { tagline: "Reads Loki" }))).toBe("Log · Reads Loki");
  });

  test("moving the captaincy moves Team lead with it", () => {
    let agents = [agent("anton", { is_captain: true }), agent("budi")];
    agents = withSaved(agents, { ...agents[1], is_captain: true });
    expect(title(agents[0])).toBe("Anton");
    expect(title(agents[1])).toBe("Budi · Team lead");
  });
});
