package provider

import (
	"reflect"
	"strings"
	"testing"
)

func TestDefaultFailover(t *testing.T) {
	d := DefaultFailover()
	if d.Enabled || !d.ReturnHome || d.RecheckMinutes != 10 {
		t.Fatalf("DefaultFailover = %+v", d)
	}
	if !reflect.DeepEqual(d.Triggers, []string{"limit", "auth", "unavailable"}) {
		t.Fatalf("default triggers = %v", d.Triggers)
	}
	// The defaults must be valid as they stand, for any instance.
	if err := ValidateFailover(Instance{Type: TypeClaude, Name: "claude"}, d, nil); err != nil {
		t.Fatalf("defaults invalid: %v", err)
	}
	// Callers must not be able to mutate the shared trigger list.
	d.Triggers[0] = "x"
	if FailoverTriggers[0] != "limit" {
		t.Fatal("DefaultFailover shares FailoverTriggers")
	}
}

func TestEffectiveFailover(t *testing.T) {
	if got := EffectiveFailover(Instance{}); !reflect.DeepEqual(got, DefaultFailover()) {
		t.Fatalf("unset = %+v, want defaults", got)
	}
	stored := &FailoverConfig{Enabled: true, Triggers: []string{"auth"}, Chain: []FailoverTarget{{Instance: "codex/a"}}, RecheckMinutes: 5}
	got := EffectiveFailover(Instance{Failover: stored})
	if got.ReturnHome || got.RecheckMinutes != 5 || len(got.Chain) != 1 {
		t.Fatalf("stored config must be read as-is: %+v", got)
	}
	got.Chain[0].Instance = "changed"
	got.Triggers[0] = "changed"
	if stored.Chain[0].Instance != "codex/a" || stored.Triggers[0] != "auth" {
		t.Fatal("EffectiveFailover shares slices with the instance")
	}
	if !got.HasTrigger("changed") || got.HasTrigger("limit") {
		t.Fatal("HasTrigger")
	}
}

func TestInstanceTag(t *testing.T) {
	if got := InstanceTag(TypeCodex, ""); got != "codex/codex" {
		t.Errorf("InstanceTag default = %q", got)
	}
	for in, want := range map[string][2]string{
		"codex":       {"codex", "codex"},
		"codex/waba":  {"codex", "waba"},
		" omp/wick  ": {"omp", "wick"},
	} {
		typ, name := SplitInstanceTag(in)
		if string(typ) != want[0] || name != want[1] {
			t.Errorf("SplitInstanceTag(%q) = %s,%s want %v", in, typ, name, want)
		}
	}
}

func TestNormalizeFailover(t *testing.T) {
	got := NormalizeFailover(FailoverConfig{
		Enabled:  true,
		Triggers: []string{" Limit", "auth", "limit", ""},
		Chain: []FailoverTarget{
			{Instance: "codex", ModelID: " gpt-5 "},
			{Instance: "  "},
			{Instance: "omp/wick", ModelID: "acc@kimi"},
			{Instance: "Codex/Waba"},
		},
		RecheckMinutes: 7,
		ReturnHome:     true,
	})
	want := FailoverConfig{
		Enabled:        true,
		Triggers:       []string{"limit", "auth"},
		Chain:          []FailoverTarget{{Instance: "codex/codex", ModelID: "gpt-5"}, {Instance: "omp/wick", ModelID: "acc@kimi"}, {Instance: "codex/Waba"}},
		RecheckMinutes: 7,
		ReturnHome:     true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NormalizeFailover =\n%+v\nwant\n%+v", got, want)
	}
}

