package main

// Real-mouse actions for `run`: mouse_move, mouse_click, mouse_wheel.
//
// Selector actions (click, hover, …) resolve an element first, which cannot
// reach into a cross-origin iframe without frame plumbing. These act on
// VIEWPORT coordinates through page.Mouse(), i.e. CDP Input.dispatchMouseEvent,
// so the browser hit-tests the point like a real pointer and the event lands
// in whatever is painted there, iframe or not.
//
// Movement follows a curved, jittered path with uneven timing so the event
// stream looks like a hand on a mouse rather than one teleporting jump.

import (
	"math"
	"math/rand"
	"time"

	"github.com/playwright-community/playwright-go"
)

const (
	maxMouseSteps   = 200
	maxClickDelayMS = 5000
	// mousePosKey stores the last pointer position on the page's window. A live
	// session reconnects over CDP on every run call, and Playwright's own
	// tracked position starts again at (0,0) each time; keeping it on the page
	// lets the next path start where the previous one ended.
	mousePosKey = "__wickMouse"
)

type point struct{ X, Y float64 }

// humanPath returns the points from `from` to `to`: a cubic Bézier with
// randomly bowed control points, ease-in-out spacing, and ±1px jitter on the
// inner points. The last point is exactly `to`. steps <= 0 derives a count
// from the distance.
func humanPath(from, to point, steps int, rng *rand.Rand) []point {
	dx, dy := to.X-from.X, to.Y-from.Y
	dist := math.Hypot(dx, dy)
	if steps <= 0 {
		steps = int(dist / 12)
		steps = max(8, min(steps, 60))
	}
	if dist < 1 {
		return []point{to}
	}
	// Control points sit along the line, pushed sideways by up to 25% of the
	// distance so the path bows like a wrist arc.
	nx, ny := -dy/dist, dx/dist
	bow := func() float64 { return (rng.Float64()*2 - 1) * dist * 0.25 }
	b1, b2 := bow(), bow()
	c1 := point{from.X + dx*0.3 + nx*b1, from.Y + dy*0.3 + ny*b1}
	c2 := point{from.X + dx*0.7 + nx*b2, from.Y + dy*0.7 + ny*b2}

	pts := make([]point, 0, steps)
	for i := 1; i <= steps; i++ {
		t := float64(i) / float64(steps)
		t = t * t * (3 - 2*t) // ease-in-out: slow start, slow landing
		u := 1 - t
		p := point{
			X: u*u*u*from.X + 3*u*u*t*c1.X + 3*u*t*t*c2.X + t*t*t*to.X,
			Y: u*u*u*from.Y + 3*u*u*t*c1.Y + 3*u*t*t*c2.Y + t*t*t*to.Y,
		}
		if i < steps {
			p.X += rng.Float64()*2 - 1
			p.Y += rng.Float64()*2 - 1
		}
		pts = append(pts, p)
	}
	pts[len(pts)-1] = to
	return pts
}

func newRNG() *rand.Rand { return rand.New(rand.NewSource(time.Now().UnixNano())) }

// jitterSleep waits base..base+spread milliseconds.
func jitterSleep(rng *rand.Rand, base, spread int) {
	time.Sleep(time.Duration(base+rng.Intn(spread+1)) * time.Millisecond)
}

// mousePos reads the last position stored on the page, falling back to the
// viewport centre (a fresh page has no known pointer position).
func mousePos(page playwright.Page) point {
	if v, err := page.Evaluate(`() => window.` + mousePosKey + ` || null`); err == nil {
		if m, ok := v.(map[string]any); ok {
			x, okx := toFloat(m["x"])
			y, oky := toFloat(m["y"])
			if okx && oky {
				return point{x, y}
			}
		}
	}
	if vp := page.ViewportSize(); vp != nil {
		return point{float64(vp.Width) / 2, float64(vp.Height) / 2}
	}
	return point{640, 400}
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	}
	return 0, false
}

func saveMousePos(page playwright.Page, p point) {
	_, _ = page.Evaluate(`(p) => { window.`+mousePosKey+` = p }`, map[string]any{"x": p.X, "y": p.Y})
}

// humanMove glides the pointer to (x, y) along humanPath.
func humanMove(page playwright.Page, x, y float64, steps int) (any, error) {
	rng := newRNG()
	from := mousePos(page)
	to := point{x, y}
	pts := humanPath(from, to, steps, rng)
	mouse := page.Mouse()
	for _, p := range pts {
		if err := mouse.Move(p.X, p.Y); err != nil {
			return nil, err
		}
		jitterSleep(rng, 6, 14)
	}
	saveMousePos(page, to)
	return map[string]any{"x": x, "y": y, "from": map[string]any{"x": from.X, "y": from.Y}, "points": len(pts)}, nil
}

// humanClick moves to (x, y), pauses like a hand settling, then presses and
// releases. delay_ms is the hold between down and up (default 60–140ms).
func humanClick(page playwright.Page, a action) (any, error) {
	if _, err := humanMove(page, *a.X, *a.Y, a.Steps); err != nil {
		return nil, err
	}
	rng := newRNG()
	jitterSleep(rng, 40, 80)
	btn := mouseButton(a.Button)
	mouse := page.Mouse()
	if err := mouse.Down(playwright.MouseDownOptions{Button: btn}); err != nil {
		return nil, err
	}
	hold := a.DelayMS
	if hold == 0 {
		hold = 60 + rng.Intn(81)
	}
	time.Sleep(time.Duration(hold) * time.Millisecond)
	if err := mouse.Up(playwright.MouseUpOptions{Button: btn}); err != nil {
		return nil, err
	}
	return map[string]any{"x": *a.X, "y": *a.Y, "button": string(*btn), "hold_ms": hold}, nil
}

// humanWheel scrolls by (dx, dy) split into uneven ticks, the way a wheel
// notches rather than jumping the whole distance in one event.
func humanWheel(page playwright.Page, dx, dy float64, steps int) (any, error) {
	if steps <= 0 {
		steps = int(math.Max(math.Abs(dx), math.Abs(dy)) / 100)
		steps = max(1, min(steps, 20))
	}
	rng := newRNG()
	mouse := page.Mouse()
	for i := 0; i < steps; i++ {
		if err := mouse.Wheel(dx/float64(steps), dy/float64(steps)); err != nil {
			return nil, err
		}
		if i < steps-1 {
			jitterSleep(rng, 30, 60)
		}
	}
	return map[string]any{"delta_x": dx, "delta_y": dy, "ticks": steps}, nil
}

func mouseButton(b string) *playwright.MouseButton {
	switch b {
	case "right":
		return playwright.MouseButtonRight
	case "middle":
		return playwright.MouseButtonMiddle
	}
	return playwright.MouseButtonLeft
}
