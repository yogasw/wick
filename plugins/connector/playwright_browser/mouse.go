package main

// Real-mouse actions for `run`: mouse_move, mouse_click, mouse_wheel.
//
// Selector actions (click, hover, …) resolve an element first, which cannot
// reach into a cross-origin iframe without frame plumbing. These act on
// VIEWPORT coordinates through page.Mouse(), i.e. CDP Input.dispatchMouseEvent,
// so the browser hit-tests the point like a real pointer and the event lands
// in whatever is painted there, iframe or not.
//
// Movement is sampled at ~60 Hz along a curved, eased path whose length in
// time follows the distance at a speed picked per gesture; longer moves
// overshoot slightly and correct back. An optional overlay draws the pointer
// so the live panel shows the hand moving (headless browsers draw none).

import (
	"encoding/json"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/playwright-community/playwright-go"
	"github.com/yogasw/wick/pkg/connector"
)

const (
	maxMouseSteps   = 200
	maxClickDelayMS = 5000
	frameMS         = 16 // ~60 Hz
	// mousePosKey stores the last pointer position on the page's window, the
	// fallback when the plugin has no remembered position for the session.
	mousePosKey = "__wickMouse"
)

// lastPos remembers the pointer per live session for the life of the plugin
// process. A live session reconnects over CDP on every run call, and both
// Playwright's tracked position and the window variable can reset (a new
// connection, a navigation); this keeps the next path starting where the
// previous one ended instead of jumping.
var lastPos sync.Map // session id → point

// sessionViewport keeps the size set_viewport pinned per live session, so
// every later run re-applies it (see runActionsInContext).
var sessionViewport sync.Map // session id → [2]int{width, height}

// mouseEnv carries what mouse actions need beyond the page.
type mouseEnv struct {
	key     string    // live session id; "" for a throwaway browser
	cursor  bool      // draw the cursor overlay
	ripple  bool      // flash a ring where a click lands
	posFile string    // where the pointer position survives plugin restarts
	run     *runState // per-run baselines for wait_for_text fresh
	// hitCheck: confirm a frame_click reached its element. Moli hit-tests
	// without z-index, so a click on a dropdown can land on the page below.
	hitCheck bool
}

func newMouseEnv(c *connector.Ctx, sessionID string) mouseEnv {
	env := mouseEnv{
		key:      sessionID,
		cursor:   !strings.EqualFold(strings.TrimSpace(c.Cfg("show_cursor")), "false"),
		ripple:   c.CfgBool("cursor_click_ripple"),
		hitCheck: isMoliEngine(c.Cfg("browser")),
	}
	if sessionID != "" {
		env.posFile = pointerPath(sessionDir(c), sessionID)
	}
	return env
}

// pointerPath is where a session's pointer position is kept. Not .json: in
// the session dir that suffix marks session metadata, which the sweepers read.
func pointerPath(dir, id string) string { return filepath.Join(dir, id+".pointer") }

// rememberPos keeps the pointer position for the session: in memory for the
// next run, and on disk so a plugin restart (every redeploy) does not send
// the next move from the middle of the screen. Written once per gesture,
// not per frame.
func rememberPos(env mouseEnv, p point) {
	if env.key == "" {
		return
	}
	lastPos.Store(env.key, p)
	if env.posFile != "" {
		if b, err := json.Marshal(p); err == nil {
			_ = os.WriteFile(env.posFile, b, 0o644)
		}
	}
}

// loadPos reads the position saved by rememberPos.
func loadPos(env mouseEnv) (point, bool) {
	if env.posFile == "" {
		return point{}, false
	}
	b, err := os.ReadFile(env.posFile)
	if err != nil {
		return point{}, false
	}
	var p point
	if json.Unmarshal(b, &p) != nil {
		return point{}, false
	}
	return p, true
}

// refreshCursor puts the overlay back where the pointer is: after a fresh
// connection, a navigation or a reload the page has lost it, and the live
// panel would show no cursor until the next mouse action.
func refreshCursor(page playwright.Page, env mouseEnv) {
	if !env.cursor || env.key == "" {
		return
	}
	placeCursor(page, env, mousePos(page, env), false)
}

type point struct{ X, Y float64 }

func smoothstep(t float64) float64 { return t * t * (3 - 2*t) }

