package main

// frame_eval, frame_text, frame_click: read and act inside an iframe,
// cross-origin included. Playwright drives every frame of the page through
// its own execution context, so a WA Flow (or any embedded widget) can be read
// as DOM rather than guessed from a screenshot. Coordinates returned by
// frame_text are page-viewport pixels, ready for mouse_click.

import (
	"fmt"
	"math"
	"strings"

	"github.com/playwright-community/playwright-go"
)

// pickFrame resolves the action's frame: frame_index into page.Frames()
// (0 = main), else the first frame whose URL contains frame_url_contains,
// else the first child frame.
func pickFrame(page playwright.Page, a action) (playwright.Frame, error) {
	frames := page.Frames()
	if a.FrameIndex != nil {
		i := *a.FrameIndex
		if i < 0 || i >= len(frames) {
			return nil, fmt.Errorf("frame_index %d out of range (%d frames): %s", i, len(frames), frameURLs(frames))
		}
		return frames[i], nil
	}
	if want := strings.TrimSpace(a.FrameURLContains); want != "" {
		for _, f := range frames {
			if strings.Contains(f.URL(), want) {
				return f, nil
			}
		}
		return nil, fmt.Errorf("no frame URL contains %q: %s", want, frameURLs(frames))
	}
	if len(frames) < 2 {
		return nil, fmt.Errorf("page has no iframe; give frame_index 0 to act on the main page")
	}
	return frames[1], nil
}

// frameURLs lists frames for an error message, so the caller can pick one.
func frameURLs(frames []playwright.Frame) string {
	parts := make([]string, 0, len(frames))
	for i, f := range frames {
		u := f.URL()
		if len(u) > 100 {
			u = u[:100] + "…"
		}
		parts = append(parts, fmt.Sprintf("[%d] %s", i, u))
	}
	return strings.Join(parts, " ")
}

// frameOffset maps frame-local CSS pixels to page-viewport pixels: the
// iframe element's box (Playwright reports it relative to the main viewport,
// nesting included) plus the scale between that box and the frame's own
// viewport, for iframes shrunk or zoomed by CSS.
func frameOffset(f playwright.Frame, innerW, innerH float64) (ox, oy, sx, sy float64) {
	sx, sy = 1, 1
	if f.ParentFrame() == nil {
		return 0, 0, sx, sy
	}
	el, err := f.FrameElement()
	if err != nil {
		return 0, 0, sx, sy
	}
	box, err := el.BoundingBox()
	if err != nil || box == nil {
		return 0, 0, sx, sy
	}
	if innerW > 0 {
		sx = box.Width / innerW
	}
	if innerH > 0 {
		sy = box.Height / innerH
	}
	return box.X, box.Y, sx, sy
}

func frameEval(page playwright.Page, a action) (any, error) {
	f, err := pickFrame(page, a)
	if err != nil {
		return nil, err
	}
	val, err := f.Evaluate(a.Script)
	if err != nil {
		return nil, err
	}
	if str, ok := val.(string); ok {
		return clip(str), nil
	}
	return val, nil
}

// frameTextJS collects the frame's text and its visible interactive elements
// with frame-local boxes.
const frameTextJS = `(sel) => {
  const scope = sel ? document.querySelector(sel) : document.body;
  if (!scope) return {error: 'selector not found: ' + sel};
  const q = 'button,a[href],input,select,textarea,[role=button],[role=link],[role=checkbox],' +
    '[role=radio],[role=option],[role=combobox],[role=listbox],[role=menuitem],[role=tab],' +
    '[role=switch],[contenteditable=true],[onclick],[tabindex]:not([tabindex="-1"])';
  const items = [];
  for (const el of scope.querySelectorAll(q)) {
    const r = el.getBoundingClientRect();
    if (r.width < 1 || r.height < 1) continue;
    const st = getComputedStyle(el);
    if (st.visibility === 'hidden' || st.display === 'none' || st.opacity === '0') continue;
    const text = (el.innerText || el.getAttribute('aria-label') || el.getAttribute('placeholder') ||
      el.getAttribute('title') || '').trim().replace(/\s+/g, ' ').slice(0, 120);
    const it = {tag: el.tagName.toLowerCase(), role: el.getAttribute('role') || '', text,
      x: r.left, y: r.top, w: r.width, h: r.height};
    if ('value' in el && el.value !== undefined && el.value !== '') it.value = String(el.value).slice(0, 120);
    if (el.type) it.type = el.type;
    if (el.checked || el.getAttribute('aria-checked') === 'true' || el.getAttribute('aria-selected') === 'true') it.checked = true;
    if (el.disabled || el.getAttribute('aria-disabled') === 'true') it.disabled = true;
    if (r.bottom < 0 || r.top > innerHeight || r.right < 0 || r.left > innerWidth) it.offscreen = true;
    items.push(it);
    if (items.length >= 150) break;
  }
  return {url: location.href, text: (scope.innerText || '').slice(0, 6000), items, vw: innerWidth, vh: innerHeight};
}`