func TestValidateFailover(t *testing.T) {
	self := Instance{Type: TypeClaude, Name: "yoga"}
	all := []Instance{
		self,
		{Type: TypeClaude, Name: "claude"},
		{Type: TypeCodex, Name: "waba"},
		{Type: TypeOMP, Name: "wick"},
		{Type: TypeOpencode, Name: "x"},
		{Type: TypeGemini, Name: "off", Disabled: true},
		{Type: TypeClaude, Name: "b"},
		{Type: TypeCodex, Name: "c"},
		{Type: TypeOMP, Name: "d"},
	}
	base := func(chain ...string) FailoverConfig {
		c := DefaultFailover()
		c.Enabled = true
		for _, tag := range chain {
			c.Chain = append(c.Chain, FailoverTarget{Instance: tag})
		}
		return c
	}
	cases := []struct {
		name string
		cfg  FailoverConfig
		want []string // substrings every one of which must be in the error; nil = valid
	}{
		{"valid three targets, mixed types", base("codex/waba", "omp/wick", "claude/claude"), nil},
		{"valid empty chain", base(), nil},
		{"self", base("claude/yoga"), []string{"itself"}},
		{"duplicate", base("codex/waba", "codex/waba"), []string{"more than once"}},
		{"unknown instance", base("codex/nope"), []string{"unknown provider instance codex/nope"}},
		{"disabled instance", base("gemini/off"), []string{"disabled"}},
		{"valid six targets", base("codex/waba", "omp/wick", "opencode/x", "claude/claude", "claude/b", "codex/c"), nil},
		{"too many targets", base("codex/waba", "omp/wick", "opencode/x", "claude/claude", "claude/b", "codex/c", "omp/d"), []string{"at most 6"}},
		{"unknown trigger", func() FailoverConfig { c := base(); c.Triggers = []string{"limit", "boom"}; return c }(), []string{`unknown trigger "boom"`}},
		{"no trigger while enabled", func() FailoverConfig { c := base(); c.Triggers = nil; return c }(), []string{"at least one trigger"}},
		{"recheck 0", func() FailoverConfig { c := base(); c.RecheckMinutes = 0; return c }(), []string{"recheck"}},
		{"recheck 61", func() FailoverConfig { c := base(); c.RecheckMinutes = 61; return c }(), []string{"recheck interval must be 1..60 minutes, got 61"}},
		{"recheck 1440 now too long", func() FailoverConfig { c := base(); c.RecheckMinutes = 1440; return c }(), []string{"recheck"}},
		{"recheck bounds ok", func() FailoverConfig { c := base(); c.RecheckMinutes = 60; return c }(), nil},
		{"recheck lower bound ok", func() FailoverConfig { c := base(); c.RecheckMinutes = 1; return c }(), nil},
		{"empty target", FailoverConfig{Enabled: true, Triggers: []string{"auth"}, RecheckMinutes: 1, Chain: []FailoverTarget{{}}}, []string{"no instance"}},
		{"every problem reported", func() FailoverConfig { c := base("claude/yoga", "codex/nope"); c.RecheckMinutes = 0; return c }(),
			[]string{"itself", "unknown provider instance", "recheck"}},
		// Off: a target removed later must not block switching the chain off,
		// but structural mistakes are still refused.
		{"disabled chain tolerates unknown target", func() FailoverConfig { c := base("codex/gone", "gemini/off"); c.Enabled = false; return c }(), nil},
		{"disabled chain still refuses self", func() FailoverConfig { c := base("claude/yoga"); c.Enabled = false; return c }(), []string{"itself"}},
		{"disabled chain may have no trigger", func() FailoverConfig { c := base(); c.Enabled = false; c.Triggers = nil; return c }(), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateFailover(self, tc.cfg, all)
			if tc.want == nil {
				if err != nil {
					t.Fatalf("want valid, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("want error containing %v, got nil", tc.want)
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q lacks %q", err, w)
				}
			}
		})
	}
}

// A bare tag naming the instance itself is still "itself".
func TestValidateFailoverBareSelf(t *testing.T) {
	self := Instance{Type: TypeCodex, Name: "codex"}
	c := DefaultFailover()
	c.Chain = []FailoverTarget{{Instance: "codex"}}
	if err := ValidateFailover(self, c, []Instance{self}); err == nil || !strings.Contains(err.Error(), "itself") {
		t.Fatalf("bare self tag: %v", err)
	}
}

