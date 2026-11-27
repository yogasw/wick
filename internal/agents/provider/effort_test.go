package provider

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestEffortResolverPreservesFullPin(t *testing.T) {
	typ := Type("test-effort-resolver")
	pin := "vendor/account@model"
	RegisterEffortResolver(typ, func(_ context.Context, ins Instance, modelID string) (EffortCapabilities, error) {
		if ins.Name != "account" || modelID != pin {
			t.Fatalf("wrong resolver input: %q / %q", ins.Name, modelID)
		}
		return EffortCapabilities{
			Options: []EffortOption{{ID: "minimal"}, {ID: "high"}},
			Default: "minimal",
			Source:  "test",
		}, nil
	})
	defer RegisterEffortResolver(typ, nil)
	caps, registered, err := EffortCapabilitiesFor(context.Background(), Instance{Type: typ, Name: "account"}, pin)
	if err != nil || !registered || caps.Default != "minimal" || len(caps.Options) != 2 {
		t.Fatalf("unexpected capability: %+v registered=%v err=%v", caps, registered, err)
	}
	_, registered, err = EffortCapabilitiesFor(context.Background(), Instance{Type: Type("unregistered-effort")}, pin)
	if err != nil || registered {
		t.Fatalf("unregistered provider: registered=%v err=%v", registered, err)
	}
}

func TestResolveEffortSelection(t *testing.T) {
	caps := EffortCapabilities{
		Options: []EffortOption{{ID: "none"}, {ID: "low"}, {ID: "high"}},
		Default: "low",
	}
	for _, tc := range []struct {
		name    string
		choices []string
		want    EffortSelection
		wantErr bool
	}{
		{name: "inherit", choices: []string{"", "high"}, want: EffortSelection{"high", "high", "high"}},
		{name: "default stops inheritance", choices: []string{"default", "high"}, want: EffortSelection{"default", "low", ""}},
		{name: "empty defaults", want: EffortSelection{"default", "low", ""}},
		{name: "none is explicit", choices: []string{"none"}, want: EffortSelection{"none", "none", "none"}},
		{name: "unsupported", choices: []string{"xhigh", "high"}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveEffortSelection(caps, tc.choices...)
			if (err != nil) != tc.wantErr || got != tc.want {
				t.Fatalf("got %+v err=%v; want %+v err=%v", got, err, tc.want, tc.wantErr)
			}
		})
	}
	got, err := ResolveEffortSelection(EffortCapabilities{}, EffortDefault)
	if err != nil || got.Effective != "" || got.Native != "" {
		t.Fatalf("unknown default should remain unknown: %+v err=%v", got, err)
	}
}

func TestEffortCapabilitiesRejectMalformedOptions(t *testing.T) {
	for _, caps := range []EffortCapabilities{
		{Options: []EffortOption{{ID: ""}}},
		{Options: []EffortOption{{ID: EffortDefault}}},
		{Options: []EffortOption{{ID: "high"}, {ID: "high"}}},
		{Options: []EffortOption{{ID: "high"}}, Default: "low"},
	} {
		if err := validateEffortCapabilities(caps); err == nil {
			t.Fatalf("accepted invalid capability: %+v", caps)
		}
	}
}

func TestEffortCapabilitiesCachedPerModel(t *testing.T) {
	typ := Type("test-effort-cache")
	var calls atomic.Int32
	RegisterEffortResolver(typ, func(_ context.Context, _ Instance, modelID string) (EffortCapabilities, error) {
		calls.Add(1)
		if modelID == "model-b" {
			return EffortCapabilities{Options: []EffortOption{{ID: "low"}}}, nil
		}
		return EffortCapabilities{Options: []EffortOption{{ID: "low"}, {ID: "high"}}, Default: "high"}, nil
	})
	defer RegisterEffortResolver(typ, nil)
	ins := Instance{Type: typ, Name: "acme"}
	for i := 0; i < 3; i++ {
		caps, _, err := EffortCapabilitiesFor(context.Background(), ins, "model-a")
		if err != nil || len(caps.Options) != 2 {
			t.Fatalf("model-a: %+v err=%v", caps, err)
		}
	}
	caps, _, _ := EffortCapabilitiesFor(context.Background(), ins, "model-b")
	if len(caps.Options) != 1 {
		t.Fatalf("model-b must not reuse model-a's answer: %+v", caps)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("resolver calls = %d, want 2 (one per model)", got)
	}
	// a returned slice must not alias the cache
	caps, _, _ = EffortCapabilitiesFor(context.Background(), ins, "model-a")
	caps.Options[0].ID = "mutated"
	caps, _, _ = EffortCapabilitiesFor(context.Background(), ins, "model-a")
	if caps.Options[0].ID != "low" {
		t.Fatalf("cache was mutated through a returned slice")
	}
	// a config change (new binary/env) is a different key
	EffortCapabilitiesFor(context.Background(), Instance{Type: typ, Name: "acme", Env: []string{"KEY=2"}}, "model-a")
	if got := calls.Load(); got != 3 {
		t.Fatalf("config change should re-resolve, calls = %d", got)
	}
	InvalidateEffortCache(typ, "acme")
	EffortCapabilitiesFor(context.Background(), ins, "model-a")
	if got := calls.Load(); got != 4 {
		t.Fatalf("invalidate should re-resolve, calls = %d", got)
	}
}

