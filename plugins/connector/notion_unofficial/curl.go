package main

import (
	"html"
	"net/url"
	"regexp"
	"strings"

	"github.com/yogasw/wick/pkg/connector"
)

// curlCreds is what we pull out of a pasted "Copy as cURL" from DevTools. Every
// field is optional — the parser fills whatever it finds.
type curlCreds struct {
	TokenV2       string
	UserAgent     string
	ClientVersion string
	ActiveUser    string
	SpaceID       string
}

// header/flag matchers. curl quotes with either ' or " depending on the shell
// the "Copy as cURL" was taken from, so we accept both. Go's RE2 has no
// backreferences, so single- and double-quoted forms are separate alternatives;
// the captured group is whichever matched.
var (
	reHeader = regexp.MustCompile(`(?:-H|--header)\s+(?:'([^']*)'|"([^"]*)")`)
	reCookie = regexp.MustCompile(`(?:-b|--cookie)\s+(?:'([^']*)'|"([^"]*)")`)
)

// capValue returns the non-empty capture group from a match produced by the
// single-or-double-quote alternation above (group 1 = ', group 2 = ").
func capValue(m []string) string {
	if len(m) > 1 && m[1] != "" {
		return m[1]
	}
	if len(m) > 2 {
		return m[2]
	}
	return ""
}

// parseCurl extracts Notion credentials from a pasted curl command. It reads the
// cookie jar (for token_v2 and, as a fallback, notion_user_id) and the relevant
// headers (user-agent, notion-client-version, x-notion-active-user-header,
// x-notion-space-id). Values are trimmed; token_v2 is URL-decoded (the cookie
// stores it percent-encoded, e.g. v03%3A...). Returns zero-value fields for
// anything absent, so callers should treat empty as "not provided".
func parseCurl(raw string) curlCreds {
	var out curlCreds
	if strings.TrimSpace(raw) == "" {
		return out
	}

	// Headers.
	for _, m := range reHeader.FindAllStringSubmatch(raw, -1) {
		k, v, ok := splitHeader(capValue(m))
		if !ok {
			continue
		}
		switch strings.ToLower(k) {
		case "user-agent":
			out.UserAgent = v
		case "notion-client-version":
			out.ClientVersion = v
		case "x-notion-active-user-header":
			out.ActiveUser = v
		case "x-notion-space-id":
			out.SpaceID = v
		case "cookie":
			applyCookies(&out, v)
		}
	}

	// -b / --cookie jar (separate from a "cookie:" header).
	for _, m := range reCookie.FindAllStringSubmatch(raw, -1) {
		applyCookies(&out, capValue(m))
	}

	return out
}

// --- html widget ops (import_form + import_curl_extract) ---

// importForm renders the paste-a-cURL textarea + Extract button. The textarea's
// name="raw" is picked up by the core html widget and sent to import_curl_extract
// on click; data-op names the extract op.
func importForm(c *connector.Ctx) (any, error) {
	return map[string]any{"html": importFormHTML("")}, nil
}

// importExtract parses the pasted cURL (sent as "raw") and returns the config
// fields to write plus a feedback line. The core html widget applies `fields`
// to the sibling config inputs and renders `html` in place.
func importExtract(c *connector.Ctx) (any, error) {
	raw := strings.TrimSpace(c.Input("raw"))
	if raw == "" {
		return map[string]any{"html": importFormHTML("Paste a cURL first, then click Extract.")}, nil
	}
	creds := parseCurl(raw)
	fields := map[string]string{}
	if creds.TokenV2 != "" {
		fields["token_v2"] = creds.TokenV2
	}
	if creds.UserAgent != "" {
		fields["user_agent"] = creds.UserAgent
	}
	if creds.ClientVersion != "" {
		fields["notion_client_version"] = creds.ClientVersion
	}
	if creds.ActiveUser != "" {
		fields["active_user_id"] = creds.ActiveUser
	}

	if len(fields) == 0 || fields["token_v2"] == "" {
		return map[string]any{
			"html": importFormHTML("Couldn't find a token_v2 cookie in that cURL. Copy it as cURL from a logged-in notion.so request."),
		}, nil
	}

	// Feedback: list which fields were filled (never echo secret values).
	filled := make([]string, 0, len(fields))
	for _, k := range []string{"token_v2", "user_agent", "notion_client_version", "active_user_id"} {
		if _, ok := fields[k]; ok {
			filled = append(filled, k)
		}
	}
	msg := "Extracted: " + strings.Join(filled, ", ") + ". Fields below are filled — click Save."
	return map[string]any{
		"fields": fields,
		"html":   importFormHTML("✓ " + msg),
	}, nil
}

