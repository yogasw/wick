package connectors

import (
	"sort"
	"testing"

	"github.com/yogasw/wick/internal/connectors/bitbucket"
	"github.com/yogasw/wick/internal/connectors/github"
	"github.com/yogasw/wick/internal/connectors/googleworkspace"
	"github.com/yogasw/wick/internal/connectors/httprest"
	"github.com/yogasw/wick/internal/connectors/loki"
	"github.com/yogasw/wick/internal/connectors/phoenix"
	"github.com/yogasw/wick/internal/connectors/slack"
	"github.com/yogasw/wick/pkg/connector"
)

func TestBuiltinModules_RegistersTheSevenPublicConnectors(t *testing.T) {
	got := make([]string, 0)
	for _, m := range builtinModules() {
		got = append(got, m.Meta.Key)
	}
	sort.Strings(got)

	want := []string{
		bitbucket.Meta().Key,
		github.Meta().Key,
		googleworkspace.Meta().Key,
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
			github.Meta().Key, httprest.Meta().Key, slack.Meta().Key,
			bitbucket.Meta().Key, loki.Meta().Key, phoenix.Meta().Key,
			googleworkspace.Meta().Key,
		}},
		{profile: ProfileAgent, wantKeys: []string{
			github.Meta().Key, httprest.Meta().Key, slack.Meta().Key,
		}},
		{profile: ProfileLite, wantNone: true},
		{profile: "totally-unknown", wantKeys: []string{
			github.Meta().Key, httprest.Meta().Key, slack.Meta().Key,
			bitbucket.Meta().Key, loki.Meta().Key, phoenix.Meta().Key,
			googleworkspace.Meta().Key,
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
