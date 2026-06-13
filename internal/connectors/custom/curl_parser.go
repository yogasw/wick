package custom

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// ParsedHeader is one extracted header with the parser's secret
// suggestion. The admin can override the toggle on the review screen.
type ParsedHeader struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Secret bool   `json:"secret"`
}

// ParsedRequest is the normalized output both parsers (cURL and AI)
// produce before field extraction. It deliberately mirrors a single
// HTTP call — one paste, one endpoint, one operation.
type ParsedRequest struct {
	Method      string         `json:"method"`
	URL         string         `json:"url"`
	Headers     []ParsedHeader `json:"headers"`
	Body        string         `json:"body,omitempty"`
	ContentType string         `json:"content_type,omitempty"`
	// SuggestedOpName lets the AI parser propose an op key; the cURL
	// parser leaves it empty and Extract derives one from method+path.
	SuggestedOpName string `json:"suggested_op_name,omitempty"`
}

// ── cURL tokenizer ───────────────────────────────────────────────────

// tokenizeCurl splits a pasted cURL command into shell words. Handles
// single quotes (literal), double quotes (backslash escapes), bare
// backslash escapes, and line continuations for bash (`\` + newline),
// PowerShell (backtick + newline), and cmd (`^` + newline).
func tokenizeCurl(s string) ([]string, error) {
	// Normalize line continuations first so the word scanner only sees
	// logical one-line input.
	s = strings.ReplaceAll(s, "\r\n", "\n")
	for _, cont := range []string{"\\\n", "`\n", "^\n"} {
		s = strings.ReplaceAll(s, cont, " ")
	}

	var (
		toks []string
		cur  strings.Builder
		in   = false // inside a word
		q    = rune(0)
	)
	flush := func() {
		if in {
			toks = append(toks, cur.String())
			cur.Reset()
			in = false
		}
	}
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case q == '\'':
			if r == '\'' {
				q = 0
			} else {
				cur.WriteRune(r)
			}
		case q == '"':
			if r == '"' {
				q = 0
			} else if r == '\\' && i+1 < len(runes) {
				i++
				cur.WriteRune(runes[i])
			} else {
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			q = r
			in = true
		case r == '\\' && i+1 < len(runes):
			i++
			cur.WriteRune(runes[i])
			in = true
		case r == ' ' || r == '\t' || r == '\n':
			flush()
		default:
			cur.WriteRune(r)
			in = true
		}
	}
	if q != 0 {
		return nil, fmt.Errorf("unterminated %c quote", q)
	}
	flush()
	return toks, nil
}

// flagsWithArg lists cURL flags whose next token is an argument we
// either consume or skip. Unknown flags without an entry are skipped
// alone, which keeps the parser tolerant of DevTools noise like
// --compressed.
var flagsWithArg = map[string]bool{
	"-X": true, "--request": true,
	"-H": true, "--header": true,
	"-d": true, "--data": true, "--data-raw": true, "--data-binary": true,
	"--data-ascii": true, "--data-urlencode": true,
	"-u": true, "--user": true,
	"--url": true,
	// Recognized-but-ignored (argument still consumed so the URL scan
	// doesn't mistake their values for the endpoint).
	"-o": true, "--output": true, "-A": true, "--user-agent": true,
	"-b": true, "--cookie": true, "-e": true, "--referer": true,
	"--connect-timeout": true, "--max-time": true, "-m": true,
	"--retry": true, "-w": true, "--write-out": true,
	"--cacert": true, "--cert": true, "--key": true,
}

