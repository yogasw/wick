package state

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/workflow"
)

// LokiPusher batches workflow RunEvents and pushes them to a Loki HTTP
// endpoint asynchronously. Events are queued into a channel; a background
// goroutine flushes every 5 seconds or whenever 100 entries accumulate.
//
// Payload is Loki-compatible push body per
// https://grafana.com/docs/loki/latest/reference/loki-http-api/#push-log-entries-to-loki
//
// Label scheme:
//   - wick_workflow  = workflow id
//   - wick_run       = run ID
//   - wick_event     = event type (node_started, node_completed, …)
//   + any extra labels from ExtraLabels
//
// Input/output JSON body is intentionally excluded from the pushed
// payload to avoid Loki size limits — only event metadata is sent.
// The full payload remains on disk in events.jsonl.
type LokiPusher struct {
	url         string
	extraLabels map[string]string // parsed from "k=v,k2=v2" config string
	queue       chan lokiEntry
	wg          sync.WaitGroup
	cancel      context.CancelFunc
}

type lokiEntry struct {
	id    string
	runID string
	ev    workflow.RunEvent
}

// NewLokiPusher creates a pusher targeting lokiURL. extraLabelStr is an
// optional "key=value,key2=value2" string. Returns nil if lokiURL is empty.
func NewLokiPusher(lokiURL, extraLabelStr string) *LokiPusher {
	if strings.TrimSpace(lokiURL) == "" {
		return nil
	}
	labels := parseLabels(extraLabelStr)
	p := &LokiPusher{
		url:         strings.TrimRight(lokiURL, "/") + "/loki/api/v1/push",
		extraLabels: labels,
		queue:       make(chan lokiEntry, 1000),
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.wg.Add(1)
	go p.loop(ctx)
	return p
}

// Push enqueues one event. Non-blocking — drops silently if queue full
// (Loki is a best-effort sink; disk events.jsonl is the source of truth).
func (p *LokiPusher) Push(id, runID string, ev workflow.RunEvent) {
	if p == nil {
		return
	}
	select {
	case p.queue <- lokiEntry{id: id, runID: runID, ev: ev}:
	default:
		// queue full — drop rather than block the engine
	}
}

// Stop drains pending entries and shuts down the background goroutine.
func (p *LokiPusher) Stop() {
	if p == nil {
		return
	}
	p.cancel()
	p.wg.Wait()
}

func (p *LokiPusher) loop(ctx context.Context) {
	defer p.wg.Done()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	var buf []lokiEntry
	flush := func() {
		if len(buf) == 0 {
			return
		}
		if err := p.send(buf); err != nil {
			log.Warn().Err(err).Int("count", len(buf)).Msg("loki: push failed")
		}
		buf = buf[:0]
	}

	for {
		select {
		case e := <-p.queue:
			buf = append(buf, e)
			if len(buf) >= 100 {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-ctx.Done():
			// Drain remaining items once.
			for {
				select {
				case e := <-p.queue:
					buf = append(buf, e)
				default:
					flush()
					return
				}
			}
		}
	}
}

// lokiPushBody is the wire format for Loki's push endpoint.
type lokiPushBody struct {
	Streams []lokiStream `json:"streams"`
}

type lokiStream struct {
	Stream map[string]string `json:"stream"`
	Values [][2]string       `json:"values"` // [nanosecond-ts, line]
}

func (p *LokiPusher) send(entries []lokiEntry) error {
	// Group entries by (id, runID, event) → one Loki stream per combo.
	type streamKey struct{ id, runID, event string }
	grouped := map[streamKey][]lokiEntry{}
	for _, e := range entries {
		k := streamKey{e.id, e.runID, e.ev.Event}
		grouped[k] = append(grouped[k], e)
	}

	streams := make([]lokiStream, 0, len(grouped))
	for k, es := range grouped {
		labels := map[string]string{
			"wick_workflow": k.id,
			"wick_run":      k.runID,
			"wick_event":    k.event,
		}
		for lk, lv := range p.extraLabels {
			labels[lk] = lv
		}
		values := make([][2]string, 0, len(es))
		for _, e := range es {
			ts := e.ev.TS
			if ts.IsZero() {
				ts = time.Now().UTC()
			}
			line := eventLine(k.id, k.runID, e.ev)
			values = append(values, [2]string{
				fmt.Sprintf("%d", ts.UnixNano()),
				line,
			})
		}
		streams = append(streams, lokiStream{Stream: labels, Values: values})
	}

	body, err := json.Marshal(lokiPushBody{Streams: streams})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, p.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("loki returned %d", resp.StatusCode)
	}
	return nil
}

// eventLine serialises only the metadata fields (no input/output body)
// to keep Loki entry size manageable.
func eventLine(id, runID string, ev workflow.RunEvent) string {
	ts := ev.TS
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	m := map[string]any{
		"ts":    ts.UTC().Format(time.RFC3339Nano),
		"id":    id,
		"run":   runID,
		"event": ev.Event,
	}
	if ev.Node != "" {
		m["node"] = ev.Node
	}
	if ev.Case != "" {
		m["case"] = ev.Case
	}
	// Extract latency_ms and error from data without leaking full payload.
	if ms, ok := ev.Data["latency_ms"]; ok {
		m["latency_ms"] = ms
	}
	if errMsg, ok := ev.Data["error"]; ok {
		m["error"] = errMsg
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func parseLabels(s string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		kv := strings.SplitN(part, "=", 2)
		if len(kv) == 2 {
			out[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
		}
	}
	return out
}
