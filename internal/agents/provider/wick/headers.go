package wick

import (
	"net/http"
	"strings"

	provider "github.com/yogasw/wick/internal/agents/provider"
)

// headers.go owns the per-model custom-header escape hatch.
//
// Why a separate knob from RawConfig: RawConfig unmarshals into
// genai.GenerateContentConfig, which is the request BODY. Headers live on
// the transport, so they need their own field — before this, the only
// headers a model call could send were the vendor auth pair hardcoded in
// each adapter.
//
// The stored form is one header per line. Parsing is deliberately lenient
// so an operator can paste a `curl` fragment straight out of a browser's
// "copy as cURL" or another client's debug log without hand-editing it:
//
//	--header 'X-Stainless-OS: Linux' \
//	-H "User-Agent: RooCode/3.53.0"
//	X-Org-Id: abc123
//
// all parse to the same thing. Custom headers are applied LAST and may
// override anything the adapter set, auth included — that's the point:
// a proxy fronting the vendor may want its own Authorization scheme, and
// spoofing a vendor SDK's client fingerprint means owning User-Agent.

// ParseHeaders turns the stored multi-line header blob into a map.
// Blank lines, `#` comments, and trailing `\` line-continuations are
// ignored. A line with no `:` is skipped rather than erroring — the field
// is a paste target, and one malformed line must not drop the rest.
//
// Later lines win on a duplicate key.
func ParseHeaders(raw string) map[string]string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	out := map[string]string{}
	for _, line := range strings.Split(raw, "\n") {
		k, v, ok := parseHeaderLine(line)
		if !ok {
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// parseHeaderLine extracts one (key, value) from a single line, stripping
// the curl flag, surrounding quotes, and a trailing continuation.
func parseHeaderLine(line string) (key, value string, ok bool) {
	s := strings.TrimSpace(line)
	s = strings.TrimSuffix(s, "\\") // curl line continuation
	s = strings.TrimSpace(s)
	if s == "" || strings.HasPrefix(s, "#") {
		return "", "", false
	}
	// Drop a leading curl header flag in any of its spellings.
	for _, flag := range []string{"--header=", "--header", "-H=", "-H"} {
		if rest, found := strings.CutPrefix(s, flag); found {
			s = strings.TrimSpace(rest)
			break
		}
	}
	s = unquote(s)
	k, v, found := strings.Cut(s, ":")
	if !found {
		return "", "", false
	}
	k = strings.TrimSpace(k)
	v = strings.TrimSpace(unquote(v))
	if k == "" {
		return "", "", false
	}
	return k, v, true
}

// unquote strips one matching pair of surrounding single or double quotes.
func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		if (s[0] == '\'' && s[len(s)-1] == '\'') || (s[0] == '"' && s[len(s)-1] == '"') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

// applyCustomHeaders overlays the model's custom headers onto the headers
// an adapter built. Custom wins — including over auth (see the package
// note above). Mutates and returns base for call-site convenience; a nil
// base with custom headers present gets a fresh map.
func applyCustomHeaders(base map[string]string, m provider.WickModel) map[string]string {
	custom := ParseHeaders(m.Headers)
	if len(custom) == 0 {
		return base
	}
	if base == nil {
		base = make(map[string]string, len(custom))
	}
	for k, v := range custom {
		base[k] = v
	}
	return base
}

// customHTTPHeader renders the model's custom headers as an http.Header,
// for the Gemini path — the genai SDK owns request construction, so wick
// hands it headers via genai.HTTPOptions instead of building the map.
// Returns nil when there are none, so HTTPOptions stays untouched.
func customHTTPHeader(m provider.WickModel) http.Header {
	custom := ParseHeaders(m.Headers)
	if len(custom) == 0 {
		return nil
	}
	h := make(http.Header, len(custom))
	for k, v := range custom {
		h.Set(k, v)
	}
	return h
}
