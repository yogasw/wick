package team

import (
	"testing"

	"github.com/yogasw/wick/internal/entity"
)

func TestScope(t *testing.T) {
	grants := []ConnectorGrant{
		{ConnectorID: "all", Level: LevelAll},
		{ConnectorID: "read", Level: LevelRead},
		{ConnectorID: "pick", Level: LevelPick, Ops: []string{"get"}},
		{ConnectorID: "odd", Level: "bogus"},
		{ConnectorID: "accs", Level: LevelAll, Accounts: []string{"a1", ""}},
		{ConnectorID: "accsonly", Level: LevelAll, Accounts: []string{"a1"}},
	}
	cases := []struct {
		name       string
		includeNew bool
		conn       string
		op         string
		destr      bool
		account    string
		wantConn   bool
		wantOp     bool
		wantAcct   bool
	}{
		{"all/destructive", false, "all", "delete", true, "x", true, true, true},
		{"read/safe", false, "read", "get", false, "", true, true, true},
		{"read/destructive", false, "read", "delete", true, "", true, false, true},
		{"pick/listed", false, "pick", "get", false, "", true, true, true},
		{"pick/unlisted", false, "pick", "list", false, "", true, false, true},
		{"pick/listed-destructive", false, "pick", "get", true, "", true, true, true},
		{"unknown level reads as read", false, "odd", "delete", true, "", true, false, true},
		{"accounts/listed", false, "accs", "get", false, "a1", true, true, true},
		{"accounts/bot listed", false, "accs", "get", false, "", true, true, true},
		{"accounts/unlisted", false, "accs", "get", false, "a2", true, true, false},
		{"accounts/bot unlisted", false, "accsonly", "get", false, "", true, true, false},
		{"absent connector", false, "other", "get", false, "", false, false, false},
		{"absent + includeNew safe", true, "other", "get", false, "a9", true, true, true},
		{"absent + includeNew destructive", true, "other", "delete", true, "", true, false, true},
		{"absent + includeNew outside owner reach", true, "elsewhere", "get", false, "", false, false, false},
		{"present ignores includeNew", true, "read", "delete", true, "", true, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := NewScope(grants, tc.includeNew, false, nil)
			if tc.includeNew {
				s = NewScope(grants, tc.includeNew, false, reachOf("other", "read", "all", "pick", "odd", "accs", "accsonly"))
			}
			if got := s.AllowConnector(tc.conn); got != tc.wantConn {
				t.Errorf("AllowConnector = %v, want %v", got, tc.wantConn)
			}
			if got := s.AllowOp(tc.conn, tc.op, tc.destr); got != tc.wantOp {
				t.Errorf("AllowOp = %v, want %v", got, tc.wantOp)
			}
			if got := s.AllowAccount(tc.conn, tc.account); got != tc.wantAcct {
				t.Errorf("AllowAccount = %v, want %v", got, tc.wantAcct)
			}
		})
	}
}

func TestScopeOfDisabledDeniesAll(t *testing.T) {
	p := entity.AgentPersona{
		Disabled:             true,
		AllowedConnectors:    EncodeGrants([]ConnectorGrant{{ConnectorID: "c", Level: LevelAll}}),
		IncludeNewConnectors: true,
	}
	s := ScopeOf(p, reachOf("c", "x"))
	if s.AllowConnector("c") || s.AllowConnector("x") || s.AllowOp("c", "get", false) || s.AllowAccount("c", "") {
		t.Fatal("disabled agent scope must deny everything")
	}
}

func TestScopeIncludeNewNeedsReach(t *testing.T) {
	p := entity.AgentPersona{IncludeNewConnectors: true}
	if ScopeOf(p, nil).AllowConnector("c") {
		t.Fatal("include-new without a known owner catalog must let nothing in")
	}
	s := ScopeOf(p, reachOf("c"))
	if !s.AllowConnector("c") || !s.AllowOp("c", "get", false) || s.AllowOp("c", "delete", true) {
		t.Fatal("include-new must admit owner-catalog connectors read-only")
	}
	if s.AllowConnector("wickmanager-row") {
		t.Fatal("a connector outside the owner's catalog must stay out")
	}
}

