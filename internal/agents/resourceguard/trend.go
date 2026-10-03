package resourceguard

import (
	"math"
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
