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
