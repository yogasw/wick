package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/playwright-community/playwright-go"
	"github.com/yogasw/wick/pkg/connector"
)

func moliCtx(cfg map[string]string) *connector.Ctx {
	return connector.NewPluginCtx(context.Background(), cfg, map[string]string{})
}

func TestIsMoliEngine(t *testing.T) {
	for _, n := range []string{"moli", "Moli", " moli "} {
		if !isMoliEngine(n) {
			t.Errorf("isMoliEngine(%q) = false", n)
		}
	}
	for _, n := range []string{"", "chromium", "cloakbrowser", "molix"} {
		if isMoliEngine(n) {
			t.Errorf("isMoliEngine(%q) = true", n)
		}
	}
	if !isKnownEngine(moliEngine) {
		t.Error("moli must be a known engine")
	}
	if isCloakEngine(moliEngine) {
		t.Error("moli must not be treated as cloak")
	}
}

func TestMoliAssetName(t *testing.T) {
	got := moliAssetName()
	if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" && got != "moli-x86_64-unknown-linux-gnu.tar.gz" {
		t.Fatalf("moliAssetName() = %q", got)
	}
	if got != "" && !strings.HasPrefix(got, "moli-") {
		t.Fatalf("unexpected asset name %q", got)
	}
}

func TestMoliServeArgs(t *testing.T) {
	// Defaults: layout on, no resources, no profile.
	got := moliServeArgs(moliCtx(map[string]string{}), 4000, "")
	want := []string{"serve", "--host", "127.0.0.1", "--port", "4000", "--layout"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("defaults:\n got  %v\n want %v", got, want)
	}

	got = moliServeArgs(moliCtx(map[string]string{
		"moli_layout":   "false",
		"moli_resource": "true",
		"proxy_server":  "socks5://p:1080",
		"proxy_bypass":  ".abc.com, localhost",
		"user_agent":    "UA/1",
	}), 4001, "/data/profile-x")
	want = []string{"serve", "--host", "127.0.0.1", "--port", "4001", "--resource",
		"--profile-dir=/data/profile-x", "--http-proxy", "socks5://p:1080",
		"--http-no-proxy", ".abc.com,localhost", "--user-agent", "UA/1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("configured:\n got  %v\n want %v", got, want)
	}

	// SSRF guard: one --block-cidrs with the whole list (Moli rejects a repeat).
	got = moliServeArgs(moliCtx(map[string]string{
		"moli_block_private_networks": "true",
		"moli_block_cidrs":            "10.0.0.0/8, 169.254.169.254/32",
	}), 4002, "")
	want = []string{"serve", "--host", "127.0.0.1", "--port", "4002", "--layout",
		"--block-private-networks", "--block-cidrs", "10.0.0.0/8,169.254.169.254/32"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("blocklist:\n got  %v\n want %v", got, want)
	}
}

func TestMoliFetchArgs(t *testing.T) {
	c := moliCtx(map[string]string{"browser": "moli"})
	got := moliFetchArgs(c, getMarkdownInput{URL: "https://abc.com", Format: "markdown"})
	want := []string{"fetch", "--dump", "markdown", "--wait", "done", "https://abc.com"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("defaults:\n got  %v\n want %v", got, want)
	}

	c = moliCtx(map[string]string{
		"browser":                     "moli",
		"proxy_server":                "http://p:3128",
		"user_agent":                  "UA/1",
		"moli_block_private_networks": "true",
		"moli_block_cidrs":            "10.0.0.0/8",
	})
	got = moliFetchArgs(c, getMarkdownInput{URL: "https://abc.com/x", Format: "semantic_tree_text", WaitSelector: "#main"})
	want = []string{"fetch", "--dump", "semantic_tree_text", "--wait", "done", "--wait-selector", "#main",
		"--http-proxy", "http://p:3128", "--user-agent", "UA/1",
		"--block-private-networks", "--block-cidrs", "10.0.0.0/8", "https://abc.com/x"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("configured:\n got  %v\n want %v", got, want)
	}
}

