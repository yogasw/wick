package hostapi

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/entity"
)

// pickyAudit refuses any batch holding a row of plugin "bad", the way a
// database refuses a whole INSERT for one bad row.
type pickyAudit struct{ memoryAudit }

func (p *pickyAudit) WriteCalls(ctx context.Context, calls []entity.PluginHostCall) error {
	for _, c := range calls {
		if c.PluginKey == "bad" || len(c.Item) > 255 {
			return errors.New("value too long")
		}
	}
	return p.memoryAudit.WriteCalls(ctx, calls)
}

func TestAuditClipsAndSurvivesABadRow(t *testing.T) {
	w := &pickyAudit{}
	a := NewAudit(w)
	a.Start(context.Background())
	long := strings.Repeat("é", 1000) // 2000 bytes
	a.Record(entity.PluginHostCall{Ts: time.Now(), PluginKey: "p", RPC: "Invoke", Item: long, Note: strings.Repeat("x", 20000)})
	a.Record(entity.PluginHostCall{Ts: time.Now(), PluginKey: "bad", RPC: "Whoami"})
	a.Record(entity.PluginHostCall{Ts: time.Now(), PluginKey: "q", RPC: "Whoami"})
	a.Close()
	rows := w.snapshot()
	if len(rows) != 2 || rows[0].PluginKey != "p" || rows[1].PluginKey != "q" {
		t.Fatalf("rows = %+v, want p and q kept", rows)
	}
	if len(rows[0].Item) > 255 || !strings.HasPrefix(long, rows[0].Item) || len(rows[0].Note) != auditNoteMax {
		t.Fatalf("item %d bytes / note %d bytes not clipped safely", len(rows[0].Item), len(rows[0].Note))
	}
	if a.Dropped() != 1 {
		t.Fatalf("dropped = %d, want 1", a.Dropped())
	}
}
