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

const scopeTextJS = `(sel) => {
  const el = sel ? document.querySelector(sel) : document.body;
  return el ? (el.innerText || '') : null;
}`

// scopeText reads the visible text of selector (or the body) in f.
func scopeText(f playwright.Frame, selector string) (string, error) {
	v, err := f.Evaluate(scopeTextJS, selector)
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

// waitForText polls until the text contains `text` or matches `pattern`.
func waitForText(page playwright.Page, a action) (any, error) {
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
	start := time.Now()
	deadline := start.Add(waitTimeout(a.TimeoutMS))
	for {
		txt, err := scopeText(f, strings.TrimSpace(a.Selector))
		if err == nil {
			for _, l := range textLines(txt) {
				if (re != nil && re.MatchString(l)) || (re == nil && strings.Contains(l, a.Text)) {
					return map[string]any{"matched": true, "line": l, "frame_url": f.URL(), "waited_ms": time.Since(start).Milliseconds()}, nil
				}
			}
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("text not seen within %dms", time.Since(start).Milliseconds())
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
	base, err := scopeText(f, sel)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	deadline := start.Add(waitTimeout(a.TimeoutMS))
	for {
		time.Sleep(pollEvery)
		now, err := scopeText(f, sel)
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
	last, err := scopeText(f, sel)
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
		now, err := scopeText(f, sel)
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
