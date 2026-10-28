import { describe, test, expect, vi, beforeEach, afterEach } from "vitest";
import { renderMarkdown } from "../markdown.js";
import { enrich, setFileContext, htmlFileKind, HTMLFILE_TEXT_PREVIEW_BYTES } from "../richRender.js";

/* An `htmlfile` fence whose path is not an HTML file must not be loaded into
   the HtmlArtifact preview (a PNG rendered as a document shows its raw bytes).
   Previewable files show inline; only the rest fall back to a download. */
function enriched(text: string): HTMLElement {
  const node = document.createElement("div");
  node.innerHTML = renderMarkdown(text);
  enrich(node, text);
  return node;
}

const fence = (path: string) => "```htmlfile\n" + path + "\n```";
const q = (path: string) => `?path=${encodeURIComponent(path)}`;
const raw = (path: string) => `/api/sessions/s1/files/raw${q(path)}`;
const read = (path: string) => `/api/sessions/s1/files/read${q(path)}`;
const download = (path: string) => `/api/sessions/s1/files/download${q(path)}`;

function stubFetch(body: unknown, ok = true) {
  const fn = vi.fn().mockResolvedValue({
    ok,
    status: ok ? 200 : 500,
    json: () => Promise.resolve(body),
    text: () => Promise.resolve("<p>x</p>"),
  });
  vi.stubGlobal("fetch", fn);
  return fn;
}

describe("htmlfile fence by extension", () => {
  beforeEach(() => {
    setFileContext("/api", "s1");
    stubFetch({});
  });
  afterEach(() => {
    setFileContext("", "");
    vi.unstubAllGlobals();
  });

  test("an image renders inline from files/raw, never the download endpoint", () => {
    const path = "artifacts/agent-avatars/client-alpha.PNG";
    const node = enriched(fence(path));
    const host = node.querySelector('[data-htmlfile-nonhtml="image"]') as HTMLElement;
    expect(host).not.toBeNull();
    const a = host.querySelector("a") as HTMLAnchorElement;
    expect(a.getAttribute("href")).toBe(raw(path));
    expect(a.hasAttribute("download")).toBe(false);
    const img = a.querySelector("img") as HTMLImageElement;
    expect(img.getAttribute("src")).toBe(raw(path));
    expect(img.alt).toBe("client-alpha.PNG");
    expect(img.className).toContain("max-w-full");
    expect(img.className).toContain("rounded-lg");
    expect(node.querySelector("iframe")).toBeNull();
    expect(fetch).not.toHaveBeenCalled();
  });

  test("a pdf renders in an iframe from files/raw", () => {
    const path = "docs/report.pdf";
    const node = enriched(fence(path));
    const frame = node.querySelector('[data-htmlfile-nonhtml="pdf"] iframe') as HTMLIFrameElement;
    expect(frame).not.toBeNull();
    expect(frame.getAttribute("src")).toBe(raw(path));
    expect(frame.title).toBe("report.pdf");
    expect(node.querySelector("[data-htmlfile-download]")).toBeNull();
  });

  test("a text file renders its content in a <pre> via files/read", async () => {
    const path = "logs/run.log";
    const f = stubFetch({ content: "line1\n<b>not html</b>" });
    const node = enriched(fence(path));
    const pre = node.querySelector('[data-htmlfile-nonhtml="text"] pre') as HTMLElement;
    expect(pre).not.toBeNull();
    await vi.waitFor(() => expect(pre.textContent).toBe("line1\n<b>not html</b>"));
    expect(f).toHaveBeenCalledWith(read(path), { credentials: "same-origin" });
    expect(pre.querySelector("b")).toBeNull();
    expect(node.querySelector("[data-htmlfile-truncated]")).toBeNull();
  });

  test("a large text file is capped with a truncated note", async () => {
    const path = "data/big.csv";
    stubFetch({ content: "x".repeat(HTMLFILE_TEXT_PREVIEW_BYTES + 10) });
    const node = enriched(fence(path));
    await vi.waitFor(() => expect(node.querySelector("[data-htmlfile-truncated]")).not.toBeNull());
    const pre = node.querySelector("pre") as HTMLElement;
    expect(pre.textContent!.length).toBe(HTMLFILE_TEXT_PREVIEW_BYTES);
    const dl = node.querySelector("[data-htmlfile-truncated] a") as HTMLAnchorElement;
    expect(dl.getAttribute("href")).toBe(download(path));
  });

  test("a text file the server will not inline falls back to a download chip", async () => {
    const path = "data/huge.json";
    stubFetch({ tooBig: true, content: "" });
    const node = enriched(fence(path));
    await vi.waitFor(() => expect(node.querySelector("[data-htmlfile-download]")).not.toBeNull());
    expect(node.querySelector("pre")).toBeNull();
  });

  test("another binary falls back to a download chip, no preview", () => {
    const path = "reports/archive.zip";
    const node = enriched(fence(path));
    const a = node.querySelector('[data-htmlfile-nonhtml="other"] a[data-htmlfile-download]') as HTMLAnchorElement;
    expect(a).not.toBeNull();
    expect(a.getAttribute("href")).toBe(download(path));
    expect(a.getAttribute("download")).toBe("archive.zip");
    expect(a.textContent).toBe("archive.zip (cannot be previewed, click to download)");
    expect(node.querySelector("img")).toBeNull();
    expect(node.querySelector("iframe")).toBeNull();
    expect(fetch).not.toHaveBeenCalled();
  });

  test("an .html path still mounts the HtmlArtifact preview", async () => {
    const path = "artifacts/dash.html";
    const node = enriched(fence(path));
    expect(node.querySelector("[data-htmlfile-nonhtml]")).toBeNull();
    await vi.waitFor(() => expect(node.querySelector("iframe")).not.toBeNull());
    expect(fetch).toHaveBeenCalledWith(download(path));
  });

  test("htmlFileKind classifies by extension, case-insensitive", () => {
    for (const p of ["a/b.png", "b.JPEG", "b.jpg", "b.gif", "b.webp", "b.Svg"]) expect(htmlFileKind(p)).toBe("image");
    expect(htmlFileKind("b.PDF")).toBe("pdf");
    for (const p of ["b.txt", "b.MD", "b.json", "b.csv", "b.log"]) expect(htmlFileKind(p)).toBe("text");
    for (const p of ["b.HTM", "b.html", "noext"]) expect(htmlFileKind(p)).toBe("html");
    expect(htmlFileKind("b.zip")).toBe("other");
    expect(htmlFileKind("dir.v2/b.xlsx")).toBe("other");
  });
});
