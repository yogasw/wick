import { describe, test, expect, afterEach } from "vitest";
import {
  artifactCSP,
  artifactSandbox,
  buildArtifactSrcdoc,
  setWidgetPolicy,
  getWidgetPolicy,
  BLOCKED_WIDGET_POLICY,
  type WidgetPolicy,
} from "../richRender.js";

/* The exact CSP string the artifact iframe carried when the policy was
   hardcoded. An all-blocked policy MUST still produce this byte for byte —
   this is the regression guard for making the policy configurable. */
const LEGACY_CSP =
  "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data:; font-src data:; media-src data:; connect-src 'none'; form-action 'none'; frame-src 'none'; object-src 'none'; base-uri 'none'";

afterEach(() => setWidgetPolicy(BLOCKED_WIDGET_POLICY));

describe("artifactCSP", () => {
  test("all-blocked policy is byte-identical to the legacy hardcoded CSP", () => {
    expect(artifactCSP(BLOCKED_WIDGET_POLICY)).toBe(LEGACY_CSP);
  });

  test("an absent policy fails closed to all-blocked", () => {
    expect(artifactCSP({})).toBe(LEGACY_CSP);
    expect(artifactCSP(undefined as unknown as WidgetPolicy)).toBe(LEGACY_CSP);
  });

  test("an unknown mode string fails closed to block", () => {
    for (const bad of ["ALL", "allow", "none", "", "list "]) {
      expect(artifactCSP({ frame_src: bad })).toContain("frame-src 'none'");
    }
  });

  test("mode all opens the directive to any https host", () => {
    expect(artifactCSP({ frame_src: "all" })).toContain("frame-src https:");
    expect(artifactCSP({ connect_src: "all" })).toContain("connect-src https:");
  });

  test("mode list emits only the allowlisted hosts", () => {
    const csp = artifactCSP({
      frame_src: "list",
      allowlist: ["https://maps.google.com", "https://*.example.com"],
    });
    expect(csp).toContain("frame-src https://maps.google.com https://*.example.com");
    // other directives are untouched by one directive going to list
    expect(csp).toContain("connect-src 'none'");
  });

  test("mode list with an empty allowlist allows nothing", () => {
    expect(artifactCSP({ frame_src: "list", allowlist: [] })).toContain("frame-src 'none'");
  });

  test("data: survives in img-src and media-src in every mode", () => {
    for (const m of ["block", "list", "all"]) {
      const csp = artifactCSP({ img_src: m, media_src: m, allowlist: ["https://cdn.example.com"] });
      const img = /img-src ([^;]+)/.exec(csp)?.[1] ?? "";
      const media = /media-src ([^;]+)/.exec(csp)?.[1] ?? "";
      expect(img, `img-src in mode ${m}`).toContain("data:");
      expect(media, `media-src in mode ${m}`).toContain("data:");
    }
  });

  test("the Google Maps case: frame-src list permits the embed", () => {
    const csp = artifactCSP({
      frame_src: "list",
      allowlist: ["https://www.google.com", "https://maps.google.com"],
    });
    expect(csp).toContain("frame-src https://www.google.com https://maps.google.com");
    expect(csp).not.toContain("frame-src 'none'");
  });
});

/* script-src is the one directive that can never lose a source: the
   artifact's own inline scripts, the height reporter, and both postMessage
   bridges are all inline. It can only GAIN external hosts. */
describe("artifactCSP script-src", () => {
  test("blocked still permits the widget's own inline scripts", () => {
    expect(artifactCSP({ script_src: "block" })).toContain("script-src 'unsafe-inline'");
  });

  test("list adds hosts and keeps inline", () => {
    const csp = artifactCSP({ script_src: "list", allowlist: ["https://cdn.example.com"] });
    expect(csp).toContain("script-src https://cdn.example.com 'unsafe-inline'");
  });

  test("all opens any HTTPS host and keeps inline", () => {
    expect(artifactCSP({ script_src: "all" })).toContain("script-src https: 'unsafe-inline'");
  });

  test("inline survives an injected allowlist entry", () => {
    const csp = artifactCSP({ script_src: "list", allowlist: ["evil.test; object-src *"] });
    expect(csp).toContain("script-src 'unsafe-inline'");
    expect(csp).not.toContain("evil.test");
    expect(csp).toContain("object-src 'none'");
  });
});

