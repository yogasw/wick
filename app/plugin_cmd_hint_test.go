package app

import (
	"testing"

	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

func TestInstallLoadHint(t *testing.T) {
	live := "a running wick will pick it up shortly"
	for _, k := range []string{wickplugin.KindConnector, wickplugin.KindTool} {
		if got := installLoadHint(k, true); got != live {
			t.Errorf("%s running: %q", k, got)
		}
		if got := installLoadHint(k, false); got != "wick is not running; it loads on next start" {
			t.Errorf("%s stopped: %q", k, got)
		}
	}
	if got := installLoadHint(wickplugin.KindJob, true); got != "restart or reload wick to load it" {
		t.Errorf("job: %q", got)
	}
}
