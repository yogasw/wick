package metrics

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNoopRecorder(t *testing.T) {
	var n Noop
	n.IncActive()
	n.DecActive()
	n.RecordRun("pg", "query", "success", 42)
	// no panic = pass
}

func TestSimpleRecorder_RecordAndServe(t *testing.T) {
	r := NewSimpleRecorder()
	r.IncActive()
	r.RecordRun("postgres", "query", "success", 120)
	r.RecordRun("postgres", "query", "success", 80)
	r.RecordRun("postgres", "query", "error", 5)
	r.RecordRun("slack", "send_message", "success", 200)
	r.DecActive()

	rr := httptest.NewRecorder()
	r.Handler().ServeHTTP(rr, httptest.NewRequest("GET", "/metrics", nil))
	require.Equal(t, 200, rr.Code)

	body := rr.Body.String()
	assert.Contains(t, body, "wick_connector_runs_active 0")
	assert.Contains(t, body, `wick_connector_runs_total{connector="postgres",operation="query",status="success"} 2`)
	assert.Contains(t, body, `wick_connector_runs_total{connector="postgres",operation="query",status="error"} 1`)
	assert.Contains(t, body, `wick_connector_runs_total{connector="slack",operation="send_message",status="success"} 1`)
	assert.Contains(t, body, `wick_connector_run_duration_seconds_avg{connector="postgres",operation="query"}`)
}

func TestSimpleRecorder_ContentType(t *testing.T) {
	r := NewSimpleRecorder()
	rr := httptest.NewRecorder()
	r.Handler().ServeHTTP(rr, httptest.NewRequest("GET", "/metrics", nil))
	assert.True(t, strings.HasPrefix(rr.Header().Get("Content-Type"), "text/plain"))
}

func TestSimpleRecorder_Concurrent(t *testing.T) {
	r := NewSimpleRecorder()
	done := make(chan struct{})
	for i := 0; i < 50; i++ {
		go func() {
			r.IncActive()
			r.RecordRun("pg", "query", "success", 10)
			r.DecActive()
			done <- struct{}{}
		}()
	}
	for i := 0; i < 50; i++ {
		<-done
	}
	rr := httptest.NewRecorder()
	r.Handler().ServeHTTP(rr, httptest.NewRequest("GET", "/metrics", nil))
	body, _ := io.ReadAll(rr.Body)
	assert.Contains(t, string(body), `wick_connector_runs_total{connector="pg",operation="query",status="success"} 50`)
}
