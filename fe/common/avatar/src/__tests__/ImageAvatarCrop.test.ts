import { describe, test, expect } from "vitest";
import { render, screen } from "@testing-library/svelte";
import ImageAvatar from "../ImageAvatar.svelte";
import { AVATAR_CROPS, CROP_PATHS, cropFrameStyle, imageCrop } from "../image.js";

const src = "/tools/agents/api/team/agents/a1/avatar/image?v=0123456789abcdef";
const image = (crop?: string) => ({ v: "0123456789abcdef", aspect: 1, fit: "fill", x: 0, y: 0, zoom: 0, src, ...(crop === undefined ? {} : { crop }) });
const clip = () => screen.getByTestId("image-avatar").querySelector<HTMLElement>(".clip")!;

describe("image avatar crops", () => {
  test("the four crops, labelled as in the mockup", () => {
    expect(AVATAR_CROPS.map(([k, l]) => `${k}:${l}`)).toEqual(["round:Round", "rounded:Rounded", "hex:Hexagon", "none:Original"]);
    expect(CROP_PATHS.hex).toBe("M25 5L75 5L100 50L75 95L25 95L0 50Z");
  });

  test("absent or unknown crop draws round, as before crops existed", () => {
    for (const c of [undefined, "", "bogus", "circle"]) expect(imageCrop(c)).toBe("round");
    render(ImageAvatar, { props: { image: image(), color: "#ef4444" } });
    expect(screen.getByTestId("image-avatar").querySelector(".frame")?.getAttribute("data-crop")).toBe("round");
    expect(clip().style.borderRadius).toBe("9999px");
    expect(clip().style.boxShadow).toContain("1.5px");
    expect(screen.getByTestId("image-avatar").querySelector("svg")).toBeNull();
  });

  test("rounded: 26% corners with the ring", () => {
    render(ImageAvatar, { props: { image: image("rounded") } });
    expect(clip().style.borderRadius).toBe("26%");
    expect(clip().style.boxShadow).toContain("1.5px");
  });

  test("hex: clipped to the polygon, the ring drawn as an outline outside the clip", () => {
    render(ImageAvatar, { props: { image: image("hex"), color: "#ef4444" } });
    expect(clip().getAttribute("style")).toContain("polygon(25% 5%, 75% 5%, 100% 50%, 75% 95%, 25% 95%, 0 50%)");
    expect(clip().style.boxShadow).toBe("");
    const path = screen.getByTestId("image-avatar").querySelector(".frame > svg path");
    expect(path?.getAttribute("d")).toBe(CROP_PATHS.hex);
    expect(path?.getAttribute("stroke")).toBe("#ef4444");
  });

  test("none (Original): a square, no clip shape", () => {
    render(ImageAvatar, { props: { image: image("none") } });
    expect(clip().style.borderRadius).toBe("0");
    expect(clip().getAttribute("style")).not.toContain("clip-path");
    expect(cropFrameStyle("none")).toBe("border-radius: 0");
  });
});
