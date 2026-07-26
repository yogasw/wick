package main

import "testing"

func TestIsCloakEngine(t *testing.T) {
	cloak := []string{"cloakbrowser", "cloakbrowser-pro", "CloakBrowser", " cloakbrowser-pro "}
	for _, n := range cloak {
		if !isCloakEngine(n) {
			t.Errorf("isCloakEngine(%q) = false, want true", n)
		}
	}
	notCloak := []string{"chromium", "firefox", "webkit", "", "cloak"}
	for _, n := range notCloak {
		if isCloakEngine(n) {
			t.Errorf("isCloakEngine(%q) = true, want false", n)
		}
	}
}

func TestIsCloakPro(t *testing.T) {
	if !isCloakPro("cloakbrowser-pro") || !isCloakPro(" cloakbrowser-pro ") {
		t.Error("expected cloakbrowser-pro to be pro")
	}
	if isCloakPro("cloakbrowser") || isCloakPro("chromium") {
		t.Error("only cloakbrowser-pro is pro")
	}
}

func TestEngineTitle(t *testing.T) {
	cases := map[string]string{
		"cloakbrowser-pro": "Cloakbrowser Pro",
		"chromium":         "Chromium",
		"cloakbrowser":     "Cloakbrowser",
		"firefox":          "Firefox",
	}
	for in, want := range cases {
		if got := engineTitle(in); got != want {
			t.Errorf("engineTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBrowserEnginesIncludesBothCloak(t *testing.T) {
	var free, pro bool
	for _, e := range browserEngines {
		if e == cloakEngine {
			free = true
		}
		if e == cloakProEngine {
			pro = true
		}
	}
	if !free || !pro {
		t.Fatalf("browserEngines missing a cloak variant: free=%v pro=%v (%v)", free, pro, browserEngines)
	}
	// Both cloak names must be known engines (install/uninstall input validation).
	if !isKnownEngine(cloakEngine) || !isKnownEngine(cloakProEngine) {
		t.Error("cloak engines must be known")
	}
}
