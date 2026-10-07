package login

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// Switching the theme must put you back on the page you were reading —
// the picker is in the navbar of every page, so landing on "/" throws
// away whatever you were looking at.
func TestThemeRedirectStaysOnThePage(t *testing.T) {
	post := func(form, referer string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/theme", strings.NewReader(url.Values{"redirect": {form}}.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Host = "wick.example"
		if referer != "" {
			r.Header.Set("Referer", referer)
		}
		return r
	}

	cases := []struct {
		name, form, referer, want string
	}{
		{"explicit redirect wins", "/tools/work-schedule", "https://wick.example/other", "/tools/work-schedule"},
		{"referer when the form says nothing", "", "https://wick.example/tools/work-schedule?tab=table", "/tools/work-schedule?tab=table"},
		{"no referer at all", "", "", "/"},
		{"another site cannot send us away", "", "https://evil.example/x", "/"},
		{"absolute url in the form is refused", "https://evil.example/x", "", "/"},
		{"protocol-relative is refused", "//evil.example/x", "", "/"},
		{"backslash trick is refused", "/\\evil.example", "", "/"},
		{"never bounce back into the POST endpoint", "/theme", "", "/"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := themeRedirect(post(c.form, c.referer)); got != c.want {
				t.Fatalf("themeRedirect = %q, want %q", got, c.want)
			}
		})
	}
}
