package managedbin_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/provider/managedbin"
	_ "github.com/yogasw/wick/internal/agents/provider/omp"
	_ "github.com/yogasw/wick/internal/agents/provider/opencode"
)

// TestE2EManagedBinaries downloads REAL omp and opencode releases from
// GitHub through the managed installer: previous + latest, verifies
// sha256 against the API digest, runs --version, checks an update to the
// same version is a no-op and that rollback needs no download.
//
// Gated: WICK_E2E_PROVIDER_BIN=1. WICK_E2E_PROVIDER_BIN_DIR picks the
// scratch root (default: a temp dir).
func TestE2EManagedBinaries(t *testing.T) {
	if os.Getenv("WICK_E2E_PROVIDER_BIN") != "1" {
		t.Skip("set WICK_E2E_PROVIDER_BIN=1 to download real provider binaries")
	}
	root := os.Getenv("WICK_E2E_PROVIDER_BIN_DIR")
	if root == "" {
		root = t.TempDir()
	}
	root = filepath.Join(root, "providers", "bin")
	m := managedbin.New()
	m.Root = func() string { return root }
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Minute)
	defer cancel()
	t.Logf("host: %s", m.Host().Label())

	for _, typ := range []string{"omp", "opencode"} {
		t.Run(typ, func(t *testing.T) {
			snap, err := m.RefreshLatest(ctx, typ)
			if err != nil || snap.Latest == nil {
				t.Fatalf("refresh latest: %v", err)
			}
			latest, rels := *snap.Latest, snap.Releases
			if st, _ := m.Status(typ); st.Latest == nil || st.Latest.Tag != latest.Tag || len(st.Releases) == 0 {
				t.Fatalf("status does not serve the cached snapshot: %+v", st)
			}
			prev := ""
			for _, r := range rels {
				if !r.Prerelease && r.Tag != latest.Tag {
					prev = r.Tag
					break
				}
			}
			if prev == "" {
				t.Fatal("no previous release to roll back to")
			}
			logInstalled := func(tag string, j *managedbin.JobInfo, start time.Time) managedbin.Status {
				st, _ := m.Status(typ)
				var vi managedbin.InstalledView
				for _, iv := range st.Installed {
					if iv.Version == j.Version {
						vi = iv
					}
				}
				t.Logf("%s %s: asset=%s asset_sha256=%s bin_sha256=%s --version=%q (%s) current=%s",
					typ, tag, vi.Asset, vi.AssetSHA256, vi.SHA256, vi.VersionOutput, time.Since(start).Round(time.Second), st.Current)
				return st
			}
			// First install through the download-only path: nothing is
			// current yet, so it activates.
			start := time.Now()
			j, err := m.Download(ctx, typ, prev)
			if err != nil {
				t.Fatalf("download %s: %v", prev, err)
			}
			if st := logInstalled(prev, j, start); st.Current != j.Version || !managedbin.MatchesTag(prev, j.Version) {
				t.Fatalf("first download did not activate: current=%s job=%+v", st.Current, j)
			}
			// Download-only of the newest: stored + verified, current stays.
			start = time.Now()
			j, err = m.Download(ctx, typ, latest.Tag)
			if err != nil {
				t.Fatalf("download %s: %v", latest.Tag, err)
			}
			if st := logInstalled(latest.Tag, j, start); st.Current != managedbin.TagVersion(prev) {
				t.Fatalf("download-only moved current to %s", st.Current)
			}
			// Activate = instant switch, sha256 re-checked.
			if err := m.Activate(typ, latest.Version); err != nil {
				t.Fatal(err)
			}
			if _, v, _ := m.CurrentPath(typ); v != latest.Version {
				t.Fatalf("activate: current=%s", v)
			}
			t.Logf("%s download-only %s kept %s active; activate switched to %s", typ, latest.Tag, prev, latest.Version)
			// same version again: no download
			j, err = m.Install(ctx, typ, latest.Tag)
			if err != nil || !strings.Contains(j.Message, "already installed") {
				t.Fatalf("no-op update: %v %+v", err, j)
			}
			t.Logf("%s update to %s again: %s", typ, latest.Tag, j.Message)
			// rollback: instant, sha256 re-checked, no download
			start = time.Now()
			if err := m.Activate(typ, managedbin.TagVersion(prev)); err != nil {
				t.Fatal(err)
			}
			raw, err := m.VerifyCurrent(ctx, typ)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("%s rollback to %s in %s, wick-run --version=%q", typ, prev, time.Since(start).Round(time.Millisecond), raw)
			// and forward again
			if err := m.Activate(typ, latest.Version); err != nil {
				t.Fatal(err)
			}
		})
	}
}