describe("artifactCSP allowlist sanitising", () => {
  test("an entry carrying CSP syntax is dropped, not emitted", () => {
    const csp = artifactCSP({
      frame_src: "list",
      allowlist: ["https://ok.test", "https://evil.test; script-src *"],
    });
    expect(csp).toContain("https://ok.test");
    expect(csp).not.toContain("evil.test");
    // script-src must remain inline-only — one directive, unpolluted
    expect(csp.match(/script-src/g)).toHaveLength(1);
    expect(csp).toContain("script-src 'unsafe-inline'");
  });

  test("non-conforming entries are dropped", () => {
    const csp = artifactCSP({
      frame_src: "list",
      allowlist: [
        "http://plaintext.test",
        "https://has.test/path",
        "*",
        "https://has space.test",
        "'self'",
        "data:",
        "https://good.test",
      ],
    });
    expect(csp).toContain("frame-src https://good.test");
    for (const bad of ["plaintext", "path", "'self'", "space"]) {
      expect(csp).not.toContain(bad);
    }
  });

  test("a non-array or non-string allowlist does not throw", () => {
    expect(() =>
      artifactCSP({ frame_src: "list", allowlist: "nope" as unknown as string[] }),
    ).not.toThrow();
    expect(artifactCSP({ frame_src: "list", allowlist: [null, 1] as unknown as string[] })).toContain(
      "frame-src 'none'",
    );
  });

  test("never emits a bare wildcard even if one reaches the list", () => {
    const csp = artifactCSP({ frame_src: "list", allowlist: ["*", "https://*"] });
    expect(csp).toContain("frame-src 'none'");
  });
});

describe("artifactSandbox", () => {
  test("allow-scripts only by default", () => {
    expect(artifactSandbox(BLOCKED_WIDGET_POLICY)).toBe("allow-scripts");
  });

  test("adds allow-popups when enabled", () => {
    expect(artifactSandbox({ allow_popups: true })).toBe("allow-scripts allow-popups");
  });

  test("adds popup escape when enabled, implying allow-popups", () => {
    const s = artifactSandbox({ allow_popup_escape: true });
    expect(s).toContain("allow-popups");
    expect(s).toContain("allow-popups-to-escape-sandbox");
  });

  test("popup escape stays off unless asked for", () => {
    expect(artifactSandbox({ allow_popups: true })).not.toContain("escape-sandbox");
    expect(artifactSandbox(BLOCKED_WIDGET_POLICY)).not.toContain("escape-sandbox");
  });

  /* allow-same-origin is the one flag no policy may ever grant: combined with
     allow-scripts on a same-origin frame it lets the widget reach into
     parent.document, read the session, and strip its own sandbox — which would
     void the secure posture for every other project too. */
  test("never grants allow-same-origin, whatever the policy says", () => {
    for (const p of [
      BLOCKED_WIDGET_POLICY,
      { allow_popups: true },
      { allow_popup_escape: true },
      { allow_same_origin: true } as unknown as WidgetPolicy,
    ]) {
      expect(artifactSandbox(p)).not.toContain("allow-same-origin");
    }
  });
});

describe("widget policy context", () => {
  test("defaults to fully blocked", () => {
    expect(getWidgetPolicy()).toEqual(BLOCKED_WIDGET_POLICY);
  });

  test("setWidgetPolicy(null) falls back to blocked rather than leaving it open", () => {
    setWidgetPolicy({ frame_src: "all", allow_popups: true });
    setWidgetPolicy(null);
    expect(artifactCSP()).toBe(LEGACY_CSP);
    expect(artifactSandbox()).toBe("allow-scripts");
  });

  test("buildArtifactSrcdoc uses the context policy when none is passed", () => {
    setWidgetPolicy({ frame_src: "list", allowlist: ["https://maps.google.com"] });
    expect(buildArtifactSrcdoc("<p>hi</p>")).toContain("frame-src https://maps.google.com");
  });

  test("an explicit policy argument wins over the context", () => {
    setWidgetPolicy({ frame_src: "all" });
    expect(buildArtifactSrcdoc("<p>hi</p>", BLOCKED_WIDGET_POLICY)).toContain("frame-src 'none'");
  });

  test("the CSP lands in a meta tag in <head>", () => {
    const out = buildArtifactSrcdoc("<p>hi</p>");
    expect(out).toMatch(/<head>.*http-equiv="Content-Security-Policy"/s);
  });
});
