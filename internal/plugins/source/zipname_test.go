package source

import "testing"

func TestParseZipName(t *testing.T) {
	cases := []struct {
		name, key, version, osArch string
		ok                         bool
	}{
		{"loki-0.3.1-linux-amd64.zip", "loki", "0.3.1", "linux/amd64", true},
		{"google_workspace-1.2.0-darwin-arm64.zip", "google_workspace", "1.2.0", "darwin/arm64", true},
		{"convert-text-alt-0.1.0-linux-amd64.zip", "convert-text-alt", "0.1.0", "linux/amd64", true},
		{"wa-message-inbound-extractor-0.2.0-rc-1-linux-arm64.zip", "wa-message-inbound-extractor", "0.2.0-rc-1", "linux/arm64", true},
		{"x2-0.1.0-linux-amd64.zip", "x2", "0.1.0", "linux/amd64", true},
		{"loki-linux-amd64.zip", "", "", "", false},
		{"loki-0.3.1-linux-amd64.tar.gz", "", "", "", false},
	}
	for _, c := range cases {
		key, version, osArch, ok := parseZipName(c.name)
		if ok != c.ok || key != c.key || version != c.version || osArch != c.osArch {
			t.Errorf("parseZipName(%q) = %q %q %q %v, want %q %q %q %v", c.name, key, version, osArch, ok, c.key, c.version, c.osArch, c.ok)
		}
	}
}
