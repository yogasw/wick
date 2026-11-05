package main

// Screenshot options shared by the screenshot op and the run action: a clip
// rectangle, and jpeg output with a quality knob.
//
// clip is the way to frame something inside a cross-origin iframe (a WA Flow
// date picker, a summary screen): a selector cannot reach into the frame, but
// a rectangle of the page can.

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

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

// checkSaveDir accepts an empty value, or an absolute directory under the app
// data tree (~/.<app>/...): agents' working folders live there, and the plugin
// must not become a way to drop files anywhere on the host. The directory may
// not exist yet (shotResult creates it); its nearest existing ancestor is what
// gets checked, after resolving symlinks.
func checkSaveDir(dir string) error {
	if dir == "" {
		return nil
	}
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("save_dir must be an absolute path")
	}
	clean := filepath.Clean(dir)
	existing := clean
	for {
		if _, err := os.Stat(existing); err == nil {
			break
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return fmt.Errorf("save_dir %q has no existing parent", dir)
		}
		existing = parent
	}
	real, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return fmt.Errorf("save_dir: %w", err)
	}
	if st, err := os.Stat(real); err != nil || !st.IsDir() {
		return fmt.Errorf("save_dir %q is not a directory", dir)
	}
	if root := appDataRoot(); root == "" || !strings.HasPrefix(real+string(os.PathSeparator), root+string(os.PathSeparator)) {
		return fmt.Errorf("save_dir must be inside the app data tree")
	}
	return nil
}

// appDataRoot is the app tree this plugin runs under: the parent of the
// plugins dir that holds its persistent data.
func appDataRoot() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	exe, _ = filepath.EvalSymlinks(exe)
	// <root>/plugins/connectors/<key>/<binary>
	return filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(exe))))
}

// shotResult returns the image as base64, or writes it into saveDir and
// returns the path.
func shotResult(raw []byte, format, saveDir string) (map[string]any, error) {
	if saveDir == "" {
		return map[string]any{"format": format, "image": base64.StdEncoding.EncodeToString(raw)}, nil
	}
	ext := ".png"
	if format == "jpeg" {
		ext = ".jpg"
	}
	if err := os.MkdirAll(saveDir, 0o755); err != nil {
		return nil, fmt.Errorf("create save_dir: %w", err)
	}
	path := filepath.Join(saveDir, fmt.Sprintf("shot-%s%s", time.Now().Format("20060102-150405.000"), ext))
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return nil, fmt.Errorf("save screenshot: %w", err)
	}
	return map[string]any{"format": format, "path": path, "bytes": len(raw)}, nil
}
