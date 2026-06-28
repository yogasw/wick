package plugin

import (
	"encoding/json"
	"testing"

	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/entity"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

func TestBuildCatalog(t *testing.T) {
	releases := []GHRelease{
		{ // older httpbin — should lose to v0.2.0
			TagName: "httpbin/v0.1.0",
			Assets:  []GHAsset{{Name: "httpbin-0.1.0-linux-amd64.zip", DownloadURL: "u/old"}},
		},
		{
			TagName: "httpbin/v0.2.0",
			Assets: []GHAsset{
				{Name: "httpbin-0.2.0-linux-amd64.zip", DownloadURL: "u/linux-amd64"},
				{Name: "httpbin-0.2.0-linux-arm64.zip", DownloadURL: "u/linux-arm64"},
				{Name: "httpbin-0.2.0.sha256", DownloadURL: "u/sum"}, // non-zip, ignored
			},
		},
		{
			TagName: "echo/v1.0.0",
			Assets:  []GHAsset{{Name: "echo-1.0.0-windows-amd64.zip", DownloadURL: "u/echo-win"}},
		},
		{TagName: "v0.26.1"}, // core release — ignored (no "/v")
	}

	// Fake manifest fetcher: name/desc keyed off the URL we mapped above.
	fetch := func(url string) (*wickplugin.Manifest, error) {
		meta := map[string][2]string{
			"u/linux-amd64": {"HTTPBin", "Sample httpbin connector"},
			"u/echo-win":    {"Echo", "Echo back the input"},
		}
		m, ok := meta[url]
		if !ok {
			return &wickplugin.Manifest{}, nil
		}
		return &wickplugin.Manifest{
			Module: connector.Module{Meta: connector.Meta{
				Name:        m[0],
				Description: m[1],
				DefaultTags: []entity.DefaultTag{{Name: "Connector"}, {Name: "API"}},
			}},
		}, nil
	}

	got := BuildCatalog(releases, fetch)

	if len(got) != 2 {
		t.Fatalf("want 2 plugins, got %d: %+v", len(got), got)
	}
	// Sorted by key: echo, httpbin.
	if got[0].Key != "echo" || got[1].Key != "httpbin" {
		t.Fatalf("want [echo httpbin], got [%s %s]", got[0].Key, got[1].Key)
	}

	hb := got[1]
	if hb.Version != "0.2.0" {
		t.Errorf("httpbin version = %q, want 0.2.0 (highest)", hb.Version)
	}
	if hb.Name != "HTTPBin" || hb.Description != "Sample httpbin connector" {
		t.Errorf("httpbin name/desc not lifted from manifest: %q / %q", hb.Name, hb.Description)
	}
	if len(hb.DefaultTags) != 2 || hb.DefaultTags[0].Name != "Connector" || hb.DefaultTags[1].Name != "API" {
		t.Errorf("httpbin DefaultTags not lifted from manifest: %+v", hb.DefaultTags)
	}
	if hb.Assets["linux/amd64"] != "u/linux-amd64" || hb.Assets["linux/arm64"] != "u/linux-arm64" {
		t.Errorf("httpbin assets wrong: %+v", hb.Assets)
	}
	if _, ok := hb.Assets["linux/amd64"]; len(hb.Assets) != 2 || !ok {
		t.Errorf("httpbin should have exactly 2 os/arch assets, got %+v", hb.Assets)
	}

	// Round-trips through the catalog parser the app uses (reader side).
	data, err := MarshalCatalog(got)
	if err != nil {
		t.Fatal(err)
	}
	var reparsed []Available
	if err := json.Unmarshal(data, &reparsed); err != nil {
		t.Fatalf("catalog JSON not parseable by reader: %v", err)
	}
	if len(reparsed) != 2 {
		t.Fatalf("reparsed %d, want 2", len(reparsed))
	}
}

func TestSplitPluginTag(t *testing.T) {
	cases := []struct {
		tag             string
		wantKey, wantV  string
		wantOK          bool
	}{
		{"httpbin/v0.1.0", "httpbin", "0.1.0", true},
		{"my-plugin/v1.2.3", "my-plugin", "1.2.3", true},
		{"v0.26.1", "", "", false},   // core release
		{"httpbin", "", "", false},   // no version
		{"/v1.0.0", "", "", false},   // empty key
		{"httpbin/v", "", "", false}, // empty version
	}
	for _, tc := range cases {
		k, v, ok := splitPluginTag(tc.tag)
		if ok != tc.wantOK || k != tc.wantKey || v != tc.wantV {
			t.Errorf("splitPluginTag(%q) = (%q,%q,%v), want (%q,%q,%v)",
				tc.tag, k, v, ok, tc.wantKey, tc.wantV, tc.wantOK)
		}
	}
}

func TestOSArchFromZipName(t *testing.T) {
	cases := []struct {
		name       string
		wantOSArch string
		wantOK     bool
	}{
		{"httpbin-0.1.0-linux-amd64.zip", "linux/amd64", true},
		{"my-plugin-1.2.3-darwin-arm64.zip", "darwin/arm64", true},
		{"httpbin-0.1.0.sha256", "", false},
		{"notazip", "", false},
	}
	for _, tc := range cases {
		oa, ok := osArchFromZipName(tc.name)
		if ok != tc.wantOK || oa != tc.wantOSArch {
			t.Errorf("osArchFromZipName(%q) = (%q,%v), want (%q,%v)",
				tc.name, oa, ok, tc.wantOSArch, tc.wantOK)
		}
	}
}