func TestParseGetMarkdown(t *testing.T) {
	parse := func(cfg, in map[string]string) (getMarkdownInput, error) {
		return parseGetMarkdown(connector.NewPluginCtx(context.Background(), cfg, in))
	}
	if _, err := parse(map[string]string{"browser": "chromium"}, map[string]string{"url": "https://abc.com"}); err == nil || !strings.Contains(err.Error(), "get_markdown needs browser=moli") {
		t.Fatalf("non-moli engine: err = %v", err)
	}
	moli := map[string]string{"browser": "moli"}
	in, err := parse(moli, map[string]string{"url": "https://abc.com", "wait_selector": " #main "})
	if err != nil || in.Format != "markdown" || in.WaitSelector != "#main" {
		t.Fatalf("defaults: %+v, %v", in, err)
	}
	if _, err := parse(moli, map[string]string{"url": "https://abc.com", "format": "pdf"}); err == nil {
		t.Fatal("format pdf must be rejected")
	}
	if _, err := parse(moli, map[string]string{"url": "ftp://abc.com"}); err == nil {
		t.Fatal("non-http url must be rejected")
	}
}

func TestApplyDeviceMoliDropsIsMobile(t *testing.T) {
	desc := &playwright.DeviceDescriptor{
		UserAgent:         "iPhone UA",
		Viewport:          &playwright.Size{Width: 393, Height: 659},
		DeviceScaleFactor: 3,
		IsMobile:          true,
		HasTouch:          true,
	}
	var opts playwright.BrowserNewContextOptions
	applyDevice(&opts, desc, "moli")
	if opts.IsMobile != nil && *opts.IsMobile {
		t.Fatal("moli: IsMobile must not be set")
	}
	if opts.Viewport == nil || opts.Viewport.Width != 393 || opts.UserAgent == nil || *opts.UserAgent != "iPhone UA" ||
		opts.DeviceScaleFactor == nil || *opts.DeviceScaleFactor != 3 || opts.HasTouch == nil || !*opts.HasTouch {
		t.Fatalf("moli: viewport/UA/DPR/touch must still apply: %+v", opts)
	}

	opts = playwright.BrowserNewContextOptions{}
	applyDevice(&opts, desc, "chromium")
	if opts.IsMobile == nil || !*opts.IsMobile {
		t.Fatal("chromium: IsMobile must be true")
	}
}

func TestMoliSessionNoteExtensions(t *testing.T) {
	dir := t.TempDir()
	c := moliCtx(map[string]string{"session_dir": dir})
	if note := moliSessionNote(len(installedExtensions(c))); strings.Contains(note, "extension") {
		t.Fatalf("no extensions installed, note = %q", note)
	}
	ext := filepath.Join(dir, "extensions", "ublock")
	if err := os.MkdirAll(ext, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ext, "manifest.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if note := moliSessionNote(len(installedExtensions(c))); !strings.Contains(note, "Moli does not load browser extensions; 1 installed extension(s) were skipped.") {
		t.Fatalf("note = %q", note)
	}
}

func TestOwnedDirArg(t *testing.T) {
	prefixes := []string{"--user-data-dir=/root/s", moliProfileFlag + "/root/s"}
	cases := map[string]string{
		"--user-data-dir=/root/s/profile-a": "/root/s/profile-a",
		"--profile-dir=/root/s/profile-b":   "/root/s/profile-b",
	}
	for arg, want := range cases {
		if got, ok := ownedDirArg(arg, prefixes); !ok || got != want {
			t.Errorf("ownedDirArg(%q) = %q,%v want %q", arg, got, ok, want)
		}
	}
	for _, arg := range []string{"--profile-dir=/elsewhere/p", "--user-data-dir=/tmp/x", "serve"} {
		if _, ok := ownedDirArg(arg, prefixes); ok {
			t.Errorf("ownedDirArg(%q) matched, want no match", arg)
		}
	}
}

func TestMoliBinaryPathOverride(t *testing.T) {
	c := moliCtx(map[string]string{"moli_executable_path": "/opt/moli/moli"})
	if got := moliBinaryPath(c); got != "/opt/moli/moli" {
		t.Fatalf("moliBinaryPath = %q", got)
	}
	if err := moliUninstall(c); err == nil {
		t.Fatal("uninstall must refuse to delete an override path")
	}
}

func TestLimitedWriter(t *testing.T) {
	var sb strings.Builder
	w := &limitedWriter{w: &sb, n: 5}
	n, _ := w.Write([]byte("hello world"))
	w.Write([]byte("more"))
	if n != 11 || sb.String() != "hello" {
		t.Fatalf("limitedWriter kept %q (n=%d)", sb.String(), n)
	}
}

func TestTouchProfileDirBumpsMtime(t *testing.T) {
	dir := t.TempDir()
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(dir, old, old); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Truncate(time.Second)
	touchProfileDir(dir, now)
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(now) {
		t.Errorf("mtime = %v, want %v", info.ModTime(), now)
	}
}
