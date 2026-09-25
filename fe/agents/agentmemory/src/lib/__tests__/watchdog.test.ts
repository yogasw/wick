import { describe, test, expect } from "vitest";
import { render, screen } from "@testing-library/svelte";

import Overview from "../Overview.svelte";
import Health from "../Health.svelte";
import { WATCHDOG_CHURN, watchdogDetail, watchdogLine, reasonWord } from "../format.js";
import { healthFindings, watchdogFinding } from "../health.js";
import type { Overview as OverviewPayload, WatchdogState } from "../types.js";

/* The watchdog as the operator meets it (PLAN §25).
 
   What these pin down is the distinction the feature is for: a watchdog that
   quietly restarts a daemon forty times a day has failed, so "working", "had
   to work", and "gave up" must never render the same. */

const WATCH: WatchdogState = {
  watching: true,
  restarts: 0,
  hung_restarts: 0,
  consecutive_failures: 0,
  gave_up: false,
  stopped_by_operator: false,
};

const state = (over: Partial<WatchdogState>): WatchdogState => ({ ...WATCH, ...over });

describe("watchdogLine", () => {
  test("an unsupervised backend says so, and says why there is no switch for it", () => {
    const l = watchdogLine(state({ watching: false }));
    expect(l.label).toBe("Not watching");
    expect(l.detail).toMatch(/same autostart signal/i);
  });

  test("a missing record reads as not watching rather than as healthy", () => {
    expect(watchdogLine(undefined).label).toBe("Not watching");
  });

  test("a quiet watchdog says it has had nothing to do", () => {
    const l = watchdogLine(WATCH);
    expect(l.label).toBe("Watching");
    expect(l.detail).toMatch(/not needed an intervention/i);
    expect(l.cls).toContain("green");
  });

  test("interventions are counted in the label", () => {
    const l = watchdogLine(state({ restarts: 1, hung_restarts: 1, last_reason: "hung" }));
    expect(l.label).toBe("Watching — 1 restart");
    expect(l.detail).toMatch(/wedged rather than dead/);
  });

  // The whole point of counting: at some number it stops being reassuring.
  test("churn changes the colour, not just the number", () => {
    const calm = watchdogLine(state({ restarts: 1 }));
    const churn = watchdogLine(state({ restarts: WATCHDOG_CHURN }));
    expect(calm.cls).toContain("green");
    expect(churn.cls).toContain("cau");
    expect(churn.cls).toContain("dark:");
  });

  test("a failing start reads as retrying, with the error", () => {
    const l = watchdogLine(state({ consecutive_failures: 2, last_error: "port 49374 in use" }));
    expect(l.label).toBe("Retrying");
    expect(l.detail).toContain("port 49374 in use");
    expect(l.detail).toMatch(/waits longer/i);
  });

  test("giving up is its own state and carries the server's reason", () => {
    const l = watchdogLine(
      state({ gave_up: true, consecutive_failures: 5, gave_up_reason: "5 starts in a row failed; the last error was: not installed" }),
    );
    expect(l.label).toBe("Gave up");
    expect(l.cls).toContain("rose");
    expect(l.detail).toContain("not installed");
  });

  test("an operator's Stop is explained, not reported as a fault", () => {
    const l = watchdogLine(state({ stopped_by_operator: true }));
    expect(l.label).toMatch(/stopped by hand/i);
    expect(l.cls).not.toContain("rose");
    expect(l.detail).toMatch(/starting it again resumes/i);
  });

  test("every reason word says what actually happened", () => {
    expect(reasonWord("hung")).toMatch(/stopped answering/);
    expect(reasonWord("dead")).toMatch(/process had exited/);
    expect(reasonWord("off")).toMatch(/should have been running/);
  });

  test("a last restart is dated", () => {
    const d = watchdogDetail(state({ restarts: 2, last_reason: "dead", last_restart_ms: Date.now() - 60_000 }));
    expect(d).toMatch(/Last: started a daemon whose process had exited/);
  });
});

describe("watchdogFinding — the Health tab", () => {
  test("an unsupervised daemon produces no finding at all", () => {
    expect(watchdogFinding(state({ watching: false }))).toBeNull();
    expect(watchdogFinding(undefined)).toBeNull();
  });

  // The silent failure this whole feature exists to prevent.
  test("giving up is critical and names the consequence", () => {
    const f = watchdogFinding(state({ gave_up: true, consecutive_failures: 5, gave_up_reason: "binary missing" }));
    expect(f?.level).toBe("critical");
    expect(f?.body).toMatch(/silently recalls and records nothing/i);
    expect(f?.fix).toBeDefined();
  });

  test("repeated restarts are a warning even though the daemon is up", () => {
    const f = watchdogFinding(state({ restarts: WATCHDOG_CHURN, hung_restarts: 1 }));
    expect(f?.level).toBe("warn");
    expect(f?.title).toContain(String(WATCHDOG_CHURN));
  });

  test("a quiet watchdog is reported as a pass, not omitted", () => {
    const f = watchdogFinding(WATCH);
    expect(f?.level).toBe("ok");
    expect(f?.id).toBe("watchdog-ok");
  });

  test("a gave-up finding sorts above the informational ones", () => {
    const findings = healthFindings(undefined, undefined, undefined, undefined, state({ gave_up: true }));
    expect(findings[0].id).toBe("watchdog-gave-up");
  });
});

// ── rendering ────────────────────────────────────────────────────────

