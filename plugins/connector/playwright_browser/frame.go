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

// pickFrame resolves the action's frame: frame_index into the frame list
// (0 = main), else the first frame whose URL (or iframe src) contains
// frame_url_contains, else the first child frame.
func pickFrame(page playwright.Page, a action) (playwright.Frame, error) {
	a = a.withFrameAlias()
	frames, srcs := listFrames(page)
	if a.FrameIndex != nil {
		i := *a.FrameIndex
		if i < 0 || i >= len(frames) {
			return nil, fmt.Errorf("frame_index %d out of range (%d frames): %s", i, len(frames), frameURLs(frames, srcs))
		}
		return frames[i], nil
	}
	if want := strings.TrimSpace(a.FrameURLContains); want != "" {
		// Child frames first: the main page's URL can carry the same words
		// (a data: page, a query string naming the widget), and the caller
		// asking by URL almost always means an iframe.
		for i := 1; i < len(frames); i++ {
			if strings.Contains(frames[i].URL(), want) || strings.Contains(srcs[i], want) {
				return frames[i], nil
			}
		}
		if strings.Contains(frames[0].URL(), want) {
			return frames[0], nil
		}
		return nil, fmt.Errorf("no frame URL contains %q: %s", want, frameURLs(frames, srcs))
	}
	if len(frames) < 2 {
		return nil, fmt.Errorf("page has no iframe; give frame_index 0 to act on the main page")
	}
	return frames[1], nil
}

// listFrames returns the main frame first, then every child frame, with the
// src attribute of the iframe element each came from ("" when unknown).
//
// Children are resolved from the <iframe> elements of the main document
// (ElementHandle.ContentFrame) before falling back to page.Frames(): on a
// live session reconnected over CDP, Frames() can keep the frame objects of
// the page as it was before a navigation in the same run, and acting on one
// of those fails with "target closed" / "frame was detached". The main frame
// comes from MainFrame(), which page.Evaluate itself uses.
func listFrames(page playwright.Page) ([]playwright.Frame, []string) {
	main := page.MainFrame()
	frames := []playwright.Frame{main}
	srcs := []string{""}
	seen := map[playwright.Frame]bool{main: true}
	add := func(f playwright.Frame, src string) {
		if f == nil || seen[f] || f.IsDetached() {
			return
		}
		seen[f] = true
		frames = append(frames, f)
		srcs = append(srcs, src)
	}
	if els, err := main.QuerySelectorAll("iframe, frame"); err == nil {
		for _, el := range els {
			src, _ := el.GetAttribute("src")
			f, err := el.ContentFrame()
			if err == nil {
				add(f, src)
			}
		}
	}
	for _, f := range page.Frames() {
		add(f, "")
	}
	return frames, srcs
}

