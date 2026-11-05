package main

import (
	"strings"
	"testing"
	"time"
)

func TestPureGlobals(t *testing.T) {
	f := newFake(t, 0)
	ev, _ := f.run("ts", `
		const enc = new TextEncoder().encode("héllo ✓");
		const back = new TextDecoder().decode(enc);
		const fromBuf = new TextDecoder("utf8").decode(enc.buffer);
		const bom = new TextDecoder().decode(new Uint8Array([0xEF, 0xBB, 0xBF, 0x61]));
		const bad = new TextDecoder().decode(new Uint8Array([0x61, 0xFF, 0x62]));
		let fatal = "";
		try { new TextDecoder("utf-8", { fatal: true }).decode(new Uint8Array([0xFF])); } catch (e) { fatal = e.name; }
		let latin = "";
		try { btoa("✓"); } catch (e) { latin = String(e.message); }
		let label = "";
		try { new TextDecoder("latin1"); } catch (e) { label = e.name; }
		return [enc instanceof Uint8Array, enc.length, back, fromBuf, bom, bad, fatal,
			btoa("hello"), atob("aGVsbG8="), atob(" aGVs bG8 "), atob(btoa("\xff\x00")).charCodeAt(0),
			latin.includes("Latin1"), label, new TextDecoder().decode(), typeof __wick_pure];
	`, nil, 5*time.Second)
	if !ev.OK {
		t.Fatalf("run failed: %s", ev.Error)
	}
	want := `[true,10,"héllo ✓","héllo ✓","a","a�b","TypeError","aGVsbG8=","hello","hello",255,true,"RangeError","","undefined"]`
	if string(ev.Result) != want {
		t.Fatalf("result = %s\nwant     %s", ev.Result, want)
	}
}

func TestPureGlobalsBadInput(t *testing.T) {
	f := newFake(t, 0)
	for _, code := range []string{
		`return atob("!!!")`,
		`return new TextDecoder().decode("text")`,
	} {
		ev, _ := f.run("js", code, nil, 5*time.Second)
		if ev.OK {
			t.Fatalf("%s: accepted: %s", code, ev.Result)
		}
	}
}

func TestPureGlobalsCapped(t *testing.T) {
	f := newFake(t, 0)
	// Built from pieces under the guard, then one call over maxAlloc.
	ev, _ := f.run("js", `
		const s = "x".repeat(1 << 23);
		const big = s + s + "x";
		const out = [];
		for (const fn of [() => btoa(big), () => atob(big), () => new TextEncoder().encode(big)]) {
			try { fn(); out.push("no"); } catch (e) { out.push(e.name + ":" + e.message.includes("allocation too large")); }
		}
		return out;
	`, nil, 20*time.Second)
	if !ev.OK || string(ev.Result) != `["RangeError:true","RangeError:true","RangeError:true"]` {
		t.Fatalf("got ok=%v result=%s err=%s", ev.OK, ev.Result, ev.Error)
	}
	if strings.Contains(ev.Error, "out of memory") {
		t.Fatal(ev.Error)
	}
}
