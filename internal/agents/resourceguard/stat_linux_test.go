//go:build linux

package resourceguard

import "testing"

func TestParseStatAndCPUMax(t *testing.T) {
	p, ok := parseStat("4242 (node (vitest)) R 4000 1 1 0 -1 0 0 0 0 0 70 30 0 0 20 0 1 0 1 100 2560 0")
	if !ok || p.PID != 4242 || p.PPID != 4000 || p.Comm != "node (vitest)" || p.CPUTicks != 100 || p.RSSBytes == 0 {
		t.Fatalf("stat = %+v %v", p, ok)
	}
	if cpuMax(140) != "140000 100000" || cpuMax(0) != "max 100000" {
		t.Fatalf("cpuMax wrong: %q %q", cpuMax(140), cpuMax(0))
	}
}
