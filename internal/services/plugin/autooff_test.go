package plugin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

// fastIdle makes one idle "second" a millisecond and checks idleness often.
func fastIdle(t *testing.T) {
	t.Helper()
	oldUnit, oldEvery := idleUnit, idleCheckEvery
	idleUnit, idleCheckEvery = time.Millisecond, 5*time.Millisecond
	t.Cleanup(func() { idleUnit, idleCheckEvery = oldUnit, oldEvery })
}

func autoOffModule(key string, a *wickplugin.ServiceAutoOff) wickplugin.ServiceModule {
	return wickplugin.ServiceModule{
		Meta:    wickplugin.ToolMeta{Key: key},
		Routes:  []wickplugin.ServiceRoute{{Prefix: "/", Auth: wickplugin.AuthPublic}},
		AutoOff: a,
	}
}

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") })
}

// stays asserts the service is still running after d.
func stays(t *testing.T, s *Service, d time.Duration) {
	t.Helper()
	time.Sleep(d)
	if st := s.Sup.Status().State; st != StateRunning {
		t.Fatalf("state = %s, want running", st)
	}
}

func TestAutoOffSleepsAndWakesOnRequest(t *testing.T) {
	fastIdle(t)
	host, f, srv := newTestHost(t, okHandler(), autoOffModule("napper", &wickplugin.ServiceAutoOff{Supported: true, DefaultIdleSeconds: 40}))
	s, _ := host.Get("napper")
	waitFor(t, "sleeping", func() bool { return s.Sup.Status().State == StateSleeping })
	if st := s.Sup.Status(); st.Restarts != 0 || st.SleptAt.IsZero() || f.count() != 1 {
		t.Fatalf("sleep counted as a crash: %+v (spawns %d)", st, f.count())
	}
	f.mu.Lock()
	f.delay = 150 * time.Millisecond
	f.mu.Unlock()
	start := time.Now()
	if code, body := get(t, srv.URL+"/x/napper/", nil); code != http.StatusOK || body != "ok" {
		t.Fatalf("wake request = %d %q", code, body)
	}
	t.Logf("cold start (fake boot 150ms): request answered in %s", time.Since(start).Round(time.Millisecond))
	if st := s.Sup.Status(); st.State != StateRunning || f.count() != 2 || st.LastWakeMS < 150 {
		t.Fatalf("after wake: %+v (spawns %d)", st, f.count())
	}
	if logs := strings.Join(s.Sup.Logs.Lines(), "\n"); !strings.Contains(logs, "sleeping until the next request") || !strings.Contains(logs, "woke up in") {
		t.Errorf("logs missing sleep/wake lines:\n%s", logs)
	}
}

func TestAutoOffParallelRequestsShareOneBoot(t *testing.T) {
	fastIdle(t)
	host, f, srv := newTestHost(t, okHandler(), autoOffModule("crowd", &wickplugin.ServiceAutoOff{Supported: true, DefaultIdleSeconds: 40}))
	s, _ := host.Get("crowd")
	waitFor(t, "sleeping", func() bool { return s.Sup.Status().State == StateSleeping })
	f.mu.Lock()
	f.delay = 200 * time.Millisecond
	f.mu.Unlock()
	var wg sync.WaitGroup
	codes := make([]int, 8)
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i], _ = get(t, srv.URL+"/x/crowd/", nil)
		}(i)
	}
	wg.Wait()
	for i, c := range codes {
		if c != http.StatusOK {
			t.Fatalf("request %d = %d", i, c)
		}
	}
	if f.count() != 2 {
		t.Fatalf("spawns = %d, want 2 (one boot for all waiting requests)", f.count())
	}
}

func TestAutoOffOpenStreamKeepsAwake(t *testing.T) {
	fastIdle(t)
	release := make(chan struct{})
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {}\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	host, _, _ := newTestHost(t, h, autoOffModule("turn", &wickplugin.ServiceAutoOff{Supported: true, DefaultIdleSeconds: 30}))
	s, _ := host.Get("turn")
	// A remote turn reaches the plugin through Transport (the events
	// stream stays open while the turn runs, waiting on replies included).
	rt, err := s.Sup.Transport()
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodGet, "http://plugin"+wickplugin.RemotePathEvents, nil)
	resp, err := (&http.Client{Transport: rt}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	stays(t, s, 200*time.Millisecond)
	close(release)
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	waitFor(t, "sleeping after the turn", func() bool { return s.Sup.Status().State == StateSleeping })
}

