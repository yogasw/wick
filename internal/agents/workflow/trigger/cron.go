package trigger

import (
	"context"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/workflow"
)

// CronScheduler ticks every minute and fires workflow cron triggers
// whose schedule matches the current minute. Registered workflows are
// kept in sync with the Router — call Sync(workflows) whenever a
// workflow is registered, hot-reloaded, or unregistered.
//
// Standalone from internal/jobs/* (which is row-backed admin-toggleable
// for per-tool jobs). Workflow cron lives in the same in-process
// trigger plane as channel events + webhooks so the whole subsystem
// drains through one Router queue.
type CronScheduler struct {
	router *Router
	clock  func() time.Time

	mu     sync.RWMutex
	cron   map[string][]cronEntry // id → entries
	cancel context.CancelFunc
	done   chan struct{}
}

type cronEntry struct {
	Schedule  string
	EntryNode string
}

// NewCronScheduler wires a scheduler against a Router.
func NewCronScheduler(r *Router) *CronScheduler {
	return &CronScheduler{
		router: r,
		clock:  func() time.Time { return time.Now() },
		cron:   map[string][]cronEntry{},
	}
}

// Start launches the minute-tick goroutine. Cancel ctx to stop.
func (c *CronScheduler) Start(ctx context.Context) {
	cctx, cancel := context.WithCancel(ctx)
	c.cancel = cancel
	c.done = make(chan struct{})
	go c.loop(cctx)
}

// Stop signals the loop to exit and waits.
func (c *CronScheduler) Stop() {
	if c.cancel != nil {
		c.cancel()
	}
	if c.done != nil {
		<-c.done
	}
}

// Sync replaces the cron entries for one id. Pass an empty slice to
// drop all cron schedules for that id. Idempotent.
func (c *CronScheduler) Sync(id string, w workflow.Workflow) {
	entries := []cronEntry{}
	for _, tr := range w.Triggers {
		if tr.Type != workflow.TriggerCron {
			continue
		}
		if tr.Schedule == "" {
			continue
		}
		entries = append(entries, cronEntry{Schedule: tr.Schedule, EntryNode: tr.EntryNode})
	}
	c.mu.Lock()
	if len(entries) == 0 {
		delete(c.cron, id)
	} else {
		c.cron[id] = entries
	}
	c.mu.Unlock()
}

// Unsync removes cron entries for id.
func (c *CronScheduler) Unsync(id string) {
	c.mu.Lock()
	delete(c.cron, id)
	c.mu.Unlock()
}

func (c *CronScheduler) loop(ctx context.Context) {
	defer close(c.done)
	// Align first tick to top-of-minute so a workflow scheduled for
	// HH:MM fires within a few hundred ms of HH:MM:00.
	now := c.clock()
	toNextMinute := time.Until(now.Truncate(time.Minute).Add(time.Minute))
	timer := time.NewTimer(toNextMinute)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			c.tick()
			timer.Reset(time.Minute)
		}
	}
}

func (c *CronScheduler) tick() {
	now := c.clock()
	c.mu.RLock()
	snap := make(map[string][]cronEntry, len(c.cron))
	for k, v := range c.cron {
		snap[k] = v
	}
	c.mu.RUnlock()
	for id, entries := range snap {
		for _, e := range entries {
			if !cronMatchesNow(e.Schedule, now) {
				continue
			}
			evt := workflow.Event{
				Type: string(workflow.TriggerCron),
				At:   now.UTC(),
			}
			if err := c.router.RunNow(context.Background(), id, evt); err != nil {
				log.Warn().Str("wf_id", id).Err(err).Msg("workflow cron: enqueue failed")
				continue
			}
			log.Info().Str("wf_id", id).Str("schedule", e.Schedule).Msg("workflow cron fired")
		}
	}
}

// ── 5-field cron matcher ─────────────────────────────────────────────
// Mirrors internal/pkg/worker/server.go (kept local to avoid the
// reverse dep — workflow shouldn't import internal/pkg/worker).

func cronMatchesNow(expr string, now time.Time) bool {
	fields := splitFields(expr)
	if len(fields) != 5 {
		return false
	}
	values := [5]int{now.Minute(), now.Hour(), now.Day(), int(now.Month()), int(now.Weekday())}
	ranges := [5][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 6}}
	for i, field := range fields {
		if !fieldMatches(field, values[i], ranges[i][0], ranges[i][1]) {
			return false
		}
	}
	return true
}

func splitFields(s string) []string {
	var fields []string
	start := -1
	for i, c := range s {
		if c == ' ' || c == '\t' {
			if start >= 0 {
				fields = append(fields, s[start:i])
				start = -1
			}
		} else if start < 0 {
			start = i
		}
	}
	if start >= 0 {
		fields = append(fields, s[start:])
	}
	return fields
}

func fieldMatches(field string, value, min, max int) bool {
	for _, part := range splitComma(field) {
		if partMatches(part, value, min, max) {
			return true
		}
	}
	return false
}

func partMatches(part string, value, min, max int) bool {
	if part == "*" {
		return true
	}
	if len(part) > 2 && part[0] == '*' && part[1] == '/' {
		step := atoiCron(part[2:])
		if step <= 0 {
			return false
		}
		return (value-min)%step == 0
	}
	if dashIdx := indexOfCron(part, '-'); dashIdx > 0 {
		slashIdx := indexOfCron(part, '/')
		var rs, re, step int
		if slashIdx > 0 {
			rs = atoiCron(part[:dashIdx])
			re = atoiCron(part[dashIdx+1 : slashIdx])
			step = atoiCron(part[slashIdx+1:])
		} else {
			rs = atoiCron(part[:dashIdx])
			re = atoiCron(part[dashIdx+1:])
			step = 1
		}
		if step <= 0 {
			step = 1
		}
		if value < rs || value > re {
			return false
		}
		return (value-rs)%step == 0
	}
	return atoiCron(part) == value
}

func splitComma(s string) []string {
	var parts []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	parts = append(parts, s[start:])
	return parts
}

func indexOfCron(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

func atoiCron(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return -1
		}
		n = n*10 + int(c-'0')
	}
	return n
}
