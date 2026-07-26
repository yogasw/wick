package main

import (
	"path/filepath"
	"testing"
)

func TestParseCloakInfo(t *testing.T) {
	// Real `cloakbrowser info` output (free tier).
	out := `CloakBrowser diagnostics
Python:    3.14.3
OS:        Windows AMD64
Platform:  windows-x64
Version:   146.0.7680.177.5 (free)
Binary:    C:\Users\me\.cloakbrowser\chromium-146.0.7680.177.5\chrome.exe
Installed: True
Cache:     C:\Users\me\.cloakbrowser\chromium-146.0.7680.177.5`

	info := parseCloakInfo(out)
	if info.Version != "146.0.7680.177.5" {
		t.Errorf("version = %q, want 146.0.7680.177.5", info.Version)
	}
	if info.Tier != "free" {
		t.Errorf("tier = %q, want free", info.Tier)
	}
	if !info.Installed {
		t.Error("expected Installed=true")
	}
	if info.Binary != `C:\Users\me\.cloakbrowser\chromium-146.0.7680.177.5\chrome.exe` {
		t.Errorf("binary path wrong: %q", info.Binary)
	}
}

func TestParseCloakInfoPro(t *testing.T) {
	out := `Version:   150.0.7871.114.3 (pro)
Binary:    /root/.cloakbrowser/chromium-150/chrome
Installed: True`
	info := parseCloakInfo(out)
	if info.Version != "150.0.7871.114.3" || info.Tier != "pro" {
		t.Fatalf("pro parse wrong: version=%q tier=%q", info.Version, info.Tier)
	}
	if !info.Installed {
		t.Error("expected Installed=true")
	}
}

// TestParseCloakInfoNoLaunch covers the exact `cloakbrowser info --no-launch`
// output the widget now runs: it carries a leading Windows charmap warning and a
// trailing "Launch: skipped (--quick)" line the parser must ignore, while still
// reading Version/Tier/Binary/Installed. The --no-launch flag is what stops the
// info call from flashing a browser window on every status render.
func TestParseCloakInfoNoLaunch(t *testing.T) {
	out := "Error: 'charmap' codec can't encode character '\\u2713' in position 11: character maps to <undefined>\r\n" +
		"CloakBrowser diagnostics\r\n" +
		"Python:    3.14.3\r\n" +
		"OS:        Windows AMD64\r\n" +
		"Platform:  windows-x64\r\n" +
		"Version:   150.0.7871.114.3 (pro)\r\n" +
		"Binary:    C:\\Users\\me\\.cloakbrowser\\chromium-150.0.7871.114.3-pro\\chrome.exe\r\n" +
		"Installed: True\r\n" +
		"Launch:    skipped (--quick)\r\n"
	info := parseCloakInfo(out)
	if info.Version != "150.0.7871.114.3" || info.Tier != "pro" {
		t.Fatalf("--no-launch parse wrong: version=%q tier=%q", info.Version, info.Tier)
	}
	if !info.Installed {
		t.Error("expected Installed=true from --no-launch output")
	}
	if info.Binary != `C:\Users\me\.cloakbrowser\chromium-150.0.7871.114.3-pro\chrome.exe` {
		t.Errorf("binary path wrong: %q", info.Binary)
	}
}

func TestParseCloakInfoNotInstalled(t *testing.T) {
	out := `Version:   (unknown)
Installed: False`
	info := parseCloakInfo(out)
	if info.Installed {
		t.Error("expected Installed=false")
	}
}

func TestParseCloakInfoGarbage(t *testing.T) {
	// Unrecognized output must not panic and yields a zero-ish struct.
	info := parseCloakInfo("some unrelated banner\nno colons here\n")
	if info.Installed || info.Version != "" || info.Tier != "" {
		t.Errorf("garbage should parse to empty, got %+v", info)
	}
}

func TestPlaywrightEngineRoot(t *testing.T) {
	// Build paths with filepath.Join so the test is OS-agnostic (separators match
	// what playwrightEngineRoot's filepath.Dir walk produces on this platform).
	root := filepath.Join("home", "me", ".cache", "ms-playwright")
	engineRev := filepath.Join(root, "chromium-1148")
	exe := filepath.Join(engineRev, "chrome-win", "chrome.exe")
	if got := playwrightEngineRoot(exe); got != engineRev {
		t.Errorf("playwrightEngineRoot(%q) = %q, want %q", exe, got, engineRev)
	}

	// No ms-playwright ancestor → "" (bail rather than delete something odd).
	weird := filepath.Join("opt", "custom", "weird", "chrome")
	if got := playwrightEngineRoot(weird); got != "" {
		t.Errorf("playwrightEngineRoot(%q) = %q, want empty", weird, got)
	}
}
