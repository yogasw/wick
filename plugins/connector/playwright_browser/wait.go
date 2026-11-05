package main

// wait_for_text, wait_for_change, observe: wait on what the page shows instead
// of a fixed sleep followed by a screenshot. Each polls the frame's text from
// the plugin side (a cheap evaluate every pollEvery), which works the same in
// the main page and in any iframe frame_* can reach, and returns only what
// changed.

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/playwright-community/playwright-go"
)

const (
	pollEvery        = 250 * time.Millisecond
	defaultWaitMS    = 10000
	maxWaitMS        = 40000 // stays under the 45s op deadline
	defaultStableMS  = 600
	maxChangedLines  = 60
	maxChangedItems  = 60
	observeTextLimit = 20000
)

// obsSnap remembers what observe last returned per session, frame and scope,
// so the next call reports only the difference.
var obsSnap sync.Map // key → obsState

type obsState struct {
	lines map[string]bool
	items map[string]bool
}

// waitFrame is the frame a wait/observe action reads: the one named by
// frame_url_contains / frame_index, else the main page.
func waitFrame(page playwright.Page, a action) (playwright.Frame, error) {
	a = a.withFrameAlias()
	if a.FrameIndex == nil && strings.TrimSpace(a.FrameURLContains) == "" {
		return page.MainFrame(), nil
	}
	return pickFrame(page, a)
}

func waitTimeout(ms int) time.Duration {
	if ms <= 0 {
		ms = defaultWaitMS
	}
	return time.Duration(min(ms, maxWaitMS)) * time.Millisecond
}

// scopeTextJS returns the visible text of the scope; with values, the current
// value of every input, textarea and select in it follows, one per line
// ("<label>: <value>"), since innerText never includes what a field holds.
const scopeTextJS = `(o) => {
  const el = o.sel ? document.querySelector(o.sel) : document.body;
  if (!el) return null;
  let t = el.innerText || '';
  if (o.values) {
    for (const f of el.querySelectorAll('input,textarea,select')) {
      if (f.type === 'password' || f.type === 'hidden') continue;
      const v = f.tagName === 'SELECT' ? (f.selectedOptions[0] || {}).text || f.value : f.value;
      if (!v) continue;
      const label = f.getAttribute('aria-label') || f.getAttribute('placeholder') || f.name || f.id || f.tagName.toLowerCase();
      t += '\n' + label + ': ' + v;
    }
  }
  return t;
}`

// scopeText reads the visible text of selector (or the body) in f, plus the
// field values when values is set.
func scopeText(f playwright.Frame, selector string, values bool) (string, error) {
	v, err := f.Evaluate(scopeTextJS, map[string]any{"sel": selector, "values": values})
	if err != nil {
		return "", err
	}
	if v == nil {
		return "", fmt.Errorf("selector not found: %s", selector)
	}
	s, _ := v.(string)
	if len(s) > observeTextLimit {
		s = s[:observeTextLimit]
	}
	return s, nil
}

func textLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// lineDiff lists lines of next missing from prev (added) and the reverse.
func lineDiff(prev, next []string) (added, removed []string) {
	in := func(list []string) map[string]int {
		m := map[string]int{}
		for _, l := range list {
			m[l]++
		}
		return m
	}
	p, n := in(prev), in(next)
	for _, l := range next {
		if p[l] > 0 {
			p[l]--
			continue
		}
		added = append(added, l)
	}
	for _, l := range prev {
		if n[l] > 0 {
			n[l]--
			continue
		}
		removed = append(removed, l)
	}
	return capList(added, maxChangedLines), capList(removed, maxChangedLines)
}

func capList(l []string, n int) []string {
	if len(l) > n {
		return l[:n]
	}
	return l
}

// runState carries what wait_for_text fresh compares against within one run:
// the text of every frame at the start of the run, and again just before each
// action that can change the page (a click, typing, a navigation, a script).
// A step that waits for a bot reply usually follows the click that triggered
// it in the same run, and the reply may already be on screen when the wait
// starts; measuring "new" from the moment the wait starts would call it old.
type runState struct {
	start         map[string]string // frame URL → normalized text, run start
	beforeTrigger map[string]string // frame URL → normalized text, before the last trigger
}

