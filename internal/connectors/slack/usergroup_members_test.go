package slack

import "testing"

func TestShapeUsergroupMembers(t *testing.T) {
	got := shapeUsergroupMembers("S01TESTGRP", map[string]any{"ok": true, "users": []any{"U01TESTUSER", "", 7, "U02TESTUSER"}})
	users, _ := got["users"].([]string)
	if got["usergroup"] != "S01TESTGRP" || len(users) != 2 || users[0] != "U01TESTUSER" || users[1] != "U02TESTUSER" {
		t.Fatalf("shape = %v", got)
	}
	if empty := shapeUsergroupMembers("S01TESTGRP", nil); len(empty["users"].([]string)) != 0 {
		t.Fatalf("nil reply = %v", empty)
	}
}