// ParseCurl parses a pasted cURL command into a ParsedRequest. It
// supports the common DevTools "Copy as cURL" surface: -X/--request,
// -H/--header, -d/--data/--data-raw/--data-binary/--data-urlencode,
// -u/--user, --url, and the positional URL. Anything that is not a
// recognizable cURL invocation is an error — the AI-parser tab is the
// fallback for fetch()/axios/prose pastes.
func ParseCurl(input string) (*ParsedRequest, error) {
	toks, err := tokenizeCurl(strings.TrimSpace(input))
	if err != nil {
		return nil, err
	}
	if len(toks) == 0 || !strings.EqualFold(toks[0], "curl") {
		return nil, fmt.Errorf("not a cURL command — paste must start with `curl` (use the AI parser for other formats)")
	}

	p := &ParsedRequest{}
	var dataParts []string
	urlencodeMode := false
	explicitMethod := false

	for i := 1; i < len(toks); i++ {
		t := toks[i]
		arg := func() string {
			if i+1 < len(toks) {
				i++
				return toks[i]
			}
			return ""
		}
		switch {
		case t == "-X" || t == "--request":
			p.Method = strings.ToUpper(arg())
			explicitMethod = true
		case t == "-H" || t == "--header":
			h := arg()
			k, v, ok := strings.Cut(h, ":")
			if !ok {
				continue
			}
			p.Headers = append(p.Headers, ParsedHeader{
				Key:   strings.TrimSpace(k),
				Value: strings.TrimSpace(v),
			})
		case t == "-d" || t == "--data" || t == "--data-raw" || t == "--data-binary" || t == "--data-ascii":
			dataParts = append(dataParts, arg())
		case t == "--data-urlencode":
			dataParts = append(dataParts, arg())
			urlencodeMode = true
		case t == "-u" || t == "--user":
			cred := arg()
			p.Headers = append(p.Headers, ParsedHeader{
				Key:    "Authorization",
				Value:  "Basic " + base64.StdEncoding.EncodeToString([]byte(cred)),
				Secret: true,
			})
		case t == "--url":
			p.URL = arg()
		case strings.HasPrefix(t, "-"):
			if flagsWithArg[t] {
				_ = arg() // consume + discard
			}
			// bare flags (-s, -k, --compressed, ...) are ignored
		default:
			if p.URL == "" {
				p.URL = t
			}
		}
	}

	if p.URL == "" {
		return nil, fmt.Errorf("no URL found in the cURL command")
	}
	if !strings.HasPrefix(p.URL, "http://") && !strings.HasPrefix(p.URL, "https://") {
		p.URL = "https://" + p.URL
	}
	if len(dataParts) > 0 {
		joiner := "&"
		if !urlencodeMode && len(dataParts) == 1 && looksLikeJSON(dataParts[0]) {
			joiner = ""
		}
		p.Body = strings.Join(dataParts, joiner)
		if !explicitMethod {
			p.Method = "POST"
		}
	}
	if p.Method == "" {
		p.Method = "GET"
	}

	// Content type: explicit header wins, then body shape.
	for _, h := range p.Headers {
		if strings.EqualFold(h.Key, "Content-Type") {
			p.ContentType = h.Value
		}
	}
	if p.ContentType == "" && p.Body != "" {
		if looksLikeJSON(p.Body) {
			p.ContentType = "application/json"
		} else {
			p.ContentType = "application/x-www-form-urlencoded"
		}
	}

	markSecretHeaders(p.Headers)
	return p, nil
}

func looksLikeJSON(s string) bool {
	s = strings.TrimSpace(s)
	return (strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}")) ||
		(strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]"))
}

// ── secret heuristics ────────────────────────────────────────────────

var (
	secretHeaderKeyRe = regexp.MustCompile(`(?i)^(authorization|x-(api|auth|token|secret)-?key|x-api-token|api-?key|apikey|key|token|secret|cookie|x-access-token)$`)
	secretValueRe     = regexp.MustCompile(`(?i)^(bearer|basic|token)\s+\S+`)
	secretParamRe     = regexp.MustCompile(`(?i)(token|api_?key|password|secret|credential)`)
)