func TestScopeFeatureGatesWithoutReach(t *testing.T) {
	// Owner catalog unknown: the old switches cannot become grants, so
	// they still gate by connector type / tool name.
	f := DefaultFeatures()
	f.Schedule, f.Notes, f.Tickets, f.Subagents, f.Source = false, false, false, false, false
	p := entity.AgentPersona{
		AllowedConnectors: EncodeGrants([]ConnectorGrant{{ConnectorID: "n1", Level: LevelAll}}),
		Features:          EncodeFeatures(f),
	}
	s := ScopeOf(p, nil)
	for _, k := range []string{"notes", "tickets", "sub-agents", "source"} {
		if s.AllowKey(k) {
			t.Errorf("AllowKey(%q) = true with its feature off", k)
		}
	}
	if !s.AllowKey("httprest") || !s.AllowKey("playwright_browser") {
		t.Error("a connector type with no feature must stay on")
	}
	if !s.AllowTool("wick_list") || !s.AllowTool("todo") {
		t.Error("tools without a feature must stay on")
	}
}

func TestScopeOfMalformedGrantsDeny(t *testing.T) {
	s := ScopeOf(entity.AgentPersona{AllowedConnectors: "{not json"}, nil)
	if s.AllowConnector("c") {
		t.Fatal("malformed grants must decode to deny")
	}
}

func TestValidateHandle(t *testing.T) {
	ok := []string{"captain", "ab", "a1", "log-investigator", "9x", "a234567890123456789012345678901"}
	bad := []string{"", "a", "-ab", "Ab", "a_b", "a b", "all", "here", "channel", "a2345678901234567890123456789012"}
	for _, h := range ok {
		if err := ValidateHandle(h); err != nil {
			t.Errorf("ValidateHandle(%q) = %v, want nil", h, err)
		}
	}
	for _, h := range bad {
		if err := ValidateHandle(h); err == nil {
			t.Errorf("ValidateHandle(%q) = nil, want error", h)
		}
	}
	if got := NormalizeHandle(" @Captain "); got != "captain" {
		t.Errorf("NormalizeHandle = %q", got)
	}
}

func TestFeaturesDefaultsAndRoundTrip(t *testing.T) {
	if f := DecodeFeatures("{}"); f.Browser || !f.Source || !f.Process {
		t.Fatalf("defaults wrong: %+v", f)
	}
	f := DefaultFeatures()
	f.Source = false
	if got := DecodeFeatures(EncodeFeatures(f)); got != f {
		t.Fatalf("round trip: %+v != %+v", got, f)
	}
	// A partial object keeps defaults for the keys it omits.
	if got := DecodeFeatures(`{"browser":true}`); !got.Browser || !got.Source {
		t.Fatalf("partial: %+v", got)
	}
}

func TestNormalizeAvatar(t *testing.T) {
	if a := DecodeAvatar(`{"shape":"hexagon"}`); a.Shape != "circle" || a.Color == "" {
		t.Fatalf("got %+v", a)
	}
	if a := DecodeAvatar(`{"shape":"diamond","color":"#000000"}`); a.Shape != "diamond" || a.Color != "#000000" {
		t.Fatalf("got %+v", a)
	}
	// A classic row never carries the blob-only fields, even if sent.
	if a := DecodeAvatar(`{"kind":"sprite","shape":"cloud","expression":"happy"}`); a.Kind != "" || a.Shape != "circle" || a.Expression != "" {
		t.Fatalf("unknown kind: %+v", a)
	}
}

func TestNormalizeBlobAvatar(t *testing.T) {
	if a := DecodeAvatar(`{"kind":"blob","shape":"cloud","color":"#4b8fea","expression":"happy"}`); a.Kind != "blob" || a.Shape != "cloud" || a.Expression != "happy" || a.Color != "#4b8fea" {
		t.Fatalf("got %+v", a)
	}
	if a := DecodeAvatar(`{"kind":"blob","shape":"diamond","expression":"smug"}`); a.Shape != "circle" || a.Expression != "neutral" || a.Color == "" {
		t.Fatalf("fallback: %+v", a)
	}
	// Old rows stay byte-identical: no kind/expression keys appear.
	if got := EncodeAvatar(Avatar{Shape: "diamond", Color: "#000000"}); got != `{"shape":"diamond","color":"#000000"}` {
		t.Fatalf("classic encode = %s", got)
	}
}

