import { render, screen, fireEvent } from "@testing-library/svelte";
import { describe, it, expect, vi } from "vitest";
import RequestRow from "../RequestRow.svelte";
import type { ReqRow } from "../types.js";

function makeRow(over: Partial<ReqRow> = {}): ReqRow {
  return {
    id: 1,
    time: "14:03:22",
    method: "POST",
    path: "/v1/messages",
    host: "localhost:9425",
    remote_addr: "[::1]:5000",
    client_ip: "::1",
    external: false,
    auth: "sk_9r…",
    user_agent: "claude-cli/1.0",
    model: "cc/opus",
    status: 200,
    duration_ms: 1200,
    req_body: '{"model":"cc/opus"}',
    resp_body: '{"id":"msg_1"}',
    ...over,
  };
}

describe("RequestRow", () => {
  it("renders the summary line", () => {
    render(RequestRow, { props: { row: makeRow(), onAnalyze: vi.fn() } });
    expect(screen.getByText("POST /v1/messages")).toBeTruthy();
    expect(screen.getByText("cc/opus")).toBeTruthy();
    expect(screen.getByText("local")).toBeTruthy();
  });

  it("shows external badge for off-machine callers", () => {
    render(RequestRow, { props: { row: makeRow({ external: true }), onAnalyze: vi.fn() } });
    expect(screen.getByText("external")).toBeTruthy();
  });

  it("hides bodies until expanded, then shows them", async () => {
    render(RequestRow, { props: { row: makeRow(), onAnalyze: vi.fn() } });
    expect(screen.queryByText("Analyze ⤢")).toBeNull();
    await fireEvent.click(screen.getByText("POST /v1/messages"));
    expect(screen.getAllByText("Analyze ⤢").length).toBe(2); // req + resp
  });

  it("calls onAnalyze with the raw body when Analyze is clicked", async () => {
    const onAnalyze = vi.fn();
    render(RequestRow, { props: { row: makeRow(), onAnalyze } });
    await fireEvent.click(screen.getByText("POST /v1/messages"));
    await fireEvent.click(screen.getAllByText("Analyze ⤢")[0]);
    expect(onAnalyze).toHaveBeenCalledOnce();
    expect(onAnalyze.mock.calls[0][1]).toBe('{"model":"cc/opus"}');
  });
});
