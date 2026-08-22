import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import WidgetPolicyEditor from "../components/WidgetPolicyEditor.svelte";
import type { WidgetPolicy } from "../types.js";

function setup(policy: WidgetPolicy = {}, inherited: WidgetPolicy = {}, allowlistText = "") {
  const onChange = vi.fn();
  const r = render(WidgetPolicyEditor, { props: { policy, inherited, allowlistText, onChange } });
  return { ...r, onChange };
}

const ON = { override: true };
const CUSTOM = { override: true, mode: "custom" };

describe("WidgetPolicyEditor — inheriting", () => {
  test("no mode toggle at all while inheriting", () => {
    setup({}, { mode: "unsecure" });
    expect(screen.getByText("inherited")).toBeTruthy();
    expect(screen.queryByRole("radiogroup", { name: "Widget mode" })).toBeNull();
  });

  test("states the inherited posture in one line per preset", () => {
    setup({}, { mode: "secure" });
    expect(screen.getByText(/Currently following global — sealed off/)).toBeTruthy();
  });

  test("inherited unsecure is called out plainly", () => {
    setup({}, { mode: "unsecure" });
    expect(screen.getByText(/everything allowed/)).toBeTruthy();
  });

  test("inherited custom summarises only what is actually open", () => {
    setup({}, { mode: "custom", frame_src: "list", allow_popups: true });
    expect(screen.getByText(/custom — embedded frames: list · popups allowed/)).toBeTruthy();
  });

  test("ticking the override reports it upward", async () => {
    const { onChange } = setup({});
    await fireEvent.click(screen.getByRole("checkbox", { name: /Override for this project/ }));
    expect(onChange).toHaveBeenCalledWith(
      expect.objectContaining({ policy: expect.objectContaining({ override: true }) }),
    );
  });
});

/* The point of the toggle: one click sets the posture. Secure and Unsecure
   must not ask the operator to configure anything. */
describe("WidgetPolicyEditor — the mode toggle", () => {
  // The emoji are aria-hidden, so the accessible name is the label alone.
  test("three presets, Secure selected by default", () => {
    setup(ON);
    for (const label of ["Secure", "Unsecure", "Custom"]) {
      expect(screen.getByRole("radio", { name: label })).toBeTruthy();
    }
    expect(screen.getByRole("radio", { name: "Secure" }).getAttribute("aria-checked")).toBe("true");
  });

  test("an unknown stored mode shows as Secure, not blank", () => {
    setup({ override: true, mode: "wide-open" });
    expect(screen.getByRole("radio", { name: "Secure" }).getAttribute("aria-checked")).toBe("true");
  });

  test("Secure hides every per-permission control", () => {
    setup({ override: true, mode: "secure" });
    expect(screen.queryByLabelText("Embedded frames")).toBeNull();
    expect(screen.queryByLabelText("Allowed hosts")).toBeNull();
    expect(screen.queryByRole("checkbox", { name: /Links may open a new tab/ })).toBeNull();
  });

  test("Unsecure also hides them — nothing to configure", () => {
    setup({ override: true, mode: "unsecure" });
    expect(screen.queryByLabelText("Embedded frames")).toBeNull();
    expect(screen.queryByLabelText("Allowed hosts")).toBeNull();
  });

  test("Unsecure warns that scripts can send data anywhere", () => {
    setup({ override: true, mode: "unsecure" });
    expect(screen.getByText(/scripts from any host/)).toBeTruthy();
    expect(screen.getByText(/send it anywhere/)).toBeTruthy();
  });

  test("picking a preset reports only the mode, leaving detail untouched", async () => {
    const { onChange } = setup({ override: true, mode: "custom", frame_src: "list" });
    await fireEvent.click(screen.getByRole("radio", { name: "Secure" }));
    // frame_src must survive: flipping to Secure and back should not wipe a
    // Custom setup the operator built.
    expect(onChange).toHaveBeenCalledWith(
      expect.objectContaining({
        policy: expect.objectContaining({ mode: "secure", frame_src: "list" }),
      }),
    );
  });

  test("Custom reveals the per-permission controls", () => {
    setup(CUSTOM);
    for (const label of [
      "Embedded frames",
      "External images",
      "External audio & video",
      "Network calls",
      "External scripts",
    ]) {
      expect((screen.getByLabelText(label) as HTMLSelectElement).value).toBe("block");
    }
    expect(screen.getByLabelText("Allowed hosts")).toBeTruthy();
  });
});