func frameText(page playwright.Page, a action) (any, error) {
	f, err := pickFrame(page, a)
	if err != nil {
		return nil, err
	}
	val, err := f.Evaluate(frameTextJS, strings.TrimSpace(a.Selector))
	if err != nil {
		return nil, err
	}
	m, ok := val.(map[string]any)
	if !ok {
		return val, nil
	}
	if e, ok := m["error"]; ok {
		return nil, fmt.Errorf("%v", e)
	}
	vw, _ := toFloat(m["vw"])
	vh, _ := toFloat(m["vh"])
	ox, oy, sx, sy := frameOffset(f, vw, vh)
	if items, ok := m["items"].([]any); ok {
		for _, raw := range items {
			it, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			x, _ := toFloat(it["x"])
			y, _ := toFloat(it["y"])
			w, _ := toFloat(it["w"])
			h, _ := toFloat(it["h"])
			px, py, pw, ph := ox+x*sx, oy+y*sy, w*sx, h*sy
			it["x"], it["y"], it["w"], it["h"] = round1(px), round1(py), round1(pw), round1(ph)
			it["cx"], it["cy"] = round1(px+pw/2), round1(py+ph/2)
		}
	}
	delete(m, "vw")
	delete(m, "vh")
	m["text"] = clip(fmt.Sprint(m["text"]))
	return m, nil
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }

// frameClick finds the element by selector or visible text, scrolls it into
// view, and clicks it with the natural mouse at a random point in the middle
// half of its box (not dead centre every time, like a hand).
func frameClick(page playwright.Page, env mouseEnv, a action) (any, error) {
	f, err := pickFrame(page, a)
	if err != nil {
		return nil, err
	}
	var loc playwright.Locator
	if sel := strings.TrimSpace(a.Selector); sel != "" {
		loc = f.Locator(sel).First()
	} else {
		// Exact text first: a substring match lands on the first label that
		// merely mentions the word ("Pilih X" heading before the "X" button).
		loc = f.GetByText(a.Text, playwright.FrameGetByTextOptions{Exact: playwright.Bool(true)}).First()
		if n, err := loc.Count(); err != nil || n == 0 {
			loc = f.GetByText(a.Text).First()
		}
	}
	if err := loc.ScrollIntoViewIfNeeded(); err != nil {
		return nil, fmt.Errorf("find element: %w", err)
	}
	box, err := loc.BoundingBox()
	if err != nil {
		return nil, fmt.Errorf("element box: %w", err)
	}
	if box == nil || box.Width < 1 || box.Height < 1 {
		return nil, fmt.Errorf("element is not visible")
	}
	rng := newRNG()
	x := box.X + box.Width*(0.25+rng.Float64()*0.5)
	y := box.Y + box.Height*(0.25+rng.Float64()*0.5)
	click := a
	click.X, click.Y = &x, &y
	out, err := humanClick(page, env, click)
	if err != nil {
		return nil, err
	}
	res, _ := out.(map[string]any)
	if res == nil {
		res = map[string]any{}
	}
	res["box"] = map[string]any{"x": round1(box.X), "y": round1(box.Y), "w": round1(box.Width), "h": round1(box.Height)}
	return res, nil
}
