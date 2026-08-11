package config

import (
	"fmt"
	"net/url"
	"strings"
)

// Widget CSP policy.
//
// HTML artifacts are model-authored, so they render in an iframe that is
// sandboxed WITHOUT allow-same-origin (opaque origin: no access to the
// parent's cookies, storage, or DOM) and carries a CSP that closes every
// exfiltration channel. That posture is the default and stays the default.
//
// ONE knob decides the posture: Mode. It is deliberately not a set of
// independent switches, because the common answers are "sealed" and "let
// it through", and making an operator assemble either one out of five
// separate fields invites a half-configured policy nobody can read off a
// screen.
//
//	PresetSecure   — everything blocked. The default.
//	PresetUnsecure — everything open. The per-directive fields are IGNORED.
//	PresetCustom   — and only then are the per-directive fields read.
//
// Every relaxation is a real widening of what untrusted HTML can reach,
// which is why it lives in config an operator must edit rather than in
// code.

// Preset values for WidgetPolicy.Mode — the single knob that decides
// whether the per-directive fields are consulted at all.
const (
	// PresetSecure blocks every configurable directive and popups,
	// regardless of what the per-directive fields hold. Default, and what
	// an empty or unrecognised value resolves to.
	PresetSecure = "secure"
	// PresetUnsecure opens every configurable directive to any HTTPS host
	// and allows popups, regardless of the per-directive fields.
	//
	// This includes script-src, so a widget may load and run code from any
	// host. Combined with an open connect-src, such a script can read
	// whatever the widget holds — including anything handed to it over the
	// file and data-table bridges — and send it anywhere. The opaque origin
	// still keeps it out of the parent's cookies, storage, and DOM. Fit for
	// trusted internal projects; PresetCustom exists for everything else.
	PresetUnsecure = "unsecure"
	// PresetCustom reads the per-directive fields as set.
	PresetCustom = "custom"
)

// Mode values for one configurable CSP directive under PresetCustom.
const (
	// ModeBlock emits 'none' — the directive allows nothing. Default.
	ModeBlock = "block"
	// ModeList emits the resolved allowlist — named hosts only.
	ModeList = "list"
	// ModeAll emits https: — any host over TLS.
	ModeAll = "all"
)

// WidgetPolicy is the effective CSP policy for HTML-artifact iframes.
// It is also the persisted per-project override shape (project.Meta),
// where Override reports whether the project opts out of the global
// policy at all.
type WidgetPolicy struct {
	// Override is meaningful only on a project's stored policy: false
	// means "inherit the global policy", and every other field is
	// ignored. It carries no meaning on a resolved policy.
	Override bool `json:"override,omitempty"`

	// Mode is the preset — see Preset*. It overrides every field below
	// unless it is PresetCustom. Empty resolves to PresetSecure.
	Mode string `json:"mode,omitempty"`

	// The per-directive fields below are read ONLY under PresetCustom.
	// Resolve() rewrites them to match the preset otherwise, so a resolved
	// policy always reads the same way regardless of which preset produced
	// it — the renderer never has to know about presets.
	FrameSrc   string `json:"frame_src,omitempty"`
	ImgSrc     string `json:"img_src,omitempty"`
	MediaSrc   string `json:"media_src,omitempty"`
	ConnectSrc string `json:"connect_src,omitempty"`

	// ScriptSrc governs EXTERNAL scripts only. Inline scripts always run —
	// that is what keeps a widget interactive — so ModeBlock here still
	// emits 'unsafe-inline', it just adds no hosts. Under PresetCustom this
	// stays ModeBlock unless an operator deliberately changes it.
	ScriptSrc string `json:"script_src,omitempty"`

	// AllowPopups adds allow-popups to the iframe's sandbox attribute,
	// which is what makes target="_blank" and window.open work. A popup
	// is NOT constrained by the parent's CSP, so this is a full
	// exfiltration channel to any host — independent of the allowlist.
	AllowPopups bool `json:"allow_popups,omitempty"`

	// Allowlist holds normalised host sources (https://host[:port],
	// optionally *.host). Consulted only by directives set to ModeList.
	Allowlist []string `json:"allowlist,omitempty"`
}

