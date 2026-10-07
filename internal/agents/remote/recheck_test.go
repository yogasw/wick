package remote

import (
	"strings"
	"testing"
	"time"
)

func TestIsTimeoutReadsWait(t *testing.T) {
	d, ok := IsTimeout(TimeoutMessage(3*time.Minute) + " This bot answers only when @mentioned.")
	if !ok || d != 3*time.Minute {
		t.Fatalf("IsTimeout = %v %v", d, ok)
	}
	if _, ok := IsTimeout("something else"); ok {
		t.Fatal("not a timeout")
	}
}

func TestRecheckSettled(t *testing.T) {
	cases := []struct {
		r    Recheck
		want bool
	}{
		{Recheck{Text: "done", Done: true}, true},
		{Recheck{Text: "text, no marker"}, true},
		{Recheck{Text: "partial", Busy: true}, false},
		{Recheck{}, false},
	}
	for _, c := range cases {
		if got := c.r.Settled(); got != c.want {
			t.Fatalf("%+v.Settled() = %v", c.r, got)
		}
	}
}

func TestPendingNoticeWording(t *testing.T) {
	got := PendingNotice("halo", 3*time.Minute)
	if !strings.HasPrefix(got, "@halo has not replied within 3m0s; its reply will be forwarded automatically") {
		t.Fatalf("notice = %q", got)
	}
}
