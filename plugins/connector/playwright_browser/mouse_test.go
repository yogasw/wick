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
	if len(pts) < 4 || len(pts) > maxMouseSteps {
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

func TestHumanPath_LongMovesLandOnTarget(t *testing.T) {
	for seed := int64(0); seed < 50; seed++ {
		rng := rand.New(rand.NewSource(seed))
		to := point{900, 700}
		pts := humanPath(point{0, 0}, to, 0, rng)
		if pts[len(pts)-1] != to {
			t.Fatalf("seed %d: ends at %v", seed, pts[len(pts)-1])
		}
		for _, p := range pts {
			if p.X > to.X*1.1 || p.Y > to.Y*1.1 {
				t.Fatalf("seed %d: overshoot too far: %v", seed, p)
			}
		}
	}
}

func TestWheelSlicesSumToTotal(t *testing.T) {
	sl := wheelSlices(300, 20)
	sum := 0.0
	for _, v := range sl {
		sum += v
	}
	if math.Abs(sum-300) > 1e-9 {
		t.Fatalf("sum = %v", sum)
	}
	if sl[0] >= sl[10] {
		t.Fatalf("not eased: first %v, middle %v", sl[0], sl[10])
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
		{action{Action: "mouse_click", X: f(1), Y: f(2), HoverDwellMS: -1}, "hover_dwell_ms"},
		{action{Action: "mouse_wheel", DeltaY: 300}, ""},
		{action{Action: "mouse_wheel"}, "must be non-zero"},
		{action{Action: "frame_eval", FrameURLContains: "flow"}, "script is required"},
		{action{Action: "frame_eval", Script: "document.title"}, ""},
		{action{Action: "frame_text"}, ""},
		{action{Action: "frame_click", FrameURLContains: "flow"}, "selector or text"},
		{action{Action: "frame_click", Text: "Provinsi"}, ""},
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
