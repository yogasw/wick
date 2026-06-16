package systemprompt

import (
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/pkg/connector"
)

// seedCatalog registers a fixed set of fake connector modules into the
// global registry so the catalog renderer has deterministic input.
// Keys are test-only (zzt_*) to avoid colliding with any real builtin
// a parallel test might register; the registry is process-global and
// has no reset, so we rely on unique keys + filtering by them.
func seedCatalog(t *testing.T) {
	t.Helper()
	mods := []connector.Module{
		{Meta: connector.Meta{Key: "zzt_charlie", Description: "third"}},
		{Meta: connector.Meta{Key: "zzt_alpha", Description: "first"}},
		{Meta: connector.Meta{Key: "zzt_bravo"}}, // no description
	}
	for _, m := range mods {
		connectors.Register(m)
	}
}

// onlyTestRows keeps the lines of a rendered catalog that belong to our
// seeded fakes, so assertions ignore whatever else is in the global
// registry during the run.
func onlyTestRows(catalog string) []string {
	var out []string
	for _, ln := range strings.Split(catalog, "\n") {
		if strings.Contains(ln, "zzt_") {
			out = append(out, ln)
		}
	}
	return out
}

func TestConnectorCatalogRendersHeaderAndRows(t *testing.T) {
	seedCatalog(t)
	cat := ConnectorCatalog(nil)
	if cat == "" {
		t.Fatal("catalog empty with registered connectors")
	}
	if !strings.HasPrefix(cat, "## Available wick connectors") {
		t.Errorf("catalog missing header, got prefix: %.40q", cat)
	}
	for _, want := range []string{"zzt_alpha", "zzt_bravo", "zzt_charlie", "first", "third"} {
		if !strings.Contains(cat, want) {
			t.Errorf("catalog missing %q", want)
		}
	}
}

func TestConnectorCatalogSortsByKey(t *testing.T) {
	seedCatalog(t)
	rows := onlyTestRows(ConnectorCatalog(nil))
	ai := indexOfRow(rows, "zzt_alpha")
	bi := indexOfRow(rows, "zzt_bravo")
	ci := indexOfRow(rows, "zzt_charlie")
	if !(ai >= 0 && ai < bi && bi < ci) {
		t.Errorf("rows not key-sorted: alpha=%d bravo=%d charlie=%d", ai, bi, ci)
	}
}

func TestConnectorCatalogDescriptionRendering(t *testing.T) {
	seedCatalog(t)
	rows := onlyTestRows(ConnectorCatalog(nil))
	// keyed row carries " — desc"; no-desc row must not.
	for _, r := range rows {
		switch {
		case strings.Contains(r, "zzt_alpha"):
			if !strings.Contains(r, "— first") {
				t.Errorf("zzt_alpha missing description: %q", r)
			}
		case strings.Contains(r, "zzt_bravo"):
			if strings.Contains(r, "—") {
				t.Errorf("zzt_bravo (no desc) rendered a dash: %q", r)
			}
		}
	}
}

func TestConnectorCatalogReadyKeysFilter(t *testing.T) {
	seedCatalog(t)
	// Only alpha marked ready → bravo/charlie dropped.
	cat := ConnectorCatalog(map[string]bool{"zzt_alpha": true})
	rows := onlyTestRows(cat)
	if len(rows) != 1 || !strings.Contains(rows[0], "zzt_alpha") {
		t.Errorf("readyKeys did not filter to the single ready connector: %v", rows)
	}
	if strings.Contains(cat, "zzt_bravo") || strings.Contains(cat, "zzt_charlie") {
		t.Error("non-ready connector leaked past the filter")
	}
}

func TestConnectorCatalogEmptyReadyKeysDropsAll(t *testing.T) {
	seedCatalog(t)
	// Non-nil but empty map → nothing matches our fakes. If the global
	// registry holds only test fakes the whole catalog is "", but other
	// suites may have registered real connectors; either way none of
	// OUR rows may appear.
	cat := ConnectorCatalog(map[string]bool{"zzt_nonexistent": true})
	if rows := onlyTestRows(cat); len(rows) != 0 {
		t.Errorf("empty/unmatched readyKeys still rendered test rows: %v", rows)
	}
}

func indexOfRow(rows []string, key string) int {
	for i, r := range rows {
		if strings.Contains(r, key) {
			return i
		}
	}
	return -1
}
