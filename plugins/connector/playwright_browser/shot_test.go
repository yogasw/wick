package main

import (
	"strings"
	"testing"
)

func TestParseClip(t *testing.T) {
	if r, err := parseClip(""); r != nil || err != nil {
		t.Fatalf("empty clip: %v %v", r, err)
	}
	r, err := parseClip(`{"x":10,"y":20,"width":300,"height":150}`)
	if err != nil || r == nil || r.X != 10 || r.Height != 150 {
		t.Fatalf("parse: %+v %v", r, err)
	}
	if _, err := parseClip(`[1,2]`); err == nil {
		t.Fatal("array clip should fail")
	}
}

func TestShotOptsValidate(t *testing.T) {
	clip := &clipRect{X: 1, Y: 2, Width: 30, Height: 40}
	cases := []struct {
		o        shotOpts
		selector string
		wantErr  string
	}{
		{shotOpts{}, "", ""},
		{shotOpts{Clip: clip, Format: "jpeg", Quality: 60}, "", ""},
		{shotOpts{Clip: clip}, "#x", "cannot be combined"},
		{shotOpts{Clip: &clipRect{Width: 0, Height: 5}}, "", "width,height > 0"},
		{shotOpts{Format: "png", Quality: 50}, "", "jpeg only"},
		{shotOpts{Format: "jpeg", Quality: 101}, "", "between 1 and 100"},
		{shotOpts{Format: "webp"}, "", "png or jpeg"},
	}
	for _, tc := range cases {
		err := tc.o.validate(tc.selector)
		if tc.wantErr == "" && err != nil {
			t.Errorf("%+v: unexpected %v", tc.o, err)
		}
		if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
			t.Errorf("%+v: got %v, want %q", tc.o, err, tc.wantErr)
		}
	}
}

func TestValidateScreenshotAction(t *testing.T) {
	a := action{Action: "screenshot", Selector: "#a", Clip: &clipRect{Width: 1, Height: 1}}
	if err := validateActionArgs(0, a); err == nil {
		t.Fatal("clip+selector should be rejected before launch")
	}
	a = action{Action: "screenshot", Clip: &clipRect{Width: 10, Height: 10}, Type: "JPEG", Quality: 70}
	if err := validateActionArgs(0, a); err != nil {
		t.Fatalf("valid action rejected: %v", err)
	}
}

func TestCheckSaveDir(t *testing.T) {
	if err := checkSaveDir(""); err != nil {
		t.Fatalf("empty: %v", err)
	}
	if err := checkSaveDir("relative/dir"); err == nil {
		t.Fatal("relative path accepted")
	}
	if err := checkSaveDir("/nonexistent-dir-for-test"); err == nil {
		t.Fatal("missing dir accepted")
	}
}

func TestProfileSeedStable(t *testing.T) {
	a, b := profileSeed("profile-a"), profileSeed("profile-a")
	if a != b || a < 10000 || a > 99999 {
		t.Fatalf("seed %d / %d", a, b)
	}
	if profileSeed("profile-a") == profileSeed("profile-b") {
		t.Log("distinct names collided (allowed, just unlikely)")
	}
}
