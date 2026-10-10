import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import PresenceDot from "../PresenceDot.svelte";
import RemoteKindIcon from "../RemoteKindIcon.svelte";
import AgentShareLine from "../AgentShareLine.svelte";

/* P44 pieces of the roster row and chat header: the status dot (spoken,
   never a hover tip), the remote source icon and the header's sharing part. */
describe("PresenceDot", () => {
  test("draws the presence and reads the label out, with no title tooltip", () => {
    const { container } = render(PresenceDot, { presence: "attention", label: "needs your attention" });
    const dot = screen.getByTestId("presence-dot");
    expect(dot.dataset.presence).toBe("attention");
    expect(dot.getAttribute("aria-hidden")).toBe("true");
    expect(dot.className).toContain("bg-amber-500");
    const label = screen.getByTestId("presence-label");
    expect(label.textContent).toBe("needs your attention");
    expect(label.className).toContain("sr-only");
    expect(container.querySelector("[title]")).toBeNull();
  });

  test("working spins, disabled is grey, online is green", () => {
    const cls = (presence: "online" | "working" | "disabled") => {
      const r = render(PresenceDot, { presence, label: presence });
      const c = r.container.querySelector<HTMLElement>("[data-testid=presence-dot]")!.className;
      r.unmount();
      return c;
    };
    expect(cls("working")).toContain("presence-spin");
    expect(cls("disabled")).toContain("bg-black-600");
    expect(cls("online")).toContain("bg-green-500");
  });
});

describe("RemoteKindIcon", () => {
  test("Slack remote: the Slack logo, named for screen readers", () => {
    render(RemoteKindIcon, { agent: { kind: "slack-remote" } });
    const icon = screen.getByTestId("remote-kind-icon");
    expect(icon.dataset.kind).toBe("slack-remote");
    expect(icon.querySelector('path[fill="#E01E5A"]')).not.toBeNull();
    expect(icon.textContent?.trim()).toBe("Slack remote");
  });

  test("A2A and plugin remotes get their own small icon", () => {
    const a = render(RemoteKindIcon, { agent: { kind: "a2a-remote" } });
    expect(a.getByTestId("remote-kind-icon").textContent?.trim()).toBe("A2A remote");
    expect(a.container.querySelector('path[fill="#E01E5A"]')).toBeNull();
    a.unmount();
    render(RemoteKindIcon, { agent: { kind: "plugin-remote" } });
    expect(screen.getByTestId("remote-kind-icon").textContent?.trim()).toBe("Plugin remote");
  });

  test("a wick agent gets nothing", () => {
    const { container } = render(RemoteKindIcon, { agent: { kind: "" } });
    expect(container.querySelector("[data-testid=remote-kind-icon]")).toBeNull();
  });
});

describe("AgentShareLine", () => {
  test("recipient: Shared by <initials> <owner>", () => {
    render(AgentShareLine, { agent: { role: "viewer", shared_by: "Yoga Setiawan" } });
    const el = screen.getByTestId("header-shared-by");
    expect(el.textContent?.replace(/\s+/g, " ").trim()).toBe("Shared by YS Yoga Setiawan");
    expect(screen.queryByTestId("header-shared-with")).toBeNull();
  });

  test("owner: Shared with N opens the sharing settings", async () => {
    const onOpenSharing = vi.fn();
    render(AgentShareLine, { agent: { share_count: 3 }, onOpenSharing });
    const btn = screen.getByTestId("header-shared-with");
    expect(btn.textContent?.trim()).toBe("Shared with 3");
    expect(btn.getAttribute("aria-label")).toBe("Shared with 3 people: open sharing settings");
    await fireEvent.click(btn);
    expect(onOpenSharing).toHaveBeenCalledOnce();
  });

  test("not shared: nothing", () => {
    const { container } = render(AgentShareLine, { agent: {} });
    expect(container.querySelector("[data-testid^=header-shared]")).toBeNull();
  });
});
