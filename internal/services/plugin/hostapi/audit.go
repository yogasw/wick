package hostapi

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/entity"
)

// AuditRetention is how long plugin_host_calls rows are kept.
const AuditRetention = 7 * 24 * time.Hour

// auditCloseTimeout bounds how long Close waits for the final flush.
const auditCloseTimeout = 5 * time.Second

// AuditWriter persists audit rows. GormStore implements it.
type AuditWriter interface {
	WriteCalls(ctx context.Context, calls []entity.PluginHostCall) error
	PurgeCallsBefore(ctx context.Context, t time.Time) (int64, error)
}

// Audit records every Host API call off the request path: Record only
// queues; one goroutine writes batches and purges rows past AuditRetention.
// A full queue, or a Record after Close, drops the record (counted in
// Dropped) rather than slow a plugin down or panic during shutdown. The
// queue channel is never closed: Close signals through stop instead, so a
// late Record cannot send on a closed channel.
type Audit struct {
	w          AuditWriter
	ch         chan entity.PluginHostCall
	batch      int
	flushEvery time.Duration
	purgeEvery time.Duration
	now        func() time.Time

	mu      sync.RWMutex // guards closed against Record
	closed  bool
	dropped atomic.Int64
	started atomic.Bool
	start   sync.Once
	stop    chan struct{}
	done    chan struct{}
}

// NewAudit returns an Audit writing to w. Call Start to run it.
func NewAudit(w AuditWriter) *Audit {
	return &Audit{w: w, ch: make(chan entity.PluginHostCall, 1024), batch: 100,
		flushEvery: 2 * time.Second, purgeEvery: time.Hour, now: time.Now,
		stop: make(chan struct{}), done: make(chan struct{})}
}

// Column limits of entity.PluginHostCall. Several values come from the
// plugin (a Rest path, an Invoke method name, a connector op), so Record
// clips every bounded column: one oversized row must never fail a batch.
const (
	auditKeyMax   = 100
	auditKindMax  = 20
	auditRPCMax   = 64
	auditScopeMax = 100
	auditItemMax  = 255
	auditUserMax  = 36
	auditSrcMax   = 16
	auditCodeMax  = 32
	auditNoteMax  = 8192
)

// clip cuts s to at most max bytes on a UTF-8 boundary.
func clip(s string, max int) string {
	if len(s) <= max {
		return s
	}
	s = s[:max]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

func clipCall(c entity.PluginHostCall) entity.PluginHostCall {
	c.PluginKey, c.Kind, c.RPC = clip(c.PluginKey, auditKeyMax), clip(c.Kind, auditKindMax), clip(c.RPC, auditRPCMax)
	c.Scope, c.Item, c.UserID = clip(c.Scope, auditScopeMax), clip(c.Item, auditItemMax), clip(c.UserID, auditUserMax)
	c.Source, c.Code, c.Note = clip(c.Source, auditSrcMax), clip(c.Code, auditCodeMax), clip(c.Note, auditNoteMax)
	return c
}

// Record queues one call, its columns clipped to their sizes. Safe on a nil
// Audit and after Close.
func (a *Audit) Record(c entity.PluginHostCall) {
	if a == nil {
		return
	}
	c = clipCall(c)
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.closed {
		a.dropped.Add(1)
		return
	}
	select {
	case a.ch <- c:
	default:
		a.dropped.Add(1)
	}
}

// Dropped is how many records were lost (full queue, after Close, or a row
// the writer refused).
func (a *Audit) Dropped() int64 { return a.dropped.Load() }

// Start runs the writer until ctx ends or Close is called; the queue is
// flushed on the way out.
func (a *Audit) Start(ctx context.Context) {
	a.start.Do(func() {
		a.started.Store(true)
		go a.loop(ctx)
	})
}

// Close stops accepting records and waits (bounded by auditCloseTimeout)
// for what is queued to be written. Safe to call more than once.
func (a *Audit) Close() {
	a.mu.Lock()
	already := a.closed
	a.closed = true
	a.mu.Unlock()
	if already {
		return
	}
	close(a.stop)
	if !a.started.Load() {
		return
	}
	select {
	case <-a.done:
	case <-time.After(auditCloseTimeout):
		log.Warn().Msg("host api audit: final flush timed out")
	}
}

func (a *Audit) loop(ctx context.Context) {
	defer close(a.done)
	flush := time.NewTicker(a.flushEvery)
	purge := time.NewTicker(a.purgeEvery)
	defer flush.Stop()
	defer purge.Stop()
	buf := make([]entity.PluginHostCall, 0, a.batch)
	write := func() {
		if len(buf) == 0 {
			return
		}
		wctx := context.WithoutCancel(ctx)
		if err := a.w.WriteCalls(wctx, buf); err != nil && len(buf) > 1 {
			// One bad row must not take the batch with it: retry row by
			// row so only the row that fails is lost.
			lost := 0
			for i := range buf {
				if err := a.w.WriteCalls(wctx, buf[i:i+1]); err != nil {
					lost++
					log.Ctx(ctx).Warn().Err(err).Str("plugin", buf[i].PluginKey).Str("rpc", buf[i].RPC).Msg("host api audit: row lost")
				}
			}
			a.dropped.Add(int64(lost))
		} else if err != nil {
			a.dropped.Add(1)
			log.Ctx(ctx).Warn().Err(err).Str("plugin", buf[0].PluginKey).Str("rpc", buf[0].RPC).Msg("host api audit: row lost")
		}
		buf = buf[:0]
	}
	a.purgeOld(ctx)
	for {
		select {
		case c := <-a.ch:
			buf = append(buf, c)
			if len(buf) >= a.batch {
				write()
			}
		case <-flush.C:
			write()
		case <-purge.C:
			a.purgeOld(ctx)
		case <-a.stop:
			a.drain(&buf)
			write()
			return
		case <-ctx.Done():
			a.drain(&buf)
			write()
			return
		}
	}
}

// drain moves whatever is queued into buf without blocking.
func (a *Audit) drain(buf *[]entity.PluginHostCall) {
	for {
		select {
		case c := <-a.ch:
			*buf = append(*buf, c)
		default:
			return
		}
	}
}

func (a *Audit) purgeOld(ctx context.Context) {
	if _, err := a.w.PurgeCallsBefore(ctx, a.now().Add(-AuditRetention)); err != nil {
		log.Ctx(ctx).Warn().Err(err).Msg("host api audit: purge failed")
	}
}