// autoSteps turns a distance into ~60 Hz frames at a speed picked per gesture
// (700–1600 px/s), bounded to 180–1400 ms.
func autoSteps(dist float64, rng *rand.Rand) int {
	speed := 700 + rng.Float64()*900
	ms := math.Max(180, math.Min(1400, dist/speed*1000))
	return max(4, min(int(ms/frameMS), maxMouseSteps))
}

// bezier samples a cubic Bézier from→to with sideways-bowed control points,
// eased, with sub-pixel jitter on the inner points. The last point is to.
func bezier(from, to point, steps int, bowFrac float64, rng *rand.Rand) []point {
	dx, dy := to.X-from.X, to.Y-from.Y
	dist := math.Hypot(dx, dy)
	if dist < 1 || steps < 1 {
		return []point{to}
	}
	nx, ny := -dy/dist, dx/dist
	bow := func() float64 { return (rng.Float64()*2 - 1) * dist * bowFrac }
	b1, b2 := bow(), bow()
	c1 := point{from.X + dx*0.3 + nx*b1, from.Y + dy*0.3 + ny*b1}
	c2 := point{from.X + dx*0.7 + nx*b2, from.Y + dy*0.7 + ny*b2}
	pts := make([]point, 0, steps)
	for i := 1; i <= steps; i++ {
		t := smoothstep(float64(i) / float64(steps))
		u := 1 - t
		p := point{
			X: u*u*u*from.X + 3*u*u*t*c1.X + 3*u*t*t*c2.X + t*t*t*to.X,
			Y: u*u*u*from.Y + 3*u*u*t*c1.Y + 3*u*t*t*c2.Y + t*t*t*to.Y,
		}
		if i < steps {
			j := 0.8 * (1 - t) // steadier as the hand lands
			p.X += (rng.Float64()*2 - 1) * j
			p.Y += (rng.Float64()*2 - 1) * j
		}
		pts = append(pts, p)
	}
	pts[len(pts)-1] = to
	return pts
}

// humanPath returns the points from `from` to `to`. steps <= 0 derives the
// count from distance and a random speed; moves over 120px usually overshoot
// by 3–7% and correct back. The last point is exactly `to`.
func humanPath(from, to point, steps int, rng *rand.Rand) []point {
	dist := math.Hypot(to.X-from.X, to.Y-from.Y)
	if dist < 1 {
		return []point{to}
	}
	if steps <= 0 {
		steps = autoSteps(dist, rng)
	}
	if dist <= 120 || steps < 10 || rng.Float64() >= 0.7 {
		return bezier(from, to, steps, 0.2, rng)
	}
	ux, uy := (to.X-from.X)/dist, (to.Y-from.Y)/dist
	over := dist * (0.03 + rng.Float64()*0.04)
	side := (rng.Float64()*2 - 1) * over * 0.5
	o := point{to.X + ux*over - uy*side, to.Y + uy*over + ux*side}
	back := max(4, steps/6)
	pts := bezier(from, o, steps-back, 0.2, rng)
	return append(pts, bezier(o, to, back, 0, rng)...)
}

func newRNG() *rand.Rand { return rand.New(rand.NewSource(time.Now().UnixNano())) }

// jitterSleep waits base..base+spread milliseconds.
func jitterSleep(rng *rand.Rand, base, spread int) {
	time.Sleep(time.Duration(base+rng.Intn(spread+1)) * time.Millisecond)
}

// mousePos is where the next path starts: the remembered position for the
// session, else the one stored on the page, else the viewport centre.
func mousePos(page playwright.Page, env mouseEnv) point {
	w, h := pageViewport(page)
	if p, ok := knownPos(page, env); ok {
		return clampPoint(p, w, h)
	}
	// Nothing known yet (a new session): start where a resting hand would
	// be, towards the lower right, not dead centre.
	p := restingPoint(w, h)
	rememberPos(env, p)
	return p
}

