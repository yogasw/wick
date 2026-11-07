package main

import (
	"context"
	"encoding/base64"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/yogasw/wick/pkg/connector"
)

func TestCDPBlocked(t *testing.T) {
	for _, ok := range []string{"Network.setExtraHTTPHeaders", "Page.navigate", "Runtime.evaluate", "DOM.getDocument", "Emulation.setGeolocationOverride"} {
		if err := cdpBlocked(ok); err != nil {
			t.Errorf("%s should pass: %v", ok, err)
		}
	}
	for _, bad := range []string{"Browser.close", "Target.createTarget", "Storage.getCookies", "Network.getAllCookies", "Security.setIgnoreCertificateErrors", "SystemInfo.getInfo"} {
		if err := cdpBlocked(bad); err == nil {
			t.Errorf("%s should be refused", bad)
		}
	}
	for _, malformed := range []string{"", "close", "network.enable", "Network.", "Network.enable; rm"} {
		if err := cdpBlocked(malformed); err == nil {
			t.Errorf("%q should be refused as malformed", malformed)
		}
	}
}

func TestParseMoliExtraArgs(t *testing.T) {
	got, err := parseMoliExtraArgs(`["--wait-script","document.title","--with-frames","--delay-ms=500"]`)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{"--wait-script", "document.title", "--with-frames", "--delay-ms=500"}) {
		t.Fatalf("unexpected %v", got)
	}
	// A --wait- flag moli adds later passes with its value.
	if got, err := parseMoliExtraArgs(`["--wait-future-thing","x"]`); err != nil || len(got) != 2 {
		t.Fatalf("new --wait- flag: %v %v", got, err)
	}
	for _, bad := range []string{
		`["--http-proxy","http://x"]`, `["--profile-dir","/tmp/p"]`, `["--eval-file","/etc/passwd"]`,
		`["--wait-script-file","/x"]`, `["--block-private-networks"]`, `["--dump","html"]`,
		`["--insecure-disable-tls-host-verification"]`, `["https://other.example"]`, `["--eval"]`, `"--with-frames"`,
	} {
		if _, err := parseMoliExtraArgs(bad); err == nil {
			t.Errorf("%s should be refused", bad)
		}
	}
	if got, err := parseMoliExtraArgs(""); err != nil || got != nil {
		t.Fatal("empty extra_args is no extras")
	}
}

func TestMoliFetchArgsExtraBeforePolicy(t *testing.T) {
	in := getMarkdownInput{URL: "https://example.com", Format: "markdown", ExtraArgs: `["--with-frames"]`}
	args := moliFetchArgs(connector.NewCtx(context.Background(), "inst", map[string]string{"browser": "moli", "moli_block_private_networks": "true"}, nil, nil, nil, nil), in)
	fi, bi := slices.Index(args, "--with-frames"), slices.Index(args, "--block-private-networks")
	if fi < 0 || bi < 0 || fi > bi {
		t.Fatalf("extras must come before the network policy: %v", args)
	}
	if args[len(args)-1] != "https://example.com" || strings.HasPrefix(args[len(args)-1], "-") {
		t.Fatalf("url stays last: %v", args)
	}
}

func TestSaveCDPData(t *testing.T) {
	dir := t.TempDir()
	got, err := saveCDPData(dir, "Page.printToPDF", base64.StdEncoding.EncodeToString([]byte("%PDF-1.7 test")))
	if err != nil {
		t.Fatal(err)
	}
	path, _ := got["path"].(string)
	if !strings.HasSuffix(path, ".pdf") || got["bytes"] != 13 {
		t.Fatalf("unexpected %v", got)
	}
	if b, err := os.ReadFile(path); err != nil || string(b) != "%PDF-1.7 test" {
		t.Fatalf("file content %q %v", b, err)
	}
	if _, err := saveCDPData(dir, "Page.printToPDF", "not base64!!"); err == nil {
		t.Fatal("bad base64 is an error")
	}
}
