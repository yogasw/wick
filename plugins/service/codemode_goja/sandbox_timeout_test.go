package main

import (
	"strings"
	"testing"
	"time"
)

func TestCallDeadline(t *testing.T) {
	for _, tc := range []struct {
		ms        int64
		remaining time.Duration
		want      time.Duration
	}{
		{0, 60 * time.Second, callTimeout},
		{0, 5 * time.Second, 4 * time.Second},
		{0, 300 * time.Millisecond, 300 * time.Millisecond},
		{2500, 60 * time.Second, 2500 * time.Millisecond},
		{90000, 30 * time.Second, 30 * time.Second},
		{0, -time.Second, minCallTimeout},
	} {
		if got := callDeadline(tc.ms, tc.remaining); got != tc.want {
			t.Errorf("callDeadline(%d, %s) = %s, want %s", tc.ms, tc.remaining, got, tc.want)
		}
	}
}

func TestHungCallTimesOutAndRunGoesOn(t *testing.T) {
	f := newFake(t, 0)
	f.hang = map[string]bool{"conn:x/hang": true}
	start := time.Now()
	ev, _ := f.run("ts", `
		let msg = "";
		try { await wick.call("conn:x/hang", {}, { timeout_ms: 200 }); } catch (e) { msg = String(e.message || e); }
		const many = wick.callMany([
			{ toolId: "conn:x/hang", params: {}, timeout_ms: 200 },
			{ toolId: "conn:x/ok", params: {} },
		]);
		const viaTool = wick.tool("conn:x/ok")({}, { timeoutMs: 1500 });
		return [msg, many[0].error, many[1].ok, viaTool.tool];
	`, nil, 10*time.Second)
	if !ev.OK {
		t.Fatalf("run failed: %s", ev.Error)
	}
	want := `["conn:x/hang: timed out after 0.2s","timed out after 0.2s",true,"conn:x/ok"]`
	if string(ev.Result) != want {
		t.Fatalf("result = %s", ev.Result)
	}
	// Each wait is the call's deadline plus the grace, not the run's 10s.
	if d := time.Since(start); d > 4*time.Second {
		t.Fatalf("took %v", d)
	}
	if ms, _ := f.timeouts.Load("conn:x/ok"); ms != int64(1500) {
		t.Fatalf("timeout_ms sent = %v", ms)
	}
}

func TestDefaultCallDeadlineLeavesRunTime(t *testing.T) {
	f := newFake(t, 0)
	f.hang = map[string]bool{"conn:x/hang": true}
	// A 2.5s run: the default deadline is 1.5s, so the script still has time
	// to handle the failure after the grace.
	ev, _ := f.run("js", `try { wick.call("conn:x/hang", {}); } catch (e) { return String(e.message); }`, nil, 2500*time.Millisecond)
	if !ev.OK || !strings.Contains(string(ev.Result), "timed out after 1.5s") {
		t.Fatalf("got ok=%v result=%s err=%s", ev.OK, ev.Result, ev.Error)
	}
	if ms, _ := f.timeouts.Load("conn:x/hang"); ms.(int64) > 1500 || ms.(int64) < 1300 {
		t.Fatalf("timeout_ms sent = %v", ms)
	}
}

func TestRunTimeoutIsFlagged(t *testing.T) {
	f := newFake(t, 0)
	f.hang = map[string]bool{"conn:x/hang": true}
	ev, _ := f.run("js", `wick.call("conn:x/hang", {}, { timeout_ms: 60000 }); return 1`, nil, 400*time.Millisecond)
	if ev.OK || !ev.TimedOut {
		t.Fatalf("got %+v", ev)
	}
	ev, _ = f.run("js", `while (true) {}`, nil, 300*time.Millisecond)
	if ev.OK || !ev.TimedOut || ev.Error != "timeout" {
		t.Fatalf("got %+v", ev)
	}
	ev, _ = f.run("js", `throw new Error("boom")`, nil, time.Second)
	if ev.OK || ev.TimedOut {
		t.Fatalf("got %+v", ev)
	}
}