func TestFailoverWarnings(t *testing.T) {
	self := Instance{Type: TypeClaude, Name: "yoga"}
	cfg := FailoverConfig{Chain: []FailoverTarget{
		{Instance: "claude/claude"},
		{Instance: "codex/waba", ModelID: "gpt-5"},
		{Instance: "omp"},
	}}
	got := FailoverWarnings(self, cfg, nil)
	want := []FailoverWarning{
		{Instance: "codex/waba", Message: "history does not carry over to codex"},
		{Instance: "omp/omp", Message: "history does not carry over to omp"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FailoverWarnings = %+v, want %+v", got, want)
	}
	if w := FailoverWarnings(self, FailoverConfig{Chain: []FailoverTarget{{Instance: "claude/claude"}}}, nil); len(w) != 0 {
		t.Fatalf("same type must not warn: %+v", w)
	}
}

// Save → Find keeps the chain; an instance that never saved one reads nil
// (defaults), and a stored "off" is not replaced by the defaults.
func TestFailoverConfigRoundTrip(t *testing.T) {
	isolateConfig(t)
	cfg := &FailoverConfig{
		Enabled:        true,
		Triggers:       []string{"limit", "auth"},
		Chain:          []FailoverTarget{{Instance: "codex/waba", ModelID: "acc%20A@gpt-5"}, {Instance: "omp/wick"}},
		RecheckMinutes: 15,
		ReturnHome:     false,
	}
	saveSeed(t, Instance{Type: TypeClaude, Name: "yoga", Failover: cfg})
	saveSeed(t, Instance{Type: TypeClaude, Name: "plain"})

	got, err := Find(TypeClaude, "yoga")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Failover, cfg) {
		t.Fatalf("round trip = %+v, want %+v", got.Failover, cfg)
	}
	plain, err := Find(TypeClaude, "plain")
	if err != nil {
		t.Fatal(err)
	}
	if plain.Failover != nil {
		t.Fatalf("unconfigured instance grew a failover block: %+v", plain.Failover)
	}

	off := &FailoverConfig{Triggers: []string{}, RecheckMinutes: 3}
	got.Failover = off
	saveSeed(t, got)
	again, _ := Find(TypeClaude, "yoga")
	if again.Failover == nil || again.Failover.Enabled || again.Failover.ReturnHome || again.Failover.RecheckMinutes != 3 || len(again.Failover.Triggers) != 0 {
		t.Fatalf("stored off/empty config must be kept as-is: %+v", again.Failover)
	}
}

// Renaming an instance re-points chains that name it, on other types too.
func TestRenameFollowsFailoverTargets(t *testing.T) {
	isolateConfig(t)
	saveSeed(t, Instance{Type: TypeCodex, Name: "waba"})
	saveSeed(t, Instance{Type: TypeClaude, Name: "yoga", Failover: &FailoverConfig{
		Enabled: true, Triggers: []string{"limit"}, RecheckMinutes: 10,
		Chain: []FailoverTarget{{Instance: "codex/waba", ModelID: "gpt-5"}, {Instance: "omp/wick"}},
	}})
	if err := Rename(TypeCodex, "waba", "waba2"); err != nil {
		t.Fatal(err)
	}
	waitBackgroundRescans()
	got, err := Find(TypeClaude, "yoga")
	if err != nil {
		t.Fatal(err)
	}
	if got.Failover.Chain[0] != (FailoverTarget{Instance: "codex/waba2", ModelID: "gpt-5"}) || got.Failover.Chain[1].Instance != "omp/wick" {
		t.Fatalf("chain after rename = %+v", got.Failover.Chain)
	}
}

func TestNormalizeFailoverRecheckDefault(t *testing.T) {
	if got := NormalizeFailover(FailoverConfig{}).RecheckMinutes; got != DefaultFailoverRecheckMinutes {
		t.Fatalf("unset recheck = %d", got)
	}
	if got := NormalizeFailover(FailoverConfig{RecheckMinutes: -1}).RecheckMinutes; got != -1 {
		t.Fatalf("negative recheck must reach validation, got %d", got)
	}
}

// A tag of no supported type is refused even on a chain that is off.
func TestValidateFailoverUnsupportedType(t *testing.T) {
	self := Instance{Type: TypeClaude, Name: "yoga"}
	for _, tag := range []string{"/x", "nope/x"} {
		c := DefaultFailover()
		c.Chain = []FailoverTarget{{Instance: tag}}
		if err := ValidateFailover(self, c, nil); err == nil || !strings.Contains(err.Error(), "unknown provider instance") {
			t.Errorf("%q: %v", tag, err)
		}
	}
}

