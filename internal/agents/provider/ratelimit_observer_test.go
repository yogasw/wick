package provider

import (
	"testing"

	"github.com/yogasw/wick/internal/agents/event"
)

func TestObserveRateLimitHandsReportToObserver(t *testing.T) {
	var got []string
	SetRateLimitObserver(func(ins Instance, info *event.RateLimitInfo) { got = append(got, ins.Name+"/"+info.Status) })
	t.Cleanup(func() { SetRateLimitObserver(nil) })

	full := &event.RateLimitInfo{Status: "allowed", Windows: map[string]event.RateLimitWindow{"five_hour": {Utilization: 39}}}
	observeRateLimit(nil, full)                                       // no instance
	observeRateLimit(&Instance{Name: "a"}, &event.RateLimitInfo{})    // no windows
	observeRateLimit(&Instance{Name: "a"}, nil)                       // no report
	observeRateLimit(&Instance{Type: TypeClaude, Name: "main"}, full) // the one that counts
	if len(got) != 1 || got[0] != "main/allowed" {
		t.Fatalf("observer saw %v", got)
	}

	SetRateLimitObserver(nil)
	observeRateLimit(&Instance{Name: "main"}, full) // no observer: no panic
}
