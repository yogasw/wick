package resourceguard

import (
	"math"
	"strconv"
	"strings"
	"time"
)

// sample is one MemAvailable reading.
type sample struct {
	at      time.Time
	availMB float64
}

// slopeMBps is the least-squares slope of available memory over the
// samples, in MB per second. Negative = memory is being eaten. A
// regression rather than first-minus-last so one noisy reading (a page
// cache drop, a reclaim burst) does not read as a trend.
func slopeMBps(s []sample) float64 {
	if len(s) < 3 {
		return 0
	}
	t0 := s[0].at
	var sx, sy, sxx, sxy float64
	n := float64(len(s))
	for _, p := range s {
		x := p.at.Sub(t0).Seconds()
		sx += x
		sy += p.availMB
		sxx += x * x
		sxy += x * p.availMB
	}
	den := n*sxx - sx*sx
	if den == 0 {
		return 0
	}
	return (n*sxy - sx*sy) / den
}

// secondsToExhaustion projects when available memory reaches zero at the
// current slope. +Inf when memory is flat or rising.
func secondsToExhaustion(availMB, slope float64) float64 {
	if slope >= 0 {
		return math.Inf(1)
	}
	return availMB / -slope
}

// trimWindow drops samples older than window, keeping the slice's array.
func trimWindow(s []sample, now time.Time, window time.Duration) []sample {
	i := 0
	for i < len(s) && now.Sub(s[i].at) > window {
		i++
	}
	return append(s[:0], s[i:]...)
}

// parseCPUTimes reads the aggregate "cpu" line of /proc/stat.
// busy = user+nice+system+irq+softirq+steal; total = busy+idle+iowait.
// guest time is already counted inside user and is left out.
func parseCPUTimes(stat string) (busy, total uint64, ok bool) {
	for _, line := range strings.Split(stat, "\n") {
		f := strings.Fields(line)
		if len(f) < 9 || f[0] != "cpu" {
			continue
		}
		v := make([]uint64, 8)
		for i := range v {
			v[i], _ = strconv.ParseUint(f[i+1], 10, 64)
		}
		// user nice system idle iowait irq softirq steal
		busy = v[0] + v[1] + v[2] + v[5] + v[6] + v[7]
		return busy, busy + v[3] + v[4], true
	}
	return 0, 0, false
}

// parseProcsRunning reads procs_running from /proc/stat.
func parseProcsRunning(stat string) int {
	for _, line := range strings.Split(stat, "\n") {
		if rest, ok := strings.CutPrefix(line, "procs_running "); ok {
			n, _ := strconv.Atoi(strings.TrimSpace(rest))
			return n
		}
	}
	return 0
}