// normalizePreset maps a stored Mode onto a known preset. Anything
// unrecognised — including the empty string written by rows that predate
// this field — fails closed to PresetSecure.
func normalizePreset(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case PresetUnsecure:
		return PresetUnsecure
	case PresetCustom:
		return PresetCustom
	default:
		return PresetSecure
	}
}

// widgetDirective describes one configurable CSP directive: where its
// value lives on WidgetPolicy, which config key holds it, and what the
// UI calls it.
//
// Everything that walks the directives — preset expansion, global config
// projection, per-key validation, the API payload — drives off THIS table.
// Adding a directive later means adding one row here plus the struct field
// and the config knob it names; nothing else has to be edited directive by
// directive.
type widgetDirective struct {
	// Field returns a pointer to this directive's field on a policy, so the
	// table can both read and write it.
	Field func(*WidgetPolicy) *string
	// ConfigKey is its key in the configs table (owner "agents").
	ConfigKey string
	// GlobalValue pulls its value out of the flat GeneralConfig.
	GlobalValue func(GeneralConfig) string
}

var widgetDirectives = []widgetDirective{
	{
		Field:       func(p *WidgetPolicy) *string { return &p.FrameSrc },
		ConfigKey:   "widget_frame_src",
		GlobalValue: func(g GeneralConfig) string { return g.WidgetFrameSrc },
	},
	{
		Field:       func(p *WidgetPolicy) *string { return &p.ImgSrc },
		ConfigKey:   "widget_img_src",
		GlobalValue: func(g GeneralConfig) string { return g.WidgetImgSrc },
	},
	{
		Field:       func(p *WidgetPolicy) *string { return &p.MediaSrc },
		ConfigKey:   "widget_media_src",
		GlobalValue: func(g GeneralConfig) string { return g.WidgetMediaSrc },
	},
	{
		Field:       func(p *WidgetPolicy) *string { return &p.ConnectSrc },
		ConfigKey:   "widget_connect_src",
		GlobalValue: func(g GeneralConfig) string { return g.WidgetConnectSrc },
	},
	{
		Field:       func(p *WidgetPolicy) *string { return &p.ScriptSrc },
		ConfigKey:   "widget_script_src",
		GlobalValue: func(g GeneralConfig) string { return g.WidgetScriptSrc },
	},
}

// WidgetDirectiveKeys returns every directive's config key. Used by the
// validator and by anything that needs to enumerate them.
func WidgetDirectiveKeys() []string {
	out := make([]string, 0, len(widgetDirectives))
	for _, d := range widgetDirectives {
		out = append(out, d.ConfigKey)
	}
	return out
}

// GlobalWidgetPolicy projects the flat GeneralConfig knobs into a
// WidgetPolicy. The allowlist arrives as a textarea, so it is split here;
// entries were validated on save, and the frontend re-filters them
// anyway, so a bad row degrades to a dropped host rather than an error at
// render time.
func GlobalWidgetPolicy(g GeneralConfig) WidgetPolicy {
	p := WidgetPolicy{
		Mode:        g.WidgetMode,
		AllowPopups: g.WidgetAllowPopups,
		Allowlist:   ParseAllowlist(g.WidgetAllowlist),
	}
	for _, d := range widgetDirectives {
		*d.Field(&p) = d.GlobalValue(g)
	}
	return p
}