// markSecretHeaders applies the design's token-detection heuristic:
// header value matching `Bearer/Basic/Token <...>`, or a header key
// from the well-known credential family, flips the secret suggestion.
func markSecretHeaders(headers []ParsedHeader) {
	for i, h := range headers {
		if h.Secret {
			continue
		}
		if secretValueRe.MatchString(h.Value) || secretHeaderKeyRe.MatchString(h.Key) {
			headers[i].Secret = true
		}
	}
}

// ── field extraction (shared by both parsers) ────────────────────────

var numericSegRe = regexp.MustCompile(`^\d+$|^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// Extract splits a ParsedRequest into the review-form Draft: stable
// values (host, credentials) become Configs, per-call values (query
// params, body fields, trailing id-ish path segments) become Inputs,
// and the request templates reference them via {{.cfg.*}} / {{.in.*}}.
func Extract(p *ParsedRequest) (*Draft, error) {
	u, err := url.Parse(p.URL)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("invalid URL %q", p.URL)
	}

	d := &Draft{Source: "curl", Icon: "🔌"}
	d.Configs = append(d.Configs, DefField{
		Key:      "base_url",
		Label:    "Base URL",
		Widget:   "url",
		Required: true,
		Default:  u.Scheme + "://" + u.Host,
		Desc:     "API base URL. Example: " + u.Scheme + "://" + u.Host,
	})

	op := DefOp{
		Inputs: []DefField{},
		Request: &OpRequest{
			Method:      p.Method,
			Headers:     map[string]string{},
			ContentType: p.ContentType,
		},
	}

	// Path: keep literal segments; turn trailing numeric/uuid segments
	// into inputs named after the preceding segment.
	segs := strings.Split(strings.Trim(u.EscapedPath(), "/"), "/")
	var pathParts []string
	var nameParts []string
	for i, seg := range segs {
		if seg == "" {
			continue
		}
		if numericSegRe.MatchString(strings.ToLower(seg)) {
			name := "id"
			if i > 0 {
				name = singularize(toFieldKey(segs[i-1])) + "_id"
			}
			op.Inputs = append(op.Inputs, DefField{
				Key:      name,
				Widget:   "text",
				Required: true,
				Desc:     "Path segment. Example: " + seg,
			})
			pathParts = append(pathParts, "{{.in."+name+"}}")
			continue
		}
		pathParts = append(pathParts, seg)
		nameParts = append(nameParts, toFieldKey(seg))
	}
	urlTmpl := "{{.cfg.base_url}}"
	if len(pathParts) > 0 {
		urlTmpl += "/" + strings.Join(pathParts, "/")
	}

	// Query params → inputs.
	if rawQ := u.Query(); len(rawQ) > 0 {
		var qParts []string
		for _, k := range sortedKeys(rawQ) {
			v := rawQ.Get(k)
			fk := toFieldKey(k)
			op.Inputs = append(op.Inputs, DefField{
				Key:      fk,
				Widget:   "text",
				Secret:   secretParamRe.MatchString(k),
				Default:  v,
				Desc:     "Query parameter `" + k + "`. Example: " + v,
				Required: false,
			})
			qParts = append(qParts, k+"={{urlquery .in."+fk+"}}")
		}
		urlTmpl += "?" + strings.Join(qParts, "&")
	}
	op.Request.URLTemplate = urlTmpl

	// Headers: credential-looking values become secret configs; the
	// rest stay literal. Content-Type lives on Request.ContentType.
	for _, h := range p.Headers {
		if strings.EqualFold(h.Key, "Content-Type") {
			continue
		}
		if h.Secret {
			cfgKey, headerTmpl := credentialConfigFor(h)
			d.Configs = append(d.Configs, DefField{
				Key:      cfgKey,
				Label:    h.Key,
				Widget:   "secret",
				Secret:   true,
				Required: true,
				Default:  credentialValueOf(h),
				Desc:     "Value for the `" + h.Key + "` header. Stored encrypted.",
			})
			op.Request.Headers[h.Key] = headerTmpl
			continue
		}
		op.Request.Headers[h.Key] = h.Value
	}

	// Body → inputs + template.
	if p.Body != "" {
		if err := extractBody(p, &op); err != nil {
			return nil, err
		}
	}

	// Op naming.
	opKey := p.SuggestedOpName
	if opKey == "" {
		last := "root"
		if len(nameParts) > 0 {
			last = nameParts[len(nameParts)-1]
		}
		opKey = strings.ToLower(p.Method) + "_" + last
	}
	op.Key = toFieldKey(opKey)
	op.Name = humanize(op.Key)
	op.Description = p.Method + " " + u.Path + " on " + u.Host + ". Returns the upstream JSON response as-is."
	op.Destructive = p.Method == "DELETE"

	d.Key = toFieldKey(strings.TrimPrefix(u.Hostname(), "api."))
	d.Name = humanize(d.Key)
	d.Description = "Imported from cURL — " + p.Method + " " + u.Host + u.Path
	d.Ops = []DefOp{op}
	dedupeConfigs(d)
	return d, nil
}

// credentialConfigFor maps one secret header to (config key, header
// value template). "Authorization: Bearer X" keeps the scheme literal
// so the stored config is just the token.
func credentialConfigFor(h ParsedHeader) (cfgKey, headerTmpl string) {
	val := h.Value
	switch {
	case strings.HasPrefix(strings.ToLower(val), "bearer "):
		return "auth_value", "Bearer {{.cfg.auth_value}}"
	case strings.HasPrefix(strings.ToLower(val), "basic "):
		return "auth_basic", "Basic {{.cfg.auth_basic}}"
	default:
		k := toFieldKey(h.Key) + "_value"
		return k, "{{.cfg." + k + "}}"
	}
}

// credentialValueOf strips the auth scheme prefix so the seeded config
// value is the bare credential.
func credentialValueOf(h ParsedHeader) string {
	for _, prefix := range []string{"bearer ", "basic "} {
		if strings.HasPrefix(strings.ToLower(h.Value), prefix) {
			return strings.TrimSpace(h.Value[len(prefix):])
		}
	}
	return h.Value
}

// extractBody tokenizes the body into inputs + a body_template. JSON
// objects flatten one level: scalar values become typed inputs, nested
// arrays/objects become textarea inputs spliced in raw.
func extractBody(p *ParsedRequest, op *DefOp) error {
	if strings.Contains(p.ContentType, "json") || looksLikeJSON(p.Body) {
		var obj map[string]any
		if err := json.Unmarshal([]byte(p.Body), &obj); err != nil {
			// Non-object JSON (array / scalar) → single raw input.
			op.Inputs = append(op.Inputs, DefField{
				Key: "body", Widget: "textarea", Required: true,
				Desc:    "Raw JSON request body.",
				Default: p.Body,
			})
			op.Request.BodyTemplate = "{{.in.body}}"
			return nil
		}
		var parts []string
		for _, k := range sortedAnyKeys(obj) {
			v := obj[k]
			fk := toFieldKey(k)
			switch tv := v.(type) {
			case string:
				op.Inputs = append(op.Inputs, DefField{
					Key: fk, Widget: "text", Required: true,
					Secret:  secretParamRe.MatchString(k),
					Default: tv,
					Desc:    "Body field `" + k + "`. Example: " + tv,
				})
				parts = append(parts, fmt.Sprintf("%q: \"{{js .in.%s}}\"", k, fk))
			case float64, bool:
				op.Inputs = append(op.Inputs, DefField{
					Key: fk, Widget: widgetForScalar(v), Required: true,
					Default: fmt.Sprintf("%v", tv),
					Desc:    "Body field `" + k + "`. Example: " + fmt.Sprintf("%v", tv),
				})
				parts = append(parts, fmt.Sprintf("%q: {{.in.%s}}", k, fk))
			default: // nested object / array → raw JSON textarea
				raw, _ := json.Marshal(v)
				op.Inputs = append(op.Inputs, DefField{
					Key: fk, Widget: "textarea", Required: true,
					Default: string(raw),
					Desc:    "Body field `" + k + "` as raw JSON.",
				})
				parts = append(parts, fmt.Sprintf("%q: {{.in.%s}}", k, fk))
			}
		}
		op.Request.BodyTemplate = "{ " + strings.Join(parts, ", ") + " }"
		if op.Request.ContentType == "" {
			op.Request.ContentType = "application/json"
		}
		return nil
	}

	// Form-encoded k=v&k2=v2.
	pairs := strings.Split(p.Body, "&")
	var parts []string
	for _, pair := range pairs {
		k, v, ok := strings.Cut(pair, "=")
		if !ok || k == "" {
			continue
		}
		uv, err := url.QueryUnescape(v)
		if err != nil {
			uv = v
		}
		fk := toFieldKey(k)
		op.Inputs = append(op.Inputs, DefField{
			Key: fk, Widget: "text", Required: true,
			Secret:  secretParamRe.MatchString(k),
			Default: uv,
			Desc:    "Form field `" + k + "`. Example: " + uv,
		})
		parts = append(parts, k+"={{urlquery .in."+fk+"}}")
	}
	if len(parts) == 0 {
		op.Inputs = append(op.Inputs, DefField{
			Key: "body", Widget: "textarea", Required: true,
			Desc: "Raw request body.", Default: p.Body,
		})
		op.Request.BodyTemplate = "{{.in.body}}"
		return nil
	}
	op.Request.BodyTemplate = strings.Join(parts, "&")
	if op.Request.ContentType == "" {
		op.Request.ContentType = "application/x-www-form-urlencoded"
	}
	return nil
}

// dedupeConfigs drops duplicate config keys (two identical auth headers
// would otherwise collide) and inputs that shadow config keys.
func dedupeConfigs(d *Draft) {
	seen := map[string]bool{}
	cfgs := d.Configs[:0]
	for _, c := range d.Configs {
		if seen[c.Key] {
			continue
		}
		seen[c.Key] = true
		cfgs = append(cfgs, c)
	}
	d.Configs = cfgs
	for oi := range d.Ops {
		inSeen := map[string]bool{}
		ins := d.Ops[oi].Inputs[:0]
		for _, f := range d.Ops[oi].Inputs {
			if inSeen[f.Key] || seen[f.Key] {
				continue
			}
			inSeen[f.Key] = true
			ins = append(ins, f)
		}
		d.Ops[oi].Inputs = ins
	}
}

// ── small string helpers ─────────────────────────────────────────────

var nonFieldRe = regexp.MustCompile(`[^a-z0-9_]+`)

// toFieldKey normalizes an arbitrary identifier (header name, JSON key,
// path segment) into a snake_case field key.
func toFieldKey(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "-", "_")
	s = strings.ReplaceAll(s, ".", "_")
	s = nonFieldRe.ReplaceAllString(s, "_")
	s = strings.Trim(s, "_")
	if s == "" {
		return "field"
	}
	if s[0] >= '0' && s[0] <= '9' {
		s = "f_" + s
	}
	return s
}

func widgetForScalar(v any) string {
	switch v.(type) {
	case float64:
		return "number"
	case bool:
		return "checkbox"
	}
	return "text"
}

func singularize(s string) string {
	switch {
	case strings.HasSuffix(s, "ies"):
		return s[:len(s)-3] + "y"
	case strings.HasSuffix(s, "ses"):
		return s[:len(s)-2]
	case strings.HasSuffix(s, "s") && !strings.HasSuffix(s, "ss"):
		return s[:len(s)-1]
	}
	return s
}

func humanize(key string) string {
	words := strings.FieldsFunc(key, func(r rune) bool { return r == '_' || r == '-' })
	for i, w := range words {
		if len(w) > 0 {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
}

func sortedKeys(v url.Values) []string {
	out := make([]string, 0, len(v))
	for k := range v {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedAnyKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
