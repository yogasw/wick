package main

// Screenshot options shared by the screenshot op and the run action: a clip
// rectangle, and jpeg output with a quality knob.
//
// clip is the way to frame something inside a cross-origin iframe (a WA Flow
// date picker, a summary screen): a selector cannot reach into the frame, but
// a rectangle of the page can.

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/playwright-community/playwright-go"
)

type clipRect struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// shotOpts is what a screenshot request may set beyond selector/full_page.
type shotOpts struct {
	Clip    *clipRect
	Format  string // png (default) | jpeg
	Quality int    // jpeg only, 1-100; 0 = Playwright default
}

// parseClip decodes the op's clip input: a JSON object {x,y,width,height}.
// Empty means no clip.
func parseClip(raw string) (*clipRect, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var r clipRect
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		return nil, fmt.Errorf("clip must be a JSON object {\"x\":..,\"y\":..,\"width\":..,\"height\":..}: %w", err)
	}
	return &r, nil
}

// validate checks the options against each other and the selector.
func (o shotOpts) validate(selector string) error {
	if o.Clip != nil {
		if selector != "" {
			return fmt.Errorf("clip and selector cannot be combined: pick one")
		}
		if o.Clip.X < 0 || o.Clip.Y < 0 || o.Clip.Width <= 0 || o.Clip.Height <= 0 {
			return fmt.Errorf("clip needs x,y >= 0 and width,height > 0")
		}
	}
	switch o.Format {
	case "", "png":
		if o.Quality != 0 {
			return fmt.Errorf("quality applies to type=jpeg only")
		}
	case "jpeg":
		if o.Quality < 0 || o.Quality > 100 {
			return fmt.Errorf("quality must be between 1 and 100")
		}
	default:
		return fmt.Errorf("type must be png or jpeg")
	}
	return nil
}

func (o shotOpts) format() string {
	if o.Format == "jpeg" {
		return "jpeg"
	}
	return "png"
}

func (o shotOpts) pwType() *playwright.ScreenshotType {
	if o.Format == "jpeg" {
		return playwright.ScreenshotTypeJpeg
	}
	return playwright.ScreenshotTypePng
}

func (o shotOpts) pwQuality() *int {
	if o.Format == "jpeg" && o.Quality > 0 {
		return playwright.Int(o.Quality)
	}
	return nil
}

// takeShot captures one element (selector), a clipped rectangle, the viewport
// or the full page, in the requested format.
func takeShot(page playwright.Page, selector string, fullPage bool, o shotOpts) ([]byte, error) {
	if selector != "" {
		el, err := page.WaitForSelector(selector)
		if err != nil {
			return nil, fmt.Errorf("selector %q: %w", selector, err)
		}
		return el.Screenshot(playwright.ElementHandleScreenshotOptions{Type: o.pwType(), Quality: o.pwQuality()})
	}
	opts := playwright.PageScreenshotOptions{
		FullPage: playwright.Bool(fullPage),
		Type:     o.pwType(),
		Quality:  o.pwQuality(),
	}
	if o.Clip != nil {
		opts.Clip = &playwright.Rect{X: o.Clip.X, Y: o.Clip.Y, Width: o.Clip.Width, Height: o.Clip.Height}
	}
	return page.Screenshot(opts)
}
