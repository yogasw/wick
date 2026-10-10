import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import { CROP_PATHS } from "@wick-fe/common-avatar";
import AvatarImageEditor from "../AvatarImageEditor.svelte";

const src = "/tools/agents/api/team/agents/a1/avatar/image?v=0123456789abcdef";
const props = (crop: Record<string, unknown>, onCrop = vi.fn()) => ({
  src, aspect: 1, crop: { fit: "fill", x: 4, y: 0, zoom: 0, ...crop }, onFile: vi.fn(), onReset: vi.fn(), onCrop,
});
const tiles = () => screen.getAllByTestId("avatar-image-crop");

describe("AvatarImageEditor › Crop", () => {
  test("four tiles, Round pressed when the saved crop has none", () => {
    render(AvatarImageEditor, { props: props({}) });
    expect(tiles().map((b) => b.textContent?.trim())).toEqual(["Round", "Rounded", "Hexagon", "Original"]);
    expect(tiles().map((b) => b.getAttribute("aria-pressed"))).toEqual(["true", "false", "false", "false"]);
    expect(tiles().every((b) => b.querySelector("[data-testid=image-avatar]"))).toBe(true);
    expect(screen.getByTestId("avatar-image-outline").querySelector("path")?.getAttribute("d")).toBe(CROP_PATHS.round);
  });

  test("the pressed tile and the frame outline follow the crop", () => {
    render(AvatarImageEditor, { props: props({ crop: "hex" }) });
    expect(tiles().find((b) => b.getAttribute("aria-pressed") === "true")?.textContent?.trim()).toBe("Hexagon");
    expect(screen.getByTestId("avatar-image-frame").getAttribute("data-crop")).toBe("hex");
    expect(screen.getByTestId("avatar-image-outline").querySelector("path")?.getAttribute("d")).toBe(CROP_PATHS.hex);
  });

  test("a tile saves its crop and keeps the position", async () => {
    const onCrop = vi.fn();
    render(AvatarImageEditor, { props: props({}, onCrop) });
    await fireEvent.click(screen.getByRole("button", { name: "Original" }));
    expect(onCrop).toHaveBeenLastCalledWith({ fit: "fill", x: 4, y: 0, zoom: 0, crop: "none" });
    await fireEvent.click(screen.getByRole("button", { name: "Rounded" }));
    expect(onCrop).toHaveBeenLastCalledWith(expect.objectContaining({ crop: "rounded" }));
  });

  test("Fit / Fill reset the position but keep the crop", async () => {
    const onCrop = vi.fn();
    render(AvatarImageEditor, { props: props({ crop: "hex" }, onCrop) });
    await fireEvent.click(screen.getByTestId("avatar-image-fit"));
    expect(onCrop).toHaveBeenLastCalledWith({ fit: "fit", x: 0, y: 0, zoom: 0, crop: "hex" });
  });
});