describe("WidgetPolicyEditor — custom detail", () => {
  test("changing a permission reports the new mode", async () => {
    const { onChange } = setup(CUSTOM);
    await fireEvent.change(screen.getByLabelText("Embedded frames"), { target: { value: "list" } });
    expect(onChange).toHaveBeenCalledWith(
      expect.objectContaining({ policy: expect.objectContaining({ frame_src: "list" }) }),
    );
  });

  test("external scripts is its own control and warns about its reach", async () => {
    const { onChange } = setup(CUSTOM);
    await fireEvent.change(screen.getByLabelText("External scripts"), { target: { value: "list" } });
    expect(onChange).toHaveBeenCalledWith(
      expect.objectContaining({ policy: expect.objectContaining({ script_src: "list" }) }),
    );
    expect(screen.getByText(/can read everything it holds/)).toBeTruthy();
  });

  test("an unknown stored permission displays as Blocked", () => {
    setup({ ...CUSTOM, frame_src: "nonsense" });
    expect((screen.getByLabelText("Embedded frames") as HTMLSelectElement).value).toBe("block");
  });

  test("popups is separate from the permissions and warns about its reach", async () => {
    const { onChange } = setup(CUSTOM);
    await fireEvent.click(screen.getByRole("checkbox", { name: /Links may open a new tab/ }));
    expect(onChange).toHaveBeenCalledWith(
      expect.objectContaining({ policy: expect.objectContaining({ allow_popups: true }) }),
    );
    expect(screen.getByText(/can reach any host/)).toBeTruthy();
  });

  test("popup escape is offered separately and reports upward", async () => {
    const { onChange } = setup({ ...CUSTOM, allow_popups: true });
    await fireEvent.click(screen.getByRole("checkbox", { name: /real origin/ }));
    expect(onChange).toHaveBeenCalledWith(
      expect.objectContaining({ policy: expect.objectContaining({ allow_popup_escape: true }) }),
    );
  });

  /* Escaping is meaningless with nothing allowed to open a tab, so the UI must
     not offer it as an independent choice that silently does nothing. */
  test("popup escape is unavailable until links may open a tab", () => {
    setup({ ...CUSTOM, allow_popups: false });
    expect((screen.getByRole("checkbox", { name: /real origin/ }) as HTMLInputElement).disabled).toBe(
      true,
    );
  });

  test("inherited hosts are shown so the operator sees what theirs are appended to", () => {
    setup(CUSTOM, { allowlist: ["https://a.test", "https://b.test"] });
    expect(screen.getByText(/From global — always included/)).toBeTruthy();
    expect(screen.getByText("https://a.test https://b.test")).toBeTruthy();
  });

  test("no inherited hosts: the read-only block is omitted entirely", () => {
    setup(CUSTOM, {});
    expect(screen.queryByText(/From global — always included/)).toBeNull();
  });

  test("editing the allowlist reports the raw text, not a parsed array", async () => {
    const { onChange } = setup(CUSTOM, {}, "");
    await fireEvent.input(screen.getByLabelText("Allowed hosts"), {
      target: { value: "maps.google.com\n*.example.com" },
    });
    expect(onChange).toHaveBeenCalledWith(
      expect.objectContaining({ allowlistText: "maps.google.com\n*.example.com" }),
    );
  });

  test("says which permissions read the allowlist", () => {
    setup({ ...CUSTOM, frame_src: "list", img_src: "list" });
    expect(screen.getByText(/Applies to embedded frames, external images/)).toBeTruthy();
  });

  test("warns when nothing reads the allowlist", () => {
    setup({ ...CUSTOM, frame_src: "all" });
    expect(screen.getByText(/nothing reads this list/)).toBeTruthy();
  });
});