// knownPos is the last position: this process's memory, the session's
// pointer file, or the value the overlay left on the page.
func knownPos(page playwright.Page, env mouseEnv) (point, bool) {
	if env.key != "" {
		if v, ok := lastPos.Load(env.key); ok {
			return v.(point), true
		}
		if p, ok := loadPos(env); ok {
			lastPos.Store(env.key, p)
			return p, true
		}
	}
	if v, err := page.Evaluate(`() => window.` + mousePosKey + ` || null`); err == nil {
		if m, ok := v.(map[string]any); ok {
			x, okx := toFloat(m["x"])
			y, oky := toFloat(m["y"])
			if okx && oky {
				return point{x, y}, true
			}
		}
	}
	return point{}, false
}

// pageViewport is the page's CSS viewport. A page reached over CDP reports no
// (or a zero) ViewportSize, so the page itself is asked before falling back
// to a common laptop size.
func pageViewport(page playwright.Page) (float64, float64) {
	if vp := page.ViewportSize(); vp != nil && vp.Width > 0 && vp.Height > 0 {
		return float64(vp.Width), float64(vp.Height)
	}
	w, h := 1366.0, 768.0
	if v, err := page.Evaluate(`() => [innerWidth, innerHeight]`); err == nil {
		if a, ok := v.([]any); ok && len(a) == 2 {
			if x, ok := toFloat(a[0]); ok && x > 0 {
				w = x
			}
			if y, ok := toFloat(a[1]); ok && y > 0 {
				h = y
			}
		}
	}
	return w, h
}

// restingPoint is a natural idle spot in a w×h viewport: lower right.
func restingPoint(w, h float64) point {
	rng := newRNG()
	return point{w*0.8 + rng.Float64()*w*0.08, h*0.72 + rng.Float64()*h*0.1}
}

