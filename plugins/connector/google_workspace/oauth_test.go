package main

import (
	"strings"
	"testing"
)

func TestOAuthScopesCoverOperations(t *testing.T) {
	scopes := OAuthMeta().Scopes
	for _, required := range []string{
		"https://www.googleapis.com/auth/gmail.readonly",
		"https://www.googleapis.com/auth/gmail.send",
		"https://www.googleapis.com/auth/calendar.readonly",
		"https://www.googleapis.com/auth/meetings.space.created",
		"https://www.googleapis.com/auth/meetings.space.readonly",
	} {
		found := false
		for _, scope := range strings.Fields(scopes) {
			if scope == required {
				found = true
			}
		}
		if !found {
			t.Errorf("OAuth scope not explicitly requested: %s", required)
		}
	}
	granted := normalizeGranted(scopes)
	for op, required := range opScopes {
		if ok, missing := evalScopes(required, granted); !ok {
			t.Errorf("%s missing scopes: %v", op, missing)
		}
	}
}

func TestWorkspaceOAuthHealth(t *testing.T) {
	const prefix = "https://www.googleapis.com/auth/"
	tests := []struct {
		name   string
		op     string
		scopes string
		want   bool
	}{
		{"modify permits send", "gmail_send", prefix + "gmail.modify", true},
		{"compose permits send", "gmail_send", prefix + "gmail.compose", true},
		{"readonly cannot send", "gmail_send", prefix + "gmail.readonly", false},
		{"send alone cannot reply", "gmail_reply", prefix + "gmail.send", false},
		{"modify permits reply", "gmail_reply", prefix + "gmail.modify", true},
		{"compose alone cannot reply", "gmail_reply", prefix + "gmail.compose", false},
		{"read and send permit reply", "gmail_reply", prefix + "gmail.readonly " + prefix + "gmail.send", true},
		{"calendar readonly permits list", "calendar_list_calendars", prefix + "calendar.readonly", true},
		{"calendar full permits list", "calendar_list_calendars", prefix + "calendar", true},
		{"calendar readonly cannot create", "calendar_create_event", prefix + "calendar.readonly", false},
		{"Meet created permits create", "meet_create_space", prefix + "meetings.space.created", true},
		{"Meet readonly cannot create", "meet_create_space", prefix + "meetings.space.readonly", false},
		{"Meet readonly permits list", "meet_list_conference_records", prefix + "meetings.space.readonly", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, missing := evalScopes(opScopes[tt.op], normalizeGranted(tt.scopes))
			if got != tt.want {
				t.Errorf("ok = %v, want %v; missing = %v", got, tt.want, missing)
			}
		})
	}
	if normalizeGranted(prefix + "meetings.space.created")[prefix+"meetings.space.readonly"] {
		t.Error("Meet created scope must not imply unrestricted readonly access")
	}
}
