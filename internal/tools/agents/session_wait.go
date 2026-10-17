package agents

import (
	"encoding/json"
	"math"
	"net/http"
	"sync"
	"time"

	"github.com/yogasw/wick/pkg/tool"
)

// sessionWaitResp is what a session's next answer waits on, for the
// conversation page: without it a turn stuck in the queue or behind a
// stalled host read "spawning…" / "thinking…" as if it were running.
// Sent live as a "wait_state" event (and in the stream snapshot on page
// open); GET /sessions/{id}/wait returns the same.
type sessionWaitResp struct {
	QueuePos       int    `json:"queue_pos"` // 1 = next; 0 = not queued
	QueueLen       int    `json:"queue_len"`
	RestartAttempt int    `json:"restart_attempt,omitempty"`
	RestartMax     int    `json:"restart_max,omitempty"`
	Provider       string `json:"provider,omitempty"` // "type/name"
	// Used / Max are the provider's slots; Max 0 = no cap of its own.
	Used int `json:"used"`
	Max  int `json:"max"`
	// GlobalUsed / GlobalMax are the slots across every provider.
	GlobalUsed int `json:"global_used"`
	GlobalMax  int `json:"global_max"`
	// Hold: the resource guard is holding new agents because the host is
	// near a hang; HoldReason says why. The rest is the host as the guard
	// last saw it, whole percent (0 when the guard is off).
	Hold        bool   `json:"hold"`
	HoldReason  string `json:"hold_reason,omitempty"`
	MemPct      int    `json:"mem_pct"`
	CPUPct      int    `json:"cpu_pct"`
	MemPressure int    `json:"mem_pressure"`
	CPUPressure int    `json:"cpu_pressure"`
}

func sessionWaitFor(sessionID, agentName string) sessionWaitResp {
	var out sessionWaitResp
	if globalPool != nil {
		w := globalPool.WaitOf(sessionID)
		out.QueuePos, out.RestartAttempt, out.RestartMax = w.QueuePos, w.RestartAttempt, w.RestartMax
		out.QueueLen = globalPool.QueueLen()
		g := globalPool.Capacity()
		out.GlobalUsed, out.GlobalMax = g.Used, g.Max
		if agentName == "" {
			agentName = "main"
		}
		if t, n := globalPool.ProviderOf(sessionID, agentName); t != "" {
			pc := globalPool.ProviderCapacity(t, n)
			out.Provider, out.Used, out.Max = t+"/"+n, pc.Used, pc.Max
		}
	}
	h := resourceGuard.Status()
	pct := func(v float64) int { return int(math.Round(v)) }
	out.Hold, out.HoldReason = h.Hold, h.Reason
	out.MemPct, out.CPUPct = pct(h.MemUsedPct), pct(h.CPUBusyPct)
	out.MemPressure, out.CPUPressure = pct(h.MemPressure), pct(h.CPUPressure)
	return out
}

func waitStateEvent(sessionID, agentName string) Event {
	body, _ := json.Marshal(sessionWaitFor(sessionID, agentName))
	return Event{SessionID: sessionID, AgentName: agentName, Type: "wait_state", Data: string(body)}
}

// sessionWait returns sessionWaitResp for one session the caller owns.
func sessionWait(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	id := c.PathValue("id")
	if sess, ok := globalMgr.Registry().Session(id); !ok || !ownsSession(c, sess) {
		c.JSON(http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	c.JSON(http.StatusOK, sessionWaitFor(id, c.R.URL.Query().Get("agent")))
}

// waitPublishEvery is how often waiting sessions get a fresh wait_state.
const waitPublishEvery = 5 * time.Second

var waitPublisherOnce sync.Once

// startWaitPublisher keeps every waiting session's wait_state current:
// queued, spawning, or working (a silent turn on a stalled host is the
// case the page could not explain). Only a changed state is sent. Started
// by the first stream snapshot, so a wick with no open page runs nothing.
func startWaitPublisher() {
	waitPublisherOnce.Do(func() {
		go func() {
			last := map[string]string{}
			t := time.NewTicker(waitPublishEvery)
			defer t.Stop()
			for range t.C {
				publishWaitStates(last)
			}
		}()
	})
}

// waitingSessions is every session queued or running a spawn or a turn,
// with the agent it waits on.
func waitingSessions() map[string]string {
	out := map[string]string{}
	if globalPool == nil {
		return out
	}
	for _, q := range globalPool.QueueSnapshot() {
		out[q.SessionID] = q.AgentName
	}
	for _, e := range globalPool.ActiveSnapshot() {
		if e.Lifecycle == "spawning" || e.Lifecycle == "working" {
			out[e.SessionID] = e.AgentName
		}
	}
	return out
}

func publishWaitStates(last map[string]string) {
	if globalBcast == nil {
		return
	}
	now := waitingSessions()
	for id, agent := range now {
		ev := waitStateEvent(id, agent)
		if last[id] == ev.Data {
			continue
		}
		last[id] = ev.Data
		globalBcast.fanout(id, ev)
	}
	for id := range last {
		if _, ok := now[id]; !ok {
			delete(last, id)
		}
	}
}