// triggerActions are the actions that can make the page show something new.
var triggerActions = map[string]bool{
	"goto": true, "go_back": true, "go_forward": true, "reload": true,
	"click": true, "dblclick": true, "tap": true, "fill": true, "type": true, "press": true,
	"check": true, "uncheck": true, "select_option": true, "set_input_files": true,
	"drag_and_drop": true, "eval": true, "mouse_click": true,
	"frame_click": true, "frame_type": true, "frame_fill": true, "frame_eval": true,
}

// snapshotFrames reads every frame's text (field values included), normalized.
func snapshotFrames(page playwright.Page) map[string]string {
	out := map[string]string{}
	frames, _ := listFrames(page)
	for _, f := range frames {
		if txt, err := scopeText(f, "", true); err == nil {
			out[f.URL()] += normSpace(txt)
		}
	}
	return out
}

// observeTrigger records the baselines: the first call (run start) fills both,
// later ones refresh beforeTrigger ahead of each trigger action.
func (r *runState) observeTrigger(page playwright.Page, a action, first bool) {
	if r == nil || (!first && !triggerActions[a.Action]) {
		return
	}
	snap := snapshotFrames(page)
	if first {
		r.start = snap
	}
	r.beforeTrigger = snap
}

// normSpace collapses every run of whitespace (newlines included) to one
// space, so a long heading split over lines still matches its text.
func normSpace(s string) string { return strings.Join(strings.Fields(s), " ") }

// countMatches counts occurrences of the wanted text (or regex) in norm and
// returns the last one with a little context.
func countMatches(norm, want string, re *regexp.Regexp) (int, string) {
	if re != nil {
		all := re.FindAllStringIndex(norm, -1)
		if len(all) == 0 {
			return 0, ""
		}
		last := all[len(all)-1]
		return len(all), norm[last[0]:last[1]]
	}
	if want == "" {
		return 0, ""
	}
	n := strings.Count(norm, want)
	if n == 0 {
		return 0, ""
	}
	i := strings.LastIndex(norm, want)
	lo, hi := max(0, i-40), min(len(norm), i+len(want)+40)
	return n, norm[lo:hi]
}

// waitForText polls until the text contains `text` or matches `pattern`,
// both compared with whitespace and newlines collapsed. With fresh, only an
// occurrence beyond those in the baseline counts; the baseline is chosen by
// `baseline`: "trigger" (default) = the frame just before the last
// page-changing action in this run (the run start when there was none),
// "run_start", or "call_start" = when this step begins. A fresh wait counts
// over the whole frame, since the baseline is taken over the whole frame.
func waitForText(page playwright.Page, env mouseEnv, a action) (any, error) {
	f, err := waitFrame(page, a)
	if err != nil {
		return nil, err
	}
	var re *regexp.Regexp
	if a.Pattern != "" {
		if re, err = regexp.Compile(a.Pattern); err != nil {
			return nil, fmt.Errorf("pattern: %w", err)
		}
	}
	want := normSpace(a.Text)
	sel := strings.TrimSpace(a.Selector)
	values := a.IncludeValues
	if a.Fresh {
		sel, values = "", true // compare like for like with the snapshot
	}
	read := func() (string, error) {
		txt, err := scopeText(f, sel, values)
		return normSpace(txt), err
	}
	base := 0
	baseline := "none"
	if a.Fresh {
		baseline = a.Baseline
		if baseline == "" {
			baseline = "trigger"
		}
		var snap map[string]string
		switch {
		case baseline == "call_start" || env.run == nil:
			baseline = "call_start"
			txt, _ := read()
			snap = map[string]string{f.URL(): txt}
		case baseline == "run_start":
			snap = env.run.start
		default:
			snap = env.run.beforeTrigger
		}
		base, _ = countMatches(snap[f.URL()], want, re)
	}
	start := time.Now()
	deadline := start.Add(waitTimeout(a.TimeoutMS))
	for {
		if txt, err := read(); err == nil {
			if n, hit := countMatches(txt, want, re); n > base {
				return map[string]any{"matched": true, "line": hit, "baseline": baseline, "frame_url": f.URL(), "waited_ms": time.Since(start).Milliseconds()}, nil
			}
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("text not seen within %dms (baseline %s, %d earlier occurrence(s) ignored)", time.Since(start).Milliseconds(), baseline, base)
		}
		time.Sleep(pollEvery)
	}
}

