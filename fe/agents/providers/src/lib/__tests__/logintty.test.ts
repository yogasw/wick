import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import {
  normalizeLoginStatus,
  normalizeUsage,
  loginTTYWSURL,
  fmtCountdown,
  usageLabel,
  fmtResetsIn,
  prettyPlan,
  applyFrame,
  apiLoginTTYStatus,
  apiLoginTTYExtend,
} from "../logintty.js";

beforeEach(() => {
  vi.resetAllMocks();
  document.body.innerHTML = `<div id="app" data-base="/tools/agents"></div>`;
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("normalizeLoginStatus", () => {
  it("maps wire snake_case to domain shape", () => {
    const st = normalizeLoginStatus({
      supported: true,
      account: { connected: true, email: "dev@abc.com", plan: "max", org: "abc org", expires_at: "2026-01-01T00:00:00Z" },
      session: { id: "abc123", state: "running", remaining_s: 240, cap_s: 1700 },
      default_ttl_s: 300,
      extend_s: 300,
      max_ttl_s: 1800,
    });
    expect(st.supported).toBe(true);
    expect(st.account.connected).toBe(true);
    expect(st.account.email).toBe("dev@abc.com");
    expect(st.account.plan).toBe("max");
    expect(st.session?.id).toBe("abc123");
    expect(st.session?.remainingS).toBe(240);
    expect(st.extendS).toBe(300);
  });

  it("handles missing account and session", () => {
    const st = normalizeLoginStatus({ supported: false });
    expect(st.supported).toBe(false);
    expect(st.account.connected).toBe(false);
    expect(st.session).toBeNull();
  });
});

describe("normalizeUsage", () => {
  it("maps windows", () => {
    const u = normalizeUsage({
      supported: true,
      windows: [{ key: "five_hour", utilization: 42.5, resets_at: "2026-09-07T12:00:00Z" }],
    });
    expect(u.supported).toBe(true);
    expect(u.windows).toHaveLength(1);
    expect(u.windows[0].key).toBe("five_hour");
    expect(u.windows[0].utilization).toBe(42.5);
  });

  it("carries the error string for unavailable usage", () => {
    const u = normalizeUsage({ supported: true, windows: [], error: "usage endpoint: 401" });
    expect(u.error).toContain("401");
    expect(u.windows).toHaveLength(0);
  });
});

describe("loginTTYWSURL", () => {
  it("builds a ws URL from the page origin", () => {
    const url = loginTTYWSURL("/tools/agents", "claude", "main");
    expect(url).toMatch(/^wss?:\/\//);
    expect(url).toContain("/tools/agents/api/providers/claude/main/logintty/ws");
  });
});

describe("fmtCountdown", () => {
  it("formats m:ss", () => {
    expect(fmtCountdown(272)).toBe("4:32");
    expect(fmtCountdown(60)).toBe("1:00");
    expect(fmtCountdown(5)).toBe("0:05");
    expect(fmtCountdown(0)).toBe("0:00");
    expect(fmtCountdown(-3)).toBe("0:00");
  });
});

describe("usageLabel", () => {
  it("labels the known windows like the CLI usage screen", () => {
    expect(usageLabel("five_hour")).toBe("Session (5hr)");
    expect(usageLabel("seven_day")).toBe("Weekly (7 day)");
    expect(usageLabel("seven_day_opus")).toBe("Weekly Fable");
    expect(usageLabel("seven_day_fable")).toBe("Weekly Fable");
    expect(usageLabel("mystery")).toBe("mystery");
  });
});

describe("fmtResetsIn", () => {
  const now = Date.parse("2026-09-07T09:00:00Z");
  it("renders hours and days like the CLI", () => {
    expect(fmtResetsIn("2026-09-07T12:10:00Z", now)).toBe("3h");
    expect(fmtResetsIn("2026-09-11T09:30:00Z", now)).toBe("4d");
    expect(fmtResetsIn("2026-09-07T09:25:00Z", now)).toBe("25m");
  });
  it("empty for past or missing timestamps", () => {
    expect(fmtResetsIn("", now)).toBe("");
    expect(fmtResetsIn("2026-09-07T08:00:00Z", now)).toBe("");
    expect(fmtResetsIn("not-a-date", now)).toBe("");
  });
});

describe("prettyPlan", () => {
  it("prefixes claude subscription types like the CLI", () => {
    expect(prettyPlan("team")).toBe("Claude team");
    expect(prettyPlan("max")).toBe("Claude max");
    expect(prettyPlan("api-key")).toBe("API key");
    expect(prettyPlan("")).toBe("");
    expect(prettyPlan("plus")).toBe("plus");
  });
});

describe("applyFrame", () => {
  const base = () => ({
    links: [] as string[],
    success: null,
    failure: null,
    remainingS: 300,
    capS: 1800,
    state: "running",
    exitErr: "",
    account: null,
  });

  it("collects links newest-first without duplicates", () => {
    let st = applyFrame(base(), { t: "link", url: "https://a/1" }, 1000);
    st = applyFrame(st, { t: "link", url: "https://a/2" }, 1001);
    st = applyFrame(st, { t: "link", url: "https://a/1" }, 1002);
    expect(st.links).toEqual(["https://a/2", "https://a/1"]);
  });

  it("stamps success and failure with the event time", () => {
    let st = applyFrame(base(), { t: "success", line: "Logged in as dev@abc.com" }, 5000);
    expect(st.success).toEqual({ line: "Logged in as dev@abc.com", at: 5000 });
    st = applyFrame(st, { t: "fail", line: "OAuth error: invalid code" }, 6000);
    expect(st.failure).toEqual({ line: "OAuth error: invalid code", at: 6000 });
  });

  it("updates ttl and terminal state with account", () => {
    let st = applyFrame(base(), { t: "ttl", remaining_s: 120, cap_s: 900 }, 0);
    expect(st.remainingS).toBe(120);
    expect(st.capS).toBe(900);
    st = applyFrame(st, { t: "state", state: "exited", account: { connected: true, email: "dev@abc.com" } }, 0);
    expect(st.state).toBe("exited");
    expect(st.account?.connected).toBe(true);
    expect(st.account?.email).toBe("dev@abc.com");
  });
});

describe("apiLoginTTYStatus", () => {
  it("GETs the logintty status endpoint", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ supported: true, account: { connected: false } }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
    vi.stubGlobal("fetch", fetchMock);
    const st = await apiLoginTTYStatus("/tools/agents", "claude", "main");
    expect(st.supported).toBe(true);
    expect(fetchMock).toHaveBeenCalledOnce();
    const url = fetchMock.mock.calls[0][0] as string;
    expect(url).toBe("/tools/agents/api/providers/claude/main/logintty");
  });
});

describe("apiLoginTTYExtend", () => {
  it("returns the new ttl on 200", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ remaining_s: 540, cap_s: 1500 }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    ));
    const r = await apiLoginTTYExtend("/tools/agents", "claude", "main");
    expect(r).toEqual({ remainingS: 540, capS: 1500 });
  });

  it("returns null when the cap is reached (409)", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ error: "cap" }), { status: 409 }),
    ));
    const r = await apiLoginTTYExtend("/tools/agents", "claude", "main");
    expect(r).toBeNull();
  });
});
