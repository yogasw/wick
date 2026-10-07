package team

import (
	"context"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/entity"
)

func TestSessionPolicyNormalizes(t *testing.T) {
	cases := []struct {
		in   string
		want SessionPolicy
	}{
		{"", SessionPolicy{Compact: CompactAuto, IdleHours: DefaultIdleHours}},
		{"{bad", SessionPolicy{Compact: CompactAuto, IdleHours: DefaultIdleHours}},
		{`{"compact":"off"}`, SessionPolicy{Compact: CompactAuto, IdleHours: DefaultIdleHours}},
		{`{"compact":"idle","idle_hours":500}`, SessionPolicy{Compact: CompactIdle, IdleHours: MaxIdleHours}},
		{`{"compact":"idle","idle_hours":-3,"summarise_threads":true}`, SessionPolicy{Compact: CompactIdle, IdleHours: MinIdleHours, SummariseThreads: true}},
	}
	for _, c := range cases {
		if got := DecodeSessionPolicy(c.in); got != c.want {
			t.Errorf("%q → %+v, want %+v", c.in, got, c.want)
		}
	}
	if got := DecodeSessionPolicy(EncodeSessionPolicy(SessionPolicy{Compact: CompactIdle, IdleHours: 3})); got.IdleHours != 3 || got.Compact != CompactIdle {
		t.Errorf("round trip = %+v", got)
	}
}

// idleRig is a fake clock and main chat for the compactor.
type idleRig struct {
	now       time.Time
	chat      MainChat
	compacted []string
	agent     entity.AgentPersona
}

func (r *idleRig) compactor() *IdleCompactor {
	return &IdleCompactor{
		Agents: func(context.Context) ([]entity.AgentPersona, error) { return []entity.AgentPersona{r.agent}, nil },
		Main:   func(entity.AgentPersona) (MainChat, bool) { return r.chat, true },
		Compact: func(_ context.Context, sid string) error {
			r.compacted = append(r.compacted, sid)
			// The compact turn itself touches the chat.
			r.chat.LastActive = r.now
			return nil
		},
		Now: func() time.Time { return r.now },
	}
}

// One compact per idle stretch: not before the threshold, once after it,
// never again until the user comes back and goes idle again.
func TestIdleCompactorOncePerIdleStretch(t *testing.T) {
	start := time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC)
	r := &idleRig{
		now:   start,
		chat:  MainChat{SessionID: "main-1", LastActive: start},
		agent: entity.AgentPersona{ID: "a1", SessionPolicy: `{"compact":"idle","idle_hours":2}`},
	}
	c := r.compactor()
	ctx := context.Background()

	r.now = start.Add(time.Hour)
	if got := c.Tick(ctx); len(got) != 0 {
		t.Fatalf("compacted before the threshold: %v", got)
	}
	r.now = start.Add(2*time.Hour + time.Minute)
	if got := c.Tick(ctx); len(got) != 1 {
		t.Fatalf("not compacted after 2h idle: %v", got)
	}
	// Hours pass with no user activity: the compact's own touch must not
	// count as activity, so nothing fires again.
	for h := 3; h < 12; h++ {
		r.now = start.Add(time.Duration(h) * time.Hour)
		if got := c.Tick(ctx); len(got) != 0 {
			t.Fatalf("compacted again at +%dh within the same idle stretch", h)
		}
	}
	// The user comes back, then leaves again.
	r.chat.LastActive = start.Add(12 * time.Hour)
	r.now = start.Add(13 * time.Hour)
	if got := c.Tick(ctx); len(got) != 0 {
		t.Fatalf("compacted 1h after new activity")
	}
	r.now = start.Add(14*time.Hour + time.Minute)
	if got := c.Tick(ctx); len(got) != 1 {
		t.Fatalf("second idle stretch not compacted: %v", got)
	}
	if len(r.compacted) != 2 {
		t.Fatalf("compacted = %v, want exactly 2", r.compacted)
	}
}

func TestIdleCompactorSkips(t *testing.T) {
	start := time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC)
	for name, mut := range map[string]func(*idleRig){
		"auto policy": func(r *idleRig) { r.agent.SessionPolicy = `{"compact":"auto"}` },
		"disabled":    func(r *idleRig) { r.agent.Disabled = true },
		"busy turn":   func(r *idleRig) { r.chat.Busy = true },
		"never used":  func(r *idleRig) { r.chat.LastActive = time.Time{} },
	} {
		r := &idleRig{
			now:   start.Add(48 * time.Hour),
			chat:  MainChat{SessionID: "main-1", LastActive: start},
			agent: entity.AgentPersona{ID: "a1", SessionPolicy: `{"compact":"idle","idle_hours":1}`},
		}
		mut(r)
		if got := r.compactor().Tick(context.Background()); len(got) != 0 {
			t.Errorf("%s: compacted %v", name, got)
		}
	}
}
