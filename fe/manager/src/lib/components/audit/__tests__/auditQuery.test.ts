import { describe, it, expect } from "vitest";
import { formatAuditQuery, parseAuditQuery, toISOFromLocal, toLocalInput } from "../auditQuery.js";

describe("auditQuery", () => {
  it("parses keys, aliases, quotes, latency and free text", () => {
    const p = parseAuditQuery('type:slack operation:send src:mcp,code_mode user:"Dev One" latency>1000 ms<5000 timed out bogus:x');
    expect(p.values).toEqual({ connector: "slack", op: "send", source: "mcp,code_mode", user: "Dev One", error: "timed out" });
    expect(p.minMs).toBe("1000");
    expect(p.maxMs).toBe("5000");
    expect(p.unknown).toEqual(["bogus:x"]);
  });

  it("writes a filter back, quoting values with spaces", () => {
    const q = formatAuditQuery(
      { source: "mcp", status: "", connector: "slack", user: "u-1", error: "rate limit", min_ms: "200", from: "", to: "", page: 1 },
      (k, v) => (k === "user" ? "Dev One" : v),
    );
    expect(q).toBe('connector:slack source:mcp user:"Dev One" error:"rate limit" latency>200');
    expect(parseAuditQuery(q).values.user).toBe("Dev One");
  });

  it("round-trips a local minute through ISO and keeps a bare date", () => {
    expect(toISOFromLocal("2026-11-04")).toBe("2026-11-04");
    expect(toLocalInput(toISOFromLocal("2026-11-04T09:10"))).toBe("2026-11-04T09:10");
    expect(toISOFromLocal("")).toBe("");
  });
});
