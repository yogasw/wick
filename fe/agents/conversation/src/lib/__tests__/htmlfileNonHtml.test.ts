import { describe, test, expect, vi, beforeEach, afterEach } from "vitest";
import { renderMarkdown } from "../markdown.js";
import { enrich, setFileContext, htmlFileKind } from "../richRender.js";

/* An `htmlfile` fence whose path is not an HTML file must not be loaded into
   the iframe preview (a PNG rendered as a document shows its raw bytes). */
function enriched(text: string): HTMLElement {
  const node = document.createElement("div");
  node.innerHTML = renderMarkdown(text);
  enrich(node, text);
  return node;
}

const fence = (path: string) => "```htmlfile\n" + path + "\n```";
const url = (path: string) => `/api/sessions/s1/files/download?path=${encodeURIComponent(path)}`;

describe("htmlfile fence by extension", () => {
  beforeEach(() => {
    setFileContext("/api", "s1");
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({ text: () => Promise.resolve("<p>x</p>") }));
  });
  afterEach(() => {
    setFileContext("", "");
    vi.unstubAllGlobals();
  });

  test("an image path renders an <img> linking to the download url", () => {
    const path = "artifacts/agent-avatars/client-dermies.PNG";
    const node = enriched(fence(path));
    const a = node.querySelector<HTMLAnchorElement>('a[data-htmlfile-nonhtml="image"]');
    expect(a).not.toBeNull();
    expect(a!.getAttribute("href")).toBe(url(path));
    const img = a!.querySelector("img") as HTMLImageElement;
    expect(img.getAttribute("src")).toBe(url(path));
    expect(img.alt).toBe("client-dermies.PNG");
    expect(img.className).toContain("max-w-full");
    expect(img.className).toContain("rounded-lg");
    expect(node.querySelector("iframe")).toBeNull();
    expect(fetch).not.toHaveBeenCalled();
  });

  test("another non-HTML path renders a download chip, no preview", () => {
    const path = "reports/data.csv";
    const node = enriched(fence(path));
    const a = node.querySelector<HTMLAnchorElement>('a[data-htmlfile-nonhtml="other"]');
    expect(a).not.toBeNull();
    expect(a!.getAttribute("href")).toBe(url(path));
    expect(a!.textContent).toBe("data.csv (not HTML, click to download)");
    expect(a!.getAttribute("download")).toBe("data.csv");
    expect(node.querySelector("img")).toBeNull();
    expect(node.querySelector("iframe")).toBeNull();
    expect(fetch).not.toHaveBeenCalled();
  });

  test("an .html path still mounts the HtmlArtifact preview", async () => {
    const path = "artifacts/dash.html";
    const node = enriched(fence(path));
    expect(node.querySelector("[data-htmlfile-nonhtml]")).toBeNull();
    await vi.waitFor(() => expect(node.querySelector("iframe")).not.toBeNull());
    expect(fetch).toHaveBeenCalledWith(url(path));
  });

  test("htmlFileKind classifies by extension, case-insensitive", () => {
    expect(htmlFileKind("a/b.png")).toBe("image");
    expect(htmlFileKind("b.JPEG")).toBe("image");
    expect(htmlFileKind("b.jpg")).toBe("image");
    expect(htmlFileKind("b.gif")).toBe("image");
    expect(htmlFileKind("b.webp")).toBe("image");
    expect(htmlFileKind("b.Svg")).toBe("image");
    expect(htmlFileKind("b.HTM")).toBe("html");
    expect(htmlFileKind("b.html")).toBe("html");
    expect(htmlFileKind("noext")).toBe("html");
    expect(htmlFileKind("b.pdf")).toBe("other");
    expect(htmlFileKind("dir.v2/b.json")).toBe("other");
  });
});
