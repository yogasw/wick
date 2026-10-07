package source

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	connplugin "github.com/yogasw/wick/internal/connectors/plugin"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

// writeZip writes a plugin zip of the given kind to a temp file.
func writeZip(t *testing.T, key, kind string) string {
	t.Helper()
	data, _ := buildZip(t, key, kind, "1.0.0")
	p := filepath.Join(t.TempDir(), key+".zip")
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestInstallPathRoutesByKind(t *testing.T) {
	for _, tc := range []struct{ key, kind, want string }{
		{"toolx", wickplugin.KindTool, wickplugin.KindTool},
		{"jobx", wickplugin.KindJob, wickplugin.KindJob},
		{"svcx", wickplugin.KindService, wickplugin.KindService},
		{"connx", wickplugin.KindConnector, wickplugin.KindConnector},
		{"legacyx", "", wickplugin.KindConnector},
	} {
		t.Run(tc.key, func(t *testing.T) {
			opts := tmpRoot(t)
			in, err := InstallPath(context.Background(), writeZip(t, tc.key, tc.kind), opts)
			if err != nil {
				t.Fatal(err)
			}
			if in.Kind != tc.want || in.Key != tc.key {
				t.Fatalf("installed %+v, want kind %s", in, tc.want)
			}
			for _, k := range wickplugin.Kinds {
				_, err := os.Stat(filepath.Join(opts.Root(k), tc.key, "plugin.json"))
				if (k == tc.want) != (err == nil) {
					t.Fatalf("kind folder %s: present=%v, want only %s", k, err == nil, tc.want)
				}
			}
		})
	}
}

func TestInstallPathFromDirectory(t *testing.T) {
	opts := tmpRoot(t)
	dir, err := connplugin.ExtractArchive(writeZip(t, "dirtool", wickplugin.KindTool), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := InstallPath(context.Background(), dir, opts); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(opts.Root(wickplugin.KindTool), "dirtool", "plugin.json")); err != nil {
		t.Fatalf("tool from a directory not installed into the tool folder: %v", err)
	}
}

func TestInstallPathUnknownKindRejected(t *testing.T) {
	opts := tmpRoot(t)
	_, err := InstallPath(context.Background(), writeZip(t, "weird", "widget"), opts)
	if err == nil || !strings.Contains(err.Error(), `unknown kind "widget"`) {
		t.Fatalf("want unknown kind error, got %v", err)
	}
	for _, k := range wickplugin.Kinds {
		if _, err := os.Stat(filepath.Join(opts.Root(k), "weird")); err == nil {
			t.Fatalf("unknown kind landed in the %s folder", k)
		}
	}
}
