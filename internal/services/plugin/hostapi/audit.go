package hostapi

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

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

// Record queues one call. Safe on a nil Audit and after Close.
func (a *Audit) Record(c entity.PluginHostCall) {
	if a == nil {
		return
	}
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

// Dropped is how many records were lost (full queue or after Close).
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
		if err := a.w.WriteCalls(context.WithoutCancel(ctx), buf); err != nil {
			log.Ctx(ctx).Warn().Err(err).Int("rows", len(buf)).Msg("host api audit: write failed, rows lost")
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
