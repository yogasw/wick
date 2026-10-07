package team

import (
	"strings"
	"testing"
)

func TestCheckGrants(t *testing.T) {
	cat := Catalog{
		"c1": {Accounts: map[string]bool{"": true, "acc-own": true}, Ops: map[string]bool{"read": true, "write": true}},
	}
	cases := []struct {
		name    string
		grants  []ConnectorGrant
		wantErr string // "" = accepted; else a substring of the error
	}{
		{"empty", nil, ""},
		{"all on visible connector", []ConnectorGrant{{ConnectorID: "c1", Level: LevelAll}}, ""},
		{"bot and own account", []ConnectorGrant{{ConnectorID: "c1", Level: LevelRead, Accounts: []string{"", "acc-own"}}}, ""},
		{"pick live ops", []ConnectorGrant{{ConnectorID: "c1", Level: LevelPick, Ops: []string{"read"}}}, ""},
		{"ops ignored unless pick", []ConnectorGrant{{ConnectorID: "c1", Level: LevelAll, Ops: []string{"gone"}}}, ""},
		{"missing id", []ConnectorGrant{{Level: LevelAll}}, "connector_id is required"},
		{"bad level", []ConnectorGrant{{ConnectorID: "c1", Level: "write"}}, "level must be"},
		{"connector outside", []ConnectorGrant{{ConnectorID: "c9", Level: LevelAll}}, "connector c9"},
		{"someone else's account", []ConnectorGrant{{ConnectorID: "c1", Level: LevelAll, Accounts: []string{"acc-other"}}}, "account c1/acc-other"},
		{"op not live", []ConnectorGrant{{ConnectorID: "c1", Level: LevelPick, Ops: []string{"read", "nuke"}}}, "op c1/nuke"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckGrants(tc.grants, cat)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want it to mention %q", err, tc.wantErr)
			}
		})
	}
	// Every rejected item is named, not just the first.
	err := CheckGrants([]ConnectorGrant{
		{ConnectorID: "c8", Level: LevelAll},
		{ConnectorID: "c1", Level: LevelPick, Ops: []string{"nuke"}},
	}, cat)
	if err == nil || !strings.Contains(err.Error(), "connector c8") || !strings.Contains(err.Error(), "op c1/nuke") {
		t.Fatalf("error should list both items, got %v", err)
	}
}

// The owner's own accounts (and the bot, "") are valid picks; an account
// the owner's catalog does not list — someone else's — is refused.
// LevelOff and the on/off tool entries are accepted.
func TestCheckGrantsAccountsOffAndTools(t *testing.T) {
	cat := Catalog{"slack": {Accounts: map[string]bool{"": true, "acc-me": true}, Ops: map[string]bool{"send": true}}}
	ok := []ConnectorGrant{
		{ConnectorID: "slack", Level: LevelAll, Accounts: []string{"acc-me"}},
		{ConnectorID: "slack", Level: LevelOff},
		{ConnectorID: "tool:todo", Level: LevelOff},
		{ConnectorID: "tool:wick_schedule_message", Level: LevelAll},
	}
	for _, g := range ok {
		if err := CheckGrants([]ConnectorGrant{g}, cat); err != nil {
			t.Errorf("%+v refused: %v", g, err)
		}
	}
	bad := []ConnectorGrant{
		{ConnectorID: "slack", Level: LevelAll, Accounts: []string{"acc-other"}},
		{ConnectorID: "tool:todo", Level: LevelRead},
		{ConnectorID: "tool:wick_list", Level: LevelOff},
	}
	for _, g := range bad {
		if err := CheckGrants([]ConnectorGrant{g}, cat); err == nil {
			t.Errorf("%+v accepted", g)
		}
	}
}
