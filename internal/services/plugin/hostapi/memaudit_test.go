package hostapi

import (
	"context"
	"sync"
	"time"

	"github.com/yogasw/wick/internal/entity"
)

// memoryAudit is an AuditWriter for tests.
type memoryAudit struct {
	mu    sync.Mutex
	calls []entity.PluginHostCall
}

func (m *memoryAudit) WriteCalls(_ context.Context, calls []entity.PluginHostCall) error {
	m.mu.Lock()
	m.calls = append(m.calls, calls...)
	m.mu.Unlock()
	return nil
}

func (m *memoryAudit) PurgeCallsBefore(_ context.Context, t time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	kept := m.calls[:0]
	var n int64
	for _, c := range m.calls {
		if c.Ts.Before(t) {
			n++
			continue
		}
		kept = append(kept, c)
	}
	m.calls = kept
	return n, nil
}

func (m *memoryAudit) snapshot() []entity.PluginHostCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]entity.PluginHostCall(nil), m.calls...)
}