func TestAutoOffPolicy(t *testing.T) {
	fastIdle(t)
	cases := []struct {
		name  string
		a     *wickplugin.ServiceAutoOff
		mode  string
		sleep bool
	}{
		{"unsupported never sleeps", &wickplugin.ServiceAutoOff{Reason: "polls a queue"}, "", false},
		{"no declaration never sleeps", nil, "", false},
		{"supported, admin forced off", &wickplugin.ServiceAutoOff{Supported: true}, AutoOffOff, false},
		{"unsupported, admin forced on", &wickplugin.ServiceAutoOff{Reason: "polls a queue"}, AutoOffOn, true},
		{"supported, default", &wickplugin.ServiceAutoOff{Supported: true}, AutoOffDefault, true},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			key := fmt.Sprintf("pol%d", i)
			host, _, _ := newTestHost(t, okHandler(), autoOffModule(key, c.a))
			// idle 30 "seconds" = 30ms here, set by the admin.
			if err := host.Tokens.SetAutoOffSetting(key, AutoOffSetting{Mode: c.mode, IdleSeconds: 30}); err != nil {
				t.Fatal(err)
			}
			s, _ := host.Get(key)
			if c.sleep {
				waitFor(t, "sleeping", func() bool { return s.Sup.Status().State == StateSleeping })
			} else {
				stays(t, s, 200*time.Millisecond)
			}
		})
	}
}

func TestAutoOffAdminStopIsNotWoken(t *testing.T) {
	fastIdle(t)
	host, f, srv := newTestHost(t, okHandler(), autoOffModule("held", &wickplugin.ServiceAutoOff{Supported: true, DefaultIdleSeconds: 30}))
	s, _ := host.Get("held")
	waitFor(t, "sleeping", func() bool { return s.Sup.Status().State == StateSleeping })
	s.Sup.Stop()
	if code, _ := get(t, srv.URL+"/x/held/", nil); code != http.StatusServiceUnavailable {
		t.Fatalf("request to a stopped service = %d, want 503", code)
	}
	if f.count() != 1 || s.Sup.Status().State != StateStopped {
		t.Fatalf("stopped service was woken: spawns %d, %+v", f.count(), s.Sup.Status())
	}
	// The admin's start wakes it as usual.
	s.Sup.Start()
	waitFor(t, "running", func() bool { return s.Sup.Status().State == StateRunning })
}

func TestAutoOffAdminSettingAudited(t *testing.T) {
	host, _, _ := newTestHost(t, okHandler(), autoOffModule("cfg", &wickplugin.ServiceAutoOff{Reason: "listens to a websocket"}))
	type entry struct{ actor, key, detail string }
	var audits []entry
	host.Audit = func(actor, key, detail string) { audits = append(audits, entry{actor, key, detail}) }
	mux := http.NewServeMux()
	host.RegisterAdmin(mux, func(h http.Handler) http.Handler { return h })
	post := func(body string) (int, ServiceView) {
		req := httptest.NewRequest(http.MethodPost, "/manager/api/service-plugins/cfg/auto-off", bytes.NewBufferString(body))
		req.Header.Set("Cookie", "session=ok")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		var v ServiceView
		_ = json.Unmarshal(rec.Body.Bytes(), &v)
		return rec.Code, v
	}
	s, _ := host.Get("cfg")
	if v := host.View(s, false).AutoOff; v.Enabled || v.Supported || v.Reason != "listens to a websocket" || v.Mode != AutoOffDefault || v.IdleSeconds != wickplugin.DefaultAutoOffIdleSeconds {
		t.Fatalf("default view = %+v", v)
	}
	if code, _ := post(`{"mode":"on"}`); code != http.StatusConflict {
		t.Fatalf("forcing on without confirm = %d, want 409", code)
	}
	if code, _ := post(`{"mode":"sideways"}`); code != http.StatusBadRequest {
		t.Fatalf("bad mode = %d, want 400", code)
	}
	if code, _ := post(`{"mode":"off","idle_seconds":5}`); code != http.StatusBadRequest {
		t.Fatalf("idle under a minute = %d, want 400", code)
	}
	if len(audits) != 0 {
		t.Fatalf("rejected changes were audited: %+v", audits)
	}
	code, v := post(`{"mode":"on","idle_seconds":600,"confirm":true}`)
	if code != http.StatusOK || !v.AutoOff.Enabled || !v.AutoOff.Forced || v.AutoOff.IdleSeconds != 600 {
		t.Fatalf("forced on = %d %+v", code, v.AutoOff)
	}
	if len(audits) != 1 || audits[0].actor != "u1@example.test" || audits[0].key != "cfg" ||
		audits[0].detail != "service auto-off: mode=default idle=15m0s -> mode=on idle=10m0s" {
		t.Fatalf("audit = %+v", audits)
	}
	if code, v = post(`{"mode":"default"}`); code != http.StatusOK || v.AutoOff.Enabled || v.AutoOff.Forced || len(audits) != 2 {
		t.Fatalf("back to default = %d %+v (audits %d)", code, v.AutoOff, len(audits))
	}
	if got := host.Tokens.AutoOffSetting("cfg"); got != (AutoOffSetting{}) {
		t.Fatalf("default setting kept a row: %+v", got)
	}
}
