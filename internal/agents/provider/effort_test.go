package provider

import (
	"context"
	"testing"
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
