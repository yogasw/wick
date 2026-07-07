package plugin

import (
	"path/filepath"
	"testing"
)

func TestDataDirFromExe(t *testing.T) {
	tests := []struct {
		name string
		exe  string
		key  string
		want string // "" means ok=false expected
	}{
		{
			name: "installed layout (connectors/<key>/<bin>) resolves to sibling of connectors",
			exe:  filepath.Join("home", ".wick-agent", "plugins", "connectors", "playwright_browser", "playwright_browser"),
			key:  "playwright_browser",
			want: filepath.Join("home", ".wick-agent", "plugins", "playwright_browser"),
		},
		{
			name: "windows .exe binary in nested key folder still resolves",
			exe:  filepath.Join("C:", "Users", "x", ".wick-lab", "plugins", "connectors", "foo", "foo.exe"),
			key:  "foo",
			want: filepath.Join("C:", "Users", "x", ".wick-lab", "plugins", "foo"),
		},
		{
			name: "non-connectors layout falls back",
			exe:  filepath.Join("tmp", "go-build123", "playwright_browser"),
			key:  "playwright_browser",
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := dataDirFromExe(tt.exe, tt.key)
			if tt.want == "" {
				if ok {
					t.Fatalf("expected fallback (ok=false), got %q", got)
				}
				return
			}
			if !ok {
				t.Fatalf("expected ok=true for %q", tt.exe)
			}
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}