const payload = (w?: WatchdogState): OverviewPayload =>
  ({
    backend: { id: "ai-memory", name: "ai-memory", blurb: "", icon: "", has_data: true },
    daemon: {
      installed: true,
      version: "2.4.0",
      running: true,
      managed: true,
      state: "running",
      pref_port: 49374,
      bound_port: 49374,
      base_url: "http://127.0.0.1:49374",
    },
    resources: { rss_bytes: 0, rss_known: false, data_dir_bytes: 0, data_dir_known: false },
    autostart_lock: { locked: true },
    watchdog: w,
  }) as unknown as OverviewPayload;

const overviewProps = {
  loading: false,
  busy: false,
  test: null,
  canManage: true,
  installMsg: "",
  installFailed: false,
  onInstall: () => {},
  onStart: () => {},
  onStop: () => {},
  onRestart: () => {},
  onTest: () => {},
};

describe("Overview — the watchdog line", () => {
  test("a healthy daemon still shows the watchdog, saying it has done nothing", () => {
    render(Overview, { props: { ...overviewProps, ov: payload(WATCH) } });
    expect(screen.getByTestId("watchdog-label").textContent).toBe("Watching");
    expect(screen.getByTestId("watchdog").textContent).toMatch(/not needed an intervention/i);
  });

  test("restarts are on the page rather than only in the log", () => {
    render(Overview, { props: { ...overviewProps, ov: payload(state({ restarts: 4, hung_restarts: 2, last_reason: "hung" })) } });
    expect(screen.getByTestId("watchdog-label").textContent).toContain("4 restarts");
    expect(screen.getByTestId("watchdog").textContent).toMatch(/wedged rather than dead/);
  });

  test("the gave-up state is unmissable", () => {
    render(Overview, {
      props: { ...overviewProps, ov: payload(state({ gave_up: true, consecutive_failures: 5, gave_up_reason: "port taken" })) },
    });
    const label = screen.getByTestId("watchdog-label");
    expect(label.textContent).toBe("Gave up");
    expect(label.className).toContain("rose");
    expect(screen.getByTestId("watchdog").textContent).toContain("port taken");
  });

  test("a payload from an older server (no watchdog field) does not claim supervision", () => {
    render(Overview, { props: { ...overviewProps, ov: payload(undefined) } });
    expect(screen.getByTestId("watchdog-label").textContent).toBe("Not watching");
  });
});

describe("Health — supervision among the other silent failures", () => {
  const healthProps = {
    report: { doctor: { rows: [] }, collisions: { checked: true, collisions: [] } },
    scope: {},
    loading: false,
    onRefresh: () => {},
    onGoOverview: () => {},
  } as never;

  test("a watchdog that gave up is triaged on the Health tab", () => {
    render(Health, { props: { ...(healthProps as object), ov: payload(state({ gave_up: true, gave_up_reason: "binary missing" })) } });
    expect(screen.getByText(/watchdog gave up/i)).toBeDefined();
    expect(screen.getByText(/binary missing/)).toBeDefined();
  });

  test("a quiet watchdog appears as a pass", () => {
    render(Health, { props: { ...(healthProps as object), ov: payload(WATCH) } });
    expect(screen.getByText(/Supervised, with nothing to report/i)).toBeDefined();
  });
});

/* The trial roster on the Health tab.

   The switch is per-project and its consequence is host-wide: turning one
   project on stops every un-opted project recording. A finding that says so
   without naming the projects leaves an operator opening them one at a time,
   which is exactly what this tab exists to prevent. */
describe("Health — the per-project trial roster", () => {
  const rosterProps = {
    report: { doctor: { rows: [] }, collisions: { checked: true, collisions: [] } },
    scope: {},
    loading: false,
    onRefresh: () => {},
    onGoOverview: () => {},
    ov: payload(WATCH),
  } as never;

  const roster = {
    trial: { active: true, projects: ["kasir"], silenced: 2 },
    recording: [{ id: "kasir-8c28230d", name: "Kasir", value: "on" as const, recording: true, reason: "on" }],
    silenced: [
      { id: "brand-1f2e3d4c", name: "Brand site", value: "" as const, recording: false, reason: "trial" },
      { id: "legacy-99887766", name: "Legacy API", value: "" as const, recording: false, reason: "trial" },
    ],
  };

  test("names both halves, not just a count", () => {
    render(Health, { props: { ...(rosterProps as object), roster } });
    const card = screen.getByTestId("trial-roster");
    expect(card.textContent).toMatch(/1 recording, 2 silent/);
    expect(screen.getByTestId("trial-recording").textContent).toContain("Kasir");
    const silent = screen.getByTestId("trial-silenced").textContent ?? "";
    expect(silent).toContain("Brand site");
    expect(silent).toContain("Legacy API");
  });

  // The name is what a person recognises; the id is what the policy is keyed
  // by. Both, so the roster can be acted on.
  test("carries the id alongside the name", () => {
    render(Health, { props: { ...(rosterProps as object), roster } });
    expect(screen.getByTestId("trial-silenced").textContent).toContain("brand-1f");
  });

  test("says what ends it, on the card itself", () => {
    render(Health, { props: { ...(rosterProps as object), roster } });
    expect(screen.getByTestId("trial-roster").textContent).toMatch(/Nothing already stored is lost/i);
  });

  test("is absent outside a trial — every project following its instance is not news", () => {
    render(Health, {
      props: { ...(rosterProps as object), roster: { trial: { active: false, silenced: 0 }, recording: [], silenced: [] } },
    });
    expect(screen.queryByTestId("trial-roster")).toBeNull();
  });

  test("a host that could not answer shows the rest of the tab anyway", () => {
    render(Health, { props: { ...(rosterProps as object), roster: null } });
    expect(screen.queryByTestId("trial-roster")).toBeNull();
    // The checks themselves still rendered.
    expect(screen.getByText(/Re-run checks/i)).toBeDefined();
  });
});