func TestFailoverWarningsLoginAndModel(t *testing.T) {
	self := Instance{Type: TypeOMP, Name: "home"}
	all := []Instance{
		self,
		{Type: TypeOMP, Name: "borrower", AuthFrom: "home"},
		{Type: TypeOMP, Name: "own"},
		{Type: TypeClaude, Name: "curated", Models: []ModelEntry{{ID: "opus"}}},
		{Type: TypeClaude, Name: "seeded"},
	}
	has := func(ws []FailoverWarning, inst, sub string) bool {
		for _, w := range ws {
			if w.Instance == inst && strings.Contains(w.Message, sub) {
				return true
			}
		}
		return false
	}
	ws := FailoverWarnings(self, FailoverConfig{Chain: []FailoverTarget{
		{Instance: "omp/borrower"}, {Instance: "omp/own"}, {Instance: "claude/curated", ModelID: "sonnet"},
	}}, all)
	if !has(ws, "omp/borrower", "shares the login of omp/home") || has(ws, "omp/own", "shares the login") {
		t.Errorf("login warnings = %+v", ws)
	}
	if !has(ws, "claude/curated", "model sonnet is not offered") {
		t.Errorf("model warning missing: %+v", ws)
	}
	ws = FailoverWarnings(self, FailoverConfig{Chain: []FailoverTarget{
		{Instance: "claude/curated", ModelID: "opus"}, {Instance: "claude/seeded", ModelID: "anything"},
	}}, all)
	if has(ws, "claude/curated", "model") || has(ws, "claude/seeded", "model") {
		t.Errorf("an offered / uncheckable pin must not warn: %+v", ws)
	}
	// Both borrowing one login is shared too.
	if !sharesLogin(Instance{Type: TypeOMP, Name: "a", AuthFrom: "x"}, Instance{Type: TypeOMP, Name: "b", AuthFrom: "x"}) {
		t.Error("common AuthFrom must count as shared")
	}
}

// Deleting an instance drops it from chains on every type; other targets
// stay in order.
func TestDeletePrunesFailoverTargets(t *testing.T) {
	isolateConfig(t)
	saveSeed(t, Instance{Type: TypeCodex, Name: "waba"})
	saveSeed(t, Instance{Type: TypeClaude, Name: "yoga", Failover: &FailoverConfig{
		Enabled: true, Triggers: []string{"limit"}, RecheckMinutes: 10,
		Chain: []FailoverTarget{{Instance: "omp/wick"}, {Instance: "codex/waba", ModelID: "gpt-5"}, {Instance: "gemini/g"}},
	}})
	if err := Delete(TypeCodex, "waba"); err != nil {
		t.Fatal(err)
	}
	got, err := Find(TypeClaude, "yoga")
	if err != nil {
		t.Fatal(err)
	}
	want := []FailoverTarget{{Instance: "omp/wick"}, {Instance: "gemini/g"}}
	if !reflect.DeepEqual(got.Failover.Chain, want) {
		t.Fatalf("chain after delete = %+v", got.Failover.Chain)
	}
}

// Saving another setting (Find → change → Save) keeps the chain.
func TestSaveOtherSettingKeepsFailover(t *testing.T) {
	isolateConfig(t)
	fo := &FailoverConfig{Enabled: true, Triggers: []string{"auth"}, RecheckMinutes: 20, Chain: []FailoverTarget{{Instance: "codex/waba"}}}
	saveSeed(t, Instance{Type: TypeClaude, Name: "yoga", Failover: fo})
	ins, err := Find(TypeClaude, "yoga")
	if err != nil {
		t.Fatal(err)
	}
	ins.OwnerPerms = map[string]bool{"rename": false}
	ins.MaxConcurrent = 2
	saveSeed(t, ins)
	again, _ := Find(TypeClaude, "yoga")
	if !reflect.DeepEqual(again.Failover, fo) {
		t.Fatalf("failover after unrelated save = %+v", again.Failover)
	}
}