// importFormHTML builds the widget markup: a textarea (name=raw) + Extract
// button (data-op=import_curl_extract), with an optional feedback note.
//
// Styling uses inline `style` with the theme's CSS variables
// (var(--color-...)) rather than Tailwind utility classes. Plugin-returned HTML
// is not scanned by the manager's Tailwind build, so utility classes that
// aren't already used in a templ source get purged and render unstyled (that's
// why the textarea looked theme-broken). CSS variables resolve at runtime and
// swap with the active theme, so this is theme-aware AND purge-proof.
func importFormHTML(note string) string {
	const (
		border  = "var(--color-white-400)"
		surface = "var(--color-white-100)"
		text    = "var(--color-black-900)"
		muted   = "var(--color-black-700)"
		accent  = "#27B199" // green-500, fixed across themes
	)
	// The dark variants come from the same vars — they already flip per theme —
	// so one set of vars covers both modes.

	taStyle := "width:100%;box-sizing:border-box;border-radius:12px;border:1px solid " + border +
		";background:" + surface + ";color:" + text + ";padding:8px 12px;font-size:12px;" +
		"font-family:ui-monospace,SFMono-Regular,Menlo,monospace;resize:vertical;min-height:88px;"

	btnStyle := "border:none;border-radius:8px;background:" + accent + ";color:#fff;" +
		"padding:8px 16px;font-size:12px;font-weight:600;cursor:pointer;"

	noteHTML := ""
	if note != "" {
		color := muted
		if strings.HasPrefix(note, "✓") {
			color = "#288C7A" // pos-400
		} else {
			color = "#EB5757" // neg-400
		}
		noteHTML = `<p style="margin-top:8px;font-size:12px;color:` + color + `;">` + html.EscapeString(note) + `</p>`
	}

	return `<div style="display:flex;flex-direction:column;gap:8px;">` +
		`<textarea name="raw" rows="4" placeholder="Paste a Copy-as-cURL of any notion.so/api/v3 request…" style="` + taStyle + `"></textarea>` +
		`<div style="display:flex;align-items:center;gap:8px;">` +
		`<button type="button" data-op="import_curl_extract" data-arg="" style="` + btnStyle + `">Extract</button>` +
		`<span style="font-size:11px;color:` + muted + `;">Fills token_v2 + headers from the pasted request.</span>` +
		`</div>` +
		noteHTML +
		`</div>`
}

// splitHeader splits "Key: Value" into its parts.
func splitHeader(h string) (key, value string, ok bool) {
	i := strings.IndexByte(h, ':')
	if i < 0 {
		return "", "", false
	}
	return strings.TrimSpace(h[:i]), strings.TrimSpace(h[i+1:]), true
}

// applyCookies scans a cookie jar string ("a=1; b=2; token_v2=…") and fills
// token_v2 (URL-decoded) and, if no active user seen yet, notion_user_id.
func applyCookies(out *curlCreds, jar string) {
	for _, part := range strings.Split(jar, ";") {
		part = strings.TrimSpace(part)
		eq := strings.IndexByte(part, '=')
		if eq < 0 {
			continue
		}
		name := strings.TrimSpace(part[:eq])
		val := strings.TrimSpace(part[eq+1:])
		switch name {
		case "token_v2":
			if dec, err := url.QueryUnescape(val); err == nil {
				out.TokenV2 = dec
			} else {
				out.TokenV2 = val
			}
		case "notion_user_id":
			if out.ActiveUser == "" {
				out.ActiveUser = val
			}
		}
	}
}