// ValidateConfigValue checks one agents config (key, value) pair on write.
// Only the widget keys have rules today; every other key is accepted
// unchanged, so this is safe to register as the owner-wide validator.
//
// Reads fail closed on a bad value anyway (normalizeMode), but a write
// that silently stores "all-of-them" and answers "saved" would leave an
// operator believing a directive is open when it is blocked. Rejecting at
// the door is the difference between a policy and a suggestion.
func ValidateConfigValue(key, value string) error {
	if key == "widget_mode" {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "", PresetSecure, PresetUnsecure, PresetCustom:
			return nil
		default:
			return fmt.Errorf("widget_mode: unknown mode %q (want %s, %s, or %s)",
				value, PresetSecure, PresetUnsecure, PresetCustom)
		}
	}
	if key == "widget_allowlist" {
		// Validated as one blob because that is how the textarea stores it.
		_, err := ValidateAllowlist(ParseAllowlist(value))
		return err
	}
	for _, d := range widgetDirectives {
		if d.ConfigKey != key {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "", ModeBlock, ModeList, ModeAll:
			return nil
		default:
			return fmt.Errorf("%s: unknown mode %q (want %s, %s, or %s)", key, value, ModeBlock, ModeList, ModeAll)
		}
	}
	return nil
}

// normalizeMode maps a stored directive value onto a known Mode.
// Anything unrecognised — including the empty string written by rows
// that predate this policy — fails closed to ModeBlock.
func normalizeMode(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case ModeList:
		return ModeList
	case ModeAll:
		return ModeAll
	default:
		return ModeBlock
	}
}

// Resolve returns the effective policy for a project.
//
// With Override false the global policy is used verbatim. With Override
// true the project's policy is taken wholesale — NOT merged field by
// field, because a per-field merge makes the effective value of a
// security policy impossible to read off one screen.
//
// The allowlist is the one exception: it appends, global first, then the
// project's additions, deduplicated case-insensitively. The practical
// case is a global list of common hosts plus a small per-project
// addition.
//
// Consequence of appending: a project cannot NARROW the global
// allowlist. A project that needs to be stricter than global picks the
// secure preset, or sets the individual directive to ModeBlock.
//
// The preset is then EXPANDED, so the returned policy always states every
// directive explicitly. Callers — including the renderer — never have to
// know a preset existed: a resolved secure policy and a hand-built
// all-blocked one are indistinguishable, by design.
func Resolve(global, project WidgetPolicy) WidgetPolicy {
	base := global
	if project.Override {
		base = project
		base.Allowlist = appendAllowlist(global.Allowlist, project.Allowlist)
	}

	preset := normalizePreset(base.Mode)
	out := WidgetPolicy{Mode: preset, Allowlist: base.Allowlist}

	switch preset {
	case PresetUnsecure:
		// Everything open, per-directive fields ignored. The allowlist is
		// carried through but nothing reads it — no directive is on ModeList.
		for _, d := range widgetDirectives {
			*d.Field(&out) = ModeAll
		}
		out.AllowPopups = true
	case PresetCustom:
		for _, d := range widgetDirectives {
			*d.Field(&out) = normalizeMode(*d.Field(&base))
		}
		out.AllowPopups = base.AllowPopups
	default: // PresetSecure
		// Sealed, per-directive fields ignored. The allowlist is dropped
		// rather than carried: nothing may read it, and a resolved policy
		// that still lists hosts reads as though something might.
		for _, d := range widgetDirectives {
			*d.Field(&out) = ModeBlock
		}
		out.AllowPopups = false
		out.Allowlist = nil
	}
	return out
}