// frameURLs lists frames for an error message, so the caller can pick one.
func frameURLs(frames []playwright.Frame, srcs []string) string {
	parts := make([]string, 0, len(frames))
	for i, f := range frames {
		u := f.URL()
		if u == "" || u == "about:blank" {
			if srcs[i] != "" {
				u = srcs[i]
			}
		}
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
  const controls = 'button,a[href],input,select,textarea,[role=button],[role=link],[role=checkbox],' +
    '[role=radio],[role=option],[role=combobox],[role=listbox],[role=menuitem],[role=tab],' +
    '[role=switch],[contenteditable=true],[onclick],[tabindex]:not([tabindex="-1"])';
  // Plus small elements named only by a title: icon controls such as a WA
  // Flow header's Back arrow (<div title="Back">), which have no role.
  const q = controls + ',[title]:not(iframe):not(abbr)';
  const items = [];
  for (const el of scope.querySelectorAll(q)) {
    const r = el.getBoundingClientRect();
    if (r.width < 1 || r.height < 1) continue;
    const st = getComputedStyle(el);
    if (st.visibility === 'hidden' || st.display === 'none' || st.opacity === '0') continue;
    if (!el.matches(controls) && (r.width > 80 || r.height > 80)) continue;
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

// trimView applies the size knobs shared by frame_text and observe:
// only_interactive drops the text, compact keeps the fields an agent clicks
// with, max_items caps the list.
func trimView(m map[string]any, a action) map[string]any {
	if a.OnlyInteractive {
		delete(m, "text")
		delete(m, "added")
		delete(m, "removed")
	}
	items, _ := m["items"].([]any)
	if a.Compact {
		for i, raw := range items {
			it, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			c := map[string]any{"text": it["text"], "cx": it["cx"], "cy": it["cy"]}
			for _, k := range []string{"value", "disabled", "checked", "offscreen"} {
				if v, ok := it[k]; ok {
					c[k] = v
				}
			}
			if tag, _ := it["tag"].(string); tag == "input" || tag == "select" || tag == "textarea" {
				c["tag"] = tag
			}
			items[i] = c
		}
	}
	if a.MaxItems > 0 && len(items) > a.MaxItems {
		items = items[:a.MaxItems]
		m["truncated"] = true
	}
	if items != nil {
		m["items"] = items
	}
	return m
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }

// locateInFrame resolves the action's element in f: a CSS/Playwright
// selector, or visible text (exact first, then substring). Only VISIBLE
// matches count, so a hidden twin earlier in the DOM (a picker's template
// button, an offscreen copy) is skipped.
func locateInFrame(f playwright.Frame, a action) playwright.Locator {
	return locate(f, a, " >> visible=true").First()
}

// rawLocator is locateInFrame without the visibility filter: the candidates
// boxFromDOM measures itself when Playwright's actionability check gives up.
func rawLocator(f playwright.Frame, a action) playwright.Locator {
	return locate(f, a, "")
}

func locate(f playwright.Frame, a action, filter string) playwright.Locator {
	if sel := strings.TrimSpace(a.Selector); sel != "" {
		return f.Locator(sel + filter)
	}
	q := strings.ReplaceAll(a.Text, `"`, `\"`)
	loc := f.Locator(`text="` + q + `"` + filter)
	if n, err := loc.Count(); err == nil && n > 0 {
		return loc
	}
	loc = f.Locator(`text=` + q + filter)
	if n, err := loc.Count(); err == nil && n > 0 {
		return loc
	}
	// Icon controls carry their name in aria-label or title, not in text: a
	// WA Flow header's back arrow is <div title="Back">, so text "Back" must
	// reach it too.
	return f.Locator(`[aria-label="` + q + `"], [title="` + q + `"]` + filter)
}

// findBox returns the page-viewport box to click for the action's element.
// It first asks Playwright for the visible match (scrolled into view, ~1.5s
// in total). When that is not actionable in time (a zero-size anchor around
// its label, an overlay on top, a control that keeps animating), it measures
// the matching elements itself (see boxFromDOM) and clicks the first one that
// is painted, so no coordinates have to be passed.
func findBox(f playwright.Frame, a action) (*playwright.Rect, string, error) {
	loc := locateInFrame(f, a)
	_ = loc.ScrollIntoViewIfNeeded(playwright.LocatorScrollIntoViewIfNeededOptions{Timeout: playwright.Float(1000)})
	box, err := loc.BoundingBox(playwright.LocatorBoundingBoxOptions{Timeout: playwright.Float(500)})
	if err == nil && box != nil && box.Width >= 1 && box.Height >= 1 {
		return box, "", nil
	}
	b, derr := boxFromDOM(f, rawLocator(f, a), "")
	if derr == nil {
		return b, "dom_box", nil
	}
	// a:text-is('OK') only matches the DEEPEST element holding the text, so
	// it finds nothing when the label sits in a <span> inside the <a>. Retry
	// as :has-text and keep only elements whose whole text is exactly it.
	if sel, want, ok := textIsAsHasText(a.Selector); ok {
		b, err2 := boxFromDOM(f, f.Locator(sel), want)
		if err2 == nil {
			return b, "dom_box_has_text", nil
		}
		derr = fmt.Errorf("%v; as %s: %v", derr, sel, err2)
	}
	if err == nil {
		return nil, "", fmt.Errorf("element not visible; DOM fallback: %v", derr)
	}
	return nil, "", fmt.Errorf("element not actionable (%v); DOM fallback: %v", err, derr)
}

// textIsAsHasText rewrites the first :text-is('X') in sel to :has-text('X')
// and returns X, for the exact-text check boxFromDOM then applies.
func textIsAsHasText(sel string) (string, string, bool) {
	i := strings.Index(sel, ":text-is(")
	if i < 0 {
		return "", "", false
	}
	rest := sel[i+len(":text-is("):]
	j := strings.Index(rest, ")")
	if j < 0 {
		return "", "", false
	}
	want := strings.Trim(strings.TrimSpace(rest[:j]), `'"`)
	return sel[:i] + ":has-text(" + rest[:j] + ")" + rest[j+1:], want, true
}

// boxJS scrolls an element to the middle of its frame and returns its box,
// or, when the element itself has no size (an inline wrapper whose children
// are positioned), the union of its painted descendants. Frame-local CSS
// pixels plus the frame viewport, for frameOffset.
const boxJS = `(el, want) => {
  if (want && (el.innerText || el.textContent || '').trim().replace(/\s+/g, ' ') !== want) return null;
  const st = getComputedStyle(el);
  if (st.display === 'none' || st.visibility === 'hidden') return null;
  el.scrollIntoView({block: 'center', inline: 'center'});
  let r = el.getBoundingClientRect();
  if (r.width < 1 || r.height < 1) {
    let x1 = Infinity, y1 = Infinity, x2 = -Infinity, y2 = -Infinity;
    for (const c of el.querySelectorAll('*')) {
      const q = c.getBoundingClientRect();
      if (q.width < 1 || q.height < 1) continue;
      x1 = Math.min(x1, q.left); y1 = Math.min(y1, q.top);
      x2 = Math.max(x2, q.right); y2 = Math.max(y2, q.bottom);
    }
    if (x1 === Infinity) return null;
    r = {left: x1, top: y1, width: x2 - x1, height: y2 - y1};
  }
  return {x: r.left, y: r.top, w: r.width, h: r.height, vw: innerWidth, vh: innerHeight};
}`

// boxFromDOM measures up to ten matches and returns the first painted one,
// mapped to page-viewport pixels.
// want, when set, keeps only elements whose whole text equals it.
func boxFromDOM(f playwright.Frame, raw playwright.Locator, want string) (*playwright.Rect, error) {
	// Element handles, not raw.Nth(i).Evaluate: the locator path waits on
	// the same resolution that just timed out, the handles are already there.
	els, err := raw.ElementHandles()
	if err != nil {
		return nil, err
	}
	n := len(els)
	if n == 0 {
		return nil, fmt.Errorf("no element matches")
	}
	defer func() {
		for _, el := range els {
			_ = el.Dispose()
		}
	}()
	var lastErr error
	for i := 0; i < min(n, 10); i++ {
		v, err := els[i].Evaluate(boxJS, want)
		if err != nil {
			lastErr = err
			continue
		}
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		x, _ := toFloat(m["x"])
		y, _ := toFloat(m["y"])
		w, _ := toFloat(m["w"])
		h, _ := toFloat(m["h"])
		vw, _ := toFloat(m["vw"])
		vh, _ := toFloat(m["vh"])
		if w < 1 || h < 1 {
			continue
		}
		ox, oy, sx, sy := frameOffset(f, vw, vh)
		return &playwright.Rect{X: ox + x*sx, Y: oy + y*sy, Width: w * sx, Height: h * sy}, nil
	}
	if lastErr != nil {
		return nil, fmt.Errorf("%d matches, none measurable: %w", n, lastErr)
	}
	return nil, fmt.Errorf("%d matches, none painted", n)
}

// clickBox clicks a random point in the middle half of box with the natural
// mouse (not dead centre every time, like a hand).
func clickBox(page playwright.Page, env mouseEnv, a action, box *playwright.Rect) (map[string]any, error) {
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

// frameClick finds the element by selector or visible text and clicks it
// with the natural mouse.
func frameClick(page playwright.Page, env mouseEnv, a action) (any, error) {
	f, err := pickFrame(page, a)
	if err != nil {
		return nil, err
	}
	box, via, err := findBox(f, a)
	if err != nil {
		return nil, err
	}
	res, err := clickBox(page, env, a, box)
	if err == nil && via != "" {
		res["fallback"] = via
	}
	return res, err
}

// frameType clicks an input in the frame like a hand, then types value one
// key at a time with a random per-character delay. The keys go through the
// page keyboard (CDP Input.dispatchKeyEvent), which the browser routes to
// the focused element, inside the iframe included. clear (frame_fill) empties
// the field first. When the field still does not hold the value afterwards
// (a widget that swallowed the keys), the element's own keyboard path is
// used as a fallback.
func frameType(page playwright.Page, env mouseEnv, a action, clear bool) (any, error) {
	f, err := pickFrame(page, a)
	if err != nil {
		return nil, err
	}
	loc := locateInFrame(f, a)
	box, via, err := findBox(f, a)
	if err != nil {
		return nil, err
	}
	res, err := clickBox(page, env, a, box)
	if err != nil {
		return nil, err
	}
	if via != "" {
		res["fallback"] = via
	}
	if clear {
		if err := loc.Fill(""); err != nil {
			return nil, fmt.Errorf("clear field: %w", err)
		}
	}
	lo, hi := 60, 180
	if len(a.PerCharDelayMS) == 2 {
		lo, hi = a.PerCharDelayMS[0], a.PerCharDelayMS[1]
	}
	rng := newRNG()
	kb := page.Keyboard()
	for _, r := range a.Value {
		if err := kb.Type(string(r)); err != nil {
			return nil, err
		}
		jitterSleep(rng, lo, hi-lo)
	}
	got, verr := loc.InputValue(playwright.LocatorInputValueOptions{Timeout: playwright.Float(1000)})
	if verr == nil && !strings.Contains(got, a.Value) {
		_ = loc.Fill("")
		if err := loc.PressSequentially(a.Value, playwright.LocatorPressSequentiallyOptions{Delay: playwright.Float(float64(lo+hi) / 2)}); err != nil {
			return nil, fmt.Errorf("type into field: %w", err)
		}
		got, verr = loc.InputValue(playwright.LocatorInputValueOptions{Timeout: playwright.Float(1000)})
		res["fallback"] = "press_sequentially"
	}
	if verr == nil {
		res["value"] = clip(got)
	}
	res["typed"] = len([]rune(a.Value))
	return res, nil
}
