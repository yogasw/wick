package channels

import (
	"context"
	"testing"
)

// bgChannel records the background hooks the registry hands it.
type bgChannel struct {
	*fakeKeyedChannel
	recheck BackgroundRecheckFn
	starts  int
	sets    [][]DetachedSurvivor
}

func (b *bgChannel) SetBackgroundRecheck(fn BackgroundRecheckFn) { b.recheck = fn }
func (b *bgChannel) OnBackgroundStart(string, DetachedSurvivor, string, bool) {
	b.starts++
}
func (b *bgChannel) OnBackgroundAgents(_ string, active []DetachedSurvivor) {
	b.sets = append(b.sets, active)
}

// The probe is wired after channels exist, so it must reach both a channel
// already added and one added later; the dispatches reach every receiver.
func TestRegistryBackgroundWiring(t *testing.T) {
	r := NewRegistry()
	served := ""
	early := &bgChannel{fakeKeyedChannel: &fakeKeyedChannel{key: "a", served: &served}}
	r.Add(early, nil)

	probe := func(context.Context, string) ([]DetachedSurvivor, bool) { return nil, true }
	r.SetBackgroundRecheck(probe)

	late := &bgChannel{fakeKeyedChannel: &fakeKeyedChannel{key: "b", served: &served}}
	r.AddKeyed("b", late, nil)
	if early.recheck == nil || late.recheck == nil {
		t.Fatalf("recheck probe missing: early=%v late=%v", early.recheck != nil, late.recheck != nil)
	}

	r.DispatchBackgroundStart("s1", DetachedSurvivor{Handle: "x"}, "task", false)
	r.DispatchBackgroundAgents("s1", []DetachedSurvivor{{Handle: "x"}})
	for _, c := range []*bgChannel{early, late} {
		if c.starts != 1 || len(c.sets) != 1 || len(c.sets[0]) != 1 {
			t.Fatalf("channel %s got starts=%d sets=%v, want one of each", c.key, c.starts, c.sets)
		}
	}
}