func TestEffortCacheExpiryAndErrors(t *testing.T) {
	typ := Type("test-effort-expiry")
	now := time.Unix(1000, 0)
	oldNow := effortNow
	effortNow = func() time.Time { return now }
	defer func() { effortNow = oldNow }()
	var calls atomic.Int32
	fail := true
	RegisterEffortResolver(typ, func(context.Context, Instance, string) (EffortCapabilities, error) {
		calls.Add(1)
		if fail {
			return EffortCapabilities{}, errors.New("probe failed")
		}
		return EffortCapabilities{Options: []EffortOption{{ID: "high"}}}, nil
	})
	defer RegisterEffortResolver(typ, nil)
	ins := Instance{Type: typ, Name: "acme"}
	if _, _, err := EffortCapabilitiesFor(context.Background(), ins, "m"); err == nil {
		t.Fatal("want probe error")
	}
	EffortCapabilitiesFor(context.Background(), ins, "m")
	if calls.Load() != 1 {
		t.Fatal("error should be cached briefly")
	}
	fail = false
	now = now.Add(effortErrorCacheTTL + time.Second)
	if caps, _, err := EffortCapabilitiesFor(context.Background(), ins, "m"); err != nil || len(caps.Options) != 1 {
		t.Fatalf("after error TTL: %+v err=%v", caps, err)
	}
	now = now.Add(effortCacheTTL + time.Second)
	EffortCapabilitiesFor(context.Background(), ins, "m")
	if calls.Load() != 3 {
		t.Fatalf("success should expire after TTL, calls = %d", calls.Load())
	}
}

func TestEffortCacheCoalescesConcurrentMisses(t *testing.T) {
	typ := Type("test-effort-coalesce")
	var calls atomic.Int32
	release := make(chan struct{})
	RegisterEffortResolver(typ, func(context.Context, Instance, string) (EffortCapabilities, error) {
		calls.Add(1)
		<-release
		return EffortCapabilities{Options: []EffortOption{{ID: "high"}}}, nil
	})
	defer RegisterEffortResolver(typ, nil)
	ins := Instance{Type: typ, Name: "acme"}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := EffortCapabilitiesFor(context.Background(), ins, "m"); err != nil {
				t.Errorf("err=%v", err)
			}
		}()
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()
	if got := calls.Load(); got != 1 {
		t.Fatalf("concurrent misses ran the resolver %d times, want 1", got)
	}
}

func TestStaticEffortResolverAndLabels(t *testing.T) {
	r := StaticEffortResolver("test", "high", "low", "xhigh", "weird")
	caps, err := r(context.Background(), Instance{}, "m")
	if err != nil || caps.Default != "high" || caps.Source != "test" || len(caps.Options) != 3 {
		t.Fatalf("caps %+v err=%v", caps, err)
	}
	if caps.Options[1].Label != "Extra High" || caps.Options[2].Label != "weird" {
		t.Fatalf("labels %+v", caps.Options)
	}
	caps.Options[0].ID = "mutated"
	again, _ := r(context.Background(), Instance{}, "m")
	if again.Options[0].ID != "low" {
		t.Fatal("a caller's edit leaked into the next answer")
	}
}

func TestResolveSpawnEffort(t *testing.T) {
	typ := Type("test-spawn-effort")
	RegisterEffortResolver(typ, func(_ context.Context, _ Instance, model string) (EffortCapabilities, error) {
		if model == "plain" {
			return EffortCapabilities{}, nil
		}
		return EffortCapabilities{Options: []EffortOption{{ID: "low"}, {ID: "high"}}}, nil
	})
	defer RegisterEffortResolver(typ, nil)
	ins := Instance{Type: typ, Name: "acme"}
	ctx := context.Background()
	if sel, err := ResolveSpawnEffort(ctx, ins, "smart", "high"); err != nil || sel.Native != "high" {
		t.Fatalf("supported pick: %+v err=%v", sel, err)
	}
	if _, err := ResolveSpawnEffort(ctx, ins, "smart", "xhigh"); err == nil {
		t.Fatal("an unsupported pick must be refused, not remapped")
	}
	if sel, err := ResolveSpawnEffort(ctx, ins, "plain", "high"); !errors.Is(err, ErrEffortNotConfigurable) || sel.Native != "" {
		t.Fatalf("model with no options: %+v err=%v", sel, err)
	}
	if sel, err := ResolveSpawnEffort(ctx, ins, "plain", EffortDefault); err != nil || sel.Native != "" {
		t.Fatalf("Default on a model with no options is fine: %+v err=%v", sel, err)
	}
	if _, err := ResolveSpawnEffort(ctx, Instance{Type: "unregistered-x"}, "m", "high"); !errors.Is(err, ErrEffortNotConfigurable) {
		t.Fatalf("provider without resolver: err=%v", err)
	}
}

func TestValidEffortChoice(t *testing.T) {
	info := EffortInfo{Configurable: true, Options: []EffortOption{{ID: "low"}, {ID: "high"}}}
	for v, want := range map[string]bool{"": true, EffortDefault: true, "high": true, "xhigh": false} {
		if got := ValidEffortChoice(info, v); got != want {
			t.Fatalf("ValidEffortChoice(%q) = %v, want %v", v, got, want)
		}
	}
}