func reachOf(ids ...string) Reach {
	r := Reach{}
	for _, id := range ids {
		r[id] = ReachItem{Key: id}
	}
	return r
}

// TestScopeLevelResolution is the order in one table: explicit grant
// (off included) > tier default > include-new > deny, all inside reach.
func TestScopeLevelResolution(t *testing.T) {
	reach := Reach{
		"notes1": {Key: "notes", Tier: TierPlatform},
		"cc":     {Key: "customconnector", Tier: TierSystem},
		"wm":     {Key: "wickmanager", Tier: TierSystem},
		"slack":  {Key: "slack"},
		"http":   {Key: "httprest"},
	}
	grants := []ConnectorGrant{
		{ConnectorID: "http", Level: LevelRead},
		{ConnectorID: "cc", Level: LevelRead},
		{ConnectorID: "gone", Level: LevelAll},
		{ConnectorID: "tool:todo", Level: LevelOff},
	}
	cases := []struct {
		name             string
		captain, inclNew bool
		grants           []ConnectorGrant
		reach            Reach
		conn, want       string
	}{
		{"platform default", false, false, nil, reach, "notes1", LevelAll},
		{"platform off override", false, false, []ConnectorGrant{{ConnectorID: "notes1", Level: LevelOff}}, reach, "notes1", LevelOff},
		{"platform read override", false, false, []ConnectorGrant{{ConnectorID: "notes1", Level: LevelRead}}, reach, "notes1", LevelRead},
		{"system default non-captain", false, false, nil, reach, "wm", LevelOff},
		{"system default captain", true, false, nil, reach, "wm", LevelAll},
		{"system ticked non-captain", false, false, grants, reach, "cc", LevelRead},
		{"system off for captain", true, false, []ConnectorGrant{{ConnectorID: "wm", Level: LevelOff}}, reach, "wm", LevelOff},
		{"connector unticked", false, false, nil, reach, "slack", LevelOff},
		{"connector include-new", false, true, nil, reach, "slack", LevelRead},
		{"connector ticked", false, false, grants, reach, "http", LevelRead},
		{"grant outside owner catalog", true, true, grants, reach, "gone", LevelOff},
		{"platform default outside owner catalog", false, false, nil, reach, "notes2", LevelOff},
		{"unknown reach: grant works", false, false, grants, nil, "http", LevelRead},
		{"unknown reach: no defaults", true, true, nil, nil, "notes1", LevelOff},
		{"tool default on", false, false, nil, reach, "tool:ask_user", LevelAll},
		{"tool switched off", false, false, grants, reach, "tool:todo", LevelOff},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := NewScope(tc.grants, tc.inclNew, tc.captain, tc.reach)
			if got := s.Level(tc.conn); got != tc.want {
				t.Fatalf("Level(%q) = %q, want %q", tc.conn, got, tc.want)
			}
		})
	}
	s := NewScope(grants, false, false, reach)
	if s.AllowTool("todo") || !s.AllowTool("ask_user") || !s.AllowTool("wick_list") {
		t.Fatal("tool off-grant not applied")
	}
	if !s.AllowOp("notes1", "delete", true) || s.AllowOp("cc", "delete", true) || !s.AllowOp("cc", "get", false) {
		t.Fatal("ops do not follow the resolved level")
	}
}

// A grant naming only a personal account refuses ops run as the bot.
func TestScopePersonalAccountOnly(t *testing.T) {
	s := NewScope([]ConnectorGrant{{ConnectorID: "slack", Level: LevelAll, Accounts: []string{"acc-me"}}}, false, false, Reach{"slack": {Key: "slack"}})
	if s.AllowAccount("slack", "") || !s.AllowAccount("slack", "acc-me") || s.AllowAccount("slack", "acc-other") {
		t.Fatal("account list must be exact")
	}
}

