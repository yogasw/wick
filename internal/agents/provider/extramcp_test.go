package provider

import (
	"strings"
	"testing"
)

func TestParseExtraMCP(t *testing.T) {
	m, err := ParseExtraMCP(`{"mcpServers":{
		"gh":{"type":"http","url":"https://api.example/mcp","headers":{"Authorization":"Bearer ${GH_TOKEN}","X-Team":"core"}},
		"fs":{"command":"npx","args":["-y","fs","${ROOT}"],"env":{"API_KEY":"${FS_KEY}"}}}}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := ExtraMCPNames(m); strings.Join(got, ",") != "fs,gh" {
		t.Fatal(got)
	}
	oc := m["gh"].OpencodeEntry()
	if oc["type"] != "remote" || oc["headers"].(map[string]string)["Authorization"] != "Bearer {env:GH_TOKEN}" {
		t.Fatalf("opencode remote %v", oc)
	}
	local := m["fs"].OpencodeEntry()
	if cmd := local["command"].([]string); strings.Join(cmd, " ") != "npx -y fs {env:ROOT}" {
		t.Fatalf("opencode local %v", cmd)
	}
	if omp := m["fs"].OMPEntry(); omp["type"] != "stdio" || omp["env"].(map[string]string)["API_KEY"] != "${FS_KEY}" {
		t.Fatalf("omp %v", omp)
	}
	// bare map (no mcpServers wrapper) is fine too
	if _, err := ParseExtraMCP(`{"x":{"url":"https://a/b"}}`); err != nil {
		t.Fatal(err)
	}
	if m, err := ParseExtraMCP("  "); err != nil || m != nil {
		t.Fatal("empty must be nil")
	}
}

func TestParseExtraMCPRejects(t *testing.T) {
	bad := map[string]string{
		"reserved":        `{"wick":{"url":"https://a"}}`,
		"plaintext token": `{"gh":{"url":"https://a","headers":{"Authorization":"Bearer ghp_abc"}}}`,
		"plaintext env":   `{"fs":{"command":"x","env":{"OPENAI_API_KEY":"sk-1"}}}`,
		"no url":          `{"gh":{"type":"http"}}`,
		"both":            `{"gh":{"url":"https://a","command":"x"}}`,
		"unknown field":   `{"gh":{"url":"https://a","bogus":1}}`,
		"bad name":        `{"a b":{"url":"https://a"}}`,
		"not json":        `{nope`,
	}
	for name, raw := range bad {
		if _, err := ParseExtraMCP(raw); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