// clampPoint keeps p inside the viewport, e.g. after it shrank.
func clampPoint(p point, w, h float64) point {
	return point{math.Max(0, math.Min(p.X, w-1)), math.Max(0, math.Min(p.Y, h-1))}
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

// cursorJS stores the position on the page and, when draw is set, moves (and
// creates on first use, or after a navigation) the overlay: a standard arrow
// pointer, white with a black outline, its tip exactly on the point. Built
// with DOM calls only, no innerHTML, so pages enforcing Trusted Types accept
// it. ripple flashes a thin ring where a press lands.
const cursorJS = `(p) => {
  window.` + mousePosKey + ` = {x: p.x, y: p.y};
  if (!p.draw) return;
  const NS = 'http://www.w3.org/2000/svg';
  let c = document.getElementById('__wick_cursor');
  if (!c) {
    c = document.createElementNS(NS, 'svg');
    c.id = '__wick_cursor';
    c.setAttribute('width', '18');
    c.setAttribute('height', '24');
    c.setAttribute('viewBox', '0 0 18 24');
    c.style.cssText = 'position:fixed;left:0;top:0;pointer-events:none;z-index:2147483647;overflow:visible;' +
      'filter:drop-shadow(0 1px 1px rgba(0,0,0,.35));';
    const path = document.createElementNS(NS, 'path');
    path.setAttribute('d', 'M1 1 L1 18.5 L5.6 14.3 L8.6 21.4 L11.6 20.1 L8.7 13.2 L15 13.2 Z');
    path.setAttribute('fill', '#fff');
    path.setAttribute('stroke', '#000');
    path.setAttribute('stroke-width', '1.3');
    path.setAttribute('stroke-linejoin', 'round');
    c.appendChild(path);
    (document.body || document.documentElement).appendChild(c);
  }
  // the path's tip sits at (1,1) in the box, so shift by one pixel
  c.style.transform = 'translate(' + (p.x - 1) + 'px,' + (p.y - 1) + 'px)';
  if (p.down && p.ripple) {
    const r = document.createElement('div');
    r.style.cssText = 'position:fixed;left:' + (p.x - 12) + 'px;top:' + (p.y - 12) + 'px;width:24px;height:24px;' +
      'border-radius:50%;border:2px solid rgba(0,0,0,.45);pointer-events:none;z-index:2147483646;' +
      'transform:scale(.3);opacity:1;transition:transform .35s ease-out,opacity .35s ease-out;';
    (document.body || document.documentElement).appendChild(r);
    requestAnimationFrame(() => { r.style.transform = 'scale(1)'; r.style.opacity = '0'; });
    setTimeout(() => r.remove(), 400);
  }
}`

// placeCursor records p and repaints the overlay. Failures are ignored: the
// overlay is cosmetic and a page mid-navigation rejects evaluate.
func placeCursor(page playwright.Page, env mouseEnv, p point, down bool) {
	if env.key != "" {
		lastPos.Store(env.key, p)
	}
	_, _ = page.Evaluate(cursorJS, map[string]any{"x": p.X, "y": p.Y, "draw": env.cursor, "down": down, "ripple": env.ripple})
}

// humanMove glides the pointer to (x, y) along humanPath at ~60 Hz.
func humanMove(page playwright.Page, env mouseEnv, x, y float64, steps int) (any, error) {
	rng := newRNG()
	from := mousePos(page, env)
	to := point{x, y}
	pts := humanPath(from, to, steps, rng)
	mouse := page.Mouse()
	start := time.Now()
	for _, p := range pts {
		if err := mouse.Move(p.X, p.Y); err != nil {
			return nil, err
		}
		if env.cursor {
			placeCursor(page, env, p, false)
		}
		jitterSleep(rng, frameMS-3, 6)
	}
	placeCursor(page, env, to, false)
	rememberPos(env, to)
	return map[string]any{
		"x": x, "y": y,
		"from":   map[string]any{"x": from.X, "y": from.Y},
		"points": len(pts), "ms": time.Since(start).Milliseconds(),
	}, nil
}

// humanClick moves to (x, y), rests on the target for hover_dwell_ms (default
// 80–220ms), then presses and releases. delay_ms is the hold between down and
// up (default 60–140ms).
func humanClick(page playwright.Page, env mouseEnv, a action) (any, error) {
	if _, err := humanMove(page, env, *a.X, *a.Y, a.Steps); err != nil {
		return nil, err
	}
	rng := newRNG()
	dwell := a.HoverDwellMS
	if dwell == 0 {
		dwell = 80 + rng.Intn(141)
	}
	time.Sleep(time.Duration(dwell) * time.Millisecond)
	btn := mouseButton(a.Button)
	mouse := page.Mouse()
	at := point{*a.X, *a.Y}
	if err := mouse.Down(playwright.MouseDownOptions{Button: btn}); err != nil {
		return nil, err
	}
	if env.cursor {
		placeCursor(page, env, at, true)
	}
	hold := a.DelayMS
	if hold == 0 {
		hold = 60 + rng.Intn(81)
	}
	time.Sleep(time.Duration(hold) * time.Millisecond)
	if err := mouse.Up(playwright.MouseUpOptions{Button: btn}); err != nil {
		return nil, err
	}
	if env.cursor {
		placeCursor(page, env, at, false)
	}
	return map[string]any{"x": *a.X, "y": *a.Y, "button": string(*btn), "hover_dwell_ms": dwell, "hold_ms": hold}, nil
}

// wheelSlices splits a scroll into eased per-frame amounts: small at the start
// and end, larger in the middle, summing to the total.
func wheelSlices(total float64, steps int) []float64 {
	out := make([]float64, steps)
	prev := 0.0
	for i := 1; i <= steps; i++ {
		s := smoothstep(float64(i) / float64(steps))
		out[i-1] = total * (s - prev)
		prev = s
	}
	return out
}

// humanWheel scrolls by (dx, dy) as an eased ~60 Hz stream of small wheel
// events over 250–600ms (or `steps` frames when given).
func humanWheel(page playwright.Page, env mouseEnv, dx, dy float64, steps int) (any, error) {
	rng := newRNG()
	if steps <= 0 {
		steps = (250 + rng.Intn(351)) / frameMS
	}
	steps = max(1, min(steps, maxMouseSteps))
	// Wheel events fire at Playwright's tracked position, which a fresh CDP
	// connection resets to (0,0); put the pointer back where the hand is so
	// the scroll reaches the pane under it, not the top-left corner.
	at := mousePos(page, env)
	mouse := page.Mouse()
	if err := mouse.Move(at.X, at.Y); err != nil {
		return nil, err
	}
	if env.cursor {
		placeCursor(page, env, at, false)
	}
	xs, ys := wheelSlices(dx, steps), wheelSlices(dy, steps)
	for i := range steps {
		if err := mouse.Wheel(xs[i], ys[i]); err != nil {
			return nil, err
		}
		if i < steps-1 {
			jitterSleep(rng, frameMS-3, 6)
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
