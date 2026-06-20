package app

import "testing"

func TestParseProfileArg(t *testing.T) {
	for _, ok := range []string{"full", "agent", "lite"} {
		got, err := parseProfileArg(ok)
		if err != nil || got != ok {
			t.Errorf("parseProfileArg(%q) = (%q, %v), want (%q, nil)", ok, got, err, ok)
		}
	}
	if _, err := parseProfileArg("nope"); err == nil {
		t.Errorf("parseProfileArg(\"nope\") = nil error, want error")
	}
}