func TestMigrateFeatures(t *testing.T) {
	f := DefaultFeatures()
	f.Notes, f.Schedule, f.Tickets = false, false, false
	reach := Reach{"n1": {Key: "notes", Tier: TierPlatform}, "t1": {Key: "tickets", Tier: TierPlatform}}
	existing := []ConnectorGrant{{ConnectorID: "t1", Level: LevelRead}}
	f2, gs, changed := MigrateFeatures(f, existing, reach)
	if !changed || !f2.Notes || !f2.Schedule || !f2.Tickets {
		t.Fatalf("features not switched back on: %+v", f2)
	}
	got := map[string]string{}
	for _, g := range gs {
		got[g.ConnectorID] = g.Level
	}
	if got["n1"] != LevelOff || got["tool:wick_schedule_message"] != LevelOff || got["t1"] != LevelRead || len(got) != 3 {
		t.Fatalf("grants = %v", got)
	}
	s := ScopeOf(entity.AgentPersona{Features: EncodeFeatures(f), AllowedConnectors: EncodeGrants(existing)}, reach)
	if s.AllowConnector("n1") || s.AllowTool("wick_schedule_message") || !s.AllowConnector("t1") {
		t.Fatal("scope does not apply migrated features")
	}
	if _, _, again := MigrateFeatures(f2, gs, reach); again {
		t.Fatal("migration is not idempotent")
	}
}

// The rail follows access: an Off row hides its feature, a default or a
// lowered (Read) row keeps it.
func TestEffectiveFeatures(t *testing.T) {
	reach := Reach{"n1": {Key: "notes", Tier: TierPlatform}, "t1": {Key: "tickets", Tier: TierPlatform}, "s1": {Key: "source", Tier: TierPlatform}}
	p := entity.AgentPersona{
		Features: EncodeFeatures(DefaultFeatures()),
		AllowedConnectors: EncodeGrants([]ConnectorGrant{
			{ConnectorID: "n1", Level: LevelOff},
			{ConnectorID: "t1", Level: LevelRead},
			{ConnectorID: "tool:wick_schedule_message", Level: LevelOff},
		}),
	}
	f := EffectiveFeatures(p, reach)
	if f.Notes || f.Schedule || !f.Tickets || !f.Source || !f.Subagents {
		t.Fatalf("effective = %+v", f)
	}
	if f.Browser {
		t.Fatal("browser tab without a browser connector")
	}
	reach["pw"] = ReachItem{Key: "playwright_browser"}
	p.AllowedConnectors = EncodeGrants([]ConnectorGrant{{ConnectorID: "pw", Level: LevelRead}})
	if !EffectiveFeatures(p, reach).Browser {
		t.Fatal("granted browser connector must show the tab")
	}
	if g := EffectiveFeatures(p, nil); !g.Notes {
		t.Fatal("unknown reach must leave stored switches alone")
	}
}

// Files and Process follow the native tools, whatever the stored flags say.
func TestEffectiveFeaturesFilesProcessFromTools(t *testing.T) {
	p := entity.AgentPersona{Features: `{"files":false,"process":true}`, AllowedNativeTools: `["Write","Bash"]`}
	if f := EffectiveFeatures(p, nil); !f.Files || !f.Process {
		t.Fatalf("Write+Bash: %+v", f)
	}
	p = entity.AgentPersona{Features: `{"files":true,"process":true}`, AllowedNativeTools: `["Grep","WebFetch"]`}
	if f := EffectiveFeatures(p, nil); f.Files || f.Process {
		t.Fatalf("no file/shell tools: %+v", f)
	}
	// A row from before the setting has every tool on.
	if f := EffectiveFeatures(entity.AgentPersona{}, nil); !f.Files || !f.Process {
		t.Fatalf("legacy row: %+v", f)
	}
}

// RosterFeatures needs no catalog: Schedule follows its tool grant,
// connector-backed switches stay as stored, Browser reads on.
func TestRosterFeatures(t *testing.T) {
	p := entity.AgentPersona{
		Features:          `{"notes":false,"tickets":true,"source":true,"subagents":true,"schedule":true}`,
		AllowedConnectors: `[{"connector_id":"tool:wick_schedule_message","level":"off"}]`,
	}
	f := RosterFeatures(p)
	if f.Schedule {
		t.Error("Schedule must follow its off tool grant")
	}
	if f.Notes || !f.Tickets {
		t.Errorf("stored switches changed: %+v", f)
	}
	if !f.Browser {
		t.Error("Browser must read on without a catalog")
	}
}
