package ui

import "context"

// Theme represents a selectable UI theme. The ClassName is the CSS
// class applied to <html> that drives the color palette overrides in
// web/src/input.css. IsDark marks themes that should also receive the
// "dark" class so Tailwind's `dark:` variants activate.
type Theme struct {
	ID        string // stored in user metadata
	Label     string // shown in UI
	ClassName string // applied to <html>
	IsDark    bool
}

// Themes is the ordered list rendered in the theme picker. Add new
// themes by appending here and defining their tokens in input.css.
var Themes = []Theme{
	{ID: "light", Label: "Light", ClassName: "theme-light", IsDark: false},
	{ID: "dark", Label: "Dark", ClassName: "theme-dark", IsDark: true},
	{ID: "github-light", Label: "GitHub Light", ClassName: "theme-github-light", IsDark: false},
	{ID: "github-dark", Label: "GitHub Dark", ClassName: "theme-github-dark", IsDark: true},
	{ID: "material-light", Label: "Material Light", ClassName: "theme-material-light", IsDark: false},
	{ID: "material-dark", Label: "Material Dark", ClassName: "theme-material-dark", IsDark: true},
	{ID: "solarized-light", Label: "Solarized Light", ClassName: "theme-solarized-light", IsDark: false},
	{ID: "solarized-dark", Label: "Solarized Dark", ClassName: "theme-solarized-dark", IsDark: true},
	{ID: "dracula", Label: "Dracula", ClassName: "theme-dracula", IsDark: true},
	{ID: "nord", Label: "Nord", ClassName: "theme-nord", IsDark: true},
	{ID: "gruvbox-dark", Label: "Gruvbox Dark", ClassName: "theme-gruvbox-dark", IsDark: true},
	{ID: "monokai", Label: "Monokai", ClassName: "theme-monokai", IsDark: true},
}

// ThemeByID returns the theme for the given ID or the first matching
// default. Unknown IDs fall back to an empty Theme which callers should
// treat as "use system preference".
func ThemeByID(id string) Theme {
	for _, t := range Themes {
		if t.ID == id {
			return t
		}
	}
	return Theme{}
}

// ── App name (configurable display name) ─────────────────────

type appNameCtxKey struct{}

// WithAppName stores the configurable app name in ctx so templates
// can render it without an explicit parameter.
func WithAppName(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, appNameCtxKey{}, name)
}

// AppNameFromContext returns the app name set via WithAppName, or
// "Wick Mini Tools" as the fallback.
func AppNameFromContext(ctx context.Context) string {
	v, _ := ctx.Value(appNameCtxKey{}).(string)
	if v == "" {
		return "Wick Mini Tools"
	}
	return v
}

// ── App description (configurable tagline) ───────────────────

type appDescCtxKey struct{}

func WithAppDescription(ctx context.Context, desc string) context.Context {
	return context.WithValue(ctx, appDescCtxKey{}, desc)
}

func AppDescFromContext(ctx context.Context) string {
	v, _ := ctx.Value(appDescCtxKey{}).(string)
	return v
}

// ── Theme ────────────────────────────────────────────────────

type themeCtxKey struct{}

// WithTheme stores the resolved theme id in ctx. Empty string means
// "no preference" and the layout falls back to the system-preference
// script.
func WithTheme(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, themeCtxKey{}, id)
}

// ThemeFromContext reads the theme id previously set via WithTheme.
func ThemeFromContext(ctx context.Context) string {
	v, _ := ctx.Value(themeCtxKey{}).(string)
	return v
}

// GuestTheme holds the three theme preferences stored in the plain guest cookie.
// Current is the active theme; Light and Dark remember the last picked
// theme of each mode so the toggle button can cycle back to them.
type GuestTheme struct {
	Current string
	Light   string
	Dark    string
}

type guestThemeCtxKey struct{}

// WithGuestTheme stores guest theme prefs in ctx (populated by Session middleware).
func WithGuestTheme(ctx context.Context, g GuestTheme) context.Context {
	return context.WithValue(ctx, guestThemeCtxKey{}, g)
}

// GuestThemeFromContext returns guest theme prefs set via WithGuestTheme.
func GuestThemeFromContext(ctx context.Context) GuestTheme {
	g, _ := ctx.Value(guestThemeCtxKey{}).(GuestTheme)
	return g
}
