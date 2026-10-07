package registry

import (
	"fmt"
	"testing"
	"time"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/session"
)

// BenchmarkRebuildSessionViews measures the cost every list request pays
// after ANY session writes its meta: the cached snapshot + sorted id list
// are dropped, so the next read rebuilds both.
//
// This is the dominant cost of a session list at scale — far above the
// visibility filter that walks the same set — so it is worth a benchmark of
// its own rather than only appearing folded into an HTTP-level one.
func BenchmarkRebuildSessionViews(b *testing.B) {
	for _, n := range []int{1000, 5000, 20000} {
		b.Run(fmt.Sprint(n, "-sessions"), func(b *testing.B) {
			r := New(agentconfig.Layout{BaseDir: b.TempDir()})
			base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
			r.sessions = make(map[string]session.Session, n)
			for i := 0; i < n; i++ {
				id := fmt.Sprintf("s-%06d", i)
				r.sessions[id] = session.Session{ID: id, Meta: session.Meta{
					ProjectID:    fmt.Sprintf("p%02d", i%20),
					Origin:       session.OriginSlack,
					Status:       session.StatusIdle,
					CreatedAt:    base,
					LastActive:   base.Add(time.Duration(i) * time.Second),
					Label:        fmt.Sprintf("session %d label", i),
					UserID:       fmt.Sprintf("u%d", i%50),
					Participants: []string{fmt.Sprintf("u%d", i%50), "u-other"},
				}}
			}
			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				r.invalidateSessionViews() // what one meta write does
				r.rebuildSessionViewsLocked()
			}
		})
	}
}
