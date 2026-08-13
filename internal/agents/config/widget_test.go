package config

import (
	"slices"
	"strings"
	"testing"
)

// allModes reads every directive off a resolved policy, so a test can
// assert the whole posture without naming each field. Driven off the same
// table Resolve uses, so a directive added later is covered automatically.
func allModes(p WidgetPolicy) map[string]string {
	out := make(map[string]string, len(widgetDirectives))
	for _, d := range widgetDirectives {
		out[d.ConfigKey] = *d.Field(&p)
	}
	return out
}

func assertAllModes(t *testing.T, p WidgetPolicy, want string) {
	t.Helper()
	for key, got := range allModes(p) {
		if got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}

/* The preset is the single knob. These pin that secure and unsecure
   IGNORE the per-directive fields entirely — the whole point of the
   toggle is that an operator never has to set them one by one. */

func TestPresetSecureIgnoresPerDirectiveFields(t *testing.T) {
	// Every field set wide open, but the preset says secure.
	global := WidgetPolicy{
		Mode:       PresetSecure,
		FrameSrc:   ModeAll,
		ImgSrc:     ModeAll,
		MediaSrc:   ModeAll,
		ConnectSrc: ModeAll,
		ScriptSrc:  ModeAll,
		// A stale AllowPopups and allowlist must not survive either.
		AllowPopups: true,
		Allowlist:   []string{"https://a.test"},
	}
	got := Resolve(global, WidgetPolicy{})

	assertAllModes(t, got, ModeBlock)
	if got.AllowPopups {
		t.Error("AllowPopups survived the secure preset")
	}
	if len(got.Allowlist) != 0 {
		t.Errorf("Allowlist survived the secure preset: %v", got.Allowlist)
	}
	if got.Mode != PresetSecure {
		t.Errorf("Mode = %q, want %q", got.Mode, PresetSecure)
	}
}

func TestPresetUnsecureIgnoresPerDirectiveFields(t *testing.T) {
	// Every field blocked, but the preset says unsecure.
	global := WidgetPolicy{
		Mode:       PresetUnsecure,
		FrameSrc:   ModeBlock,
		ImgSrc:     ModeBlock,
		MediaSrc:   ModeBlock,
		ConnectSrc: ModeBlock,
		ScriptSrc:  ModeBlock,
	}
	got := Resolve(global, WidgetPolicy{})

	assertAllModes(t, got, ModeAll)
	if !got.AllowPopups {
		t.Error("unsecure preset did not allow popups")
	}
	if got.Mode != PresetUnsecure {
		t.Errorf("Mode = %q, want %q", got.Mode, PresetUnsecure)
	}
	if !got.AllowPopupEscape {
		t.Error("unsecure preset did not let popups escape the sandbox")
	}
}

/* Popup escape is what gives an opened tab a REAL origin instead of the
   opaque one it inherits from a sandboxed frame. Without it a link to a
   third-party site loads with origin "null" and that site's own XHR is
   refused by its CORS check, so the page arrives broken. It is a genuine
   widening — the escaping tab is outside the widget CSP — hence off under
   secure and opt-in under custom. */
func TestPopupEscapePerPreset(t *testing.T) {
	for _, tc := range []struct {
		preset string
		set    bool
		want   bool
	}{
		{PresetSecure, true, false},   // preset overrides the field
		{PresetUnsecure, false, true}, // preset overrides the field
		{PresetCustom, true, true},
		{PresetCustom, false, false},
	} {
		got := Resolve(WidgetPolicy{Mode: tc.preset, AllowPopupEscape: tc.set}, WidgetPolicy{})
		if got.AllowPopupEscape != tc.want {
			t.Errorf("%s preset with field=%v: AllowPopupEscape = %v, want %v",
				tc.preset, tc.set, got.AllowPopupEscape, tc.want)
		}
	}
}

func TestPresetCustomReadsPerDirectiveFields(t *testing.T) {
	got := Resolve(WidgetPolicy{
		Mode:        PresetCustom,
		FrameSrc:    ModeList,
		ImgSrc:      ModeAll,
		MediaSrc:    ModeBlock,
		ConnectSrc:  ModeList,
		ScriptSrc:   ModeBlock,
		AllowPopups: true,
		Allowlist:   []string{"https://maps.google.com"},
	}, WidgetPolicy{})

	if got.FrameSrc != ModeList || got.ImgSrc != ModeAll || got.MediaSrc != ModeBlock {
		t.Fatalf("custom modes not honoured: %+v", got)
	}
	if got.ScriptSrc != ModeBlock {
		t.Errorf("ScriptSrc = %q, want %q — custom must not open scripts on its own", got.ScriptSrc, ModeBlock)
	}
	if !got.AllowPopups {
		t.Error("custom AllowPopups dropped")
	}
	if !slices.Equal(got.Allowlist, []string{"https://maps.google.com"}) {
		t.Errorf("custom allowlist dropped: %v", got.Allowlist)
	}
}

func TestUnknownAndEmptyPresetFailClosed(t *testing.T) {
	for _, mode := range []string{"", "  ", "SECURE-ish", "open", "yes", "all"} {
		got := Resolve(WidgetPolicy{Mode: mode, FrameSrc: ModeAll}, WidgetPolicy{})
		if got.Mode != PresetSecure {
			t.Errorf("preset %q resolved to %q, want %q", mode, got.Mode, PresetSecure)
		}
		assertAllModes(t, got, ModeBlock)
	}
}

func TestPresetIsCaseInsensitive(t *testing.T) {
	for _, in := range []string{"UNSECURE", "Unsecure", " unsecure "} {
		if got := Resolve(WidgetPolicy{Mode: in}, WidgetPolicy{}); got.Mode != PresetUnsecure {
			t.Errorf("preset %q resolved to %q", in, got.Mode)
		}
	}
}

// A project override carries its OWN preset, so a project can be secure
// inside an unsecure install and vice versa.
func TestProjectPresetOverridesGlobalPreset(t *testing.T) {
	global := WidgetPolicy{Mode: PresetUnsecure}

	secured := Resolve(global, WidgetPolicy{Override: true, Mode: PresetSecure})
	assertAllModes(t, secured, ModeBlock)
	if secured.AllowPopups {
		t.Error("project secure override did not close popups")
	}

	opened := Resolve(WidgetPolicy{Mode: PresetSecure}, WidgetPolicy{Override: true, Mode: PresetUnsecure})
	assertAllModes(t, opened, ModeAll)
}

// Under a non-custom preset the allowlist is not read, so it must not be
// presented as though it were.
func TestSecurePresetDropsAllowlistEvenWithProjectHosts(t *testing.T) {
	got := Resolve(
		WidgetPolicy{Mode: PresetSecure, Allowlist: []string{"https://a.test"}},
		WidgetPolicy{Override: true, Mode: PresetSecure, Allowlist: []string{"https://b.test"}},
	)
	if len(got.Allowlist) != 0 {
		t.Fatalf("allowlist = %v, want empty under the secure preset", got.Allowlist)
	}
}

/* The tests below exercise the PER-DIRECTIVE path, which only the custom
   preset reads — under secure/unsecure the fields are ignored by design
   (see the preset tests above). */

func TestResolveInheritsGlobalWhenNoOverride(t *testing.T) {
	global := WidgetPolicy{
		Mode:        PresetCustom,
		FrameSrc:    ModeList,
		ImgSrc:      ModeAll,
		AllowPopups: true,
		Allowlist:   []string{"https://example.com"},
	}
	// Everything on the project is deliberately set to the opposite of
	// global: with Override false none of it may leak through.
	project := WidgetPolicy{
		Override:    false,
		Mode:        PresetCustom,
		FrameSrc:    ModeAll,
		ImgSrc:      ModeBlock,
		AllowPopups: false,
		Allowlist:   []string{"https://evil.test"},
	}

	got := Resolve(global, project)

	if got.FrameSrc != ModeList || got.ImgSrc != ModeAll {
		t.Fatalf("modes not inherited from global: %+v", got)
	}
	if !got.AllowPopups {
		t.Fatal("AllowPopups not inherited from global")
	}
	if !slices.Equal(got.Allowlist, []string{"https://example.com"}) {
		t.Fatalf("allowlist = %v, want global only", got.Allowlist)
	}
}

func TestResolveTakesProjectModesWholesale(t *testing.T) {
	global := WidgetPolicy{
		Mode:        PresetCustom,
		FrameSrc:    ModeAll,
		ImgSrc:      ModeAll,
		MediaSrc:    ModeAll,
		ConnectSrc:  ModeAll,
		AllowPopups: true,
	}
	// Only FrameSrc is set on the project. The unset fields must NOT
	// fall back to global's ModeAll — a project override is wholesale, so
	// they resolve to block.
	project := WidgetPolicy{Override: true, Mode: PresetCustom, FrameSrc: ModeList}

	got := Resolve(global, project)

	if got.FrameSrc != ModeList {
		t.Fatalf("FrameSrc = %q, want %q", got.FrameSrc, ModeList)
	}
	for name, v := range map[string]string{
		"ImgSrc": got.ImgSrc, "MediaSrc": got.MediaSrc, "ConnectSrc": got.ConnectSrc,
	} {
		if v != ModeBlock {
			t.Errorf("%s = %q, want %q (wholesale override, not merged)", name, v, ModeBlock)
		}
	}
	if got.AllowPopups {
		t.Error("AllowPopups leaked from global into a wholesale override")
	}
}

func TestResolveAppendsAllowlist(t *testing.T) {
	tests := []struct {
		name          string
		global, extra []string
		want          []string
	}{
		{"global only", []string{"https://a.test"}, nil, []string{"https://a.test"}},
		{"project only", nil, []string{"https://b.test"}, []string{"https://b.test"}},
		{
			"both append, global first",
			[]string{"https://a.test"}, []string{"https://b.test"},
			[]string{"https://a.test", "https://b.test"},
		},
		{
			"duplicate across both is deduped case-insensitively",
			[]string{"https://a.test"}, []string{"https://A.TEST", "https://b.test"},
			[]string{"https://a.test", "https://b.test"},
		},
		{"blank entries dropped", []string{"", "  "}, []string{"https://b.test"}, []string{"https://b.test"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Resolve(
				WidgetPolicy{Mode: PresetCustom, Allowlist: tc.global},
				WidgetPolicy{Override: true, Mode: PresetCustom, Allowlist: tc.extra},
			)
			if !slices.Equal(got.Allowlist, tc.want) {
				t.Fatalf("allowlist = %v, want %v", got.Allowlist, tc.want)
			}
		})
	}
}

// A project cannot narrow the global allowlist — documented, accepted
// consequence of append semantics. Pinned so a future change to Resolve
// has to face it deliberately.
func TestResolveProjectCannotNarrowAllowlist(t *testing.T) {
	got := Resolve(
		WidgetPolicy{Mode: PresetCustom, FrameSrc: ModeList, Allowlist: []string{"https://wide.test"}},
		WidgetPolicy{Override: true, Mode: PresetCustom, FrameSrc: ModeList},
	)
	if !slices.Contains(got.Allowlist, "https://wide.test") {
		t.Fatal("global host disappeared; append semantics changed")
	}
}

func TestResolveUnknownAndEmptyModeFailClosed(t *testing.T) {
	for _, mode := range []string{"", "  ", "ALL_THE_THINGS", "none", "allow"} {
		got := Resolve(WidgetPolicy{Mode: PresetCustom, FrameSrc: mode}, WidgetPolicy{})
		if got.FrameSrc != ModeBlock {
			t.Errorf("mode %q resolved to %q, want %q", mode, got.FrameSrc, ModeBlock)
		}
	}
}

func TestResolveModeIsCaseInsensitive(t *testing.T) {
	got := Resolve(WidgetPolicy{Mode: PresetCustom, FrameSrc: "ALL", ImgSrc: "List"}, WidgetPolicy{})
	if got.FrameSrc != ModeAll {
		t.Errorf("FrameSrc = %q, want %q", got.FrameSrc, ModeAll)
	}
	if got.ImgSrc != ModeList {
		t.Errorf("ImgSrc = %q, want %q", got.ImgSrc, ModeList)
	}
}

func TestNormalizeHostSourceAccepts(t *testing.T) {
	tests := []struct{ in, want string }{
		{"example.com", "https://example.com"},
		{"https://example.com", "https://example.com"},
		{"https://example.com/", "https://example.com"},
		{"*.example.com", "https://*.example.com"},
		{"https://*.example.com", "https://*.example.com"},
		{"example.com:8443", "https://example.com:8443"},
		{"maps.google.com", "https://maps.google.com"},
		{"HTTPS://Example.COM", "https://example.com"},
		{"localhost:3000", "https://localhost:3000"},
		{"my-host.example.com", "https://my-host.example.com"},
	}
	for _, tc := range tests {
		got, err := NormalizeHostSource(tc.in)
		if err != nil {
			t.Errorf("NormalizeHostSource(%q) errored: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("NormalizeHostSource(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNormalizeHostSourceRejects(t *testing.T) {
	for _, in := range []string{
		"",
		"   ",
		"*",
		"http://example.com",
		"ftp://example.com",
		"ws://example.com",
		"example.com/path",
		"https://example.com/a/b",
		"example.com?q=1",
		"example.com#frag",
		"user:pass@example.com",
		"exa*mple.com",
		"*.*.example.com",
		"*.",
		"example..com",
		"nodot",
		"exa mple.com",
		"example.com; script-src *",
		"example.com'",
		`example.com"`,
		"a.test,b.test",
		"exam\tple.com",
		"exam\x00ple.com",
		"exämple.com",
	} {
		if _, err := NormalizeHostSource(in); err == nil {
			t.Errorf("NormalizeHostSource(%q) accepted, want rejection", in)
		}
	}
}

// The injection case that matters most: an entry carrying CSP syntax must
// never reach the emitted header.
func TestValidateAllowlistRejectsDirectiveInjection(t *testing.T) {
	_, err := ValidateAllowlist([]string{"https://ok.test", "evil.test; script-src *"})
	if err == nil {
		t.Fatal("directive injection accepted")
	}
}

func TestValidateAllowlistNormalisesAndReportsOffender(t *testing.T) {
	got, err := ValidateAllowlist([]string{"example.com", "*.cdn.test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !slices.Equal(got, []string{"https://example.com", "https://*.cdn.test"}) {
		t.Fatalf("got %v", got)
	}

	_, err = ValidateAllowlist([]string{"example.com", "http://bad.test"})
	if err == nil {
		t.Fatal("expected an error for the plaintext entry")
	}
	// The message must name the entry so an operator can find the line.
	if !strings.Contains(err.Error(), "http://bad.test") {
		t.Fatalf("error %q does not name the offending entry", err)
	}
}

func TestValidateAllowlistEmpty(t *testing.T) {
	got, err := ValidateAllowlist(nil)
	if err != nil || got != nil {
		t.Fatalf("got (%v, %v), want (nil, nil)", got, err)
	}
}

func TestValidateConfigValueMode(t *testing.T) {
	for _, ok := range []string{"", "secure", "unsecure", "custom", "SECURE", " custom "} {
		if err := ValidateConfigValue("widget_mode", ok); err != nil {
			t.Errorf("widget_mode %q rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{"block", "all", "open", "insecure", "yes"} {
		if err := ValidateConfigValue("widget_mode", bad); err == nil {
			t.Errorf("widget_mode %q accepted, want rejection", bad)
		}
	}
}

func TestValidateConfigValue(t *testing.T) {
	// Driven off the directive table, so a directive added later is
	// validated by this test without editing it.
	modeKeys := WidgetDirectiveKeys()
	if len(modeKeys) == 0 {
		t.Fatal("no directives declared")
	}

	for _, key := range modeKeys {
		for _, ok := range []string{"", "block", "list", "all", "ALL", " list "} {
			if err := ValidateConfigValue(key, ok); err != nil {
				t.Errorf("ValidateConfigValue(%q, %q) errored: %v", key, ok, err)
			}
		}
		for _, bad := range []string{"none", "allow", "all-of-them", "*"} {
			if err := ValidateConfigValue(key, bad); err == nil {
				t.Errorf("ValidateConfigValue(%q, %q) accepted, want rejection", key, bad)
			}
		}
	}

	if err := ValidateConfigValue("widget_allowlist", "example.com\n*.cdn.test"); err != nil {
		t.Errorf("valid allowlist rejected: %v", err)
	}
	if err := ValidateConfigValue("widget_allowlist", "evil.test; script-src *"); err == nil {
		t.Error("allowlist directive injection accepted")
	}
	if err := ValidateConfigValue("widget_allowlist", ""); err != nil {
		t.Errorf("empty allowlist rejected: %v", err)
	}

	// A bool row needs no rule, and every non-widget key must pass through
	// untouched — this is registered as the owner-wide validator.
	for _, key := range []string{"widget_allow_popups", "max_concurrent", "system_prompt", "anything_else"} {
		if err := ValidateConfigValue(key, "whatever ; value"); err != nil {
			t.Errorf("ValidateConfigValue(%q, ...) must pass through, got: %v", key, err)
		}
	}
}

func TestParseAllowlist(t *testing.T) {
	in := "example.com\r\n\n  *.cdn.test  \n# a comment\n\n"
	got := ParseAllowlist(in)
	if !slices.Equal(got, []string{"example.com", "*.cdn.test"}) {
		t.Fatalf("got %v", got)
	}
}
