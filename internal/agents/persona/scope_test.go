package persona

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
		{"present ignores includeNew", true, "read", "delete", true, "", true, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := NewScope(grants, tc.includeNew)
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
	s := ScopeOf(p)
	if s.AllowConnector("c") || s.AllowConnector("x") || s.AllowOp("c", "get", false) || s.AllowAccount("c", "") {
		t.Fatal("disabled agent scope must deny everything")
	}
}

func TestScopeOfMalformedGrantsDeny(t *testing.T) {
	s := ScopeOf(entity.AgentPersona{AllowedConnectors: "{not json"})
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
}