// appendAllowlist concatenates global and extra, dropping duplicates
// case-insensitively while preserving first-seen order.
func appendAllowlist(global, extra []string) []string {
	seen := make(map[string]struct{}, len(global)+len(extra))
	out := make([]string, 0, len(global)+len(extra))
	for _, list := range [][]string{global, extra} {
		for _, h := range list {
			h = strings.TrimSpace(h)
			if h == "" {
				continue
			}
			k := strings.ToLower(h)
			if _, dup := seen[k]; dup {
				continue
			}
			seen[k] = struct{}{}
			out = append(out, h)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ParseAllowlist splits the textarea form of the allowlist (one host per
// line) into entries. Blank lines and # comments are dropped.
func ParseAllowlist(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out
}

// ValidateAllowlist normalises every entry to a bare CSP host source and
// returns the normalised list. It reports the FIRST offending entry
// rather than silently dropping anything: an operator who typed a host
// they believed was allowed must not be left thinking it took effect.
func ValidateAllowlist(entries []string) ([]string, error) {
	out := make([]string, 0, len(entries))
	for _, raw := range entries {
		h, err := NormalizeHostSource(raw)
		if err != nil {
			return nil, fmt.Errorf("allowlist entry %q: %w", raw, err)
		}
		out = append(out, h)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// NormalizeHostSource turns one operator-entered entry into a CSP host
// source of the form https://host[:port], where host may lead with a
// "*." subdomain wildcard.
//
// Rejections, and why each one is a rejection rather than a coercion:
//
//   - "*" alone: that is ModeAll. Saying it as an allowlist entry hides
//     an unrestricted directive behind something that reads restricted.
//   - plaintext http://: the widget runs in an opaque origin; posting to
//     plaintext is a downgrade with no upside.
//   - a path, query, or fragment: CSP host sources do not path-match, so
//     a path here would silently not mean what it appears to mean.
//   - CSP metacharacters and whitespace: these are what a directive
//     injection would be built from.
func NormalizeHostSource(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", fmt.Errorf("empty")
	}
	if strings.ContainsAny(s, " \t;'\",") {
		return "", fmt.Errorf("must not contain whitespace, quotes, comma, or semicolon")
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("must not contain control characters")
		}
	}
	if s == "*" {
		return "", fmt.Errorf(`"*" allows every host — set the directive to %q instead`, ModeAll)
	}

	switch {
	case strings.HasPrefix(strings.ToLower(s), "http://"):
		return "", fmt.Errorf("plaintext http:// is not allowed; use https://")
	case strings.HasPrefix(strings.ToLower(s), "https://"):
		// keep as-is for parsing
	case strings.Contains(s, "://"):
		return "", fmt.Errorf("only https:// is allowed")
	default:
		s = "https://" + s
	}

	u, err := url.Parse(s)
	if err != nil {
		return "", fmt.Errorf("not a valid host")
	}
	if u.Path != "" && u.Path != "/" {
		return "", fmt.Errorf("must not contain a path — CSP matches hosts, not paths")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("must not contain a query or fragment")
	}
	if u.User != nil {
		return "", fmt.Errorf("must not contain credentials")
	}

	// Lower-cased so the emitted source and the dedup key agree: hosts are
	// case-insensitive, and two entries differing only in case are one host.
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if host == "" {
		return "", fmt.Errorf("missing host")
	}
	if err := validateHost(host); err != nil {
		return "", err
	}

	out := "https://" + host
	if port != "" {
		out += ":" + port
	}
	return out, nil
}

// validateHost checks the host part, allowing a single leading "*."
// wildcard label and otherwise requiring plain DNS labels.
func validateHost(host string) error {
	h := strings.ToLower(host)
	if rest, ok := strings.CutPrefix(h, "*."); ok {
		if rest == "" {
			return fmt.Errorf("wildcard needs a domain after it, e.g. *.example.com")
		}
		if strings.Contains(rest, "*") {
			return fmt.Errorf("only one leading *. wildcard is allowed")
		}
		h = rest
	} else if strings.Contains(h, "*") {
		return fmt.Errorf("a wildcard is only allowed as a leading *. label")
	}

	if !strings.Contains(h, ".") && h != "localhost" {
		return fmt.Errorf("must be a dotted host name, e.g. example.com")
	}
	for _, label := range strings.Split(h, ".") {
		if label == "" {
			return fmt.Errorf("has an empty label")
		}
		for _, r := range label {
			isAlnum := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
			if !isAlnum && r != '-' {
				return fmt.Errorf("has an invalid character %q in the host", r)
			}
		}
	}
	return nil
}
