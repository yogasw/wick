package connectors

import (
	"sort"
	"testing"

	"github.com/yogasw/wick/internal/connectors/httprest"
	"github.com/yogasw/wick/internal/connectors/loki"
	"github.com/yogasw/wick/internal/connectors/phoenix"
	"github.com/yogasw/wick/internal/connectors/slack"
	"github.com/yogasw/wick/pkg/connector"
)

func TestBuiltinModules_RegistersThePublicConnectors(t *testing.T) {
	got := make([]string, 0)
	for _, m := range builtinModules() {
		got = append(got, m.Meta.Key)
	}
	sort.Strings(got)

	// github, bitbucket, and google_workspace were moved out-of-tree to
	// downloadable plugins (plugins/connector/*), so they are no longer
	// builtins. Install them via `<app> plugin install <key>`.
	want := []string{
		httprest.Meta().Key,
		loki.Meta().Key,
		phoenix.Meta().Key,
		slack.Meta().Key,
	}
	sort.Strings(want)

	if len(got) != len(want) {
		t.Fatalf("builtinModules() count = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("builtinModules()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func keySet(mods []connector.Module) map[string]bool {
	out := map[string]bool{}
	for _, m := range mods {
		out[m.Meta.Key] = true
	}
	return out
}

func TestProfileModules(t *testing.T) {
	cases := []struct {
		profile  string
		wantKeys []string
		wantNone bool
	}{
		{profile: ProfileFull, wantKeys: []string{
			httprest.Meta().Key, slack.Meta().Key,
			loki.Meta().Key, phoenix.Meta().Key,
		}},
		{profile: ProfileAgent, wantKeys: []string{
			httprest.Meta().Key, slack.Meta().Key,
		}},
		{profile: ProfileLite, wantNone: true},
		{profile: "totally-unknown", wantKeys: []string{
			httprest.Meta().Key, slack.Meta().Key,
			loki.Meta().Key, phoenix.Meta().Key,
		}},
	}

	for _, tc := range cases {
		t.Run(tc.profile, func(t *testing.T) {
			got := keySet(profileModules(tc.profile))
			if tc.wantNone {
				if len(got) != 0 {
					t.Fatalf("profile %q: want no modules, got %v", tc.profile, got)
				}
				return
			}
			if len(got) != len(tc.wantKeys) {
				t.Fatalf("profile %q: got %d modules, want %d", tc.profile, len(got), len(tc.wantKeys))
			}
			for _, k := range tc.wantKeys {
				if !got[k] {
					t.Errorf("profile %q: missing key %q", tc.profile, k)
				}
			}
		})
	}
}
