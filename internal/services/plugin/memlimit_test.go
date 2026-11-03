package plugin

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

func TestSupervisorRestartsOverMemoryCap(t *testing.T) {
	oldEvery, oldRSS, oldMin := memCheckEvery, rssOfSocket, BackoffMin
	var rss atomic.Int64
	memCheckEvery, BackoffMin = 10*time.Millisecond, 10*time.Millisecond
	rssOfSocket = func(string) int64 { return rss.Load() }
	t.Cleanup(func() { memCheckEvery, rssOfSocket, BackoffMin = oldEvery, oldRSS, oldMin })

	var capMB atomic.Int64
	capMB.Store(100)
	dir := t.TempDir()
	f := &fakeProcs{handler: http.NotFoundHandler()}
	host := NewHost(NewTokens(""), dir)
	host.spawn = f.spawn
	host.Config = func(string) map[string]string {
		return map[string]string{ConfigMaxRSSMB: fmt.Sprint(capMB.Load())}
	}
	s := host.Add("hungry", "1.0.0", wickplugin.ServiceModule{Meta: wickplugin.ToolMeta{Key: "hungry"}}, "bin")
	s.Sup.Start()
	t.Cleanup(host.StopAll)
	waitFor(t, "running", func() bool { return s.Sup.Status().State == StateRunning })

	rss.Store(90 << 20) // under the cap: left alone
	time.Sleep(60 * time.Millisecond)
	if f.count() != 1 {
		t.Fatalf("restarted under the cap: %d spawns", f.count())
	}

	rss.Store(150 << 20)
	waitFor(t, "memory restart", func() bool { return f.count() >= 2 })
	rss.Store(10 << 20)
	waitFor(t, "running again", func() bool { return s.Sup.Status().State == StateRunning })
	if st := s.Sup.Status(); !strings.Contains(st.LastError, ConfigMaxRSSMB) || st.Restarts < 1 {
		t.Fatalf("status = %+v", st)
	}
	found := false
	for _, l := range s.Sup.Logs.Lines() {
		if strings.Contains(l, "over the 100 MB limit") {
			found = true
		}
	}
	if !found {
		t.Fatalf("logs = %q", s.Sup.Logs.Lines())
	}

	capMB.Store(0) // cap removed: no more restarts
	n := f.count()
	rss.Store(900 << 20)
	time.Sleep(60 * time.Millisecond)
	if f.count() != n {
		t.Fatalf("restarted without a cap")
	}
}

func TestRSSOfPIDReadsProc(t *testing.T) {
	if _, err := os.Stat("/proc/self/status"); err != nil {
		t.Skip("no /proc")
	}
	if got := rssOfPID(os.Getpid()); got <= 0 {
		t.Fatalf("rss of self = %d", got)
	}
	if rssOfPID(0) != 0 || maxRSSFrom(map[string]string{ConfigMaxRSSMB: "x"}) != 0 || maxRSSFrom(map[string]string{ConfigMaxRSSMB: " 2 "}) != 2<<20 {
		t.Fatal("helpers")
	}
}
