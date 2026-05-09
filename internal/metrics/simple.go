package metrics

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// SimpleRecorder is a zero-dependency Recorder backed by atomic counters.
// It exposes a Prometheus-compatible text scrape endpoint via Handler().
// Wire it in at boot:
//
//	rec := metrics.NewSimpleRecorder()
//	connectorsSvc.SetMetrics(rec)
//	mux.Handle("GET /metrics", rec.Handler())
type SimpleRecorder struct {
	active atomic.Int64

	mu      sync.RWMutex
	runs    map[string]*atomic.Int64 // "key:op:status" → count
	latSum  map[string]*atomic.Int64 // "key:op" → total ms
	latCnt  map[string]*atomic.Int64 // "key:op" → count
}

// NewSimpleRecorder returns a ready-to-use SimpleRecorder.
func NewSimpleRecorder() *SimpleRecorder {
	return &SimpleRecorder{
		runs:   make(map[string]*atomic.Int64),
		latSum: make(map[string]*atomic.Int64),
		latCnt: make(map[string]*atomic.Int64),
	}
}

func (r *SimpleRecorder) RecordRun(connectorKey, operationKey, status string, latencyMs int) {
	runKey := connectorKey + ":" + operationKey + ":" + status
	latKey := connectorKey + ":" + operationKey

	r.mu.Lock()
	if r.runs[runKey] == nil {
		r.runs[runKey] = &atomic.Int64{}
	}
	if r.latSum[latKey] == nil {
		r.latSum[latKey] = &atomic.Int64{}
		r.latCnt[latKey] = &atomic.Int64{}
	}
	cnt := r.runs[runKey]
	ls := r.latSum[latKey]
	lc := r.latCnt[latKey]
	r.mu.Unlock()

	cnt.Add(1)
	ls.Add(int64(latencyMs))
	lc.Add(1)
}

func (r *SimpleRecorder) IncActive() { r.active.Add(1) }
func (r *SimpleRecorder) DecActive() { r.active.Add(-1) }

// Handler returns an HTTP handler that serves a Prometheus-compatible
// text exposition of the collected metrics.
func (r *SimpleRecorder) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		fmt.Fprintf(w, "# HELP wick_connector_runs_active Number of connector operations currently in-flight.\n")
		fmt.Fprintf(w, "# TYPE wick_connector_runs_active gauge\n")
		fmt.Fprintf(w, "wick_connector_runs_active %d\n\n", r.active.Load())

		r.mu.RLock()
		runKeys := make([]string, 0, len(r.runs))
		for k := range r.runs {
			runKeys = append(runKeys, k)
		}
		latKeys := make([]string, 0, len(r.latCnt))
		for k := range r.latCnt {
			latKeys = append(latKeys, k)
		}
		r.mu.RUnlock()

		sort.Strings(runKeys)
		sort.Strings(latKeys)

		fmt.Fprintf(w, "# HELP wick_connector_runs_total Total connector operation executions.\n")
		fmt.Fprintf(w, "# TYPE wick_connector_runs_total counter\n")
		for _, k := range runKeys {
			r.mu.RLock()
			ctr := r.runs[k]
			r.mu.RUnlock()
			parts := strings.SplitN(k, ":", 3)
			if len(parts) != 3 {
				continue
			}
			fmt.Fprintf(w, `wick_connector_runs_total{connector=%q,operation=%q,status=%q} %d`+"\n",
				parts[0], parts[1], parts[2], ctr.Load())
		}

		fmt.Fprintf(w, "\n# HELP wick_connector_run_duration_seconds_avg Average connector operation latency in seconds.\n")
		fmt.Fprintf(w, "# TYPE wick_connector_run_duration_seconds_avg gauge\n")
		for _, k := range latKeys {
			r.mu.RLock()
			ls := r.latSum[k]
			lc := r.latCnt[k]
			r.mu.RUnlock()
			cnt := lc.Load()
			if cnt == 0 {
				continue
			}
			avgSec := float64(ls.Load()) / float64(cnt) / 1000.0
			parts := strings.SplitN(k, ":", 2)
			if len(parts) != 2 {
				continue
			}
			fmt.Fprintf(w, `wick_connector_run_duration_seconds_avg{connector=%q,operation=%q} %.6f`+"\n",
				parts[0], parts[1], avgSec)
		}
	})
}