// waitForChange polls until the text differs from what it was when the step
// started, then returns the added and removed lines.
func waitForChange(page playwright.Page, a action) (any, error) {
	f, err := waitFrame(page, a)
	if err != nil {
		return nil, err
	}
	sel := strings.TrimSpace(a.Selector)
	base, err := scopeText(f, sel, a.IncludeValues)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	deadline := start.Add(waitTimeout(a.TimeoutMS))
	for {
		time.Sleep(pollEvery)
		now, err := scopeText(f, sel, a.IncludeValues)
		if err == nil && now != base {
			added, removed := lineDiff(textLines(base), textLines(now))
			return map[string]any{"changed": true, "added": added, "removed": removed, "frame_url": f.URL(), "waited_ms": time.Since(start).Milliseconds()}, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("no change within %dms", time.Since(start).Milliseconds())
		}
	}
}

// observe waits until the scope's text has been still for until_stable_ms
// (the screen finished rendering), then returns only what changed since the
// previous observe on the same session, frame and scope: added/removed text
// lines and the interactive elements (with page-viewport boxes) that are new
// or changed. The first call returns everything.
func observe(page playwright.Page, env mouseEnv, a action) (any, error) {
	f, err := waitFrame(page, a)
	if err != nil {
		return nil, err
	}
	sel := strings.TrimSpace(a.Selector)
	stable := time.Duration(a.UntilStableMS) * time.Millisecond
	if stable <= 0 {
		stable = defaultStableMS * time.Millisecond
	}
	start := time.Now()
	deadline := start.Add(waitTimeout(a.TimeoutMS))
	last, err := scopeText(f, sel, a.IncludeValues)
	if err != nil {
		return nil, err
	}
	still := time.Now()
	settled := true
	for time.Since(still) < stable {
		if time.Now().After(deadline) {
			settled = false
			break
		}
		time.Sleep(pollEvery)
		now, err := scopeText(f, sel, a.IncludeValues)
		if err != nil {
			return nil, err
		}
		if now != last {
			last, still = now, time.Now()
		}
	}

	full, err := frameText(page, frameAction(a, f, page))
	if err != nil {
		return nil, err
	}
	m, _ := full.(map[string]any)
	key := env.key + "|" + f.URL() + "|" + sel
	cur := obsState{lines: map[string]bool{}, items: map[string]bool{}}
	lines := textLines(last)
	for _, l := range lines {
		cur.lines[l] = true
	}
	var items []any
	if raw, ok := m["items"].([]any); ok {
		items = raw
	}
	prevAny, had := obsSnap.Load(key)
	prev, _ := prevAny.(obsState)
	var added, removed []string
	var changed []any
	for _, l := range lines {
		if !had || !prev.lines[l] {
			added = append(added, l)
		}
	}
	if had {
		for l := range prev.lines {
			if !cur.lines[l] {
				removed = append(removed, l)
			}
		}
	}
	for _, it := range items {
		sig := fmt.Sprint(it)
		cur.items[sig] = true
		if !had || !prev.items[sig] {
			changed = append(changed, it)
		}
	}
	obsSnap.Store(key, cur)
	if len(changed) > maxChangedItems {
		changed = changed[:maxChangedItems]
	}
	return trimView(map[string]any{
		"frame_url": f.URL(),
		"first":     !had,
		"settled":   settled,
		"waited_ms": time.Since(start).Milliseconds(),
		"url":       m["url"],
		"added":     capList(added, maxChangedLines),
		"removed":   capList(removed, maxChangedLines),
		"items":     changed,
	}, a), nil
}

// frameAction points frameText at the frame already resolved for observe:
// the main page as index 0, or the child by its position in listFrames.
func frameAction(a action, f playwright.Frame, page playwright.Page) action {
	out := a
	frames, _ := listFrames(page)
	for i, fr := range frames {
		if fr == f {
			idx := i
			out.FrameIndex = &idx
			out.FrameURLContains = ""
			return out
		}
	}
	return out
}

// needsBaseline reports whether any step waits with fresh, so runs that do
// not pay for snapshots they would never read.
func needsBaseline(actions []action) bool {
	for _, a := range actions {
		if a.Action == "wait_for_text" && a.Fresh {
			return true
		}
	}
	return false
}
