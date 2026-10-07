import { describe, test, expect } from "vitest";
import { linkify, firstUrl } from "../linkify.js";

describe("linkify", () => {
  test("splits text and a URL", () => {
    expect(linkify("Slack discussion : https://abc.slack.com/archives/C0123/p179")).toEqual([
      { kind: "text", text: "Slack discussion : " },
      { kind: "url", url: "https://abc.slack.com/archives/C0123/p179" },
    ]);
  });

  test("a value that is only a URL is one url segment", () => {
    expect(linkify("https://helpdesk.example.com/tickets/1001")).toEqual([
      { kind: "url", url: "https://helpdesk.example.com/tickets/1001" },
    ]);
  });

  test("several URLs, text between them kept", () => {
    const segs = linkify("a https://x.io/1 and http://y.io/2 end");
    expect(segs.map((s) => (s.kind === "url" ? `<${s.url}>` : s.text)).join("")).toBe(
      "a <https://x.io/1> and <http://y.io/2> end",
    );
  });

  test("trailing sentence punctuation is not part of the URL", () => {
    expect(linkify("see https://x.io/a.")).toEqual([
      { kind: "text", text: "see " },
      { kind: "url", url: "https://x.io/a" },
      { kind: "text", text: "." },
    ]);
    expect(firstUrl("(https://x.io/a)")).toBe("https://x.io/a");
  });

  test("a paren that belongs to the URL is kept", () => {
    expect(firstUrl("https://en.wikipedia.org/wiki/Foo_(bar)")).toBe("https://en.wikipedia.org/wiki/Foo_(bar)");
  });

  test("query strings and fragments survive", () => {
    expect(firstUrl("x https://a.io/p?q=1&r=2#h y")).toBe("https://a.io/p?q=1&r=2#h");
  });

  test("never turns a non-http scheme into a link", () => {
    expect(linkify("javascript:alert(1)")).toEqual([{ kind: "text", text: "javascript:alert(1)" }]);
    expect(firstUrl("data:text/html,x file:///etc/passwd")).toBeNull();
  });

  test("plain text and empty input", () => {
    expect(linkify("app-abc123")).toEqual([{ kind: "text", text: "app-abc123" }]);
    expect(linkify("")).toEqual([]);
    expect(firstUrl(undefined)).toBeNull();
  });
});
