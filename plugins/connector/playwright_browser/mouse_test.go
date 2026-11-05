package main

import (
	"context"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yogasw/wick/pkg/connector"
)

func TestHumanPath_EndsExactlyOnTarget(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	from, to := point{10, 10}, point{410, 310}
	pts := humanPath(from, to, 0, rng)
	if len(pts) < 8 || len(pts) > 60 {
		t.Fatalf("derived step count out of range: %d", len(pts))
	}
	if pts[len(pts)-1] != to {
		t.Fatalf("last point = %v, want %v", pts[len(pts)-1], to)
	}
	// No single hop should cover most of the distance (no teleport).
	total := math.Hypot(to.X-from.X, to.Y-from.Y)
	prev := from
	for _, p := range pts {
		if d := math.Hypot(p.X-prev.X, p.Y-prev.Y); d > total/2 {
			t.Fatalf("hop of %.1fpx out of %.1fpx", d, total)
		}
		prev = p
	}
}

func TestHumanPath_ExplicitStepsAndZeroDistance(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	if got := len(humanPath(point{0, 0}, point{100, 0}, 25, rng)); got != 25 {
		t.Fatalf("steps=25 gave %d points", got)
	}
	pts := humanPath(point{5, 5}, point{5, 5}, 0, rng)
	if len(pts) != 1 || pts[0] != (point{5, 5}) {
		t.Fatalf("zero distance: %v", pts)
	}
}

func TestValidateMouseActions(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	cases := []struct {
		a       action
		wantErr string
	}{
		{action{Action: "mouse_click", X: f(10), Y: f(20)}, ""},
		{action{Action: "mouse_move", X: f(0), Y: f(0), Steps: 30}, ""},
		{action{Action: "mouse_click", X: f(10)}, "x and y are required"},
		{action{Action: "mouse_click", X: f(-1), Y: f(2)}, ">= 0"},
		{action{Action: "mouse_click", X: f(1), Y: f(2), Button: "side"}, "button must be"},
		{action{Action: "mouse_click", X: f(1), Y: f(2), DelayMS: 999999}, "delay_ms"},
		{action{Action: "mouse_move", X: f(1), Y: f(2), Steps: 999}, "steps"},
		{action{Action: "mouse_wheel", DeltaY: 300}, ""},
		{action{Action: "mouse_wheel"}, "must be non-zero"},
	}
	for _, tc := range cases {
		err := validateActionArgs(0, tc.a)
		if tc.wantErr == "" && err != nil {
			t.Errorf("%+v: unexpected error %v", tc.a, err)
		}
		if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
			t.Errorf("%+v: error %v, want containing %q", tc.a, err, tc.wantErr)
		}
	}
}

func TestProfileDelete_RefusesLockedProfile(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, profilePrefix+"keep-me"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := map[string]string{"session_dir": dir, "locked_profiles": "other, Keep-Me"}
	c := connector.NewCtx(context.Background(), "inst", cfg, map[string]string{"name": "keep-me"}, nil, nil, nil)
	_, err := profileDelete(c)
	if err == nil || !strings.Contains(err.Error(), "locked") {
		t.Fatalf("want locked error, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, profilePrefix+"keep-me")); err != nil {
		t.Fatalf("locked profile was removed: %v", err)
	}
}
